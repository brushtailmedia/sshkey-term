package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Own messages: the sender header of a message the local user sent renders
// in the palette green (the same #22C55E as the sidebar's online dot) and
// stays bold; everyone else's stays plain bold. Color profiles are stripped
// under test, so assert on the selected style rather than on rendered bytes.
func TestUsernameStyleFor_OwnMessagesAreGreen(t *testing.T) {
	m := NewMessages()
	m.SetSelfUserID("usr_self")

	own := m.usernameStyleFor("usr_self")
	if got := own.GetForeground(); got != lipgloss.Color("#22C55E") {
		t.Errorf("own username foreground = %v, want #22C55E", got)
	}
	if !own.GetBold() {
		t.Errorf("own username style must stay bold")
	}

	other := m.usernameStyleFor("usr_other")
	if got := other.GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("other username foreground = %v, want none", got)
	}
	if !other.GetBold() {
		t.Errorf("other username style must stay bold")
	}
}

// An empty sender ID (system rows, legacy rows) must never pick the green
// style — including the startup window where selfUserID is still "".
func TestUsernameStyleFor_EmptyIDsNeverMatch(t *testing.T) {
	m := NewMessages() // selfUserID unset
	if got := m.usernameStyleFor("").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("empty sender with unset self rendered green: %v", got)
	}
	m.SetSelfUserID("usr_self")
	if got := m.usernameStyleFor("").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("empty sender id rendered green: %v", got)
	}
}
