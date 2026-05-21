package tui

// Tests for the shared PickerModel widget (spec: shared-picker-widget.md,
// decisions #1–#13) and the `/whois` proving milestone (#12): bare
// `/whois` opens the picker; typed `/whois @user` bypasses it
// (typed-bypass coexists, #1c); a picker selection rejoins the
// post-resolution step `whoisReadout` (#3/#5).

import (
	"bytes"
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

// Typed `/verify @user` on an already-verified user must surface a
// friendly status and NOT re-open the safety-number flow — matches
// the picker candidate filter which excludes already-verified users.
func TestVerifyTyped_AlreadyVerifiedStatusOnly(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	a.handleSlashCommand(&SlashCommandMsg{Command: "/verify", Arg: "Alice"})
	if a.verify.IsVisible() {
		t.Fatal("typed /verify on an already-verified user must NOT open VerifyModel")
	}
	if !strings.Contains(a.statusBar.errorMsg, "already verified") {
		t.Fatalf("expected 'already verified' status, got %q", a.statusBar.errorMsg)
	}
}

// Typed `/unverify @user` on a user who isn't currently verified must
// surface a friendly status and NOT open the new confirm dialog —
// matches the picker candidate filter (`unverifyCandidates` returns
// only currently-verified). Covers both "pinned but verified=0" and
// "no pinned key at all": neither is meaningfully unverifiable.
func TestUnverifyTyped_NotVerifiedStatusOnly(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st) // pinned but NOT MarkVerified'd
	a.handleSlashCommand(&SlashCommandMsg{Command: "/unverify", Arg: "Alice"})
	if a.unverifyConfirm.IsVisible() {
		t.Fatal("typed /unverify on a not-verified user must NOT open the confirm")
	}
	if !strings.Contains(a.statusBar.errorMsg, "not verified") {
		t.Fatalf("expected 'not verified' status, got %q", a.statusBar.errorMsg)
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

// --- /unverify (§9 step 4) ---

func TestUnverifyConfirm_YEmitsMsg(t *testing.T) {
	var m UnverifyConfirmModel
	m.Show("usr_alice", "Alice")
	if !m.IsVisible() {
		t.Fatal("Show must mark visible")
	}
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m2.IsVisible() {
		t.Fatal("y must Hide the confirm")
	}
	if cmd == nil {
		t.Fatal("y must emit a command")
	}
	msg, ok := cmd().(UnverifyConfirmMsg)
	if !ok || msg.TargetID != "usr_alice" {
		t.Fatalf("y emitted %v, want UnverifyConfirmMsg{usr_alice}", cmd())
	}
}

func TestUnverifyConfirm_NCancels(t *testing.T) {
	var m UnverifyConfirmModel
	m.Show("usr_alice", "Alice")
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m2.IsVisible() {
		t.Fatal("n must Hide the confirm")
	}
	if cmd != nil {
		t.Fatal("n must NOT emit a command (cancel = no-op)")
	}
}

func TestUnverifyPicker_BareOpensPicker(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	// Seed bob as a verified candidate so the picker is non-empty.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	if err := st.PinKey("usr_bob", "SHA256:b", "ssh-ed25519 b"); err != nil {
		t.Fatalf("PinKey: %v", err)
	}
	if err := st.MarkVerified("usr_bob"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	a.handleSlashCommand(&SlashCommandMsg{Command: "/unverify"})
	if !a.picker.IsVisible() {
		t.Fatal("bare /unverify must open the shared picker (§9 step 4)")
	}
	if a.picker.req.Verb != "/unverify" || !a.picker.req.ShowFilter {
		t.Fatalf("picker req = {%q, filter=%v}, want {/unverify, true}", a.picker.req.Verb, a.picker.req.ShowFilter)
	}
	if a.unverifyConfirm.IsVisible() {
		t.Fatal("bare /unverify must NOT open the confirm directly — picker first")
	}
}

func TestUnverifyPicker_TypedOpensConfirmNotImmediate(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	a.handleSlashCommand(&SlashCommandMsg{Command: "/unverify", Arg: "Alice"})
	if a.picker.IsVisible() {
		t.Fatal("typed /unverify @user must BYPASS the picker (#1c)")
	}
	if !a.unverifyConfirm.IsVisible() {
		t.Fatal("typed /unverify @user MUST now open the confirm (no longer immediate clear) — #8 the one net-new dialog")
	}
	// The verification must still be intact until the user confirms.
	info, _ := st.GetPinnedKeyInfo("usr_alice")
	if !info.Verified {
		t.Fatal("typed /unverify must NOT clear verification before the confirm — that was the silent-trust-removal gap #8 closes")
	}
}

func TestUnverifyPicker_SelectionOpensConfirm(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	model, _ := (*a).Update(PickerSelectedMsg{
		Request:    PickerRequest{Verb: "/unverify", Source: PickerSourceSlash},
		SelectedID: "usr_alice",
	})
	na := model.(App)
	if !na.unverifyConfirm.IsVisible() {
		t.Fatal("PickerSelectedMsg{/unverify} must open the new confirm — picker selection is NOT an immediate clear")
	}
	info, _ := st.GetPinnedKeyInfo("usr_alice")
	if !info.Verified {
		t.Fatal("picker selection must NOT clear verification before the confirm")
	}
}

func TestUnverifyConfirm_YClearsVerified(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	seedAlice(t, c, st)
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	// Drive an UnverifyConfirmMsg straight into App.Update — same Msg
	// the dialog's y/enter would emit.
	model, _ := (*a).Update(UnverifyConfirmMsg{TargetID: "usr_alice"})
	na := model.(App)
	info, _ := st.GetPinnedKeyInfo("usr_alice")
	if info.Verified {
		t.Fatal("UnverifyConfirmMsg must call Store.ClearVerified")
	}
	if !strings.Contains(na.statusBar.errorMsg, "Verification removed") {
		t.Fatalf("status should confirm removal, got %q", na.statusBar.errorMsg)
	}
}

func TestUnverifyCandidates_OnlyVerifiedExcludeSelfRetired(t *testing.T) {
	a, c, st := newPickerWhoisApp(t)
	// alice: pinned + verified → eligible.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	if err := st.PinKey("usr_alice", "SHA256:a", "ssh-ed25519 a"); err != nil {
		t.Fatalf("PinKey: %v", err)
	}
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	// bob: pinned but NOT verified → must be excluded.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	if err := st.PinKey("usr_bob", "SHA256:b", "ssh-ed25519 b"); err != nil {
		t.Fatalf("PinKey bob: %v", err)
	}
	// dave: pinned + verified BUT RETIRED → must be excluded (#9a).
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_dave", DisplayName: "Dave"})
	if err := st.PinKey("usr_dave", "SHA256:d", "ssh-ed25519 d"); err != nil {
		t.Fatalf("PinKey dave: %v", err)
	}
	if err := st.MarkVerified("usr_dave"); err != nil {
		t.Fatalf("MarkVerified dave: %v", err)
	}
	client.SetRetiredForTesting(c, "usr_dave", "2026-05-20T00:00:00Z")
	// self: pinned + verified → must NOT appear in own list.
	if err := st.PinKey("usr_self", "SHA256:s", "ssh-ed25519 s"); err != nil {
		t.Fatalf("PinKey self: %v", err)
	}
	if err := st.MarkVerified("usr_self"); err != nil {
		t.Fatalf("MarkVerified self: %v", err)
	}

	got := a.unverifyCandidates()
	if len(got) != 1 || got[0].ID != "usr_alice" {
		ids := make([]string, len(got))
		for i, it := range got {
			ids[i] = it.ID
		}
		t.Fatalf("unverifyCandidates = %v, want exactly [usr_alice] (unverified/retired/self excluded)", ids)
	}
}

// --- Mutating verbs (§9 step 5: /add /kick /promote /demote /transfer) ---

// Each mutating verb shares the same bare-flow contract:
//   - bare outside a group → friendly status, no picker
//   - bare in a group as non-admin → friendly status, no picker
//   - bare in a group as admin → picker opens with right req
// Driven through `handleSlashCommand` (the real dispatch), but via a
// constructed SlashCommandMsg since the router-level coverage is
// `TestInputParser_GroupAdminRoutesToAppInAllForms` below.

func mutatingVerbs() []string {
	return []string{"/add", "/kick", "/promote", "/demote", "/transfer"}
}

// setupGroupAsAdmin seeds c so usr_self is a member + admin of g_x,
// alongside alice (non-admin member) and bob (non-member candidate
// for /add). Returns nothing — caller uses the names directly.
func setupGroupAsAdmin(t *testing.T, c *client.Client) {
	t.Helper()
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
	client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_self"})
}

func TestMutatingPicker_BareOutsideGroupStatusOnly(t *testing.T) {
	for _, verb := range mutatingVerbs() {
		a, _, _ := newPickerWhoisApp(t)
		a.handleSlashCommand(&SlashCommandMsg{Command: verb})
		if a.picker.IsVisible() {
			t.Errorf("%s bare outside group must NOT open the picker", verb)
		}
		if !strings.Contains(a.statusBar.errorMsg, "only works inside a group") {
			t.Errorf("%s expected 'only works inside a group' status, got %q", verb, a.statusBar.errorMsg)
		}
	}
}

func TestMutatingPicker_BareNonAdminStatusOnly(t *testing.T) {
	for _, verb := range mutatingVerbs() {
		a, c, _ := newPickerWhoisApp(t)
		// self is a member but NOT an admin
		client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
		client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_alice"})
		a.handleSlashCommand(&SlashCommandMsg{Command: verb, Group: "g_x"})
		if a.picker.IsVisible() {
			t.Errorf("%s bare as non-admin must NOT open the picker", verb)
		}
		if !strings.Contains(a.statusBar.errorMsg, "not an admin") {
			t.Errorf("%s expected 'not an admin' status, got %q", verb, a.statusBar.errorMsg)
		}
	}
}

func TestMutatingPicker_BareInGroupAsAdminOpensPicker(t *testing.T) {
	for _, verb := range mutatingVerbs() {
		a, c, _ := newPickerWhoisApp(t)
		setupGroupAsAdmin(t, c)
		a.handleSlashCommand(&SlashCommandMsg{Command: verb, Group: "g_x"})
		if !a.picker.IsVisible() {
			t.Errorf("%s bare in group as admin MUST open the picker", verb)
			continue
		}
		if a.picker.req.Verb != verb || a.picker.req.Group != "g_x" || !a.picker.req.ShowFilter {
			t.Errorf("%s picker req = {%q,%q,filter=%v}, want {%q,g_x,true}",
				verb, a.picker.req.Verb, a.picker.req.Group, a.picker.req.ShowFilter, verb)
		}
	}
}

// Picker selection routes each verb to the right existing XConfirm
// via the new *ConfirmForTarget helpers. /add → addConfirm,
// /kick → kickConfirm, /promote → promoteConfirm, /demote → demoteConfirm,
// /transfer → transferConfirm.
func TestMutatingPicker_SelectionOpensRightConfirm(t *testing.T) {
	cases := []struct {
		verb            string
		selectedID      string
		extraSetup      func(c *client.Client) // e.g. make selected an admin for /demote
		wantVisibleFn   func(a App) bool
		wantVisibleName string
	}{
		{"/add", "usr_bob", nil, func(a App) bool { return a.addConfirm.IsVisible() }, "addConfirm"},
		{"/kick", "usr_alice", nil, func(a App) bool { return a.kickConfirm.IsVisible() }, "kickConfirm"},
		{"/promote", "usr_alice", nil, func(a App) bool { return a.promoteConfirm.IsVisible() }, "promoteConfirm"},
		{"/demote", "usr_alice", func(c *client.Client) {
			// alice must be an admin for /demote to open the confirm.
			client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
		}, func(a App) bool { return a.demoteConfirm.IsVisible() }, "demoteConfirm"},
		{"/transfer", "usr_alice", nil, func(a App) bool { return a.transferConfirm.IsVisible() }, "transferConfirm"},
	}
	for _, tc := range cases {
		a, c, _ := newPickerWhoisApp(t)
		setupGroupAsAdmin(t, c)
		if tc.extraSetup != nil {
			tc.extraSetup(c)
		}
		model, _ := (*a).Update(PickerSelectedMsg{
			Request:    PickerRequest{Verb: tc.verb, Source: PickerSourceSlash, Group: "g_x"},
			SelectedID: tc.selectedID,
		})
		na := model.(App)
		if !tc.wantVisibleFn(na) {
			t.Errorf("%s picker selection should open %s, but it is not visible (status=%q)",
				tc.verb, tc.wantVisibleName, na.statusBar.errorMsg)
		}
	}
}

// Candidate builders — each verb has a different inclusion rule;
// one focused test per builder.

func TestAddCandidates_ExcludesMembersSelfRetired(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"}) // member → excluded
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})     // non-member → eligible
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_carol", DisplayName: "Carol"}) // non-member but retired
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
	client.SetRetiredForTesting(c, "usr_carol", "2026-05-20T00:00:00Z")

	got := a.addCandidates("g_x")
	if len(got) != 1 || got[0].ID != "usr_bob" {
		ids := make([]string, len(got))
		for i, it := range got {
			ids[i] = it.ID
		}
		t.Fatalf("addCandidates = %v, want [usr_bob] (members/self/retired excluded)", ids)
	}
}

func TestKickCandidates_MembersExcludeSelfRetired(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice", "usr_bob"})
	client.SetRetiredForTesting(c, "usr_bob", "2026-05-20T00:00:00Z")
	got := a.kickCandidates("g_x")
	if len(got) != 1 || got[0].ID != "usr_alice" {
		t.Fatalf("kickCandidates = %v, want [usr_alice] only (self + retired excluded)", got)
	}
}

func TestPromoteCandidates_NonAdminsExcludeSelfRetired(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"}) // member, non-admin → eligible
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})     // member, already admin → excluded
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice", "usr_bob"})
	client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_self", "usr_bob"})
	got := a.promoteCandidates("g_x")
	if len(got) != 1 || got[0].ID != "usr_alice" {
		t.Fatalf("promoteCandidates = %v, want [usr_alice] only (admins + self excluded)", got)
	}
}

func TestDemoteCandidates_AdminsIncludeSelfExcludeRetired(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_self", DisplayName: "Zelf"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})
	client.SetGroupMembersForTesting(c, "g_x", []string{"usr_self", "usr_alice", "usr_bob"})
	client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_self", "usr_alice", "usr_bob"})
	client.SetRetiredForTesting(c, "usr_bob", "2026-05-20T00:00:00Z")
	got := a.demoteCandidates("g_x")
	// Self IS included (self-demote is valid). bob retired → excluded.
	if len(got) != 2 {
		t.Fatalf("demoteCandidates = %v, want 2 (self + alice; bob retired excluded)", got)
	}
	hasSelf := false
	for _, it := range got {
		if it.ID == "usr_self" {
			hasSelf = true
		}
		if it.ID == "usr_bob" {
			t.Fatal("demoteCandidates must exclude retired admin")
		}
	}
	if !hasSelf {
		t.Fatal("demoteCandidates MUST include self (self-demote is permitted; last-admin guard applies via DemoteConfirm)")
	}
}

// §9 step 6: InfoPanelAdminKeyMsg routes per-verb. `/add` opens the
// shared picker (target is not in the panel list); `r/p/x` invoke
// the matching ConfirmForTarget for the highlighted member. App
// owns all routing; the panel stayed dumb (just emitted the msg).
func TestInfoPanelAdminKeyMsg_Routing(t *testing.T) {
	cases := []struct {
		verb            string
		targetID        string
		setup           func(t *testing.T, c *client.Client)
		wantVisibleFn   func(a App) bool
		wantVisibleName string
	}{
		{"/add", "", setupGroupAsAdmin,
			func(a App) bool { return a.picker.IsVisible() && a.picker.req.Verb == "/add" && a.picker.req.Source == PickerSourceInfoPanel },
			"picker (Source=info_panel, Verb=/add)"},
		{"/kick", "usr_alice", setupGroupAsAdmin,
			func(a App) bool { return a.kickConfirm.IsVisible() }, "kickConfirm"},
		{"/promote", "usr_alice", setupGroupAsAdmin,
			func(a App) bool { return a.promoteConfirm.IsVisible() }, "promoteConfirm"},
		{"/demote", "usr_alice", func(t *testing.T, c *client.Client) {
			setupGroupAsAdmin(t, c)
			// alice must be an admin for /demote to open the confirm.
			client.SetGroupAdminsForTesting(c, "g_x", []string{"usr_self", "usr_alice"})
		}, func(a App) bool { return a.demoteConfirm.IsVisible() }, "demoteConfirm"},
	}
	for _, tc := range cases {
		a, c, _ := newPickerWhoisApp(t)
		tc.setup(t, c)
		model, _ := (*a).Update(InfoPanelAdminKeyMsg{Verb: tc.verb, Group: "g_x", TargetID: tc.targetID})
		na := model.(App)
		if !tc.wantVisibleFn(na) {
			t.Errorf("InfoPanelAdminKeyMsg{%q} did not route to %s (statusBar=%q)",
				tc.verb, tc.wantVisibleName, na.statusBar.errorMsg)
		}
	}
}

// --- §9 step 7: member-panel "Add to existing group" ---

// setupGroupsForAddToGroup wires sidebar + client so the local user
// (usr_self) is admin of g_admin1+g_admin2, member-only of g_member,
// with the given members per group.
func setupGroupsForAddToGroup(t *testing.T, c *client.Client, a *App, members map[string][]string, adminGroups []string) {
	t.Helper()
	a.sidebar = NewSidebar()
	var groups []protocol.GroupInfo
	for gid, ms := range members {
		groups = append(groups, protocol.GroupInfo{ID: gid, Name: gid, Members: ms})
		client.SetGroupMembersForTesting(c, gid, ms)
	}
	a.sidebar.SetGroups(groups)
	for _, gid := range adminGroups {
		client.SetGroupAdminsForTesting(c, gid, []string{"usr_self"})
	}
}

func TestAddToGroupCandidates_OnlyEligibleGroups(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	// g_a: self admin, bob not a member → ELIGIBLE.
	// g_b: self admin, bob already a member → excluded (rule 3).
	// g_c: self NOT admin → excluded (rule 1).
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{
			"g_a": {"usr_self", "usr_alice"},
			"g_b": {"usr_self", "usr_bob"},
			"g_c": {"usr_carol", "usr_bob"},
		},
		[]string{"g_a", "g_b"},
	)
	got := a.addToGroupCandidates("usr_bob")
	if len(got) != 1 || got[0].ID != "g_a" {
		ids := make([]string, len(got))
		for i, it := range got {
			ids[i] = it.ID
		}
		t.Fatalf("addToGroupCandidates(usr_bob) = %v, want [g_a] (non-admin g_c + already-member g_b excluded)", ids)
	}
}

func TestAddToGroupCandidates_SelfAndRetiredReturnNil(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	if got := a.addToGroupCandidates("usr_self"); got != nil {
		t.Fatalf("addToGroupCandidates(self) = %v, want nil (rule 4: self)", got)
	}
	client.SetRetiredForTesting(c, "usr_dave", "2026-05-20T00:00:00Z")
	if got := a.addToGroupCandidates("usr_dave"); got != nil {
		t.Fatalf("addToGroupCandidates(retired) = %v, want nil (rule 4: retired)", got)
	}
}

func TestBuildMemberMenuItems_PrependsAddToGroupWhenEligible(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	items := a.buildMemberMenuItems("usr_bob", "Bob")
	// Default 4 + "Add to group..." prepended at position 0 = 5 total.
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5 (add-to-group + 4 default)", len(items))
	}
	if items[0].Action != "add_to_existing_group" {
		t.Fatalf("items[0] Action = %q, want add_to_existing_group (top-of-list placement)", items[0].Action)
	}
	// The 4 defaults follow in their original order.
	wantOrder := []string{"add_to_existing_group", "create_group", "message", "verify", "profile"}
	for i, want := range wantOrder {
		if items[i].Action != want {
			t.Fatalf("items[%d] Action = %q, want %q (full order)", i, items[i].Action, want)
		}
	}
}

func TestBuildMemberMenuItems_HidesAddToGroupWhenNoEligible(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	// No groups set up → no eligible groups → action hidden.
	items := a.buildMemberMenuItems("usr_bob", "Bob")
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4 (default only — no eligible groups)", len(items))
	}
	for _, it := range items {
		if it.Action == "add_to_existing_group" {
			t.Fatal("add_to_existing_group must NOT appear when no eligible groups exist")
		}
	}
}

func TestAddToGroupAction_OpensPickerWithSubject(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})

	model, _ := (*a).Update(MemberActionMsg{Action: "add_to_existing_group", User: "usr_bob"})
	na := model.(App)
	if !na.picker.IsVisible() {
		t.Fatal("MemberActionMsg{add_to_existing_group} must open the shared picker")
	}
	if na.picker.req.Verb != "add_to_group" || na.picker.req.Source != PickerSourceMemberPanel {
		t.Fatalf("picker req = {Verb:%q Source:%q}, want {add_to_group, member_panel}",
			na.picker.req.Verb, na.picker.req.Source)
	}
	if na.picker.req.SubjectUserID != "usr_bob" {
		t.Fatalf("picker req.SubjectUserID = %q, want usr_bob (carries the user being added)", na.picker.req.SubjectUserID)
	}
}

// After a successful /add, the local user is focused on the target
// group IF they weren't already viewing it. Self-correcting across
// all add paths: typed /add and the bare-/add pickers leave you in
// the group you're already in (no-op); the member-panel
// "Add to existing group" flow lands you in the just-added group.
func TestAddConfirm_FocusesTargetGroupWhenDifferent(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	var enc bytes.Buffer
	client.SetEncoderForTesting(c, protocol.NewEncoder(&enc))
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	a.messages = NewMessages()
	a.messages.SetContext("room_other", "", "") // user is viewing a ROOM, not g_a

	model, _ := (*a).Update(AddConfirmMsg{Group: "g_a", TargetID: "usr_bob"})
	na := model.(App)
	if na.messages.group != "g_a" {
		t.Fatalf("after AddConfirmMsg, messages.group = %q, want g_a (focus should switch to the target group)", na.messages.group)
	}
}

func TestAddConfirm_NoFocusChangeWhenAlreadyInTargetGroup(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	var enc bytes.Buffer
	client.SetEncoderForTesting(c, protocol.NewEncoder(&enc))
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	a.messages = NewMessages()
	a.messages.SetContext("", "g_a", "") // already in g_a (typed /add path)

	model, _ := (*a).Update(AddConfirmMsg{Group: "g_a", TargetID: "usr_bob"})
	na := model.(App)
	if na.messages.group != "g_a" {
		t.Fatalf("focus should stay on g_a, got messages.group=%q", na.messages.group)
	}
	// And we should not have stepped through an unnecessary
	// SetContext switch (room/dm fields unchanged).
	if na.messages.room != "" || na.messages.dm != "" {
		t.Fatalf("unrelated context fields drifted: room=%q dm=%q", na.messages.room, na.messages.dm)
	}
}

func TestAddToGroupPicker_SelectionOpensAddConfirm(t *testing.T) {
	a, c, _ := newPickerWhoisApp(t)
	setupGroupsForAddToGroup(t, c, a,
		map[string][]string{"g_a": {"usr_self"}},
		[]string{"g_a"},
	)
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_bob", DisplayName: "Bob"})

	// Picker selection: picked GROUP id "g_a"; subject user carried
	// in Request.SubjectUserID. Note the OPPOSITE id semantics vs
	// slash /add (where SelectedID would be the user, not the group).
	model, _ := (*a).Update(PickerSelectedMsg{
		Request: PickerRequest{
			Verb:            "add_to_group",
			Source:          PickerSourceMemberPanel,
			SubjectUserID:   "usr_bob",
			SubjectUserName: "Bob",
		},
		SelectedID: "g_a",
	})
	na := model.(App)
	if !na.addConfirm.IsVisible() {
		t.Fatal("picker selection must open AddConfirmModel for the picked group + subject user")
	}
}

// --- /mute (Mode-A fix: previously an empty router case = silent no-op) ---

func TestMute_RouterForwardsWithActiveContext(t *testing.T) {
	cases := []struct {
		text, room, group, dm    string
		wantRoom, wantGroup, wantDM string
	}{
		{"/mute", "room_x", "", "", "room_x", "", ""},
		{"/mute", "", "group_x", "", "", "group_x", ""},
		{"/mute", "", "", "dm_x", "", "", "dm_x"},
		{"/mute", "", "", "", "", "", ""}, // no context — App surfaces friendly status
	}
	for _, tc := range cases {
		i := &InputModel{}
		recognized := i.handleCommand(tc.text, nil, tc.room, tc.group, tc.dm)
		if !recognized {
			t.Errorf("%q with context {room=%q group=%q dm=%q} should be RECOGNIZED", tc.text, tc.room, tc.group, tc.dm)
			continue
		}
		sc := i.PendingCommand()
		if sc == nil || sc.Command != "/mute" || sc.Room != tc.wantRoom || sc.Group != tc.wantGroup || sc.DM != tc.wantDM {
			t.Errorf("%q in {room=%q group=%q dm=%q} routed as %+v, want context preserved", tc.text, tc.room, tc.group, tc.dm, sc)
		}
	}
}

func TestMute_ToggleInActiveContext(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.muted = make(map[string]bool)

	// First /mute in a room → muted=true, status confirms.
	a.handleSlashCommand(&SlashCommandMsg{Command: "/mute", Room: "room_x"})
	if !a.muted["room_x"] {
		t.Fatal("first /mute should set muted[room_x]=true")
	}
	if !strings.HasPrefix(a.statusBar.errorMsg, "Muted:") {
		t.Fatalf("status should confirm muted, got %q", a.statusBar.errorMsg)
	}

	// Second /mute on same context → unmuted=false, status flips.
	a.handleSlashCommand(&SlashCommandMsg{Command: "/mute", Room: "room_x"})
	if a.muted["room_x"] {
		t.Fatal("second /mute on muted target should unmute")
	}
	if !strings.HasPrefix(a.statusBar.errorMsg, "Unmuted:") {
		t.Fatalf("status should confirm unmuted, got %q", a.statusBar.errorMsg)
	}
}

func TestMute_NoActiveContextStatusOnly(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.muted = make(map[string]bool)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/mute"}) // no room/group/dm
	if !strings.Contains(a.statusBar.errorMsg, "needs an active") {
		t.Fatalf("expected friendly status for /mute with no context, got %q", a.statusBar.errorMsg)
	}
	if len(a.muted) != 0 {
		t.Fatalf("no-context /mute must not mutate the muted map, got %v", a.muted)
	}
}

// --- /rename (Mode-B fix: bare-no-arg/non-group now surfaces friendly status) ---

func TestRename_RouterForwardsAllForms(t *testing.T) {
	cases := []struct {
		text, group, room string
		wantArg, wantGroup string
	}{
		{"/rename", "group_x", "", "", "group_x"},          // bare in group
		{"/rename NewName", "group_x", "", "NewName", "group_x"}, // typed in group
		{"/rename", "", "room_x", "", ""},                   // bare in room
		{"/rename NewName", "", "room_x", "NewName", ""},   // typed in room
	}
	for _, tc := range cases {
		i := &InputModel{}
		recognized := i.handleCommand(tc.text, nil, tc.room, tc.group, "")
		if !recognized {
			t.Errorf("%q must be recognized", tc.text)
			continue
		}
		sc := i.PendingCommand()
		if sc == nil || sc.Command != "/rename" || sc.Arg != tc.wantArg || sc.Group != tc.wantGroup {
			t.Errorf("%q in {group=%q room=%q} routed as %+v, want forward-with-context", tc.text, tc.group, tc.room, sc)
		}
	}
}

func TestRename_BareInGroupShowsUsage(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/rename", Group: "group_x"})
	if !strings.Contains(a.statusBar.errorMsg, "Usage: /rename <name>") {
		t.Fatalf("bare /rename in group should show usage, got %q", a.statusBar.errorMsg)
	}
}

func TestRename_NonGroupShowsOnlyWorksInside(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/rename", Arg: "NewName", Room: "room_x"})
	if !strings.Contains(a.statusBar.errorMsg, "only works inside a group") {
		t.Fatalf("/rename in non-group should surface friendly status, got %q", a.statusBar.errorMsg)
	}
}

// --- Mode-A router robustness (the original /typing bug class) ---

func TestRouter_UnknownCommandEmitsUnrecognizedAndReturnsFalse(t *testing.T) {
	i := &InputModel{}
	recognized := i.handleCommand("/notarealverb hello", nil, "room_x", "", "")
	if recognized {
		t.Fatal("unknown verb must return false from handleCommand")
	}
	sc := i.PendingCommand()
	if sc == nil || sc.Command != "/unrecognized" || sc.Arg != "/notarealverb" {
		t.Fatalf("unknown verb pendingCmd = %+v, want {/unrecognized, Arg: /notarealverb}", sc)
	}
	if sc.Room != "room_x" {
		t.Errorf("context should be forwarded, got Room=%q", sc.Room)
	}
}

func TestRouter_KnownCommandsReturnTrue(t *testing.T) {
	// Drive a canonical sample of router-known verbs and assert each
	// is recognized. The list is hand-maintained — the cost is that
	// a verb added to handleCommand without being added here goes
	// untested by this drift-guard. The benefit is catching the
	// classic "verb in completion/help but not in router" bug
	// (which is precisely how /typing was silently swallowed).
	for _, text := range []string{
		"/mute", "/typing", "/typing on", "/leave", "/delete", "/undo", "/whoami",
		"/info", "/members", "/groupinfo", "/settings", "/pending", "/mykey",
		"/admins", "/audit", "/help", "/?", "/verify @x", "/unverify @x",
		"/whois @x", "/role @x", "/role", "/add @x", "/kick @x", "/promote @x",
		"/demote @x", "/transfer @x", "/rename NewName", "/rename",
		"/setstatus", "/setstatus away", "/search query", "/upload /tmp/x",
		"/groupcreate", "/groupcreate @a @b", "/dmcreate", "/dmcreate @a",
		"/topic", "/topic new text",
	} {
		i := &InputModel{}
		if !i.handleCommand(text, nil, "room_x", "group_x", "dm_x") {
			t.Errorf("known verb %q should be recognized — router gap?", text)
		}
	}
}

func TestUnrecognized_AppSurfacesStatusWithTypo(t *testing.T) {
	a, _, _ := newPickerWhoisApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/unrecognized", Arg: "/notarealverb"})
	if !strings.Contains(a.statusBar.errorMsg, "Unknown command: /notarealverb") {
		t.Fatalf("expected status to name the typo, got %q", a.statusBar.errorMsg)
	}
	if !strings.Contains(a.statusBar.errorMsg, "/?") {
		t.Fatalf("status should point at /? for the list, got %q", a.statusBar.errorMsg)
	}
}

func TestRouter_DontResetInputOnUnknownCommand(t *testing.T) {
	// The `enter` branch in Update reads handleCommand's bool to
	// decide whether to reset the input. This test asserts the
	// invariant directly: with the input field populated, processing
	// an unknown verb leaves the text in place. Drift-guard for
	// "user has to retype the entire line to fix a typo".
	i := NewInput()
	i.textInput.SetValue("/notarealverb hello") // simulate the typed line
	recognized := i.handleCommand(i.textInput.Value(), nil, "", "", "")
	if recognized {
		t.Fatal("precondition: unknown verb should return false")
	}
	// The `enter` branch only resets when recognized; this test
	// codifies that contract at the boolean level so the conditional
	// reset can be verified without re-running the full key handler.
	if i.textInput.Value() == "" {
		t.Fatal("handleCommand must not touch the input itself; the enter branch is responsible (and skips reset on !recognized)")
	}
}

// TestInputParser_GroupAdminRoutesToAppInAllForms exercises the REAL
// router path for /add /kick /promote /demote /transfer — the
// regression guard for the same router-guard gap that bit /role.
// The previous `if group != "" && arg != ""` silently dropped bare
// forms; this test would have failed against that.
func TestInputParser_GroupAdminRoutesToAppInAllForms(t *testing.T) {
	for _, verb := range mutatingVerbs() {
		cases := []struct {
			text, group, room, dm string
			wantArg, wantGroup    string
		}{
			{verb, "", "", "", "", ""},                       // bare outside group
			{verb, "group_x", "", "", "", "group_x"},         // bare in group
			{verb + " @alice", "group_x", "", "", "@alice", "group_x"}, // typed in group
			{verb + " @alice", "", "room_x", "", "@alice", ""},         // typed outside group
		}
		for _, tc := range cases {
			i := &InputModel{}
			i.handleCommand(tc.text, nil, tc.room, tc.group, tc.dm)
			sc := i.PendingCommand()
			if sc == nil {
				t.Errorf("%q in {group=%q room=%q} produced no pendingCmd — router still guarding bare/non-group", tc.text, tc.group, tc.room)
				continue
			}
			if sc.Command != verb || sc.Arg != tc.wantArg || sc.Group != tc.wantGroup {
				t.Errorf("%q in {group=%q room=%q} routed as {Command:%q Arg:%q Group:%q}, want {%q %q %q}",
					tc.text, tc.group, tc.room, sc.Command, sc.Arg, sc.Group, verb, tc.wantArg, tc.wantGroup)
			}
		}
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
