// Package keygen provides passphrase strength validation for client
// key-generation flows (the first-launch wizard and the add-server
// dialog). Mirror of the server-side `internal/keygen` package in
// sshkey-chat — identical structure and constants except for the
// score floor, which is softer for user keys than admin keys.
//
// Phase 16 Gap 4 — see refactor_plan.md for the rationale on
// duplicating this logic across both repos rather than extracting a
// shared module. Short version: ~30 LOC of validation, changes
// rarely, and matches the existing precedent of duplicated protocol
// types between the two repos.
//
// Score policy:
//
//	0-1 (seconds to minutes)  → block
//	2   (hours)               → warn-and-continue
//	3-4 (days to centuries)   → silent pass
//
// The warn-and-continue tier is a softer floor than the server-side
// admin keygen (which hard-blocks at 2). Regular users get the warning
// but can proceed if they insist; admin keys can't.
package keygen

import (
	"fmt"
	"strings"

	"github.com/trustelem/zxcvbn"
	"github.com/trustelem/zxcvbn/match"
)

// MinPassphraseLength is the hard floor below which any passphrase is
// rejected regardless of zxcvbn score. 12 chars matches the server
// side and is enough for a random passphrase to clear score 3.
const MinPassphraseLength = 12

// Score thresholds. See package doc for the meaning of each tier.
const (
	// MinUserBlockScore: any score below this is hard-rejected. A
	// passphrase that zxcvbn estimates would be cracked in seconds or
	// minutes is too weak to ship even with a warning.
	MinUserBlockScore = 2

	// MinUserSilentScore: any score below this triggers a warning;
	// at or above is silent pass. So scores of 2 produce a warning,
	// scores of 3-4 produce no feedback at all.
	MinUserSilentScore = 3
)

// offlineFastGuessesPerSec is the attacker model used for crack-time
// estimates: GPU rig attacking a stolen, encrypted SSH private key
// file offline. Same value as the server side (10^10).
const offlineFastGuessesPerSec = 1e10

// ValidationResult captures the outcome of a strength check. The
// caller (TUI keygen flow) uses the three fields to decide what to do:
//
//   - Blocked == true: hard reject, do not proceed regardless of
//     anything else. Show Message as the error.
//   - Blocked == false && Warning != "": passphrase is borderline;
//     show Warning to the user and let them confirm whether to
//     proceed anyway.
//   - Blocked == false && Warning == "": silent pass.
type ValidationResult struct {
	Blocked bool
	Warning string
	Message string
}

// ValidateUserPassphrase runs strength validation on a user-chosen
// passphrase for client-side keygen flows. Returns a ValidationResult
// the TUI uses to decide between hard-block, warn-and-confirm, and
// silent-pass.
func ValidateUserPassphrase(pass string) ValidationResult {
	return ValidateUserPassphraseWithContext(pass, nil)
}

// HintTier captures the three visual states of the live strength
// indicator in the TUI wizard and add-server dialog.
type HintTier int

const (
	// HintHidden means the indicator should not render (passphrase is
	// below MinPassphraseLength — showing weakness for a too-short
	// passphrase is noisy and obvious).
	HintHidden HintTier = iota
	// HintBlock means the passphrase would be hard-rejected on submit.
	// TUI renders in error style.
	HintBlock
	// HintWarn means the passphrase is borderline and would require
	// confirmation on submit. TUI renders in warning style.
	HintWarn
	// HintPass means the passphrase is strong enough to submit
	// silently. TUI renders in success/dim style.
	HintPass
)

// LiveHint is the return type of LivePassphraseHint — a compact
// summary suitable for rendering under the passphrase input field as
// the user types. The TUI picks a style based on Tier and renders
// Text verbatim.
type LiveHint struct {
	Tier HintTier
	Text string
}

// LivePassphraseHint runs a fast strength check for every-keystroke
// feedback under the passphrase input. Returns HintHidden when the
// passphrase is shorter than MinPassphraseLength so the first N
// keystrokes don't trigger a rolling "weak, weak, weak" indicator.
// Once the length floor clears, returns a compact label matching the
// tier the user would hit on submit (block / warn / pass).
//
// The text is deliberately shorter than the submit-time validation
// messages: users re-reading the same long diagnostic on every
// keystroke is noise. Only the tier indicator, icon, and crack-time
// estimate appear live; the full pattern explanation is reserved for
// the submit-time message.
func LivePassphraseHint(pass string, context []string) LiveHint {
	if len(pass) < MinPassphraseLength {
		return LiveHint{Tier: HintHidden}
	}
	result := zxcvbn.PasswordStrength(pass, context)
	crackTime := crackTimeDisplay(result.Guesses)
	switch {
	case result.Score < MinUserBlockScore:
		return LiveHint{
			Tier: HintBlock,
			Text: fmt.Sprintf("✗ weak — cracked in %s", crackTime),
		}
	case result.Score < MinUserSilentScore:
		return LiveHint{
			Tier: HintWarn,
			Text: fmt.Sprintf("! borderline — cracked in %s", crackTime),
		}
	default:
		label := "strong"
		if result.Score == 4 {
			label = "very strong"
		}
		return LiveHint{
			Tier: HintPass,
			Text: fmt.Sprintf("✓ %s", label),
		}
	}
}

// ValidateUserPassphraseWithContext is like ValidateUserPassphrase
// but accepts context strings (display name, server hostname) that
// zxcvbn will penalize if they appear in the passphrase.
func ValidateUserPassphraseWithContext(pass string, context []string) ValidationResult {
	if pass == "" {
		return ValidationResult{
			Blocked: true,
			Message: "passphrase is required — generating an unencrypted key is unsafe",
		}
	}
	if len(pass) < MinPassphraseLength {
		return ValidationResult{
			Blocked: true,
			Message: fmt.Sprintf("passphrase must be at least %d characters (got %d)", MinPassphraseLength, len(pass)),
		}
	}

	result := zxcvbn.PasswordStrength(pass, context)
	switch {
	case result.Score < MinUserBlockScore:
		return ValidationResult{
			Blocked: true,
			Message: buildBlockMessage(result),
		}
	case result.Score < MinUserSilentScore:
		return ValidationResult{
			Blocked: false,
			Warning: buildWarnMessage(result),
		}
	default:
		return ValidationResult{}
	}
}

// buildBlockMessage formats a zxcvbn result into a hard-rejection
// message shown when the user's passphrase is too weak to use.
func buildBlockMessage(r zxcvbn.Result) string {
	crackTime := crackTimeDisplay(r.Guesses)
	reason := patternExplanation(r.Sequence)
	return fmt.Sprintf("passphrase is too weak: could be cracked in %s — %s. Choose a stronger one (try 4+ unrelated words, or a random passphrase from a password manager).", crackTime, reason)
}

// buildWarnMessage formats a zxcvbn result into a warning message
// shown when the passphrase is borderline. The message is a statement
// of the weakness; the TUI appends a single confirmation instruction
// after it (see wizard.go / addserver.go). Keep this function's output
// free of question marks or "Continue anyway?" phrasing so the combined
// text doesn't stutter.
func buildWarnMessage(r zxcvbn.Result) string {
	crackTime := crackTimeDisplay(r.Guesses)
	reason := patternExplanation(r.Sequence)
	return fmt.Sprintf("This passphrase could be cracked in %s — %s.", crackTime, reason)
}

// crackTimeDisplay converts a zxcvbn guesses estimate into a
// human-readable crack-time string under the offline-fast attacker
// model. Identical thresholds to the server side.
func crackTimeDisplay(guesses float64) string {
	seconds := guesses / offlineFastGuessesPerSec
	switch {
	case seconds < 1:
		return "less than a second"
	case seconds < 60:
		return fmt.Sprintf("%d seconds", int(seconds))
	case seconds < 3600:
		return fmt.Sprintf("%d minutes", int(seconds/60))
	case seconds < 86400:
		return fmt.Sprintf("%d hours", int(seconds/3600))
	case seconds < 2592000: // 30 days
		return fmt.Sprintf("%d days", int(seconds/86400))
	case seconds < 31536000: // 1 year
		return fmt.Sprintf("%d months", int(seconds/2592000))
	case seconds < 315360000: // 10 years
		return fmt.Sprintf("%d years", int(seconds/31536000))
	default:
		return "centuries"
	}
}

// patternExplanation walks the zxcvbn match sequence and returns a
// human-readable explanation of the dominant weakness. Mirrors the
// server-side logic so warning messages on the client and admin
// rejection messages on the server have consistent voice.
func patternExplanation(seq []*match.Match) string {
	if len(seq) == 0 {
		return "passphrase is too short or too predictable"
	}

	var reasons []string
	for _, m := range seq {
		switch m.Pattern {
		case "dictionary":
			if m.L33t {
				reasons = append(reasons, fmt.Sprintf("leetspeak substitutions in %q are easy to guess", m.MatchedWord))
			} else if m.Reversed {
				reasons = append(reasons, fmt.Sprintf("reversed word %q is easy to guess", m.MatchedWord))
			} else {
				reasons = append(reasons, fmt.Sprintf("%q is a common word in the %s dictionary", m.MatchedWord, humanDictName(m.DictionaryName)))
			}
		case "spatial":
			reasons = append(reasons, fmt.Sprintf("keyboard pattern %q is easy to guess", m.Token))
		case "repeat":
			reasons = append(reasons, fmt.Sprintf("repeated sequence %q is easy to guess", m.Token))
		case "sequence":
			reasons = append(reasons, fmt.Sprintf("sequence %q (like abc or 123) is easy to guess", m.Token))
		case "regex":
			if m.RegexName == "recent_year" {
				reasons = append(reasons, fmt.Sprintf("recent year %q is easy to guess", m.Token))
			}
		case "date":
			reasons = append(reasons, fmt.Sprintf("date pattern %q is easy to guess", m.Token))
		}
	}

	if len(reasons) == 0 {
		return "passphrase is too short or too simple"
	}
	if len(reasons) > 2 {
		reasons = reasons[:2]
	}
	return strings.Join(reasons, "; ")
}

// humanDictName translates zxcvbn's internal dictionary names into
// something more friendly. Identical to the server side.
func humanDictName(name string) string {
	switch name {
	case "passwords":
		return "common password"
	case "english_wikipedia":
		return "English"
	case "female_names", "male_names", "surnames":
		return "names"
	case "us_tv_and_film":
		return "pop-culture"
	default:
		return name
	}
}
