package client

// V8 — tests for the in-memory room-member cache and the handler wiring
// that maintains it (room_list snapshot, room_event deltas,
// room_members_list refresh, and the clear paths).

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func TestStartupHydrate_LoadsRoomMemberCacheFromStore(t *testing.T) {
	c := newClientWithRoomStore(t)
	if err := c.store.UpsertRoomWithMembers("rm_loaded", "general", "", []string{"usr_a", "usr_b"}); err != nil {
		t.Fatalf("seed loaded room: %v", err)
	}
	if err := c.store.UpsertRoom("rm_unloaded", "support", "", 0); err != nil {
		t.Fatalf("seed unloaded room: %v", err)
	}

	warm := New(Config{})
	warm.hydrateLocalCache(c.store)

	got, ok := warm.RoomMembers("rm_loaded")
	if !ok || !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("hydrated rm_loaded = %v ok=%v, want [usr_a usr_b]", got, ok)
	}
	if _, ok := warm.RoomMembers("rm_unloaded"); ok {
		t.Fatal("room with NULL member_ids should not hydrate into roomMemberCache")
	}
}

func TestRoomList_PopulatesCacheAndPersists(t *testing.T) {
	c := newClientWithRoomStore(t)

	raw := json.RawMessage(`{"type":"room_list","rooms":[
		{"id":"rm_a","name":"general","topic":"chat","members":["usr_a","", " usr_b ", "usr_a", "\t"]},
		{"id":"rm_b","name":"eng","topic":"","members":["usr_c"]}
	]}`)
	c.handleInternal("room_list", raw)

	// In-memory cache: normalized by Client.setRoomMembers.
	got, ok := c.RoomMembers("rm_a")
	if !ok || !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("rm_a cache = %v ok=%v, want [usr_a usr_b]", got, ok)
	}
	// Persisted member_ids.
	stored, loaded, err := c.store.GetRoomMembers("rm_a")
	if err != nil || !loaded || !reflect.DeepEqual(stored, []string{"usr_a", "usr_b"}) {
		t.Fatalf("rm_a persisted = %v loaded=%v err=%v", stored, loaded, err)
	}
}

func TestRoomEventJoin_AppendsWhenLoaded(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a"]}]}`))

	c.handleInternal("room_event", json.RawMessage(`{"type":"room_event","room":"rm_a","event":"join","user":"usr_b","by":"admin"}`))

	got, _ := c.RoomMembers("rm_a")
	if !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("after join cache = %v, want [usr_a usr_b]", got)
	}
	// Delta must NOT be persisted (snapshots persist, deltas don't).
	stored, _, _ := c.store.GetRoomMembers("rm_a")
	if !reflect.DeepEqual(stored, []string{"usr_a"}) {
		t.Fatalf("persisted member_ids = %v, want [usr_a] (deltas not persisted)", stored)
	}
}

func TestRoomEventJoin_NoOpWhenUnloaded(t *testing.T) {
	c := newClientWithRoomStore(t)
	// No room_list for rm_x — cache has no entry.
	c.handleInternal("room_event", json.RawMessage(`{"type":"room_event","room":"rm_x","event":"join","user":"usr_a","by":"admin"}`))

	if _, ok := c.RoomMembers("rm_x"); ok {
		t.Fatal("unloaded room must not be synthesized from a single delta")
	}
}

func TestRoomEventLeave_RemovesWhenLoaded(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b","usr_c"]}]}`))

	c.handleInternal("room_event", json.RawMessage(`{"type":"room_event","room":"rm_a","event":"leave","user":"usr_b","by":"admin"}`))

	got, _ := c.RoomMembers("rm_a")
	if !reflect.DeepEqual(got, []string{"usr_a", "usr_c"}) {
		t.Fatalf("after leave cache = %v, want [usr_a usr_c]", got)
	}
}

func TestRoomMembersList_ReplacesCacheAndDB(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a"]}]}`))

	// Explicit r-refresh response with a different set.
	c.handleInternal("room_members_list", json.RawMessage(`{"type":"room_members_list","room":"rm_a","members":["usr_x","usr_y","usr_z"]}`))

	got, _ := c.RoomMembers("rm_a")
	if !reflect.DeepEqual(got, []string{"usr_x", "usr_y", "usr_z"}) {
		t.Fatalf("cache after refresh = %v, want [usr_x usr_y usr_z]", got)
	}
	stored, loaded, _ := c.store.GetRoomMembers("rm_a")
	if !loaded || !reflect.DeepEqual(stored, []string{"usr_x", "usr_y", "usr_z"}) {
		t.Fatalf("persisted after refresh = %v loaded=%v", stored, loaded)
	}
}

// TestRoomMembersList_IgnoredForReadOnlyRoom verifies the audit-fix-#6
// follow-up: a stale room_members_list response arriving after the room
// became read-only (retired/left) must NOT repopulate the cache or
// member_ids. The client-layer handler is the authoritative guard.
func TestRoomMembersList_IgnoredForReadOnlyRoom(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		eventRaw  string
	}{
		{"retired", "room_retired", `{"type":"room_retired","room":"rm_a","display_name":"g (retired)","retired_at":"2026-01-01T00:00:00Z","retired_by":"admin"}`},
		{"left", "room_left", `{"type":"room_left","room":"rm_a","reason":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientWithRoomStore(t)
			c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a"]}]}`))
			// Room becomes read-only — clears cache + member_ids.
			c.handleInternal(tc.eventType, json.RawMessage(tc.eventRaw))
			if _, ok := c.RoomMembers("rm_a"); ok {
				t.Fatal("precondition: cache should be cleared after the room becomes read-only")
			}

			// Stale refresh response for the now-read-only room.
			c.handleInternal("room_members_list", json.RawMessage(`{"type":"room_members_list","room":"rm_a","members":["usr_x","usr_y"]}`))

			if _, ok := c.RoomMembers("rm_a"); ok {
				t.Fatal("stale room_members_list must not repopulate the cache for a read-only room")
			}
			if _, loaded, _ := c.store.GetRoomMembers("rm_a"); loaded {
				t.Fatal("stale room_members_list must not repopulate member_ids for a read-only room")
			}
		})
	}
}

func TestClearPaths_DropCacheAndMemberIDs(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		raw  string
	}{
		{"room_left", "room_left", `{"type":"room_left","room":"rm_a","reason":""}`},
		{"left_rooms", "left_rooms", `{"type":"left_rooms","rooms":[{"room":"rm_a","reason":"removed","initiated_by":"usr_admin","left_at":1700000000}]}`},
		{"room_retired", "room_retired", `{"type":"room_retired","room":"rm_a","display_name":"g (retired)","retired_at":"2026-01-01T00:00:00Z","retired_by":"admin"}`},
		{"retired_rooms", "retired_rooms", `{"type":"retired_rooms","rooms":[{"type":"room_retired","room":"rm_a","display_name":"g (retired)","retired_at":"2026-01-01T00:00:00Z","retired_by":"admin"}]}`},
		{"room_deleted", "room_deleted", `{"type":"room_deleted","room":"rm_a"}`},
		{"deleted_rooms", "deleted_rooms", `{"type":"deleted_rooms","rooms":["rm_a"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientWithRoomStore(t)
			c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b"]}]}`))
			// Precondition: loaded.
			if _, ok := c.RoomMembers("rm_a"); !ok {
				t.Fatal("precondition: rm_a should be cached")
			}

			c.handleInternal(tc.typ, json.RawMessage(tc.raw))

			if _, ok := c.RoomMembers("rm_a"); ok {
				t.Fatalf("%s: cache entry should be dropped", tc.name)
			}
			// room_deleted hard-deletes the row; for the others the row
			// stays but member_ids must be NULL (not loaded).
			if _, loaded, _ := c.store.GetRoomMembers("rm_a"); loaded {
				t.Fatalf("%s: member_ids should be NULL after clear", tc.name)
			}
		})
	}
}

func TestSyncBatchEvents_DoNotMutateRoomMemberCache(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b"]}]}`))

	c.handleInternal("sync_batch", json.RawMessage(`{
		"type":"sync_batch",
		"messages":[],
		"reactions":[],
		"events":[
			{"type":"room_event","room":"rm_a","event":"leave","user":"usr_b","by":"usr_admin"},
			{"type":"room_event","room":"rm_a","event":"join","user":"usr_c","by":"usr_admin"}
		],
		"epoch_keys":[],
		"page":1,
		"has_more":false
	}`))

	got, ok := c.RoomMembers("rm_a")
	if !ok || !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("sync_batch Events mutated roomMemberCache: got %v ok=%v, want [usr_a usr_b]", got, ok)
	}
}

func TestRetiredRoomsCatchup_FreshDeviceCreatesRow(t *testing.T) {
	c := newClientWithRoomStore(t)
	// No prior room_list — fresh device, no local row for rm_fresh.
	raw := json.RawMessage(`{"type":"retired_rooms","rooms":[
		{"type":"room_retired","room":"rm_fresh","display_name":"x (retired)","retired_at":"2026-01-01T00:00:00Z","retired_by":"admin"}
	]}`)
	c.handleInternal("retired_rooms", raw)

	// EnsureRetiredRoom must have INSERTed the row (UPDATE-only MarkRoomRetired
	// would have silently dropped it).
	if !c.store.IsRoomRetired("rm_fresh") {
		t.Fatal("fresh-device retired_rooms catchup should record the retirement")
	}
	// No member-list UI: member_ids stays NULL.
	if _, loaded, _ := c.store.GetRoomMembers("rm_fresh"); loaded {
		t.Fatal("retired room must not have a loaded member list")
	}
}

func TestReconnectOrdering_RetiredThenRoomList(t *testing.T) {
	c := newClientWithRoomStore(t)
	// Catchup order on the wire: retired_rooms BEFORE room_list.
	c.handleInternal("retired_rooms", json.RawMessage(`{"type":"retired_rooms","rooms":[{"type":"room_retired","room":"rm_retired","display_name":"r (retired)","retired_at":"2026-01-01T00:00:00Z","retired_by":"admin"}]}`))
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_active","name":"g","topic":"","members":["usr_a"]}]}`))

	// Active room cached; retired room absent from cache.
	if _, ok := c.RoomMembers("rm_active"); !ok {
		t.Fatal("active room should be cached")
	}
	if _, ok := c.RoomMembers("rm_retired"); ok {
		t.Fatal("retired room should not be in the member cache")
	}
	if !c.store.IsRoomRetired("rm_retired") {
		t.Fatal("retired room should remain retired after room_list")
	}
}

func TestReconnectOrdering_PopulateThenLeftRoomsClearsCache(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b"]}]}`))
	c.handleInternal("left_rooms", json.RawMessage(`{"type":"left_rooms","rooms":[{"room":"rm_a","reason":"removed","initiated_by":"usr_admin","left_at":1700000000}]}`))

	if _, ok := c.RoomMembers("rm_a"); ok {
		t.Fatal("left_rooms after room_list should clear roomMemberCache")
	}
	if _, loaded, _ := c.store.GetRoomMembers("rm_a"); loaded {
		t.Fatal("left_rooms after room_list should clear persisted member_ids")
	}
}

func TestReconnectOrdering_LeftRoomsThenRoomListRejoinLoadsActiveRoom(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("left_rooms", json.RawMessage(`{"type":"left_rooms","rooms":[{"room":"rm_a","reason":"removed","initiated_by":"usr_admin","left_at":1700000000}]}`))
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b"]}]}`))

	got, ok := c.RoomMembers("rm_a")
	if !ok || !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("active room_list should rehydrate rejoined room: got %v ok=%v", got, ok)
	}
	if c.store.IsRoomLeft("rm_a") {
		t.Fatal("active room_list should clear local left marker")
	}
}

func TestRoomMembers_DefensiveCopy(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a","usr_b"]}]}`))

	got, _ := c.RoomMembers("rm_a")
	got[0] = "MUTATED"
	again, _ := c.RoomMembers("rm_a")
	if again[0] != "usr_a" {
		t.Fatalf("defensive copy violated: %v", again)
	}
}

// TestGroupMembers_DefensiveCopy mirrors TestRoomMembers_DefensiveCopy: the
// info-panel live refresh (Finding 1) reads group membership on every
// Update/View cycle, so GroupMembers must return a copy, never the backing
// slice.
func TestGroupMembers_DefensiveCopy(t *testing.T) {
	c := New(Config{})
	SetGroupMembersForTesting(c, "grp_a", []string{"usr_a", "usr_b"})

	got := c.GroupMembers("grp_a")
	got[0] = "MUTATED"
	again := c.GroupMembers("grp_a")
	if again[0] != "usr_a" {
		t.Fatalf("defensive copy violated: %v", again)
	}
}

// TestRoomMemberCache_ConcurrentAccess exercises the cache under -race with
// concurrent snapshot writes, delta events, and reads.
func TestRoomMemberCache_ConcurrentAccess(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.handleInternal("room_list", json.RawMessage(`{"type":"room_list","rooms":[{"id":"rm_a","name":"g","topic":"","members":["usr_a"]}]}`))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.handleInternal("room_event", json.RawMessage(`{"type":"room_event","room":"rm_a","event":"join","user":"usr_x","by":"a"}`))
				c.handleInternal("room_event", json.RawMessage(`{"type":"room_event","room":"rm_a","event":"leave","user":"usr_x","by":"a"}`))
				_, _ = c.RoomMembers("rm_a")
				c.setRoomMembers("rm_a", []string{"usr_a", "usr_b"})
			}
		}()
	}
	wg.Wait()
}
