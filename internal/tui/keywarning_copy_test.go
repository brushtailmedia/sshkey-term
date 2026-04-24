package tui

// Phase 21 F3.c + F3.d + F28 drift-guard tests.
//
// F3.c: the KeyWarningAcceptMsg handler must include a /verify nudge
//       in the post-accept status-bar message so users who want to
//       verify have a clear next step.
//
// F3.d: KeyWarningModel.View() must NOT say "the user's key was
//       rotated" — under the no-rotation protocol invariant that
//       language is false (see PROTOCOL.md "Keys as Identities").
//       It must say something pointing at the real attack classes
//       (compromised server, server bug, DB tampering).
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

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// -------- F3.d modal copy --------

// TestKeyWarningModal_NoRotationFraming drift-guards the Phase 21
// F3.d closure — the modal must not tell users the key "was rotated"
// (false by protocol design) and must surface the real attack classes.
func TestKeyWarningModal_NoRotationFraming(t *testing.T) {
	var m KeyWarningModel
	m.Show("alice", "SHA256:old", "SHA256:new")
	out := m.View(80)

	for _, banned := range []string{"was rotated", "been rotated", "key rotation"} {
		if strings.Contains(out, banned) {
			t.Errorf("modal copy contains banned phrase %q (conflicts with no-rotation invariant):\n%s",
				banned, out)
		}
	}

	for _, required := range []string{
		"Keys do not rotate",      // explicit invariant statement
		"compromised server",      // attack class 1
		"server bug",              // attack class 2
		"tampering",               // attack class 3
		"different user ID",       // points at the legitimate new-key flow
		"Old:",                    // fingerprint labels
		"New:",                    // fingerprint labels
		"SHA256:old",              // old fingerprint rendered
		"SHA256:new",              // new fingerprint rendered
		"[a] Accept new key",      // choice 1 still present
		"[d] Disconnect",          // choice 2 still present
	} {
		if !strings.Contains(out, required) {
			t.Errorf("modal copy missing required text %q:\n%s", required, out)
		}
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

// -------- F3.c accept-confirmation nudge --------

// TestKeyWarningAccept_StatusBarIncludesVerifyNudge verifies the
// Phase 21 F3.c closure: the accept-confirmation status-bar message
// points users at /verify so they have a clear next step.
func TestKeyWarningAccept_StatusBarIncludesVerifyNudge(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenUnencrypted(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:        "usr_alice",
		DisplayName: "Alice",
	})

	a := App{
		client:    c,
		statusBar: NewStatusBar(),
	}
	// App.Update is a value-receiver method — it mutates a copy and
	// returns the updated model. Capture and re-bind to read the
	// post-dispatch statusBar state.
	updated, _ := a.Update(KeyWarningAcceptMsg{User: "usr_alice"})
	a = updated.(App)

	msg := a.statusBar.errorMsg
	for _, required := range []string{
		"Alice",                           // resolves display name, not raw userID
		"/verify",                         // nudge points at the verify command
		"safety number",                   // explains what /verify will do
	} {
		if !strings.Contains(msg, required) {
			t.Errorf("status bar missing %q; got %q", required, msg)
		}
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
