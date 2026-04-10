package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDeleteRoomConfirm_InitialHidden(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	if d.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestDeleteRoomConfirm_ShowActive(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", false)
	if !d.IsVisible() {
		t.Error("after Show should be visible")
	}
	if d.room != "room_abc" {
		t.Errorf("room = %q, want room_abc", d.room)
	}
	if d.roomName != "general" {
		t.Errorf("roomName = %q, want general", d.roomName)
	}
	if d.retired {
		t.Error("retired should be false when Show called with retired=false")
	}
}

func TestDeleteRoomConfirm_ShowRetired(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general_V1St", true)
	if !d.retired {
		t.Error("retired should be true when Show called with retired=true")
	}
}

func TestDeleteRoomConfirm_HideClearsState(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", true)
	d.Hide()
	if d.IsVisible() {
		t.Error("after Hide should not be visible")
	}
	if d.room != "" || d.roomName != "" || d.retired {
		t.Errorf("Hide should clear state, got room=%q name=%q retired=%v", d.room, d.roomName, d.retired)
	}
}

func TestDeleteRoomConfirm_YEmitsConfirm(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", false)
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d.IsVisible() {
		t.Error("after y, dialog should hide")
	}
	if cmd == nil {
		t.Fatal("y should emit a DeleteRoomConfirmMsg")
	}
	msg, ok := cmd().(DeleteRoomConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteRoomConfirmMsg, got %T", cmd())
	}
	if msg.Room != "room_abc" {
		t.Errorf("emitted Room = %q, want room_abc", msg.Room)
	}
}

func TestDeleteRoomConfirm_EnterEmitsConfirm(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", true)
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should emit a DeleteRoomConfirmMsg")
	}
	msg, ok := cmd().(DeleteRoomConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteRoomConfirmMsg, got %T", cmd())
	}
	if msg.Room != "room_abc" {
		t.Errorf("emitted Room = %q, want room_abc", msg.Room)
	}
}

func TestDeleteRoomConfirm_NCancels(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", false)
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if d.IsVisible() {
		t.Error("after n, dialog should hide")
	}
	if cmd != nil {
		t.Error("n should NOT emit a confirm message")
	}
}

func TestDeleteRoomConfirm_EscCancels(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", false)
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.IsVisible() {
		t.Error("after Esc, dialog should hide")
	}
	if cmd != nil {
		t.Error("Esc should NOT emit a confirm message")
	}
}

func TestDeleteRoomConfirm_ViewActive_ContainsKeyElements(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general", false)
	view := d.View(80)

	expectedFragments := []string{
		"Delete room?",
		"general",
		"remove you from the room",
		"admin will need to add you back",
		"[y] Delete",
		"[n] Cancel",
	}
	for _, frag := range expectedFragments {
		if !strings.Contains(view, frag) {
			t.Errorf("active view missing fragment %q", frag)
		}
	}

	// Active view should NOT mention "archived"
	if strings.Contains(view, "archived") {
		t.Error("active room view should not mention 'archived'")
	}
}

func TestDeleteRoomConfirm_ViewRetired_ContainsKeyElements(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "general_V1St", true)
	view := d.View(80)

	expectedFragments := []string{
		"Delete archived room?",
		"general_V1St",
		"room is archived",
		"cannot be undone",
		"[y] Delete",
		"[n] Cancel",
	}
	for _, frag := range expectedFragments {
		if !strings.Contains(view, frag) {
			t.Errorf("retired view missing fragment %q", frag)
		}
	}

	// Retired view should NOT suggest re-adding via admin
	if strings.Contains(view, "admin will need to add you back") {
		t.Error("retired room view should not suggest admin re-add")
	}
}

func TestDeleteRoomConfirm_ViewWhenHiddenIsEmpty(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	if got := d.View(80); got != "" {
		t.Errorf("hidden view should be empty, got %q", got)
	}
}

func TestDeleteRoomConfirm_ViewFallbackName(t *testing.T) {
	d := DeleteRoomConfirmModel{}
	d.Show("room_abc", "", false)
	view := d.View(80)
	if !strings.Contains(view, "this room") {
		t.Error("view should fall back to 'this room' when name is empty")
	}
}
