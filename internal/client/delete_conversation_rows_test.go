package client

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestHandleInternal_GroupDeleted_RemovesGroupRow(t *testing.T) {
	c := newClientWithRoomStore(t)
	if err := c.store.StoreGroup("group_a", "Project A", "usr_alice,usr_bob"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}

	raw, _ := json.Marshal(protocol.GroupDeleted{
		Type:  "group_deleted",
		Group: "group_a",
	})
	c.handleInternal("group_deleted", raw)

	all, err := c.store.GetAllGroups()
	if err != nil {
		t.Fatalf("GetAllGroups: %v", err)
	}
	if _, ok := all["group_a"]; ok {
		t.Fatalf("group_a should be removed from local groups row on delete")
	}
}

func TestHandleInternal_DeletedGroups_RemovesGroupRows(t *testing.T) {
	c := newClientWithRoomStore(t)
	if err := c.store.StoreGroup("group_a", "A", "usr_alice,usr_bob"); err != nil {
		t.Fatalf("StoreGroup group_a: %v", err)
	}
	if err := c.store.StoreGroup("group_b", "B", "usr_alice,usr_carol"); err != nil {
		t.Fatalf("StoreGroup group_b: %v", err)
	}

	raw, _ := json.Marshal(protocol.DeletedGroupsList{
		Type:   "deleted_groups",
		Groups: []string{"group_a", "group_b"},
	})
	c.handleInternal("deleted_groups", raw)

	all, err := c.store.GetAllGroups()
	if err != nil {
		t.Fatalf("GetAllGroups: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("all deleted groups should be removed from local rows, got %d", len(all))
	}
}

func TestHandleInternal_RoomDeleted_RemovesRoomRow(t *testing.T) {
	c := newClientWithRoomStore(t)
	if err := c.store.UpsertRoom("room_a", "general", "topic", 2); err != nil {
		t.Fatalf("UpsertRoom: %v", err)
	}

	raw, _ := json.Marshal(protocol.RoomDeleted{
		Type: "room_deleted",
		Room: "room_a",
	})
	c.handleInternal("room_deleted", raw)

	if got := c.store.GetRoomName("room_a"); got != "room_a" {
		t.Fatalf("room row should be removed after delete, got room name %q", got)
	}
}

func TestHandleInternal_DeletedRooms_RemovesRoomRows(t *testing.T) {
	c := newClientWithRoomStore(t)
	if err := c.store.UpsertRoom("room_a", "general", "", 2); err != nil {
		t.Fatalf("UpsertRoom room_a: %v", err)
	}
	if err := c.store.UpsertRoom("room_b", "support", "", 3); err != nil {
		t.Fatalf("UpsertRoom room_b: %v", err)
	}

	raw, _ := json.Marshal(protocol.DeletedRoomsList{
		Type:  "deleted_rooms",
		Rooms: []string{"room_a", "room_b"},
	})
	c.handleInternal("deleted_rooms", raw)

	if got := c.store.GetRoomName("room_a"); got != "room_a" {
		t.Fatalf("room_a row should be removed after deleted_rooms catchup, got %q", got)
	}
	if got := c.store.GetRoomName("room_b"); got != "room_b" {
		t.Fatalf("room_b row should be removed after deleted_rooms catchup, got %q", got)
	}
}
