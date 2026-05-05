package tui

import (
	"strings"
	"testing"
)

// Phase 12 Chunk 7 — sidebar tests for retired rooms + RemoveRoom.

// TestSidebar_MarkRoomRetired verifies the flag flips when set.
func TestSidebar_MarkRoomRetired(t *testing.T) {
	s := NewSidebar()
	if s.IsRoomRetired("room_x") {
		t.Error("room should not be retired initially")
	}
	s.MarkRoomRetired("room_x")
	if !s.IsRoomRetired("room_x") {
		t.Error("room should be retired after MarkRoomRetired")
	}
}

// TestSidebar_RoomRetiredShowsMarker verifies the rendered sidebar has
// the "(retired)" marker and renders the room in archived style.
func TestSidebar_RoomRetiredShowsMarker(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_x"})
	s.MarkRoomRetired("room_x")

	view := s.View(40, 20, false)
	if !strings.Contains(view, "(retired)") {
		t.Errorf("retired room should show (retired), got:\n%s", view)
	}
}

// TestSidebar_RoomRetiredTakesPrecedenceOverLeft verifies that when a
// room is both left AND retired, the (retired) marker wins. This matches
// the priority in the render loop: retirement is the cause, and the
// user should see that instead of "(left)".
func TestSidebar_RoomRetiredTakesPrecedenceOverLeft(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_x"})
	s.MarkRoomLeft("room_x")
	s.MarkRoomRetired("room_x")

	view := s.View(40, 20, false)
	if !strings.Contains(view, "(retired)") {
		t.Errorf("should show (retired) when both left and retired, got:\n%s", view)
	}
	if strings.Contains(view, "(left)") {
		t.Errorf("should NOT show (left) when retired takes priority, got:\n%s", view)
	}
}

// TestSidebar_RoomLeftButNotRetiredShowsLeft verifies the existing
// (left) wording still applies when the room hasn't been retired.
func TestSidebar_RoomLeftButNotRetiredShowsLeft(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_x"})
	s.MarkRoomLeft("room_x")

	view := s.View(40, 20, false)
	if !strings.Contains(view, "(left)") {
		t.Errorf("left-only room should show (left), got:\n%s", view)
	}
}

// TestSidebar_RoomLeftMarkerVisibleWhenNameIsLong ensures status markers stay
// visible even when names are long enough to truncate in narrow sidebar widths.
func TestSidebar_RoomLeftMarkerVisibleWhenNameIsLong(t *testing.T) {
	s := NewSidebar()
	roomID := "room_with_a_very_long_name_that_must_be_truncated"
	s.SetRooms([]string{roomID})
	s.MarkRoomLeft(roomID)

	// Sidebar width in app layout is 20, so content width is 18.
	view := s.View(20, 20, false)
	if !strings.Contains(view, "(left)") {
		t.Errorf("left marker should remain visible after truncation, got:\n%s", view)
	}
}

// TestSidebar_ActiveRoomNoMarker verifies an active, unread room renders
// without any state markers.
func TestSidebar_ActiveRoomNoMarker(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_x"})

	view := s.View(40, 20, false)
	if strings.Contains(view, "(retired)") || strings.Contains(view, "(left)") {
		t.Errorf("healthy active room should have no markers, got:\n%s", view)
	}
}

// TestSidebar_RemoveRoom verifies the room_deleted handler can drop
// a room from the sidebar by ID, clearing unread, left/retired flags,
// and resetting selectedRoom.
func TestSidebar_RemoveRoom(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_1", "room_2", "room_3"})
	s.SetUnread("room_2", 5)
	s.MarkRoomLeft("room_2")
	s.MarkRoomRetired("room_2")
	s.selectedRoom = "room_2"

	s.RemoveRoom("room_2")

	if len(s.rooms) != 2 {
		t.Fatalf("expected 2 rooms after remove, got %d", len(s.rooms))
	}
	for _, r := range s.rooms {
		if r == "room_2" {
			t.Error("room_2 should be gone")
		}
	}
	if s.unread["room_2"] != 0 {
		t.Errorf("unread badge should be cleared, got %d", s.unread["room_2"])
	}
	if s.leftRooms["room_2"] {
		t.Error("leftRooms flag should be cleared")
	}
	if s.retiredRooms["room_2"] {
		t.Error("retiredRooms flag should be cleared")
	}
	if s.selectedRoom != "" {
		t.Errorf("selectedRoom should be cleared when removed room was selected, got %q", s.selectedRoom)
	}
}

// TestSidebar_RemoveRoom_PreservesSelectionForOtherRoom verifies that
// removing one room does not clear selectedRoom if it pointed at a
// different room.
func TestSidebar_RemoveRoom_PreservesSelectionForOtherRoom(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_1", "room_2"})
	s.selectedRoom = "room_1"

	s.RemoveRoom("room_2")

	if s.selectedRoom != "room_1" {
		t.Errorf("removing room_2 should not clear selectedRoom room_1, got %q", s.selectedRoom)
	}
}

// TestSidebar_RemoveRoom_NonexistentIsNoop verifies removing a room
// not in the sidebar is a harmless no-op.
func TestSidebar_RemoveRoom_NonexistentIsNoop(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_1"})

	s.RemoveRoom("room_does_not_exist")

	if len(s.rooms) != 1 {
		t.Errorf("expected room_1 to remain, got %d entries", len(s.rooms))
	}
}

// TestSidebar_RetiredRoomSuppressesUnread verifies that a retired room
// does not show its unread count (matching the left-room behavior).
// The user can't really "unread" an archived room.
func TestSidebar_RetiredRoomSuppressesUnread(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"room_x"})
	s.SetUnread("room_x", 7)
	s.MarkRoomRetired("room_x")

	view := s.View(40, 20, false)
	if strings.Contains(view, "(7)") {
		t.Errorf("retired room should not show unread count, got:\n%s", view)
	}
}
