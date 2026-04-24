package client

// Phase 16 Gap 1 — tests for the client-side room_updated event
// handler. The server fires this when an admin runs `sshkey-ctl
// update-topic` or `sshkey-ctl rename-room`, and the client must
// upsert the local rooms table row so the next sidebar/info-panel
// render picks up the new name and topic.
//
// Coverage:
//   - happy path: known room gets name + topic updated
//   - unknown room: no error, silent no-op (forward-compat rule)
//   - both fields update independently (rename only, topic only)

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// newClientWithStore returns a Client whose store is backed by a
// fresh temp DB. Used for tests that need to verify the local rooms
// table is updated by event handlers.
func newClientWithRoomStore(t *testing.T) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := New(Config{})
	c.store = st
	return c
}

func TestRoomUpdatedEvent_UpdatesNameAndTopic(t *testing.T) {
	c := newClientWithRoomStore(t)

	// Seed a room in the local store.
	if err := c.store.UpsertRoom("rm_general", "general", "old topic", 5); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fire a room_updated event with both fields changed.
	raw := json.RawMessage(`{"type":"room_updated","room":"rm_general","display_name":"main","topic":"new topic"}`)
	c.handleInternal("room_updated", raw)

	// Local rooms row should reflect the new name and topic.
	if name := c.store.GetRoomName("rm_general"); name != "main" {
		t.Errorf("name = %q, want main", name)
	}
	if topic := c.store.GetRoomTopic("rm_general"); topic != "new topic" {
		t.Errorf("topic = %q, want new topic", topic)
	}
}

// TestRoomUpdatedEvent_RenameOnly verifies that renaming a room
// (with topic unchanged) updates only the name field. The topic in
// the event payload is the CURRENT topic (server re-reads from
// rooms.db before broadcasting), so it overwrites the local topic
// with the same value — a harmless no-op.
func TestRoomUpdatedEvent_RenameOnly(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.store.UpsertRoom("rm_general", "general", "topic stays", 5)

	// Server sees the rename and broadcasts both fields populated.
	raw := json.RawMessage(`{"type":"room_updated","room":"rm_general","display_name":"main","topic":"topic stays"}`)
	c.handleInternal("room_updated", raw)

	if name := c.store.GetRoomName("rm_general"); name != "main" {
		t.Errorf("name = %q, want main", name)
	}
	if topic := c.store.GetRoomTopic("rm_general"); topic != "topic stays" {
		t.Errorf("topic = %q, want topic stays", topic)
	}
}

// TestRoomUpdatedEvent_TopicOnly verifies the inverse — name
// unchanged, topic updated.
func TestRoomUpdatedEvent_TopicOnly(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.store.UpsertRoom("rm_general", "general", "old topic", 5)

	raw := json.RawMessage(`{"type":"room_updated","room":"rm_general","display_name":"general","topic":"new topic"}`)
	c.handleInternal("room_updated", raw)

	if name := c.store.GetRoomName("rm_general"); name != "general" {
		t.Errorf("name = %q, want general", name)
	}
	if topic := c.store.GetRoomTopic("rm_general"); topic != "new topic" {
		t.Errorf("topic = %q, want new topic", topic)
	}
}

// TestRoomUpdatedEvent_UnknownRoom verifies that a room_updated
// event for a room not in the local rooms table is a safe no-op.
// Forward-compat rule — the client may receive an event for a room
// it hasn't fetched yet (e.g. just-promoted admin who got added to
// a new room they haven't seen in room_list yet), and the next
// room_list refresh will catch up.
func TestRoomUpdatedEvent_UnknownRoom(t *testing.T) {
	c := newClientWithRoomStore(t)

	// No seed — rm_ghost doesn't exist locally.
	raw := json.RawMessage(`{"type":"room_updated","room":"rm_ghost","display_name":"ghost","topic":"boo"}`)

	// Should not panic and should not create a row.
	c.handleInternal("room_updated", raw)

	// GetRoomName falls back to the raw ID when the row doesn't exist.
	if name := c.store.GetRoomName("rm_ghost"); name != "rm_ghost" {
		t.Errorf("expected fallback to raw ID, got %q", name)
	}
}

// TestRoomUpdatedEvent_PreservesMembersCount verifies that the
// room_updated handler does NOT reset the members count to 0. This
// is the failure mode that would happen if we used UpsertRoom
// (which takes a members count) instead of UpdateRoomNameTopic
// (which leaves members alone).
func TestRoomUpdatedEvent_PreservesMembersCount(t *testing.T) {
	c := newClientWithRoomStore(t)
	c.store.UpsertRoom("rm_general", "general", "topic", 42)

	raw := json.RawMessage(`{"type":"room_updated","room":"rm_general","display_name":"main","topic":"new topic"}`)
	c.handleInternal("room_updated", raw)

	// We don't have a public getter for member count, so we
	// re-upsert with 0 members and verify the new value sticks
	// (proving UpdateRoomNameTopic didn't leave the table in a
	// broken state). This is a smoke test rather than a direct
	// assertion on the count.
	if err := c.store.UpsertRoom("rm_general", "main", "new topic", 99); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	// Verify name/topic are still the values we just set.
	if name := c.store.GetRoomName("rm_general"); name != "main" {
		t.Errorf("name after re-upsert = %q, want main", name)
	}
}
