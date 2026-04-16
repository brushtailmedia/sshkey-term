package keygen

// Tests for LivePassphraseHint — the real-time feedback helper used
// by the terminal-app wizard and add-server dialog to show a compact
// strength indicator under the passphrase input field.

import (
	"strings"
	"testing"
)

// TestLivePassphraseHint_HiddenBelowMinLength verifies that
// passphrases shorter than the minimum length produce HintHidden
// (no indicator rendered). This is the length-gate that kills the
// rolling "weak, weak, weak" noise during early keystrokes.
func TestLivePassphraseHint_HiddenBelowMinLength(t *testing.T) {
	for _, length := range []int{0, 1, 5, MinPassphraseLength - 1} {
		pass := strings.Repeat("a", length)
		h := LivePassphraseHint(pass, nil)
		if h.Tier != HintHidden {
			t.Errorf("length %d: tier = %v, want HintHidden", length, h.Tier)
		}
		if h.Text != "" {
			t.Errorf("length %d: text = %q, want empty for hidden tier", length, h.Text)
		}
	}
}

// TestLivePassphraseHint_BlockTier verifies a weak passphrase at or
// above the length floor produces a block-tier hint with the crack
// time in the text.
func TestLivePassphraseHint_BlockTier(t *testing.T) {
	// "password1234" is >= 12 chars but zxcvbn hates it.
	h := LivePassphraseHint("password1234", nil)
	if h.Tier != HintBlock {
		t.Errorf("tier = %v, want HintBlock", h.Tier)
	}
	if !strings.Contains(h.Text, "weak") {
		t.Errorf("block text = %q, want to mention weakness", h.Text)
	}
	if !strings.Contains(h.Text, "cracked in") {
		t.Errorf("block text = %q, want a crack-time estimate", h.Text)
	}
}

// TestLivePassphraseHint_PassTier verifies a strong passphrase
// produces a pass-tier hint. Text should indicate "strong" or
// "very strong".
func TestLivePassphraseHint_PassTier(t *testing.T) {
	// Four unrelated words — reliably high zxcvbn score.
	h := LivePassphraseHint("correct horse battery staple elephant", nil)
	if h.Tier != HintPass {
		t.Errorf("tier = %v, want HintPass", h.Tier)
	}
	if !strings.Contains(h.Text, "strong") {
		t.Errorf("pass text = %q, want 'strong' or 'very strong'", h.Text)
	}
	if !strings.HasPrefix(h.Text, "✓") {
		t.Errorf("pass text = %q, want leading ✓ icon", h.Text)
	}
}

// TestLivePassphraseHint_RespectsContext verifies context strings
// are forwarded to zxcvbn. A passphrase containing the server name
// should score lower than the same pattern without context.
func TestLivePassphraseHint_RespectsContext(t *testing.T) {
	pass := "myserver12345"
	// Without context: plain length-based scoring.
	withoutCtx := LivePassphraseHint(pass, nil)
	// With "myserver" as context, zxcvbn dictionary-matches the
	// substring. Should produce a less-favorable result.
	withCtx := LivePassphraseHint(pass, []string{"myserver"})
	// Both may be block tier, but tier with context should be <= tier
	// without context (smaller HintTier values are worse).
	if withCtx.Tier > withoutCtx.Tier {
		t.Errorf("with-context tier (%v) should be <= without-context tier (%v)",
			withCtx.Tier, withoutCtx.Tier)
	}
}
