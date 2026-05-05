package tui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestPinnedBar_ClearsOutsideRoomContext(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.roomPins = make(map[string][]string)

	a.roomPins["room_a"] = []string{"msg_1"}
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", From: "bob", Body: "hello", Room: "room_a"},
	}
	a.messages.SetContext("room_a", "", "")
	a.syncPinnedBarForContext()
	if !a.pinnedBar.HasPins() {
		t.Fatal("expected pins in room context")
	}

	a.messages.SetContext("", "", "dm_1")
	a.onContextSwitch()
	if a.pinnedBar.HasPins() {
		t.Fatal("pins should be cleared in DM context")
	}
}

func TestPinnedBar_ScopedPerRoomOnSwitch(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.roomPins = make(map[string][]string)
	a.messages.SetContext("room_a", "", "")
	a.onContextSwitch()

	rawA, _ := json.Marshal(protocol.Pins{
		Type:     "pins",
		Room:     "room_a",
		Messages: []string{"msg_a1"},
	})
	a.handleServerMessage(ServerMsg{Type: "pins", Raw: rawA})

	rawB, _ := json.Marshal(protocol.Pins{
		Type:     "pins",
		Room:     "room_b",
		Messages: []string{"msg_b1"},
	})
	a.handleServerMessage(ServerMsg{Type: "pins", Raw: rawB})

	// Active room_a should still show room_a pins.
	if got := a.pinnedBar.PinIDs(); !reflect.DeepEqual(got, []string{"msg_a1"}) {
		t.Fatalf("room_a pin ids = %v, want [msg_a1]", got)
	}

	// Switch to room_b: bar should swap to room_b pins, not leak room_a.
	a.messages.SetContext("room_b", "", "")
	a.onContextSwitch()
	a.syncPinnedBarForContext()
	if got := a.pinnedBar.PinIDs(); !reflect.DeepEqual(got, []string{"msg_b1"}) {
		t.Fatalf("room_b pin ids = %v, want [msg_b1]", got)
	}
}

func TestPinnedBar_PinsPayloadUsesResolvedDisplayName(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.roomPins = make(map[string][]string)
	a.messages.SetContext("room_a", "", "")
	a.onContextSwitch()

	client.SetProfileForTesting(a.client, &protocol.Profile{
		User:        "usr_bob",
		DisplayName: "Bob",
	})

	rawPinnedMsg, err := json.Marshal(protocol.Message{
		Type: "message",
		ID:   "msg_1",
		From: "usr_bob",
		Room: "room_a",
		TS:   1,
	})
	if err != nil {
		t.Fatalf("marshal pinned message: %v", err)
	}

	rawPins, err := json.Marshal(protocol.Pins{
		Type:        "pins",
		Room:        "room_a",
		Messages:    []string{"msg_1"},
		MessageData: []json.RawMessage{rawPinnedMsg},
	})
	if err != nil {
		t.Fatalf("marshal pins: %v", err)
	}

	a.handleServerMessage(ServerMsg{Type: "pins", Raw: rawPins})
	if !a.pinnedBar.HasPins() {
		t.Fatal("expected pinned bar to have pins")
	}
	if got := a.pinnedBar.pins[0].From; got != "Bob" {
		t.Fatalf("pinned preview From = %q, want Bob", got)
	}
}
