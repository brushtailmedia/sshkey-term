package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestSidebar_RemoveGroup verifies the group_deleted handler can drop
// a group DM from the sidebar by ID, also clearing the unread badge,
// the leftGroups archived flag, and resetting selectedGroup if it
// pointed at the removed entry.
func TestSidebar_RemoveGroup(t *testing.T) {
	s := NewSidebar()
	s.SetGroups([]protocol.GroupInfo{
		{ID: "group_1", Members: []string{"me", "alice"}, Name: "Project A"},
		{ID: "group_2", Members: []string{"me", "bob"}, Name: "Project B"},
		{ID: "group_3", Members: []string{"me", "carol"}, Name: "Project C"},
	})
	s.SetUnread("group_2", 5)
	s.MarkGroupLeft("group_2")
	s.selectedGroup = "group_2"

	s.RemoveGroup("group_2")

	if len(s.groups) != 2 {
		t.Fatalf("expected 2 groups after remove, got %d", len(s.groups))
	}
	for _, g := range s.groups {
		if g.ID == "group_2" {
			t.Error("group_2 should be gone")
		}
	}
	if s.unread["group_2"] != 0 {
		t.Errorf("unread badge should be cleared, got %d", s.unread["group_2"])
	}
	if s.leftGroups["group_2"] {
		t.Error("leftGroups flag should be cleared")
	}
	if s.selectedGroup != "" {
		t.Errorf("selectedGroup should be cleared when removed group was selected, got %q", s.selectedGroup)
	}
}

// TestSidebar_RemoveGroup_PreservesSelectionForOtherGroup verifies that
// removing one group does not clear selectedGroup if it pointed at a
// different group.
func TestSidebar_RemoveGroup_PreservesSelectionForOtherGroup(t *testing.T) {
	s := NewSidebar()
	s.SetGroups([]protocol.GroupInfo{
		{ID: "group_1", Members: []string{"me", "alice"}},
		{ID: "group_2", Members: []string{"me", "bob"}},
	})
	s.selectedGroup = "group_1"

	s.RemoveGroup("group_2")

	if s.selectedGroup != "group_1" {
		t.Errorf("removing group_2 should not clear selectedGroup group_1, got %q", s.selectedGroup)
	}
}

// TestSidebar_RemoveGroup_NonexistentIsNoop verifies removing a group
// not in the sidebar is a harmless no-op.
func TestSidebar_RemoveGroup_NonexistentIsNoop(t *testing.T) {
	s := NewSidebar()
	s.SetGroups([]protocol.GroupInfo{
		{ID: "group_1", Members: []string{"me", "alice"}},
	})

	s.RemoveGroup("group_does_not_exist")

	if len(s.groups) != 1 {
		t.Errorf("expected group_1 to remain, got %d entries", len(s.groups))
	}
}
