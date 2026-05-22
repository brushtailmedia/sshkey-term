package tui

// V3: the TUI room_added_to handler — sidebar insertion (no focus steal), the
// os: actor label vs a real-user display name, and in-place left-state refresh
// when the re-added room is the one being viewed.

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestRoomAddedToTUI_AddsToSidebarWithoutStealingFocus(t *testing.T) {
	a, _ := newReAddHarness(t)
	a.sidebar.SetRooms([]string{"rm_current"})
	a.messages.SetContext("rm_current", "", "")

	raw := mustJSONRaw(t, protocol.RoomAddedTo{
		Type:    "room_added_to",
		Room:    "rm_new",
		Name:    "New Room",
		Members: []string{"usr_self", "usr_a"},
		AddedBy: "os:1000",
	})
	a.handleServerMessage(ServerMsg{Type: "room_added_to", Raw: raw})

	found := false
	for _, r := range a.sidebar.rooms {
		if r == "rm_new" {
			found = true
		}
	}
	if !found {
		t.Errorf("sidebar should contain rm_new, got %v", a.sidebar.rooms)
	}
	if a.messages.room != "rm_current" {
		t.Errorf("room_added_to must not steal focus, context = %q", a.messages.room)
	}
	// CLI add (os:<uid>) renders the friendly "server admin" label, not os:1000.
	if !strings.Contains(a.statusBar.errorMsg, "server admin added you to 'New Room'") {
		t.Errorf("status toast = %q, want server-admin label", a.statusBar.errorMsg)
	}
}

func TestRoomAddedToTUI_RealUserActorLabel(t *testing.T) {
	a, _ := newReAddHarness(t)
	client.SetProfileForTesting(a.client, &protocol.Profile{User: "usr_admin", DisplayName: "Admin Alice"})

	raw := mustJSONRaw(t, protocol.RoomAddedTo{
		Type:    "room_added_to",
		Room:    "rm_x",
		Name:    "X",
		Members: []string{"usr_self"},
		AddedBy: "usr_admin",
	})
	a.handleServerMessage(ServerMsg{Type: "room_added_to", Raw: raw})

	if !strings.Contains(a.statusBar.errorMsg, "Admin Alice added you to 'X'") {
		t.Errorf("status toast = %q, want resolved display name", a.statusBar.errorMsg)
	}
}

func TestRoomAddedToTUI_ReactivatesViewedReaddedRoom(t *testing.T) {
	a, st := newReAddHarness(t)
	if err := st.UpsertRoom("rm_back", "Back", "", 0); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := st.MarkRoomLeft("rm_back", 1000, "removed"); err != nil {
		t.Fatalf("mark left: %v", err)
	}
	a.sidebar.SetRooms([]string{"rm_back"})
	a.sidebar.MarkRoomLeft("rm_back")
	a.messages.SetContext("rm_back", "", "")
	a.syncMessagesLeftState()
	if !a.messages.IsLeft() {
		t.Fatal("precondition: pane should be read-only for the left room")
	}

	// The client layer clears left_at before the TUI handler runs; simulate
	// that (a.handleServerMessage here drives only the TUI handler).
	if err := st.MarkRoomRejoined("rm_back"); err != nil {
		t.Fatalf("simulate client-layer clear: %v", err)
	}

	raw := mustJSONRaw(t, protocol.RoomAddedTo{
		Type:    "room_added_to",
		Room:    "rm_back",
		Name:    "Back",
		Members: []string{"usr_self", "usr_a"},
		AddedBy: "os:1000",
	})
	a.handleServerMessage(ServerMsg{Type: "room_added_to", Raw: raw})

	if a.sidebar.IsRoomLeft("rm_back") {
		t.Error("sidebar grey should be cleared for the re-added room")
	}
	if a.messages.IsLeft() {
		t.Error("messages pane should reactivate (writable) for the viewed re-added room")
	}
}
