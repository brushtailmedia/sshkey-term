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

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
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
