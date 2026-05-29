package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInfoPanel_ExplicitCopyKeysNoAutoCopy locks the 2026-05-30 UX change:
// the identity panel no longer auto-copies the public key on open (clobbering
// the clipboard on a view is surprising), and copying is explicit via `c`
// (public key) / `f` (fingerprint) — both re-copyable, fixing the one-way `f`
// door the panel had before.
func TestInfoPanel_ExplicitCopyKeysNoAutoCopy(t *testing.T) {
	i := InfoPanelModel{
		visible:         true,
		isUser:          true,
		userID:          "usr_alice",
		userPubKey:      "ssh-ed25519 AAAATESTKEY alice",
		userFingerprint: "SHA256:testfp",
	}

	// No auto-copy on open: the panel starts with no clipboard notice.
	if i.userClipboardNotice != "" {
		t.Fatalf("fresh panel should have no clipboard notice (no auto-copy), got %q", i.userClipboardNotice)
	}

	key := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

	// `c` copies the public key.
	i, _ = i.Update(key('c'))
	if i.userClipboardNotice != "Public key copied to clipboard." {
		t.Fatalf("after c: notice = %q, want public-key copied", i.userClipboardNotice)
	}

	// `f` copies the fingerprint (swaps the notice).
	i, _ = i.Update(key('f'))
	if i.userClipboardNotice != "Fingerprint copied to clipboard." {
		t.Fatalf("after f: notice = %q, want fingerprint copied", i.userClipboardNotice)
	}

	// `c` again switches back to the public key — the reversibility the
	// one-way `f` lacked before.
	i, _ = i.Update(key('c'))
	if i.userClipboardNotice != "Public key copied to clipboard." {
		t.Fatalf("after second c: notice = %q, want public-key copied (re-copyable)", i.userClipboardNotice)
	}
}
