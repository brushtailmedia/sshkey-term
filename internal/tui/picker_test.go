package tui

// Tests for the shared PickerModel widget (spec: shared-picker-widget.md,
// decisions #1–#13) and the `/whois` proving milestone (#12): bare
// `/whois` opens the picker; typed `/whois @user` bypasses it
// (typed-bypass coexists, #1c); a picker selection rejoins the
// post-resolution step `whoisReadout` (#3/#5).

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func runes(s string) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// --- Widget unit tests (pure; no App/client) ---

func TestPicker_FilterThenEnterSelects(t *testing.T) {
	var p PickerModel
	req := PickerRequest{Verb: "/whois", Source: PickerSourceSlash, ShowFilter: true}
	p.Show(req, []PickerItem{
		{ID: "u1", Primary: "Alice"},
		{ID: "u2", Primary: "Bob"},
		{ID: "u3", Primary: "Carol", Search: []string{"charlie"}},
	})
	if !p.IsVisible() || len(p.filtered) != 3 {
		t.Fatalf("Show: visible=%v filtered=%d (want true,3)", p.IsVisible(), len(p.filtered))
	}
	// Filter by an alias term (Search) — should narrow to Carol.
	for _, r := range "char" {
		p, _ = p.Update(runes(string(r)))
	}
	if len(p.filtered) != 1 || p.filtered[0].ID != "u3" {
		t.Fatalf("filter 'char' → %d items, want 1 (Carol via Search alias)", len(p.filtered))
	}
	// Enter emits PickerSelectedMsg echoing the request + selected ID.
	var cmd tea.Cmd
	p, cmd = p.Update(key(tea.KeyEnter))
	if p.IsVisible() {
		t.Fatal("picker must Hide after Enter")
	}
	if cmd == nil {
		t.Fatal("Enter must emit a command")
	}
	msg := cmd()
	sel, ok := msg.(PickerSelectedMsg)
	if !ok {
		t.Fatalf("Enter emitted %T, want PickerSelectedMsg", msg)
	}
	if sel.SelectedID != "u3" || sel.Request.Verb != "/whois" {
		t.Fatalf("PickerSelectedMsg = {%q,%q}, want {u3,/whois}", sel.SelectedID, sel.Request.Verb)
	}
}

func TestPicker_EscHides(t *testing.T) {
	var p PickerModel
	p.Show(PickerRequest{Verb: "/whois"}, []PickerItem{{ID: "u1", Primary: "A"}})
	p, _ = p.Update(key(tea.KeyEsc))
	if p.IsVisible() {
		t.Fatal("Esc must Hide the picker")
	}
}

func TestPicker_NoFilterVimNavAndQuit(t *testing.T) {
	var p PickerModel
	p.Show(PickerRequest{Verb: "/kick", ShowFilter: false}, []PickerItem{
		{ID: "u1", Primary: "A"}, {ID: "u2", Primary: "B"}, {ID: "u3", Primary: "C"},
	})
	p, _ = p.Update(runes("j")) // down
	p, _ = p.Update(runes("j")) // down
	if p.cursor != 2 {
		t.Fatalf("after jj cursor=%d, want 2", p.cursor)
	}
	p, _ = p.Update(runes("k")) // up
	if p.cursor != 1 {
		t.Fatalf("after k cursor=%d, want 1", p.cursor)
	}
	// In no-filter mode runes are nav, NOT filter input.
	if p.filter != "" || len(p.filtered) != 3 {
		t.Fatalf("no-filter mode must not filter on runes (filter=%q n=%d)", p.filter, len(p.filtered))
	}
	p, _ = p.Update(runes("q"))
	if p.IsVisible() {
		t.Fatal("'q' must Hide in no-filter mode")
	}
}

func TestPicker_ScrollAndClamp(t *testing.T) {
	var p PickerModel
	items := make([]PickerItem, pickerVisibleRows+8)
	for i := range items {
		items[i] = PickerItem{ID: string(rune('a' + i)), Primary: string(rune('a' + i))}
	}
	p.Show(PickerRequest{Verb: "/whois"}, items)
	// Drive the cursor to the end; scroll must follow and clamp.
	for i := 0; i < len(items)+5; i++ {
		p, _ = p.Update(key(tea.KeyDown))
	}
	if p.cursor != len(items)-1 {
		t.Fatalf("cursor=%d, want clamped to %d", p.cursor, len(items)-1)
	}
	if p.scroll != len(items)-pickerVisibleRows {
		t.Fatalf("scroll=%d, want %d (cursor in viewport)", p.scroll, len(items)-pickerVisibleRows)
	}
	if v := p.View(80); !strings.Contains(v, "↑ more") {
		t.Fatalf("scrolled-down view should show '↑ more'")
	}
	p, _ = p.Update(key(tea.KeyHome))
	if p.cursor != 0 || p.scroll != 0 {
		t.Fatalf("Home → cursor/scroll=%d/%d, want 0/0", p.cursor, p.scroll)
	}
	if v := p.View(80); !strings.Contains(v, "↓ more") {
		t.Fatalf("scrolled-top long-list view should show '↓ more'")
	}
}

func TestPicker_MouseWheelMovesCursorWithoutFiltering(t *testing.T) {
	var p PickerModel
	items := make([]PickerItem, pickerVisibleRows+8)
	for i := range items {
		items[i] = PickerItem{ID: string(rune('a' + i)), Primary: string(rune('a' + i))}
	}
	p.Show(PickerRequest{Verb: "/whois", ShowFilter: true}, items)

	p, _ = p.HandleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if p.cursor != pickerMouseWheelStep {
		t.Fatalf("wheel down cursor=%d, want %d", p.cursor, pickerMouseWheelStep)
	}
	if p.filter != "" {
		t.Fatalf("mouse wheel must not write to filter, got %q", p.filter)
	}

	for i := 0; i < len(items); i++ {
		p, _ = p.HandleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	if p.cursor != len(items)-1 {
		t.Fatalf("wheel down should clamp cursor at end, got %d want %d", p.cursor, len(items)-1)
	}
	if p.scroll != len(items)-pickerVisibleRows {
		t.Fatalf("wheel down should keep cursor visible, scroll=%d want %d", p.scroll, len(items)-pickerVisibleRows)
	}

	p, _ = p.HandleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if p.cursor != len(items)-1-pickerMouseWheelStep {
		t.Fatalf("wheel up cursor=%d, want %d", p.cursor, len(items)-1-pickerMouseWheelStep)
	}
	if p.filter != "" {
		t.Fatalf("mouse wheel must still not write to filter, got %q", p.filter)
	}
}

func TestPicker_DropsLeakedMouseSequencesFromFilter(t *testing.T) {
	var p PickerModel
	items := []PickerItem{{ID: "u1", Primary: "Alice"}}
	p.Show(PickerRequest{Verb: "/whois", ShowFilter: true}, items)

	p, _ = p.Update(runes("[<64;54;21M"))
	if p.filter != "" {
		t.Fatalf("packed leaked mouse sequence should not reach filter, got %q", p.filter)
	}

	for _, r := range "[<65;54;21M" {
		p, _ = p.Update(runes(string(r)))
	}
	if p.filter != "" {
		t.Fatalf("chunked leaked mouse sequence should not reach filter, got %q", p.filter)
	}

	p, _ = p.Update(runes("Ali"))
	if p.filter != "Ali" {
		t.Fatalf("normal filter input should still work, got %q", p.filter)
	}
}

func TestPicker_EmptyFilterState(t *testing.T) {
	var p PickerModel
	p.Show(PickerRequest{Verb: "/whois", ShowFilter: true}, []PickerItem{{ID: "u1", Primary: "Alice"}})
	for _, r := range "zzz" {
		p, _ = p.Update(runes(string(r)))
	}
	if len(p.filtered) != 0 {
		t.Fatalf("no-match filter → %d items, want 0", len(p.filtered))
	}
	if _, cmd := p.Update(key(tea.KeyEnter)); cmd != nil {
		t.Fatal("Enter on empty filtered set must be a no-op (no command)")
	}
	if v := p.View(80); !strings.Contains(v, "No matches") {
		t.Fatal("empty filtered view should show 'No matches'")
	}
}

func TestAppMouse_RoutesWheelToVisiblePicker(t *testing.T) {
	a := App{width: 100, height: 40}
	items := make([]PickerItem, pickerVisibleRows+4)
	for i := range items {
		items[i] = PickerItem{ID: string(rune('a' + i)), Primary: string(rune('a' + i))}
	}
	a.picker.Show(PickerRequest{Verb: "/whois", ShowFilter: true}, items)

	model, cmd := a.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 1, Y: 1})
	if cmd != nil {
		t.Fatalf("picker mouse wheel should not emit command, got %T", cmd())
	}
	na := model.(App)
	if na.picker.cursor != pickerMouseWheelStep {
		t.Fatalf("App did not route wheel to picker: cursor=%d want %d", na.picker.cursor, pickerMouseWheelStep)
	}
	if na.picker.filter != "" {
		t.Fatalf("App-routed mouse wheel must not write to filter, got %q", na.picker.filter)
	}
}

// --- /whois proving milestone (App + client) ---

func newPickerWhoisApp(t *testing.T) (*App, *client.Client, *store.Store) {
	t.Helper()
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	client.SetUserIDForTesting(c, "usr_self")
	a := &App{client: c, statusBar: NewStatusBar()}
	return a, c, st
}

func seedAlice(t *testing.T, c *client.Client, st *store.Store) {
	t.Helper()
	client.SetProfileForTesting(c, &protocol.Profile{
		User: "usr_alice", DisplayName: "Alice",
		KeyFingerprint: "SHA256:abc", PubKey: "ssh-ed25519 AAAA",
	})
	if err := st.PinKey("usr_alice", "SHA256:abc", "ssh-ed25519 AAAA"); err != nil {
		t.Fatalf("PinKey: %v", err)
	}
}

func TestWhoisPicker_BareOpensPicker(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/whois"})
	if !a.picker.IsVisible() {
		t.Fatal("bare /whois must open the shared picker (#12)")
	}
	if a.picker.req.Verb != "/whois" || !a.picker.req.ShowFilter {
		t.Fatalf("picker req = {%q, filter=%v}, want {/whois, true}", a.picker.req.Verb, a.picker.req.ShowFilter)
	}
	if a.infoPanel.IsVisible() {
		t.Fatal("bare /whois must NOT open the readout panel directly")
	}
}

func TestWhoisPicker_TypedBypassesPicker(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/whois", Arg: "Alice"})
	if a.picker.IsVisible() {
		t.Fatal("typed /whois @user must BYPASS the picker (#1c typed-bypass coexists)")
	}
	if !a.infoPanel.IsVisible() {
		t.Fatal("typed /whois Alice should open the readout panel directly (unchanged path)")
	}
}

func TestWhoisPicker_SelectionRoutesToReadout(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	model, _ := (*a).Update(PickerSelectedMsg{
		Request:    PickerRequest{Verb: "/whois", Source: PickerSourceSlash},
		SelectedID: "usr_alice",
	})
	na := model.(App)
	if !na.infoPanel.IsVisible() || !na.infoPanel.isUser {
		t.Fatal("PickerSelectedMsg{/whois} must route to whoisReadout → user info panel (#3/#5)")
	}
}

func TestWhoisCandidates_ExcludesSelfSorted(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_self", DisplayName: "Zelf"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})

	got := a.whoisCandidates()
	for _, it := range got {
		if it.ID == "usr_self" {
			t.Fatal("whoisCandidates must exclude self")
		}
		if it.Secondary != "" {
			t.Fatalf("live (non-retired) profile %q must have empty Secondary", it.ID)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (alice, bob; self excluded)", len(got))
	}
	if got[0].Primary != "Alice" || got[1].Primary != "Bob" {
		t.Fatalf("not sorted by display name: %q, %q", got[0].Primary, got[1].Primary)
	}
}

// --- /verify + /role (§9 step 3) ---

func TestVerifyPicker_BareOpensPicker(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	// Seed alice as a pinned-but-unverified candidate.
	seedAlice(t, c, st)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/verify"})
	if !a.picker.IsVisible() {
		t.Fatal("bare /verify must open the shared picker (§9 step 3)")
	}
	if a.picker.req.Verb != "/verify" || !a.picker.req.ShowFilter {
		t.Fatalf("picker req = {%q, filter=%v}, want {/verify, true}", a.picker.req.Verb, a.picker.req.ShowFilter)
	}
}

func TestVerifyPicker_TypedBypassesPicker(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/verify", Arg: "Alice"})
	if a.picker.IsVisible() {
		t.Fatal("typed /verify @user must BYPASS the picker (#1c)")
	}
	if !a.verify.IsVisible() {
		t.Fatal("typed /verify Alice should open the VerifyModel directly (unchanged path)")
	}
}

func TestVerifyPicker_SelectionOpensVerifyModel(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	model, _ := (*a).Update(PickerSelectedMsg{
		Request:    PickerRequest{Verb: "/verify", Source: PickerSourceSlash},
		SelectedID: "usr_alice",
	})
	na := model.(App)
	if !na.verify.IsVisible() {
		t.Fatal("PickerSelectedMsg{/verify} must route to VerifyModel.Show (#3 clean ID seam)")
	}
}

func TestVerifyCandidates_ExcludesVerifiedRetiredNoKeySelf(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)

	// alice: pinned, NOT verified → eligible.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice", KeyFingerprint: "SHA256:a"})
	if err := st.PinKey("usr_alice", "SHA256:a", "ssh-ed25519 a"); err != nil {
		t.Fatalf("PinKey alice: %v", err)
	}
	// bob: pinned AND verified → must be excluded.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob", KeyFingerprint: "SHA256:b"})
	if err := st.PinKey("usr_bob", "SHA256:b", "ssh-ed25519 b"); err != nil {
		t.Fatalf("PinKey bob: %v", err)
	}
	if err := st.MarkVerified("usr_bob"); err != nil {
		t.Fatalf("MarkVerified bob: %v", err)
	}
	// carol: profile but NO pinned key → must be excluded (#6: can't verify someone with no pinned fingerprint).
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_carol", DisplayName: "Carol"})
	// dave: pinned-unverified but RETIRED → must be excluded (#9a action verbs exclude retired).
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_dave", DisplayName: "Dave", KeyFingerprint: "SHA256:d"})
	if err := st.PinKey("usr_dave", "SHA256:d", "ssh-ed25519 d"); err != nil {
		t.Fatalf("PinKey dave: %v", err)
	}
	client.SetRetiredForTesting(c, "usr_dave", "2026-05-20T00:00:00Z")
	// self should never appear.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_self", DisplayName: "Zelf", KeyFingerprint: "SHA256:s"})

	got := a.verifyCandidates()
	if len(got) != 1 || got[0].ID != "usr_alice" {
		ids := make([]string, len(got))
		for i, it := range got {
			ids[i] = it.ID
		}
		t.Fatalf("verifyCandidates = %v, want exactly [usr_alice] (verified/no-key/retired/self all excluded)", ids)
	}
}

func TestRolePicker_BareOutsideGroupStatusOnly(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/role"}) // sc.Group == ""
	if a.picker.IsVisible() {
		t.Fatal("bare /role outside a group MUST NOT open the picker (§6 invalid-context)")
	}
	if !strings.Contains(a.statusBar.errorMsg, "only works inside a group") {
		t.Fatalf("expected 'only works inside a group' message, got %q", a.statusBar.errorMsg)
	}
	// Wording: must NOT end with "DM" (owner-asked, 2026-05-20).
	if strings.HasSuffix(strings.TrimSpace(a.statusBar.errorMsg), "DM") {
		t.Fatalf("message should not end with 'DM', got %q", a.statusBar.errorMsg)
	}
}

// Typed /role @user outside a group used to silently return — fire
// the same friendly status as the bare branch instead.
func TestRolePicker_TypedOutsideGroupStatusOnly(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/role", Arg: "@alice", Room: "room_x"})
	if a.picker.IsVisible() {
		t.Fatal("typed /role @user outside a group must NOT open the picker")
	}
	if !strings.Contains(a.statusBar.errorMsg, "only works inside a group") {
		t.Fatalf("typed /role outside a group should fire the same status, got %q", a.statusBar.errorMsg)
	}
	if strings.HasSuffix(strings.TrimSpace(a.statusBar.errorMsg), "DM") {
		t.Fatalf("message should not end with 'DM', got %q", a.statusBar.errorMsg)
	}
}

func TestRolePicker_BareInGroupOpensPicker(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})

	a.handleSlashCommand(&SlashCommandMsg{Command: "/role", Group: "g_x"})
	if !a.picker.IsVisible() {
		t.Fatal("bare /role inside a group must open the picker")
	}
	if a.picker.req.Verb != "/role" || a.picker.req.Group != "g_x" || !a.picker.req.ShowFilter {
		t.Fatalf("picker req = {%q,%q,filter=%v}, want {/role,g_x,true}", a.picker.req.Verb, a.picker.req.Group, a.picker.req.ShowFilter)
	}
}

func TestRolePicker_TypedBypassesPicker(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
	client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_alice"})

	a.handleSlashCommand(&SlashCommandMsg{Command: "/role", Group: "g_x", Arg: "Alice"})
	if a.picker.IsVisible() {
		t.Fatal("typed /role @user must BYPASS the picker (#1c)")
	}
	if !strings.Contains(a.statusBar.errorMsg, "admin") {
		t.Fatalf("typed /role should produce role readout in status, got %q", a.statusBar.errorMsg)
	}
}

func TestRolePicker_SelectionRoutesToReadout(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
	client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_alice"})

	model, _ := (*a).Update(PickerSelectedMsg{
		Request:    PickerRequest{Verb: "/role", Source: PickerSourceSlash, Group: "g_x"},
		SelectedID: "usr_alice",
	})
	na := model.(App)
	if !strings.Contains(na.statusBar.errorMsg, "Alice") || !strings.Contains(na.statusBar.errorMsg, "admin") {
		t.Fatalf("PickerSelectedMsg{/role} must route to roleReadout → status 'Alice — admin', got %q", na.statusBar.errorMsg)
	}
}

func TestRoleCandidates_GroupMembersExcludeSelfMarkRetired(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice", "usr_bob"})
	client.SetRetiredForTesting(c, "usr_bob", "2026-05-20T00:00:00Z")

	got := a.roleCandidates("g_x")
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (alice + bob; self excluded)", len(got))
	}
	if got[0].ID == "usr_self" || got[1].ID == "usr_self" {
		t.Fatal("roleCandidates must exclude self")
	}
	// Sorted alphabetically: Alice, Bob.
	if got[0].ID != "usr_alice" || got[1].ID != "usr_bob" {
		t.Fatalf("order = [%q,%q], want [usr_alice,usr_bob]", got[0].ID, got[1].ID)
	}
	if got[0].Secondary != "" {
		t.Fatalf("live member Alice should have empty Secondary, got %q", got[0].Secondary)
	}
	if got[1].Secondary != "retired" {
		t.Fatalf("retired member Bob should have Secondary=\"retired\", got %q", got[1].Secondary)
	}
}

func TestRoleCandidates_EmptyOutsideGroup(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	if got := a.roleCandidates(""); got != nil {
		t.Fatalf("roleCandidates(\"\") = %v, want nil", got)
	}
}

// TestInputParser_RoleRoutesToAppInAllForms exercises the REAL input
// router path (`handleCommand` → `PendingCommand`) for /role — the
// path the App-handler-in-isolation tests above do NOT touch, and
// the gap that initially left bare `/role` silently dropped by the
// input router (same class as the earlier /typing wiring bug). This
// is the regression guard.
func TestInputParser_RoleRoutesToAppInAllForms(t *testing.T) {
	cases := []struct {
		text          string
		room, group, dm string
		wantArg       string
		wantGroup     string
	}{
		// Bare /role in a group → must forward so App can open the picker.
		{"/role", "", "group_x", "", "", "group_x"},
		// Typed /role @user in a group → must forward (existing behavior).
		{"/role @alice", "", "group_x", "", "@alice", "group_x"},
		// Bare /role outside a group → still forwards; App surfaces the
		// invalid-context status. (Pre-fix this was dropped silently.)
		{"/role", "room_x", "", "", "", ""},
		// Typed /role @user outside a group → forwards too; App's typed
		// handler surfaces the friendly group-only error.
		{"/role @alice", "", "", "dm_x", "@alice", ""},
	}
	for _, tc := range cases {
		i := &InputModel{}
		i.handleCommand(tc.text, nil, tc.room, tc.group, tc.dm)
		sc := i.PendingCommand()
		if sc == nil {
			t.Errorf("%q in {room=%q group=%q dm=%q} produced no pendingCmd — /role unwired in router",
				tc.text, tc.room, tc.group, tc.dm)
			continue
		}
		if sc.Command != "/role" || sc.Arg != tc.wantArg || sc.Group != tc.wantGroup {
			t.Errorf("%q in {room=%q group=%q dm=%q} routed as {Command:%q Arg:%q Group:%q}, want {/role %q %q}",
				tc.text, tc.room, tc.group, tc.dm, sc.Command, sc.Arg, sc.Group, tc.wantArg, tc.wantGroup)
		}
	}
}
