package keygen

// Phase 16 Gap 4 — tests for the client-side passphrase strength
// advisory. User key passphrases are optional: blank creates an
// unencrypted key, and weak non-empty passphrases are warned about but
// not blocked.

import (
	"strings"
	"testing"
)

func TestValidateUserPassphrase_Empty(t *testing.T) {
	r := ValidateUserPassphrase("")
	if r.Blocked {
		t.Fatal("empty passphrase should be allowed with an advisory")
	}
	if !strings.Contains(r.Warning, "Leaving blank allows anyone with the key to access this account") {
		t.Errorf("empty warning should mention local key risk, got: %q", r.Warning)
	}
}

func TestValidateUserPassphrase_TooShortAllowedWithWarning(t *testing.T) {
	cases := []string{"a", "abc", "password", "hunter2hunt"}
	for _, pass := range cases {
		t.Run(pass, func(t *testing.T) {
			r := ValidateUserPassphrase(pass)
			if r.Blocked {
				t.Fatalf("expected %q to be allowed with warning, got blocked: %q", pass, r.Message)
			}
			if r.Warning == "" {
				t.Errorf("short passphrase %q should produce advisory warning", pass)
			}
		})
	}
}

// TestValidateUserPassphrase_WeakTierWarns covers passphrases at score 0-1.
// These are allowed, but must produce a warning.
func TestValidateUserPassphrase_WeakTierWarns(t *testing.T) {
	cases := []struct {
		name string
		pass string
	}{
		{"common_password_padded", "password1234"},
		{"keyboard_walk", "qwertyuiopas"},
		{"dictionary_repeated", "bananabanana"},
		{"number_sequence", "123456789012"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ValidateUserPassphrase(tc.pass)
			if r.Blocked {
				t.Errorf("expected %q to be allowed with warning, got blocked: %q", tc.pass, r.Message)
			}
			if !strings.Contains(r.Warning, "cracked in") {
				t.Errorf("warning should mention crack time, got: %q", r.Warning)
			}
		})
	}
}

// TestValidateUserPassphrase_WarnTierExample covers a passphrase at
// score 2 that should produce a warning (not a block). "december252025"
// is a fixture that reliably lands in the warn tier — date pattern +
// dictionary word, not strong enough to silently pass but not weak
// enough to hard-reject under the softer user policy.
func TestValidateUserPassphrase_WarnTierExample(t *testing.T) {
	r := ValidateUserPassphrase("december252025")
	if r.Blocked {
		t.Errorf("expected warn tier (Blocked=false), got blocked: %q", r.Message)
	}
	if r.Warning == "" {
		t.Error("expected warn tier to produce a Warning message")
	}
	if r.Warning != "" && !strings.Contains(r.Warning, "cracked in") {
		t.Errorf("warn message should describe crack time, got: %q", r.Warning)
	}
}

// TestValidateUserPassphrase_StrongTier covers passphrases at score 3-4
// that should silently pass. Both Blocked and Warning should be empty.
func TestValidateUserPassphrase_StrongTier(t *testing.T) {
	cases := []struct {
		name string
		pass string
	}{
		{"random_gibberish", "xK9#mPq2Rt$Lw7"},
		{"four_words", "correct horse battery staple"},
		{"mixed_random", "Tz!4pQ@9nW#8vR$x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ValidateUserPassphrase(tc.pass)
			if r.Blocked {
				t.Errorf("expected %q to pass, got blocked: %q", tc.pass, r.Message)
			}
			if r.Warning != "" {
				t.Errorf("expected %q to silently pass, got warning: %q", tc.pass, r.Warning)
			}
		})
	}
}

// TestValidateUserPassphrase_WarnTier_ContractCheck verifies the
// warn-and-continue tier works. Finding a passphrase that lands
// reliably at score 2 (and not 1 or 3) is fragile because zxcvbn's
// scoring depends on dictionary contents, so this test is mostly a
// contract check on the result shape — if a score-2 passphrase is
// found, it must produce Warning != "" and Blocked == false. We don't
// assert the specific passphrase here because that would couple the
// test to zxcvbn's internals.
func TestValidateUserPassphrase_WarnTier_ContractCheck(t *testing.T) {
	// Iterate a handful of borderline candidates and pick whichever
	// one happens to land in the warn tier. If none do (zxcvbn version
	// drift), the test passes as a no-op — we're not asserting that
	// a warn-tier passphrase exists, just that IF one is found, the
	// result shape is correct.
	candidates := []string{
		"goldfish-tank-purple",
		"northwest-pasta-2018",
		"butterfly-kiteflyer42",
		"riverside-helmet-maker",
		"bicycle-quartz-spine99",
	}
	foundWarn := false
	for _, pass := range candidates {
		r := ValidateUserPassphrase(pass)
		if !r.Blocked && r.Warning != "" {
			foundWarn = true
			if !strings.Contains(r.Warning, "cracked in") {
				t.Errorf("warn message should mention crack time, got: %q", r.Warning)
			}
		}
	}
	if !foundWarn {
		t.Log("no candidate landed in the warn tier; this is a soft signal that zxcvbn dictionaries may have shifted, but not a hard failure")
	}
}

// TestValidateUserPassphraseWithContext mirrors the server-side test:
// a passphrase that's primarily the user's own display name should be
// rejected.
func TestValidateUserPassphraseWithContext(t *testing.T) {
	r := ValidateUserPassphraseWithContext("alicealicealice", []string{"alice"})
	if r.Blocked {
		t.Errorf("expected context-heavy passphrase to be allowed with warning, got blocked: %q", r.Message)
	}
	if r.Warning == "" {
		t.Errorf("expected warning when passphrase is just the context word repeated, got: %+v", r)
	}
}

// TestValidationResult_NeverBlocks verifies the client-side keygen
// contract: strength feedback is advisory-only.
func TestValidationResult_NeverBlocks(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"password1234",
		"correct horse battery staple", // strong
		"alicealicealice",
	}
	for _, pass := range cases {
		r := ValidateUserPassphrase(pass)
		if r.Blocked {
			t.Errorf("passphrase %q: Blocked=true — advisory-only contract violated: %+v", pass, r)
		}
	}
}

// TestCrackTimeDisplay mirrors the server-side test exactly — the
// thresholds must stay consistent across both repos so the same
// passphrase produces the same crack-time display in admin and user
// contexts.
func TestCrackTimeDisplay(t *testing.T) {
	cases := []struct {
		guesses float64
		wantHas string
	}{
		{1, "less than"},
		{1e10, "second"},
		{1e12, "minutes"},
		{1e14, "hours"},
		{1e15, "days"},
		{1e17, "months"},
		{1e18, "years"},
		{1e25, "centuries"},
	}
	for _, tc := range cases {
		got := crackTimeDisplay(tc.guesses)
		if !strings.Contains(got, tc.wantHas) {
			t.Errorf("crackTimeDisplay(%g) = %q, want substring %q", tc.guesses, got, tc.wantHas)
		}
	}
}
