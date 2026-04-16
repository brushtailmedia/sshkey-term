package store

// Phase 20 — client-side tests for the new leave_reason column,
// MarkXLeft/MarkXRejoined signature extensions, LeftRoom/ArchivedGroup
// struct changes, and the new room_events store helpers.

import (
	"testing"
)

// TestMarkRoomLeft_StoresReason verifies the Phase 20 signature
// extension — leave_reason is persisted and readable via GetLeftRooms.
func TestMarkRoomLeft_StoresReason(t *testing.T) {
	s := openTestStore(t)

	if err := s.UpsertRoom("rm_a", "general", "", 5); err != nil {
		t.Fatalf("store room: %v", err)
	}
	if err := s.MarkRoomLeft("rm_a", 1700000000, "removed"); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	rooms, err := s.GetLeftRooms()
	if err != nil {
		t.Fatalf("get left rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("want 1 left room, got %d", len(rooms))
	}
	if rooms[0].LeaveReason != "removed" {
		t.Errorf("LeaveReason = %q, want removed", rooms[0].LeaveReason)
	}
}

// TestMarkRoomRejoined_ClearsLeaveReason verifies Phase 20 scope item 19 —
// rejoining clears both left_at AND leave_reason so the local mirror
// matches the server's fresh state.
func TestMarkRoomRejoined_ClearsLeaveReason(t *testing.T) {
	s := openTestStore(t)

	s.UpsertRoom("rm_a", "general", "", 5)
	s.MarkRoomLeft("rm_a", 1700000000, "removed")

	// Verify left state.
	rooms, _ := s.GetLeftRooms()
	if len(rooms) != 1 || rooms[0].LeaveReason != "removed" {
		t.Fatalf("precondition: expected 1 left room with reason 'removed', got %+v", rooms)
	}

	// Rejoin — should clear left_at AND leave_reason.
	if err := s.MarkRoomRejoined("rm_a"); err != nil {
		t.Fatalf("rejoin: %v", err)
	}

	rooms, _ = s.GetLeftRooms()
	if len(rooms) != 0 {
		t.Errorf("after rejoin: expected 0 left rooms (left_at cleared), got %d", len(rooms))
	}
}

// TestMarkGroupLeft_StoresReason — group analogue.
func TestMarkGroupLeft_StoresReason(t *testing.T) {
	s := openTestStore(t)

	if err := s.StoreGroup("grp_a", "lunch", "alice,bob"); err != nil {
		t.Fatalf("store group: %v", err)
	}
	if err := s.MarkGroupLeft("grp_a", 1700000000, "retirement"); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	got, err := s.GetArchivedGroups()
	if err != nil {
		t.Fatalf("get archived: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 archived group, got %d", len(got))
	}
	if got[0].LeaveReason != "retirement" {
		t.Errorf("LeaveReason = %q, want retirement", got[0].LeaveReason)
	}
}

// TestMarkGroupRejoined_ClearsLeaveReason — group analogue of the room test.
func TestMarkGroupRejoined_ClearsLeaveReason(t *testing.T) {
	s := openTestStore(t)

	s.StoreGroup("grp_a", "lunch", "alice,bob")
	s.MarkGroupLeft("grp_a", 1700000000, "removed")

	if err := s.MarkGroupRejoined("grp_a"); err != nil {
		t.Fatalf("rejoin: %v", err)
	}

	got, _ := s.GetArchivedGroups()
	if len(got) != 0 {
		t.Errorf("after rejoin: expected 0 archived groups, got %d", len(got))
	}
}

// TestRecordRoomEvent_ClientSide verifies the client's parallel
// RecordRoomEvent helper (separate table from group_events so the
// existing group-side code paths are untouched).
func TestRecordRoomEvent_ClientSide(t *testing.T) {
	s := openTestStore(t)

	if err := s.RecordRoomEvent(
		"rm_a", "leave", "alice", "admin", "removed", "", 1700000000,
	); err != nil {
		t.Fatalf("record: %v", err)
	}

	events, err := s.GetRoomEvents("rm_a", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Event != "leave" || e.User != "alice" || e.By != "admin" || e.Reason != "removed" {
		t.Errorf("event fields mismatch: %+v", e)
	}
}

// TestGetRoomEvents_OrderedDesc verifies newest-first ordering for
// the TUI's /audit-style displays.
func TestGetRoomEvents_OrderedDesc(t *testing.T) {
	s := openTestStore(t)

	s.RecordRoomEvent("rm_a", "join", "alice", "admin", "", "", 1000)
	s.RecordRoomEvent("rm_a", "topic", "", "admin", "", "new topic", 2000)
	s.RecordRoomEvent("rm_a", "leave", "bob", "admin", "removed", "", 3000)

	events, _ := s.GetRoomEvents("rm_a", 10)
	if len(events) != 3 {
		t.Fatalf("want 3, got %d", len(events))
	}
	if events[0].TS != 3000 || events[1].TS != 2000 || events[2].TS != 1000 {
		t.Errorf("want TS DESC order (3000, 2000, 1000), got %d, %d, %d",
			events[0].TS, events[1].TS, events[2].TS)
	}
}
