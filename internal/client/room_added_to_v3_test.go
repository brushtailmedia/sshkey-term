package client

// V3: the client-layer room_added_to handler hydrates the keyed room-member
// cache + persists the room (member IDs) + clears any prior left_at.

import (
	"encoding/json"
	"testing"
)

func TestRoomAddedTo_PopulatesCacheAndStore(t *testing.T) {
	c := newClientWithRoomStore(t)
	raw := json.RawMessage(`{"type":"room_added_to","room":"rm_new","name":"New Room","topic":"hi","members":["usr_a","usr_b"],"added_by":"os:1000"}`)
	c.handleInternal("room_added_to", raw)

	// In-memory keyed cache populated (no RequestRoomMembers fetch needed).
	members, ok := c.RoomMembers("rm_new")
	if !ok {
		t.Fatal("room member cache should be populated")
	}
	if len(members) != 2 || members[0] != "usr_a" || members[1] != "usr_b" {
		t.Fatalf("cache members = %v, want [usr_a usr_b]", members)
	}

	// Persisted to the local store via the V8 combined helper.
	stored, loaded, err := c.store.GetRoomMembers("rm_new")
	if err != nil {
		t.Fatalf("GetRoomMembers: %v", err)
	}
	if !loaded || len(stored) != 2 {
		t.Fatalf("stored members loaded=%v, %v", loaded, stored)
	}
}

func TestRoomAddedTo_ClearsPriorLeftState(t *testing.T) {
	c := newClientWithRoomStore(t)
	// Seed the room as previously LEFT (admin re-add scenario).
	if err := c.store.UpsertRoomWithMembers("rm_back", "Back", "", []string{"usr_a"}); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := c.store.MarkRoomLeft("rm_back", 1000, "removed"); err != nil {
		t.Fatalf("mark left: %v", err)
	}
	if !c.store.IsRoomLeft("rm_back") {
		t.Fatal("precondition: rm_back should be left")
	}

	raw := json.RawMessage(`{"type":"room_added_to","room":"rm_back","name":"Back","members":["usr_a","usr_c"],"added_by":"usr_admin"}`)
	c.handleInternal("room_added_to", raw)

	if c.store.IsRoomLeft("rm_back") {
		t.Error("room_added_to should clear left_at via MarkRoomRejoined")
	}
	members, ok := c.RoomMembers("rm_back")
	if !ok || len(members) != 2 {
		t.Errorf("cache should reflect the new member set, got ok=%v %v", ok, members)
	}
}

func TestRoomAddedTo_MalformedIsSafe(t *testing.T) {
	c := newClientWithRoomStore(t)
	// Logs + breaks; must not panic or populate any cache.
	c.handleInternal("room_added_to", json.RawMessage(`{bad json`))
	if _, ok := c.RoomMembers("rm_x"); ok {
		t.Error("malformed room_added_to must not populate the cache")
	}
}
