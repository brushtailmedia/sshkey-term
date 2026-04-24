package tui

// Phase 16 Gap 4 — tests for the TUI wiring of the live strength
// hint (wizard + addserver) and the hostname-context helper in
// addserver.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/keygen"
)

// TestAddServerZxcvbnContext_IncludesNameAndHost verifies that the
// helper returns both the display name and hostname fields as zxcvbn
// context strings so passphrases containing either get penalized.
func TestAddServerZxcvbnContext_IncludesNameAndHost(t *testing.T) {
	a := NewAddServer()
	a.inputs[0].SetValue("My Server")
	a.inputs[1].SetValue("chat.example.com")
	a.inputs[2].SetValue("2222")
	a.inputs[3].SetValue("~/.ssh/id_ed25519")

	ctx := addServerZxcvbnContext(a)
	if len(ctx) != 2 {
		t.Fatalf("context len = %d, want 2 (name + host)", len(ctx))
	}
	if ctx[0] != "My Server" {
		t.Errorf("ctx[0] = %q, want 'My Server'", ctx[0])
	}
	if ctx[1] != "chat.example.com" {
		t.Errorf("ctx[1] = %q, want 'chat.example.com'", ctx[1])
	}
}

// TestAddServerZxcvbnContext_SkipsEmptyFields verifies the helper
// omits blank / whitespace-only fields rather than passing empty
// strings that zxcvbn would no-op against.
func TestAddServerZxcvbnContext_SkipsEmptyFields(t *testing.T) {
	a := NewAddServer()
	a.inputs[0].SetValue("")
	a.inputs[1].SetValue("   ")

	ctx := addServerZxcvbnContext(a)
	if len(ctx) != 0 {
		t.Errorf("context with empty fields = %v, want empty slice", ctx)
	}
}

// TestRenderStrengthHint_HiddenTier verifies HintHidden renders as
// the empty string so callers can concatenate unconditionally.
func TestRenderStrengthHint_HiddenTier(t *testing.T) {
	if got := renderStrengthHint(keygen.LiveHint{Tier: keygen.HintHidden}); got != "" {
		t.Errorf("HintHidden rendered as %q, want empty string", got)
	}
}

// TestRenderStrengthHint_BlockIncludesText verifies the block tier
// applies a style to the hint text without discarding it.
func TestRenderStrengthHint_BlockIncludesText(t *testing.T) {
	got := renderStrengthHint(keygen.LiveHint{
		Tier: keygen.HintBlock,
		Text: "✗ weak — cracked in seconds",
	})
	if !strings.Contains(got, "weak") {
		t.Errorf("block render = %q, want to contain hint text", got)
	}
	if !strings.Contains(got, "cracked in seconds") {
		t.Errorf("block render = %q, want to preserve full text", got)
	}
}

// TestRenderStrengthHint_PassIncludesText verifies the pass tier
// applies a style without discarding text.
func TestRenderStrengthHint_PassIncludesText(t *testing.T) {
	got := renderStrengthHint(keygen.LiveHint{
		Tier: keygen.HintPass,
		Text: "✓ very strong",
	})
	if !strings.Contains(got, "very strong") {
		t.Errorf("pass render = %q, want to contain hint text", got)
	}
}

// TestWizard_KeygenUpdateRefreshesStrengthHint verifies that typing
// in the passphrase input while in the keygen step updates the
// wizard's strengthHint field.
func TestWizard_KeygenUpdateRefreshesStrengthHint(t *testing.T) {
	w := NewWizard()
	w.step = WizardKeyGenerate
	w.chosenName = "Alice"
	w.genFocused = 1 // passphrase field
	w.genPassInput.SetValue("correct horse battery staple elephant")

	// Simulate a keystroke by calling updateKeyGenerate with a
	// non-special rune. Any rune keypress that doesn't match a special
	// case triggers the fall-through path where strengthHint is
	// recomputed.
	model, _ := w.updateKeyGenerate(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	w = model.(WizardModel)

	// A strong passphrase should produce HintPass.
	if w.strengthHint.Tier != keygen.HintPass {
		t.Errorf("strengthHint.Tier = %v, want HintPass for strong passphrase", w.strengthHint.Tier)
	}
}
