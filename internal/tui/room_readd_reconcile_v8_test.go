package tui

// Finding 2 (V8 follow-ups): re-add reconciliation.
//
// When a previously-left room reappears in a fresh room_list (an admin
// re-added the user; the room returns on reconnect), the client-layer
// room_list handler (client.go) clears the store left_at for every active
// room BEFORE this TUI handler runs (handleInternal precedes the UI forward).
// So by the time the TUI room_list handler executes, the only stale state
// left to reconcile is in-memory:
//
//   - Gap A: the sidebar row still carries its same-session grey flag, because
//     SetRooms does not clear leftRooms. The marker loop's else now clears it.
//   - Gap B: if the user is viewing that room, the messages pane's read-only
//     flag is stale until a context switch. The handler now calls
//     syncMessagesLeftState() to refresh it in place.
//
// These tests exercise the in-memory models given the client-layer store-clear
// precondition, which is simulated here with MarkRoomRejoined (the redundant
// TUI re-add loop was removed — see the room_list handler comment).

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newReAddHarness(t *testing.T) (App, *store.Store) {
	t.Helper()
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := client.New(client.Config{DeviceID: "dev_readd"})
	client.SetStoreForTesting(c, st)
	client.SetUserIDForTesting(c, "usr_self")

	a := App{
		client:    c,
		messages:  NewMessages(),
		statusBar: NewStatusBar(),
		sidebar:   NewSidebar(),
	}
	return a, st
}

// Gap A: a re-added room's stale sidebar grey is cleared by the marker-loop
// else, even though SetRooms leaves leftRooms untouched.
func TestRoomListReAdd_ClearsStaleSidebarGrey(t *testing.T) {
	a, st := newReAddHarness(t)

	// Earlier this session the user left rm_a: store left_at > 0 and the
	// sidebar greyed the row.
	if err := st.UpsertRoom("rm_a", "Room A", "", 0); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := st.MarkRoomLeft("rm_a", 1000, ""); err != nil {
		t.Fatalf("mark left: %v", err)
	}
	a.sidebar.SetRooms([]string{"rm_a"})
	a.sidebar.MarkRoomLeft("rm_a")
	if !a.sidebar.IsRoomLeft("rm_a") {
		t.Fatal("precondition: rm_a should start greyed in the sidebar")
	}

	// Admin re-adds the user. On reconnect the client-layer room_list handler
	// clears left_at for every active room BEFORE this TUI handler runs;
	// simulate that precondition (the TUI re-add loop was removed).
	if err := st.MarkRoomRejoined("rm_a"); err != nil {
		t.Fatalf("simulate client-layer clear: %v", err)
	}

	a.handleServerMessage(ServerMsg{Type: "room_list", Raw: mustJSONRaw(t, protocol.RoomList{
		Type:  "room_list",
		Rooms: []protocol.RoomInfo{{ID: "rm_a", Name: "Room A"}},
	})})

	if a.sidebar.IsRoomLeft("rm_a") {
		t.Error("re-added room: sidebar grey should be cleared (Gap A else)")
	}
	if st.IsRoomLeft("rm_a") {
		t.Error("re-added room: store left_at should be cleared")
	}
}

// Gap B: when the user is currently viewing the re-added room, the messages
// pane's read-only flag is refreshed in place so the compose box reactivates
// without a context switch.
func TestRoomListReAdd_ReactivatesViewedRoomComposePane(t *testing.T) {
	a, st := newReAddHarness(t)

	if err := st.UpsertRoom("rm_a", "Room A", "", 0); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := st.MarkRoomLeft("rm_a", 1000, ""); err != nil {
		t.Fatalf("mark left: %v", err)
	}
	// User is viewing rm_a; the pane is read-only because the room is left.
	a.messages.SetContext("rm_a", "", "")
	a.syncMessagesLeftState()
	if !a.messages.IsLeft() {
		t.Fatal("precondition: messages pane should start read-only for the left room")
	}

	// Simulate the client-layer left_at clear that precedes this handler.
	if err := st.MarkRoomRejoined("rm_a"); err != nil {
		t.Fatalf("simulate client-layer clear: %v", err)
	}

	a.handleServerMessage(ServerMsg{Type: "room_list", Raw: mustJSONRaw(t, protocol.RoomList{
		Type:  "room_list",
		Rooms: []protocol.RoomInfo{{ID: "rm_a", Name: "Room A"}},
	})})

	if a.messages.IsLeft() {
		t.Error("re-added room being viewed: messages pane should become writable (Gap B)")
	}
}

// Negative: a still-left room (the server no longer sends it; merged back via
// GetLeftRooms) must stay greyed and read-only. The marker loop's if-branch
// keeps the grey; the else must NOT fire for it.
func TestRoomList_StillLeftRoomStaysGreyed(t *testing.T) {
	a, st := newReAddHarness(t)

	if err := st.UpsertRoom("rm_gone", "Gone", "", 0); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := st.MarkRoomLeft("rm_gone", 1000, ""); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	// A fresh room_list that does NOT contain rm_gone (server dropped it after
	// the leave). It is merged back from GetLeftRooms and must stay greyed.
	a.handleServerMessage(ServerMsg{Type: "room_list", Raw: mustJSONRaw(t, protocol.RoomList{
		Type:  "room_list",
		Rooms: nil,
	})})

	if !a.sidebar.IsRoomLeft("rm_gone") {
		t.Error("still-left room (not in server list) must stay greyed")
	}
	if !st.IsRoomLeft("rm_gone") {
		t.Error("still-left room: store left_at must remain set")
	}
}
