package tui

import (
	"strings"
	"testing"
)

// Phase 12 Chunk 7 — messages view tests for the retired-room
// read-only banner (distinct from the self-leave banner).

func TestMessages_SetRoomRetired(t *testing.T) {
	m := NewMessages()
	if m.IsRoomRetired() {
		t.Error("fresh model should not be flagged as retired room")
	}
	m.SetRoomRetired(true)
	if !m.IsRoomRetired() {
		t.Error("SetRoomRetired(true) should flip the flag")
	}
	m.SetRoomRetired(false)
	if m.IsRoomRetired() {
		t.Error("SetRoomRetired(false) should clear the flag")
	}
}

func TestMessages_SetContextClearsRoomRetired(t *testing.T) {
	m := NewMessages()
	m.SetRoomRetired(true)
	m.SetContext("room_new", "", "")
	if m.IsRoomRetired() {
		t.Error("SetContext should clear the roomRetired flag (caller syncs after)")
	}
}

func TestMessages_ViewRetiredBanner(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_x", "", "")
	m.SetRoomRetired(true)

	view := m.View(80, 20, false)
	if !strings.Contains(view, "archived by an admin") {
		t.Errorf("retired banner should say 'archived by an admin', got:\n%s", view)
	}
	if !strings.Contains(view, "/delete") {
		t.Errorf("retired banner should mention /delete, got:\n%s", view)
	}
	if !strings.Contains(view, "read-only") {
		t.Errorf("retired banner should say read-only, got:\n%s", view)
	}
}

func TestMessages_ViewRetiredBannerTakesPrecedenceOverLeft(t *testing.T) {
	// Both flags set: retired should win (it's the cause).
	m := NewMessages()
	m.SetContext("room_x", "", "")
	m.SetLeft(true)
	m.SetRoomRetired(true)

	view := m.View(80, 20, false)
	if !strings.Contains(view, "archived by an admin") {
		t.Errorf("retired banner should win over left banner, got:\n%s", view)
	}
	if strings.Contains(view, "you left this room") {
		t.Errorf("should not show 'you left this room' when retired takes precedence, got:\n%s", view)
	}
}

func TestMessages_ViewLeftBannerWhenNotRetired(t *testing.T) {
	// Leaving a non-retired room should still show the old self-leave wording.
	m := NewMessages()
	m.SetContext("room_x", "", "")
	m.SetLeft(true)

	view := m.View(80, 20, false)
	if !strings.Contains(view, "you left this room") {
		t.Errorf("self-leave banner should be present, got:\n%s", view)
	}
	if strings.Contains(view, "archived by an admin") {
		t.Errorf("should not show retired wording for self-left room, got:\n%s", view)
	}
}

func TestMessages_ViewNoBannerWhenActive(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_x", "", "")
	// No flags set

	view := m.View(80, 20, false)
	if strings.Contains(view, "archived by an admin") {
		t.Errorf("active room should have no retired banner, got:\n%s", view)
	}
	if strings.Contains(view, "you left this") {
		t.Errorf("active room should have no self-leave banner, got:\n%s", view)
	}
}
