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
//	empty                    → warn: key will be unencrypted
//	0-1 (seconds to minutes) → weak advisory
//	2   (hours)              → borderline advisory
//	3-4 (days to centuries)  → strong / very strong advisory
//
// User key passphrases are optional. The TUI shows this guidance live,
// but does not block weak non-empty passphrases; if blank passphrases are
// allowed, forcing a 12-character floor for non-blank passphrases is
// inconsistent UX.
package keygen

import (
	"fmt"
	"strings"

	"github.com/trustelem/zxcvbn"
	"github.com/trustelem/zxcvbn/match"
)

// UnencryptedKeyWarning is the immediate empty-passphrase advisory shown by
// the wizard and Add Server key-generation flows.
const UnencryptedKeyWarning = "⚠ Leaving blank allows anyone with the key to access this account"

// Score thresholds. See package doc for the meaning of each tier.
const (
	// MinUserBorderlineScore: scores below this render as "weak".
	// They are allowed, but the live advisory should make the local
	// device-theft risk obvious.
	MinUserBorderlineScore = 2

	// MinUserSilentScore: scores below this render a warning-style
	// advisory; scores of 3-4 render as strong.
	MinUserSilentScore = 3
)

// offlineFastGuessesPerSec is the attacker model used for crack-time
// estimates: GPU rig attacking a stolen, encrypted SSH private key
// file offline. Same value as the server side (10^10).
const offlineFastGuessesPerSec = 1e10

// ValidationResult captures advisory-only strength feedback. Blocked and
// Message are retained for the existing caller/test shape, but user keygen
// strength no longer blocks submission; weak values are warnings only.
type ValidationResult struct {
	Blocked bool
	Warning string
	Message string
}

// ValidateUserPassphrase runs advisory-only strength validation on a
// user-chosen passphrase for client-side keygen flows.
func ValidateUserPassphrase(pass string) ValidationResult {
	return ValidateUserPassphraseWithContext(pass, nil)
}

// HintTier captures the three visual states of the live strength
// indicator in the TUI wizard and add-server dialog.
type HintTier int

const (
	// HintHidden means the indicator should not render. The keygen views use it
	// only as the zero-value/off state; active passphrase fields show either the
	// empty-passphrase warning, a zxcvbn advisory, or a success indicator.
	HintHidden HintTier = iota
	// HintBlock means "weak" in the live advisory. It is red, but still
	// allowed; there is no hard-submit block.
	HintBlock
	// HintWarn means the passphrase is blank or borderline. TUI renders
	// in warning style.
	HintWarn
	// HintPass means the passphrase is strong. TUI renders in success style.
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
// feedback under the passphrase input. Blank passphrases immediately
// show the unencrypted-key warning; any non-blank passphrase runs
// zxcvbn from the first character.
//
// The text is deliberately shorter than the submit-time validation
// messages: users re-reading the same long diagnostic on every
// keystroke is noise. Only the tier indicator, icon, and crack-time
// estimate appear live; the full pattern explanation is reserved for
// the submit-time message.
func LivePassphraseHint(pass string, context []string) LiveHint {
	if pass == "" {
		return LiveHint{Tier: HintWarn, Text: UnencryptedKeyWarning}
	}
	result := zxcvbn.PasswordStrength(pass, context)
	crackTime := crackTimeDisplay(result.Guesses)
	switch {
	case result.Score < MinUserBorderlineScore:
		return LiveHint{
			Tier: HintBlock,
			Text: fmt.Sprintf("weak — cracked in %s", crackTime),
		}
	case result.Score < MinUserSilentScore:
		return LiveHint{
			Tier: HintWarn,
			Text: fmt.Sprintf("borderline — cracked in %s", crackTime),
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
		return ValidationResult{Warning: UnencryptedKeyWarning}
	}

	result := zxcvbn.PasswordStrength(pass, context)
	if result.Score < MinUserSilentScore {
		return ValidationResult{
			Blocked: false,
			Warning: buildWarnMessage(result),
		}
	}
	return ValidationResult{}
}

// buildWarnMessage formats a zxcvbn result into a warning message
// shown when the passphrase is weak or borderline. This is advisory-only:
// the TUI does not append confirmation wording or block submission.
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
