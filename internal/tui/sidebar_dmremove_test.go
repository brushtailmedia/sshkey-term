package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestSidebar_RemoveDM verifies that the dm_left handler can drop a 1:1 DM
// from the sidebar by ID, also clearing any unread badge for that DM and
// resetting the selected-DM cursor if it was pointing at the removed entry.
func TestSidebar_RemoveDM(t *testing.T) {
	s := NewSidebar()
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"me", "alice"}},
		{ID: "dm_2", Members: []string{"me", "bob"}},
		{ID: "dm_3", Members: []string{"me", "carol"}},
	})
	s.SetUnreadDM("dm_2", 5)
	s.selectedDM = "dm_2"

	s.RemoveDM("dm_2")

	if len(s.dms) != 2 {
		t.Fatalf("expected 2 DMs after remove, got %d", len(s.dms))
	}
	for _, dm := range s.dms {
		if dm.ID == "dm_2" {
			t.Error("dm_2 should be gone")
		}
	}
	if s.unread["dm_2"] != 0 {
		t.Errorf("unread badge should be cleared, got %d", s.unread["dm_2"])
	}
	if s.selectedDM != "" {
		t.Errorf("selectedDM should be cleared when removed DM was selected, got %q", s.selectedDM)
	}
}

// TestSidebar_RemoveDM_PreservesSelectionForOtherDM verifies that removing
// one DM does not clear selectedDM if it was pointing at a different one.
func TestSidebar_RemoveDM_PreservesSelectionForOtherDM(t *testing.T) {
	s := NewSidebar()
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"me", "alice"}},
		{ID: "dm_2", Members: []string{"me", "bob"}},
	})
	s.selectedDM = "dm_1"

	s.RemoveDM("dm_2")

	if s.selectedDM != "dm_1" {
		t.Errorf("removing dm_2 should not clear selectedDM dm_1, got %q", s.selectedDM)
	}
}

// TestSidebar_RemoveDM_NonexistentIsNoop verifies that removing a DM ID
// that isn't in the sidebar is a harmless no-op.
func TestSidebar_RemoveDM_NonexistentIsNoop(t *testing.T) {
	s := NewSidebar()
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"me", "alice"}},
	})

	s.RemoveDM("dm_does_not_exist")

	if len(s.dms) != 1 {
		t.Errorf("expected dm_1 to remain, got %d entries", len(s.dms))
	}
}
