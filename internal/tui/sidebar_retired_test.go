package tui

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestSidebar_MarkRetired(t *testing.T) {
	s := NewSidebar()
	s.MarkRetired("alice")
	if !s.retired["alice"] {
		t.Error("alice should be marked retired")
	}
}

func TestSidebar_GroupWithRetiredMemberShowsMarker(t *testing.T) {
	s := NewSidebar()
	s.MarkRetired("alice")
	s.SetGroups([]protocol.GroupInfo{
		{ID: "group_1", Members: []string{"me", "alice", "bob"}},
	})

	view := s.View(40, 20, false)
	if !strings.Contains(view, "[retired]") {
		t.Errorf("group with retired member should show [retired], got:\n%s", view)
	}
}

func TestSidebar_GroupNoRetiredNoMarker(t *testing.T) {
	s := NewSidebar()
	// No one retired
	s.SetGroups([]protocol.GroupInfo{
		{ID: "group_1", Members: []string{"me", "bob"}},
	})

	view := s.View(40, 20, false)
	if strings.Contains(view, "[retired]") {
		t.Errorf("healthy group should not show retired, got:\n%s", view)
	}
}

func TestSidebar_DMRetiredShowsMarker(t *testing.T) {
	s := NewSidebar()
	s.selfUserID = "me"
	s.MarkRetired("alice")
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"alice", "me"}},
	})

	view := s.View(40, 20, false)
	if !strings.Contains(view, "[retired]") {
		t.Errorf("1:1 DM with retired partner should show [retired], got:\n%s", view)
	}
}

func TestSidebar_DMNoRetiredNoMarker(t *testing.T) {
	s := NewSidebar()
	s.selfUserID = "me"
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"bob", "me"}},
	})

	view := s.View(40, 20, false)
	if strings.Contains(view, "[retired]") {
		t.Errorf("healthy DM should not show retired, got:\n%s", view)
	}
}
