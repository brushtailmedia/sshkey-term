package tui

import (
	"strings"
	"testing"
	"time"
)

func TestTypingIndicator_SingleUser(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "", "")
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
	m.SetContext("general", "", "")
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
	m.SetContext("general", "", "")
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
	m.SetContext("general", "", "")
	m.typingUsers["usr_a"] = time.Now().Add(-10 * time.Second) // expired
	m.resolveName = func(s string) string { return s }

	view := m.View(60, 20, false)
	if strings.Contains(view, "typing") {
		t.Error("expired typing indicator should not render")
	}
}

// TestTypingIndicator_ClearedOnContextSwitch is the regression for the
// per-context typing namespace bug. The previous behaviour kept the
// typingUsers map across context switches, so a recent typing event
// from carol in group_X would briefly display "carol is typing" in
// group_Y after alice switched. SetContext now clears the map.
func TestTypingIndicator_ClearedOnContextSwitch(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_X", "")
	m.typingUsers["carol"] = time.Now()
	m.resolveName = func(s string) string { return s }

	// Sanity check: typing is visible in group_X
	view := m.View(60, 20, false)
	if !strings.Contains(view, "typing") {
		t.Fatal("precondition: carol should be typing in group_X")
	}

	// Switch to group_Y — typing should be cleared
	m.SetContext("", "group_Y", "")
	view = m.View(60, 20, false)
	if strings.Contains(view, "typing") {
		t.Errorf("after context switch, typing indicator should be cleared, got: %s", view)
	}
	if len(m.typingUsers) != 0 {
		t.Errorf("typingUsers map should be empty after SetContext, got %v", m.typingUsers)
	}
}

// TestTypingIndicator_NotAddedForOtherContext verifies that SetTyping
// rejects events that don't match the current active context. This is
// the OTHER half of the per-context namespace — even if the typing
// event arrives, it shouldn't pollute the active view.
func TestTypingIndicator_NotAddedForOtherContext(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_X", "")

	// Carol typing in group_Y while alice is viewing group_X
	m.SetTyping("carol", "", "group_Y", "")

	if len(m.typingUsers) != 0 {
		t.Errorf("typing for non-active context should be rejected, got %v", m.typingUsers)
	}

	// Carol typing in group_X (the active context) should be accepted
	m.SetTyping("carol", "", "group_X", "")
	if _, ok := m.typingUsers["carol"]; !ok {
		t.Error("typing for active context should be accepted")
	}
}
