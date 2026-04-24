package tui

import (
	"strings"
	"testing"
)

// Phase 12 Chunk 7 — info panel rendering tests for the new /leave
// and /delete hints across active / left / retired room states.

// TestInfoPanel_ActiveRoomShowsLeaveAndDeleteHints verifies an active
// room's info panel surfaces both /leave and /delete as valid actions.
func TestInfoPanel_ActiveRoomShowsLeaveAndDeleteHints(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "room_general",
	}

	view := i.View(80)
	if !strings.Contains(view, "/leave") {
		t.Errorf("active room info panel should mention /leave, got:\n%s", view)
	}
	if !strings.Contains(view, "/delete") {
		t.Errorf("active room info panel should mention /delete, got:\n%s", view)
	}
}

// TestInfoPanel_LeftRoomShowsDeleteOnly verifies a left (but not
// retired) room shows only /delete — /leave is pointless once you've
// already left.
func TestInfoPanel_LeftRoomShowsDeleteOnly(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "room_general",
		left:    true,
	}

	view := i.View(80)
	if !strings.Contains(view, "/delete") {
		t.Errorf("left room info panel should mention /delete, got:\n%s", view)
	}
	if strings.Contains(view, "/leave") {
		t.Errorf("left room info panel should not mention /leave, got:\n%s", view)
	}
	if !strings.Contains(view, "you left this room") {
		t.Errorf("left room info panel should show status, got:\n%s", view)
	}
}

// TestInfoPanel_RetiredRoomShowsAdminArchivedStatus verifies the
// retired-room wording takes priority over "you left".
func TestInfoPanel_RetiredRoomShowsAdminArchivedStatus(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "room_general",
		retired: true,
	}

	view := i.View(80)
	if !strings.Contains(view, "archived by an admin") {
		t.Errorf("retired room info panel should say 'archived by an admin', got:\n%s", view)
	}
	if !strings.Contains(view, "/delete") {
		t.Errorf("retired room info panel should mention /delete, got:\n%s", view)
	}
	if strings.Contains(view, "/leave") {
		t.Errorf("retired room info panel should not mention /leave (irrelevant), got:\n%s", view)
	}
}

// TestInfoPanel_RetiredRoomTakesPrecedenceOverLeft verifies that when
// both flags are set, the retired wording wins.
func TestInfoPanel_RetiredRoomTakesPrecedenceOverLeft(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "room_general",
		left:    true,
		retired: true,
	}

	view := i.View(80)
	if !strings.Contains(view, "archived by an admin") {
		t.Errorf("retired should win over left in info panel, got:\n%s", view)
	}
	if strings.Contains(view, "you left this room") {
		t.Errorf("should not show 'you left this room' when retired wins, got:\n%s", view)
	}
}

// TestInfoPanel_ActiveRoomNoPlaceholderText verifies the obsolete
// "(coming in a later phase)" placeholder has been removed from the
// info panel output now that /delete is fully wired up.
func TestInfoPanel_ActiveRoomNoPlaceholderText(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "room_general",
	}
	view := i.View(80)
	if strings.Contains(view, "coming in a later phase") {
		t.Errorf("info panel should not contain 'coming in a later phase' placeholder, got:\n%s", view)
	}

	// Also check the left state
	i.left = true
	view = i.View(80)
	if strings.Contains(view, "coming in a later phase") {
		t.Errorf("left-room info panel should not contain 'coming in a later phase' placeholder, got:\n%s", view)
	}
}

// TestInfoPanel_ActiveGroupShowsLeaveAndDeleteHints verifies active
// group DMs also get both hints.
func TestInfoPanel_ActiveGroupShowsLeaveAndDeleteHints(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		group:   "group_1",
		isGroup: true,
	}

	view := i.View(80)
	if !strings.Contains(view, "/leave") {
		t.Errorf("active group info panel should mention /leave, got:\n%s", view)
	}
	if !strings.Contains(view, "/delete") {
		t.Errorf("active group info panel should mention /delete, got:\n%s", view)
	}
}

// TestInfoPanel_LeftGroupShowsDeleteOnly verifies a left group shows
// only /delete, matching the left-room behavior.
func TestInfoPanel_LeftGroupShowsDeleteOnly(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		group:   "group_1",
		isGroup: true,
		left:    true,
	}

	view := i.View(80)
	if !strings.Contains(view, "/delete") {
		t.Errorf("left group info panel should mention /delete, got:\n%s", view)
	}
	if strings.Contains(view, "/leave") {
		t.Errorf("left group info panel should not mention /leave, got:\n%s", view)
	}
	if !strings.Contains(view, "you left this group") {
		t.Errorf("left group info panel should show status, got:\n%s", view)
	}
}
