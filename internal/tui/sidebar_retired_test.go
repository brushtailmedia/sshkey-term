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

func TestSidebar_OneOnOneWithRetiredShowsMarker(t *testing.T) {
	s := NewSidebar()
	s.MarkRetired("alice")
	s.SetConversations([]protocol.ConversationInfo{
		{ID: "conv_1", Members: []string{"me", "alice"}},
	})

	view := s.View(40, 20, false)
	if !strings.Contains(view, "[retired]") {
		t.Errorf("1:1 conv with retired partner should show [retired], got:\n%s", view)
	}
}

func TestSidebar_GroupWithRetiredMemberShowsMarker(t *testing.T) {
	// Even though groups don't strictly need the marker (retired users are
	// removed from conversation_members), our sidebar checks `len==2`, so
	// if a 3-person group had a retired member BEFORE they were removed
	// server-side, this test doesn't apply. We only mark 1:1s in the sidebar.
	s := NewSidebar()
	s.MarkRetired("alice")
	// 3-member conversation is a group — sidebar only marks retired on
	// 2-member convs.
	s.SetConversations([]protocol.ConversationInfo{
		{ID: "conv_group", Members: []string{"me", "alice", "bob"}},
	})

	view := s.View(40, 20, false)
	if strings.Contains(view, "[retired]") {
		t.Errorf("sidebar should not mark 3+-member groups as retired, got:\n%s", view)
	}
}

func TestSidebar_OneOnOneNoRetiredNoMarker(t *testing.T) {
	s := NewSidebar()
	// No one retired
	s.SetConversations([]protocol.ConversationInfo{
		{ID: "conv_1", Members: []string{"me", "bob"}},
	})

	view := s.View(40, 20, false)
	if strings.Contains(view, "[retired]") {
		t.Errorf("healthy 1:1 should not show retired, got:\n%s", view)
	}
}
