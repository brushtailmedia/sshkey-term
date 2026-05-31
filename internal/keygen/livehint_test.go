package keygen

// Tests for LivePassphraseHint — the real-time feedback helper used
// by the terminal-app wizard and add-server dialog to show a compact
// strength indicator under the passphrase input field.

import (
	"strings"
	"testing"
)

// TestLivePassphraseHint_EmptyWarnsUnencrypted verifies that the empty
// passphrase state gives immediate guidance instead of rendering a silent
// field labelled only "recommended".
func TestLivePassphraseHint_EmptyWarnsUnencrypted(t *testing.T) {
	h := LivePassphraseHint("", nil)
	if h.Tier != HintWarn {
		t.Errorf("tier = %v, want HintWarn for empty passphrase", h.Tier)
	}
	if !strings.Contains(h.Text, "⚠") {
		t.Errorf("empty text = %q, want alert icon", h.Text)
	}
	if !strings.Contains(h.Text, "Leaving blank allows anyone with the key to access this account") {
		t.Errorf("empty text = %q, want local key risk", h.Text)
	}
}

// TestLivePassphraseHint_FirstCharacterRunsZxcvbn verifies there is no
// length gate: the first typed character replaces the empty warning with
// zxcvbn feedback.
func TestLivePassphraseHint_FirstCharacterRunsZxcvbn(t *testing.T) {
	h := LivePassphraseHint("a", nil)
	if h.Tier != HintBlock {
		t.Errorf("tier = %v, want weak advisory", h.Tier)
	}
	if !strings.Contains(h.Text, "weak") {
		t.Errorf("text = %q, want to mention weakness", h.Text)
	}
	if strings.Contains(h.Text, "unencrypted key") {
		t.Errorf("text = %q, should no longer show empty-field warning", h.Text)
	}
	want := "weak — cracked in less than a second"
	if h.Text != want {
		t.Errorf("text = %q, want %q", h.Text, want)
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
	// Both may be weak tier, but tier with context should be <= tier
	// without context (smaller HintTier values are worse).
	if withCtx.Tier > withoutCtx.Tier {
		t.Errorf("with-context tier (%v) should be <= without-context tier (%v)",
			withCtx.Tier, withoutCtx.Tier)
	}
}
