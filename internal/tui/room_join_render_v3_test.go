package tui

// Regression: room_event{join} is intentionally NOT rendered as an inline
// system message (rooms are operator-managed; the "os:<uid> added X" line is
// noise and replays on every reconnect). Other room events (leave/topic/...)
// still render. Groups are unaffected (handled separately).

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestRoomEventJoin_NotRenderedAsSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("rm_a", "", "")
	before := len(a.messages.messages)

	raw := mustJSONRaw(t, protocol.RoomEvent{
		Type:  "room_event",
		Room:  "rm_a",
		Event: "join",
		User:  "usr_x",
		By:    "os:0",
	})
	a.handleServerMessage(ServerMsg{Type: "room_event", Raw: raw})

	if got := len(a.messages.messages) - before; got != 0 {
		t.Errorf("room_event{join} should render no system message, got %d new", got)
	}
}

func TestRoomEventLeave_StillRendersSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("rm_a", "", "")
	before := len(a.messages.messages)

	raw := mustJSONRaw(t, protocol.RoomEvent{
		Type:   "room_event",
		Room:   "rm_a",
		Event:  "leave",
		User:   "usr_x",
		Reason: "removed",
	})
	a.handleServerMessage(ServerMsg{Type: "room_event", Raw: raw})

	if got := len(a.messages.messages) - before; got != 1 {
		t.Errorf("room_event{leave} should still render exactly one system message, got %d new", got)
	}
}
