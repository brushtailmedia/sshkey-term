package keygen

// Phase 16 Gap 4 — tests for the client-side passphrase strength
// validator. Mirrors the server-side test suite in sshkey-chat with
// the same passphrase fixtures plus tier checks specific to the
// three-tier client model (block / warn / silent).

import (
	"strings"
	"testing"
)

func TestValidateUserPassphrase_Empty(t *testing.T) {
	r := ValidateUserPassphrase("")
	if !r.Blocked {
		t.Fatal("empty passphrase should be blocked")
	}
	if !strings.Contains(r.Message, "required") {
		t.Errorf("empty error should mention 'required', got: %q", r.Message)
	}
}

func TestValidateUserPassphrase_TooShort(t *testing.T) {
	cases := []string{"a", "abc", "password", "hunter2hunt"}
	for _, pass := range cases {
		t.Run(pass, func(t *testing.T) {
			r := ValidateUserPassphrase(pass)
			if !r.Blocked {
				t.Fatalf("expected %q to be blocked for length", pass)
			}
			if !strings.Contains(r.Message, "at least") {
				t.Errorf("short error should mention 'at least', got: %q", r.Message)
			}
		})
	}
}

// TestValidateUserPassphrase_BlockTier covers passphrases at score 0-1
// that should be hard-blocked under the user-tier policy. These are
// passphrases that zxcvbn estimates would be cracked in seconds or
// minutes — too weak even for warn-and-continue.
//
// Note: passphrases at score 2 are NOT in this test because the user
// tier softens them to warn-and-continue (only the admin tier
// hard-blocks at 2). For example "december252025" lands at score 2
// on this side and shows a warning instead of blocking.
func TestValidateUserPassphrase_BlockTier(t *testing.T) {
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
			if !r.Blocked {
				t.Errorf("expected %q to be blocked, got result: %+v", tc.pass, r)
				return
			}
			if !strings.Contains(r.Message, "cracked in") {
				t.Errorf("block message should mention crack time, got: %q", r.Message)
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
	if !r.Blocked {
		t.Errorf("expected blocked when passphrase is just the context word repeated, got: %+v", r)
	}
}

// TestValidationResult_TierShapeIsExclusive verifies the three-state
// invariant: at most one of Blocked / Warning is set per result.
// Catches accidental "both set" results that would confuse the TUI.
func TestValidationResult_TierShapeIsExclusive(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"password1234",         // block
		"correct horse battery staple", // strong
		"alicealicealice",
	}
	for _, pass := range cases {
		r := ValidateUserPassphrase(pass)
		if r.Blocked && r.Warning != "" {
			t.Errorf("passphrase %q: Blocked=true AND Warning set — shape invariant violated: %+v", pass, r)
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
