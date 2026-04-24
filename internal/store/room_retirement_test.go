package store

import (
	"testing"
)

// Phase 12 Chunk 6 — client store tests for MarkRoomRetired,
// IsRoomRetired, and PurgeRoomMessages.

// TestMarkRoomRetired_SetsFlagAndUpdatesDisplayName verifies that
// MarkRoomRetired flips the retired_at column to the given timestamp
// and overwrites the cached display name with the post-retirement
// suffixed form.
func TestMarkRoomRetired_SetsFlagAndUpdatesDisplayName(t *testing.T) {
	s := openTestStore(t)

	// Seed a room via UpsertRoom first
	if err := s.UpsertRoom("room_x", "engineering", "Eng chat", 3); err != nil {
		t.Fatalf("UpsertRoom: %v", err)
	}

	// Verify it starts non-retired
	if s.IsRoomRetired("room_x") {
		t.Fatal("room should not be retired initially")
	}

	// Retire it
	if err := s.MarkRoomRetired("room_x", "engineering_V1St", 12345); err != nil {
		t.Fatalf("MarkRoomRetired: %v", err)
	}

	// Verify retired_at is set
	if !s.IsRoomRetired("room_x") {
		t.Error("room should be retired after MarkRoomRetired")
	}

	// Verify display name was updated to the suffixed form
	name := s.GetRoomName("room_x")
	if name != "engineering_V1St" {
		t.Errorf("display name = %q, want engineering_V1St", name)
	}
}

// TestIsRoomRetired_FalseForMissing verifies that querying a
// nonexistent room returns false (not an error).
func TestIsRoomRetired_FalseForMissing(t *testing.T) {
	s := openTestStore(t)
	if s.IsRoomRetired("room_nonexistent") {
		t.Error("missing room should not be reported as retired")
	}
}

// TestIsRoomRetired_FalseForActiveRoom verifies the default state of
// a freshly inserted room.
func TestIsRoomRetired_FalseForActiveRoom(t *testing.T) {
	s := openTestStore(t)
	s.UpsertRoom("room_active", "general", "General", 2)
	if s.IsRoomRetired("room_active") {
		t.Error("active room should not be reported as retired")
	}
}

// TestMarkRoomRetired_IndependentFromLeftAt verifies Q9: retired_at
// and left_at are tracked independently. A user can be retired
// without having left the room.
func TestMarkRoomRetired_IndependentFromLeftAt(t *testing.T) {
	s := openTestStore(t)
	s.UpsertRoom("room_x", "general", "General", 2)

	// Retire without leaving
	s.MarkRoomRetired("room_x", "general_XXXX", 100)

	// left_at should still be 0 (not left)
	if s.IsRoomLeft("room_x") {
		t.Error("room should not be marked left just because it was retired")
	}
	// But retired_at should be set
	if !s.IsRoomRetired("room_x") {
		t.Error("room should be retired")
	}

	// Now also leave it
	s.MarkRoomLeft("room_x", 200, "")

	// Both flags should now be set
	if !s.IsRoomLeft("room_x") || !s.IsRoomRetired("room_x") {
		t.Error("both left and retired flags should be set")
	}
}

// TestPurgeRoomMessages_DropsMessages verifies that messages are
// cleared from the room.
func TestPurgeRoomMessages_DropsMessages(t *testing.T) {
	s := openTestStore(t)

	// Insert messages in two rooms
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "hi", TS: 1, Room: "room_target"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "b", Body: "ho", TS: 2, Room: "room_target"})
	s.InsertMessage(StoredMessage{ID: "m3", Sender: "c", Body: "keep", TS: 3, Room: "room_other"})

	if err := s.PurgeRoomMessages("room_target"); err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}

	// Target room should be empty
	msgs, _ := s.GetRoomMessages("room_target", 10)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages in purged room, got %d", len(msgs))
	}

	// Other room should be untouched
	other, _ := s.GetRoomMessages("room_other", 10)
	if len(other) != 1 {
		t.Errorf("other room should be untouched, got %d messages", len(other))
	}
}

// TestPurgeRoomMessages_DropsReactions verifies that reactions
// attached to the room's messages are also purged.
func TestPurgeRoomMessages_DropsReactions(t *testing.T) {
	s := openTestStore(t)

	s.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "hi", TS: 1, Room: "room_target"})
	s.InsertReaction(StoredReaction{ReactionID: "r1", MessageID: "m1", User: "b", Emoji: "👍", TS: 2})

	// Confirm reaction exists
	reactions, _ := s.GetReactionsForMessages([]string{"m1"})
	if len(reactions) != 1 {
		t.Fatalf("expected 1 reaction before purge, got %d", len(reactions))
	}

	if err := s.PurgeRoomMessages("room_target"); err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}

	// Reactions should be gone
	reactions, _ = s.GetReactionsForMessages([]string{"m1"})
	if len(reactions) != 0 {
		t.Errorf("expected 0 reactions after purge, got %d", len(reactions))
	}
}

// TestPurgeRoomMessages_DropsEpochKeys verifies that epoch keys for
// the room are removed as part of the purge.
func TestPurgeRoomMessages_DropsEpochKeys(t *testing.T) {
	s := openTestStore(t)

	key := []byte("32-byte-symmetric-key-for-test00")
	if err := s.StoreEpochKey("room_target", 1, key); err != nil {
		t.Fatalf("StoreEpochKey: %v", err)
	}

	// Confirm stored
	got, _ := s.GetEpochKey("room_target", 1)
	if got == nil {
		t.Fatal("epoch key should exist before purge")
	}

	if err := s.PurgeRoomMessages("room_target"); err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}

	// Should be gone
	got, _ = s.GetEpochKey("room_target", 1)
	if got != nil {
		t.Error("epoch key should be deleted by purge")
	}
}

// TestPurgeRoomMessages_Idempotent verifies that calling purge on an
// already-empty room is a no-op.
func TestPurgeRoomMessages_Idempotent(t *testing.T) {
	s := openTestStore(t)

	// Purge a room that never had any data
	if err := s.PurgeRoomMessages("room_never_existed"); err != nil {
		t.Errorf("purge of never-used room should be no-op, got: %v", err)
	}

	// Insert, purge, purge again
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "hi", TS: 1, Room: "room_x"})
	if err := s.PurgeRoomMessages("room_x"); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	if err := s.PurgeRoomMessages("room_x"); err != nil {
		t.Errorf("second purge should be no-op, got: %v", err)
	}
}
