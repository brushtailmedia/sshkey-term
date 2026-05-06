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

func TestInput_CtrlJDoesNotInsertNewlineMarker(t *testing.T) {
	i := NewInput()
	i.textInput.SetValue("abc")
	i.textInput.SetCursor(3)

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyCtrlJ}, nil, "general", "", "")

	if got := i.Value(); got != "abc" {
		t.Fatalf("ctrl+j should not mutate input, got %q", got)
	}
	if got := i.ValueForSend(); got != "abc" {
		t.Fatalf("ctrl+j should not create send newline, got %q", got)
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
