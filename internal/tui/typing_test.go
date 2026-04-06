package tui

import (
	"strings"
	"testing"
	"time"
)

func TestTypingIndicator_SingleUser(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "")
	m.typingUsers["usr_a"] = time.Now()
	m.resolveName = func(s string) string {
		if s == "usr_a" {
			return "Alice"
		}
		return s
	}

	view := m.View(60, 20, false)
	if !strings.Contains(view, "Alice is typing...") {
		t.Errorf("single user should show 'Alice is typing...', got: %s", view)
	}
}

func TestTypingIndicator_TwoUsers(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "")
	m.typingUsers["usr_a"] = time.Now()
	m.typingUsers["usr_b"] = time.Now()
	m.resolveName = func(s string) string {
		switch s {
		case "usr_a":
			return "Alice"
		case "usr_b":
			return "Bob"
		}
		return s
	}

	view := m.View(60, 20, false)
	if !strings.Contains(view, "are typing...") {
		t.Errorf("two users should show 'X and Y are typing...', got: %s", view)
	}
}

func TestTypingIndicator_ThreePlusUsers(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "")
	m.typingUsers["usr_a"] = time.Now()
	m.typingUsers["usr_b"] = time.Now()
	m.typingUsers["usr_c"] = time.Now()
	m.resolveName = func(s string) string { return s }

	view := m.View(60, 20, false)
	if !strings.Contains(view, "3 people are typing...") {
		t.Errorf("3+ users should show '3 people are typing...', got: %s", view)
	}
}

func TestTypingIndicator_ExpiredNotShown(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "")
	m.typingUsers["usr_a"] = time.Now().Add(-10 * time.Second) // expired
	m.resolveName = func(s string) string { return s }

	view := m.View(60, 20, false)
	if strings.Contains(view, "typing") {
		t.Error("expired typing indicator should not render")
	}
}
