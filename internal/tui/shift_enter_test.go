package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInput_InsertComposeNewlineAndValueForSend(t *testing.T) {
	i := NewInput()
	i.textInput.SetValue("abc")
	i.textInput.SetCursor(1)

	i.InsertComposeNewline()

	if got := i.Value(); got != "a"+composeNewlineMarker+"bc" {
		t.Fatalf("input value = %q, want marker inserted at cursor", got)
	}
	if got := i.ValueForSend(); got != "a\nbc" {
		t.Fatalf("ValueForSend = %q, want real newline", got)
	}
}

// Ctrl+J (the line-feed byte) is the alias of Alt+Enter: a distinct key
// on every terminal, independent of the macOS "Option as Meta" setting.
// It inserts the draft marker and never sends. (Until 2026-10-23 this test
// asserted the opposite — Ctrl+J was a no-op; the owner asked for the
// alias because Shift+Enter cannot be distinguished from Enter.)
func TestInput_CtrlJInsertsNewlineMarker(t *testing.T) {
	i := NewInput()
	i.textInput.SetValue("ab")
	i.textInput.SetCursor(1)

	i, cmd := i.Update(tea.KeyMsg{Type: tea.KeyCtrlJ}, nil, "general", "", "")

	if got := i.Value(); got != "a"+composeNewlineMarker+"b" {
		t.Fatalf("input value = %q, want newline marker inserted", got)
	}
	if got := i.ValueForSend(); got != "a\nb" {
		t.Fatalf("ValueForSend = %q, want real newline", got)
	}
	if cmd != nil {
		t.Fatalf("ctrl+j must not send")
	}
}

func TestInput_AltEnterInsertsNewlineMarker(t *testing.T) {
	i := NewInput()
	i.textInput.SetValue("ab")
	i.textInput.SetCursor(1)

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, nil, "general", "", "")

	if got := i.Value(); got != "a"+composeNewlineMarker+"b" {
		t.Fatalf("input value = %q, want newline marker inserted", got)
	}
	if got := i.ValueForSend(); got != "a\nb" {
		t.Fatalf("ValueForSend = %q, want real newline", got)
	}
}

func TestInput_EnterSendsInsteadOfInsertingNewlineMarker(t *testing.T) {
	i := NewInput()
	i.textInput.SetValue("ab")
	i.textInput.SetCursor(1)

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, "general", "", "")

	if got := i.Value(); got != "" {
		t.Fatalf("enter should dispatch and clear input, got %q", got)
	}
}
