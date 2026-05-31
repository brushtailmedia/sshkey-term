package tui

// Phase 21 F3.c + F3.d + F28 drift-guard tests.
//
// F3.d: KeyWarningModel.View() must NOT say "the user's key was
//       rotated" — under the no-rotation protocol invariant that
//       language is false (see PROTOCOL.md "Keys as Identities").
//       It must say something pointing at the real attack classes
//       (server/state corruption or compromise) and must not provide
//       an accept-new-key path.
//
// F28:  The verified-badge rendering path uses lipgloss-styled ✓
//       glyphs across sidebar, infopanel, and memberpanel. These
//       tests lock in that the render paths consult the Verified
//       flag (the feature was pre-existing; this is a drift guard
//       that a future refactor doesn't strip the ✓ render paths).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

// -------- F3.d modal copy --------

// TestKeyWarningModal_NoRotationFraming drift-guards the Phase 21
// F3.d closure — the modal must not tell users the key "was rotated"
// (false by protocol design) and must surface the real attack classes.
func TestKeyWarningModal_NoRotationFraming(t *testing.T) {
	var m KeyWarningModel
	m.Show("alice", "SHA256:old", "SHA256:new")
	out := m.View(80)

	for _, banned := range []string{"was rotated", "been rotated", "key rotation", "Accept new key"} {
		if strings.Contains(out, banned) {
			t.Errorf("modal copy contains banned phrase %q (conflicts with no-rotation invariant):\n%s",
				banned, out)
		}
	}

	for _, required := range []string{
		"Account key changed for alice",
		"Account keys are immutable",
		"server/state corruption or compromise",
		"The changed key was not accepted",
		"retire the old account",
		"approve a new account",
		"Old:",           // fingerprint labels
		"New:",           // fingerprint labels
		"SHA256:old",     // old fingerprint rendered
		"SHA256:new",     // new fingerprint rendered
		"[d] Disconnect", // only action
	} {
		if !strings.Contains(out, required) {
			t.Errorf("modal copy missing required text %q:\n%s", required, out)
		}
	}
}

func TestKeyWarningModal_AcceptKeyIsNotMapped(t *testing.T) {
	var m KeyWarningModel
	m.Show("alice", "SHA256:old", "SHA256:new")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatal("immutable account-key warning must not accept a changed key")
	}
	if !updated.IsVisible() {
		t.Fatal("pressing a should leave the warning visible")
	}
}

// TestKeyWarningModal_HiddenRendersEmpty verifies the hidden-state
// View returns empty — prevents accidental always-render after a
// future refactor.
func TestKeyWarningModal_HiddenRendersEmpty(t *testing.T) {
	var m KeyWarningModel
	if out := m.View(80); out != "" {
		t.Errorf("hidden modal should render empty, got %q", out)
	}
}

// -------- F28 verified-badge render paths --------

// TestF28_SidebarRendersVerifiedBadge drift-guards the sidebar DM
// verified-badge render path. The sidebar checks resolveVerified and
// appends verifiedMarker when true.
func TestF28_SidebarRendersVerifiedBadge(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenUnencrypted(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Pin + verify Alice.
	st.PinKey("usr_alice", "SHA256:abc", "ssh-ed25519 A")
	st.MarkVerified("usr_alice")

	// Pin Bob but DO NOT verify.
	st.PinKey("usr_bob", "SHA256:def", "ssh-ed25519 B")

	s := NewSidebar()
	s.selfUserID = "usr_self"
	s.resolveName = func(uid string) string {
		switch uid {
		case "usr_alice":
			return "Alice"
		case "usr_bob":
			return "Bob"
		}
		return uid
	}
	s.resolveVerified = func(uid string) bool {
		_, verified, _ := st.GetPinnedKey(uid)
		return verified
	}
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_alice", Members: []string{"usr_self", "usr_alice"}},
		{ID: "dm_bob", Members: []string{"usr_self", "usr_bob"}},
	})

	out := s.View(40, 20, false)

	// Alice should have the ✓ glyph; Bob should not.
	aliceLine := ""
	bobLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Alice") {
			aliceLine = line
		}
		if strings.Contains(line, "Bob") {
			bobLine = line
		}
	}
	if !strings.Contains(aliceLine, "✓") {
		t.Errorf("verified Alice's sidebar line missing ✓:\n%q", aliceLine)
	}
	if strings.Contains(bobLine, "✓") {
		t.Errorf("unverified Bob's sidebar line should not have ✓:\n%q", bobLine)
	}
}

// TestF28_InfoPanelCarriesVerifiedFlag drift-guards the infopanel
// memberInfo.Verified field population — ShowRoom / ShowGroup / ShowDM
// must all set Verified from store.GetPinnedKey so the ✓ glyph renders
// in the member list.
func TestF28_InfoPanelCarriesVerifiedFlag(t *testing.T) {
	// Direct struct test — construct a memberInfo with Verified=true,
	// render it, assert ✓ appears. Guards against a refactor that
	// drops the render path at infopanel.go:426-428.
	m := memberInfo{
		User:        "usr_alice",
		DisplayName: "Alice",
		Verified:    true,
	}
	// The rendering happens in a closure inside View; we exercise it
	// indirectly via a minimal InfoPanelModel.
	p := InfoPanelModel{
		visible: true,
		isDM:    true,
		members: []memberInfo{m},
	}
	out := p.View(60)
	if !strings.Contains(out, "✓") {
		t.Errorf("infopanel render should include ✓ for verified member:\n%s", out)
	}
}
