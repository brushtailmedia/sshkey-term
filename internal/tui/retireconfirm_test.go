package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// typeKey is a test helper that sends each rune of s as a separate KeyMsg.
// Uses tea.KeyRunes with a single rune to simulate individual typed characters.
func typeKey(r RetireConfirmModel, s string) RetireConfirmModel {
	for _, ch := range s {
		r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	return r
}

func pressKey(r RetireConfirmModel, keyStr string) (RetireConfirmModel, tea.Cmd) {
	// Map strings to tea.KeyMsg — simulate common keys by constructing a
	// KeyMsg whose String() returns what we want.
	var msg tea.KeyMsg
	switch keyStr {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyStr)}
	}
	return r.Update(msg)
}

func TestRetireConfirm_InitialState(t *testing.T) {
	r := NewRetireConfirm()
	if r.IsVisible() {
		t.Error("fresh model should not be visible")
	}
	r.Show()
	if !r.IsVisible() {
		t.Error("after Show(), should be visible")
	}
	if r.reasonIdx != 0 {
		t.Errorf("reasonIdx = %d, want 0", r.reasonIdx)
	}
	if r.focused != 0 {
		t.Errorf("focused = %d, want 0 (reason selector)", r.focused)
	}
}

func TestRetireConfirm_Hide(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r.Hide()
	if r.IsVisible() {
		t.Error("after Hide(), should not be visible")
	}
}

func TestRetireConfirm_ReasonNavigationDown(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()

	// Start at idx 0, down moves to 1
	r, _ = pressKey(r, "down")
	if r.reasonIdx != 1 {
		t.Errorf("after down: reasonIdx = %d, want 1", r.reasonIdx)
	}

	// Down to 2
	r, _ = pressKey(r, "down")
	if r.reasonIdx != 2 {
		t.Errorf("after down x2: reasonIdx = %d, want 2", r.reasonIdx)
	}

	// Down at last should stay at last
	r, _ = pressKey(r, "down")
	if r.reasonIdx != 2 {
		t.Errorf("reasonIdx should stay at max: got %d", r.reasonIdx)
	}
}

func TestRetireConfirm_ReasonNavigationUp(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r, _ = pressKey(r, "down")
	r, _ = pressKey(r, "down")
	if r.reasonIdx != 2 {
		t.Fatalf("precondition: expected idx 2, got %d", r.reasonIdx)
	}

	r, _ = pressKey(r, "up")
	if r.reasonIdx != 1 {
		t.Errorf("after up: reasonIdx = %d, want 1", r.reasonIdx)
	}

	r, _ = pressKey(r, "up")
	if r.reasonIdx != 0 {
		t.Errorf("after up x2: reasonIdx = %d, want 0", r.reasonIdx)
	}

	// Up at 0 should stay at 0
	r, _ = pressKey(r, "up")
	if r.reasonIdx != 0 {
		t.Errorf("reasonIdx should stay at 0: got %d", r.reasonIdx)
	}
}

func TestRetireConfirm_EnterOnReasonAdvancesToPhrase(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r, _ = pressKey(r, "enter")
	if r.focused != 1 {
		t.Errorf("Enter on reason should advance focus to phrase input (got focused=%d)", r.focused)
	}
}

func TestRetireConfirm_TabSwitchesFocus(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	if r.focused != 0 {
		t.Fatalf("precondition: focused=%d", r.focused)
	}
	r, _ = pressKey(r, "tab")
	if r.focused != 1 {
		t.Errorf("after tab: focused = %d, want 1", r.focused)
	}
	r, _ = pressKey(r, "tab")
	if r.focused != 0 {
		t.Errorf("after tab x2: focused = %d, want 0 (wrap)", r.focused)
	}
}

func TestRetireConfirm_EscCancels(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r, cmd := pressKey(r, "esc")
	if r.IsVisible() {
		t.Error("after Esc, dialog should hide")
	}
	if cmd != nil {
		t.Error("Esc should NOT emit a RetireConfirmMsg")
	}
}

func TestRetireConfirm_WrongPhraseRejects(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r, _ = pressKey(r, "enter") // advance to phrase field
	if r.focused != 1 {
		t.Fatal("should be on phrase input")
	}

	// Type wrong phrase
	r = typeKey(r, "wrong text")
	r, cmd := pressKey(r, "enter")

	if cmd != nil {
		// Run the cmd to see if it produced a RetireConfirmMsg (it shouldn't)
		msg := cmd()
		if _, ok := msg.(RetireConfirmMsg); ok {
			t.Error("wrong phrase should NOT submit")
		}
	}
	if r.err == "" {
		t.Error("wrong phrase should set error message")
	}
	if !strings.Contains(r.err, retireConfirmPhrase) {
		t.Errorf("error should mention the required phrase, got: %q", r.err)
	}
	if !r.IsVisible() {
		t.Error("dialog should remain visible after wrong phrase")
	}
}

func TestRetireConfirm_CorrectPhraseSubmits(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	r, _ = pressKey(r, "enter") // advance to phrase
	r = typeKey(r, retireConfirmPhrase)
	r, cmd := pressKey(r, "enter")

	if cmd == nil {
		t.Fatal("correct phrase should emit a command")
	}
	msg := cmd()
	confirm, ok := msg.(RetireConfirmMsg)
	if !ok {
		t.Fatalf("expected RetireConfirmMsg, got %T", msg)
	}
	if confirm.Reason != "self_compromise" {
		t.Errorf("default reason = %q, want self_compromise", confirm.Reason)
	}
	if r.IsVisible() {
		t.Error("dialog should hide after successful submission")
	}
}

func TestRetireConfirm_SelectedReasonSubmits(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	// Select "switching_key" (idx 1)
	r, _ = pressKey(r, "down")
	if r.reasonIdx != 1 {
		t.Fatalf("precondition: reasonIdx=%d", r.reasonIdx)
	}
	r, _ = pressKey(r, "enter") // advance
	r = typeKey(r, retireConfirmPhrase)
	r, cmd := pressKey(r, "enter")

	if cmd == nil {
		t.Fatal("should emit")
	}
	confirm := cmd().(RetireConfirmMsg)
	if confirm.Reason != "switching_key" {
		t.Errorf("reason = %q, want switching_key", confirm.Reason)
	}
}

func TestRetireConfirm_View_ContainsKeyElements(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	view := r.View(80)

	expectedFragments := []string{
		"Retire Account",
		"permanently end",
		"cannot be undone",
		"RETIRE MY ACCOUNT",
		"I suspect my key was compromised",
		"I'm switching to a new key",
		"Other",
	}
	for _, frag := range expectedFragments {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing expected fragment: %q", frag)
		}
	}
}

func TestRetireConfirm_AllReasonsValid(t *testing.T) {
	// Iterate all reasons, verify they all submit correctly
	for i := 0; i < len(retireReasons); i++ {
		r := NewRetireConfirm()
		r.Show()
		for j := 0; j < i; j++ {
			r, _ = pressKey(r, "down")
		}
		r, _ = pressKey(r, "enter")
		r = typeKey(r, retireConfirmPhrase)
		_, cmd := pressKey(r, "enter")
		if cmd == nil {
			t.Errorf("reason %d should submit", i)
			continue
		}
		confirm := cmd().(RetireConfirmMsg)
		if confirm.Reason != retireReasons[i].Value {
			t.Errorf("reason %d: got %q, want %q", i, confirm.Reason, retireReasons[i].Value)
		}
	}
}
