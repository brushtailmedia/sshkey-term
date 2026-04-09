package store

import (
	"testing"
	"time"
)

// TestGetActiveGroupIDs verifies that GetActiveGroupIDs returns only
// rows where left_at == 0, used by the client.go group_list dispatch
// handler to reconcile against the server's authoritative list.
func TestGetActiveGroupIDs(t *testing.T) {
	s := openTestStore(t)

	// Three groups: two active, one already left
	if err := s.StoreGroup("group_active1", "Active 1", "alice,bob"); err != nil {
		t.Fatalf("store group_active1: %v", err)
	}
	if err := s.StoreGroup("group_active2", "Active 2", "alice,carol"); err != nil {
		t.Fatalf("store group_active2: %v", err)
	}
	if err := s.StoreGroup("group_left", "Already Left", "alice,dave"); err != nil {
		t.Fatalf("store group_left: %v", err)
	}
	if err := s.MarkGroupLeft("group_left", time.Now().Unix()); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	got, err := s.GetActiveGroupIDs()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 active groups, got %d (%v)", len(got), got)
	}
	want := map[string]bool{"group_active1": true, "group_active2": true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected active group: %q", id)
		}
	}
}

// TestGetActiveGroupIDs_None verifies the empty case returns nil without
// error.
func TestGetActiveGroupIDs_None(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetActiveGroupIDs()
	if err != nil {
		t.Errorf("empty case should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

// TestGetActiveRoomIDs verifies the room equivalent. Same shape as
// TestGetActiveGroupIDs.
func TestGetActiveRoomIDs(t *testing.T) {
	s := openTestStore(t)

	if err := s.UpsertRoom("room_general", "general", "General chat", 5); err != nil {
		t.Fatalf("upsert general: %v", err)
	}
	if err := s.UpsertRoom("room_eng", "engineering", "Engineering", 3); err != nil {
		t.Fatalf("upsert eng: %v", err)
	}
	if err := s.UpsertRoom("room_archived", "archive", "Archived room", 2); err != nil {
		t.Fatalf("upsert archived: %v", err)
	}
	if err := s.MarkRoomLeft("room_archived", time.Now().Unix()); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	got, err := s.GetActiveRoomIDs()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 active rooms, got %d (%v)", len(got), got)
	}
	want := map[string]bool{"room_general": true, "room_eng": true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected active room: %q", id)
		}
	}
}

// TestGroupListReconciliation_OfflineLeaveCatchup simulates the exact
// reconciliation logic the client.go group_list handler runs:
//
//  1. Local has group_A and group_B as active
//  2. Server's group_list comes back with only group_A (alice left
//     group_B on another device while this one was offline)
//  3. Reconciliation: walk local active IDs, mark any missing from
//     server's set as left
//  4. Result: group_A still active, group_B marked as left
//
// This is the regression test for the multi-device offline catchup gap
// — without the reconciliation, group_B would silently disappear from
// the sidebar with no archived marker.
func TestGroupListReconciliation_OfflineLeaveCatchup(t *testing.T) {
	s := openTestStore(t)

	// Local state: alice is in two groups, both active
	s.StoreGroup("group_A", "A", "alice,bob")
	s.StoreGroup("group_B", "B", "alice,carol")

	// Server's authoritative response only includes group_A.
	// (alice ran /leave on group_B from another device while this
	// one was offline, so the server removed her from group_B's
	// member list and stopped including it in alice's group_list.)
	serverResponse := map[string]bool{"group_A": true}

	// Simulate the reconciliation
	activeIDs, err := s.GetActiveGroupIDs()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	now := time.Now().Unix()
	for _, id := range activeIDs {
		if !serverResponse[id] {
			if err := s.MarkGroupLeft(id, now); err != nil {
				t.Fatalf("mark left: %v", err)
			}
		}
	}

	// Verify: group_A still active, group_B marked as left
	if s.GetGroupLeftAt("group_A") != 0 {
		t.Error("group_A should still be active")
	}
	if s.GetGroupLeftAt("group_B") == 0 {
		t.Error("group_B should be marked as left after reconciliation")
	}
}

// TestRoomListReconciliation_OfflineLeaveCatchup is the room equivalent
// of the above. For rooms there are TWO ways to be removed from a room
// (self /leave on another device, OR admin removal), and the
// reconciliation handles both paths the same way.
func TestRoomListReconciliation_OfflineLeaveCatchup(t *testing.T) {
	s := openTestStore(t)

	s.UpsertRoom("room_A", "general", "General", 3)
	s.UpsertRoom("room_B", "engineering", "Engineering", 5)

	// Server returns only room_A (alice was removed from room_B by an
	// admin while this device was offline)
	serverResponse := map[string]bool{"room_A": true}

	activeIDs, err := s.GetActiveRoomIDs()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	now := time.Now().Unix()
	for _, id := range activeIDs {
		if !serverResponse[id] {
			if err := s.MarkRoomLeft(id, now); err != nil {
				t.Fatalf("mark left: %v", err)
			}
		}
	}

	if s.GetRoomLeftAt("room_A") != 0 {
		t.Error("room_A should still be active")
	}
	if s.GetRoomLeftAt("room_B") == 0 {
		t.Error("room_B should be marked as left after reconciliation")
	}
}

// TestReconciliation_ComposesWithDeletedGroups verifies that the
// reconciliation order in the handshake (deleted_groups before
// group_list) leaves no stale state. Sequence:
//
//  1. alice has 3 local groups: A, B, C all active
//  2. alice /deleted A on another device → A arrives in deleted_groups
//     handler → marked left + purged
//  3. alice /leave'd B on another device → B is missing from group_list
//  4. C is still active
//
// After both handlers run, only C should be active. A should be
// marked-left (purged), B should be marked-left (not purged).
func TestReconciliation_ComposesWithDeletedGroups(t *testing.T) {
	s := openTestStore(t)

	s.StoreGroup("group_A", "A", "alice,bob")
	s.StoreGroup("group_B", "B", "alice,carol")
	s.StoreGroup("group_C", "C", "alice,dave")

	// Insert a message into A (to verify it gets purged)
	if err := s.InsertMessage(StoredMessage{
		ID: "ma1", Sender: "alice", Body: "hello", TS: 1, Group: "group_A",
	}); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	now := time.Now().Unix()

	// Step 1: deleted_groups handler runs FIRST in the handshake.
	// alice /deleted group_A on another device. Mark left + purge.
	s.MarkGroupLeft("group_A", now)
	if err := s.PurgeGroupMessages("group_A"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Step 2: group_list handler runs SECOND. Server returns only
	// group_C. Reconciliation walks active locals, marks missing as left.
	serverResponse := map[string]bool{"group_C": true}
	activeIDs, _ := s.GetActiveGroupIDs() // should already exclude group_A
	for _, id := range activeIDs {
		if !serverResponse[id] {
			s.MarkGroupLeft(id, now)
		}
	}

	// Verify final state:
	// - group_A: marked left, messages purged
	// - group_B: marked left (via reconciliation), messages NOT purged
	// - group_C: still active
	if s.GetGroupLeftAt("group_A") == 0 {
		t.Error("group_A should be marked left after deleted_groups handler")
	}
	msgsA, _ := s.GetGroupMessages("group_A", 100)
	if len(msgsA) != 0 {
		t.Errorf("group_A messages should be purged, got %d", len(msgsA))
	}

	if s.GetGroupLeftAt("group_B") == 0 {
		t.Error("group_B should be marked left via reconciliation")
	}

	if s.GetGroupLeftAt("group_C") != 0 {
		t.Error("group_C should still be active")
	}
}
