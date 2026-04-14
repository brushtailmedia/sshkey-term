package store

import (
	"testing"
)

// These tests share openTestStore from store_test.go (unencrypted
// temp-dir helper). The store_test.go helper is the canonical pattern
// for all store package tests — adding a second local helper would
// duplicate the plumbing.

// --- IsLocalUserGroupAdmin / SetLocalUserGroupAdmin ---

func TestIsLocalUserGroupAdmin_UnknownGroupReturnsFalseNoError(t *testing.T) {
	s := openTestStore(t)
	isAdmin, err := s.IsLocalUserGroupAdmin("group_does_not_exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isAdmin {
		t.Errorf("expected false for unknown group, got true")
	}
}

func TestSetLocalUserGroupAdmin_UpsertPath(t *testing.T) {
	s := openTestStore(t)
	// Seed a group row via StoreGroup first — SetLocalUserGroupAdmin
	// is a pure UPDATE and relies on the row existing.
	if err := s.StoreGroup("group_test", "Test Group", "alice,bob"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}

	// Initial state: is_admin defaults to 0 from the CREATE TABLE.
	if got, _ := s.IsLocalUserGroupAdmin("group_test"); got {
		t.Errorf("expected initial false, got true")
	}

	// Promote.
	if err := s.SetLocalUserGroupAdmin("group_test", true); err != nil {
		t.Fatalf("set true: %v", err)
	}
	if got, _ := s.IsLocalUserGroupAdmin("group_test"); !got {
		t.Errorf("expected true after promote, got false")
	}

	// Demote.
	if err := s.SetLocalUserGroupAdmin("group_test", false); err != nil {
		t.Fatalf("set false: %v", err)
	}
	if got, _ := s.IsLocalUserGroupAdmin("group_test"); got {
		t.Errorf("expected false after demote, got true")
	}
}

// TestSetLocalUserGroupAdmin_DoesNotClobberMembers is the regression
// for the design choice that admin state changes (promote/demote) must
// not overwrite the members list. The StoreGroup upsert writes name
// and members columns; SetLocalUserGroupAdmin only writes is_admin.
// A promote followed by a members read should see the original
// members unchanged.
func TestSetLocalUserGroupAdmin_DoesNotClobberMembers(t *testing.T) {
	s := openTestStore(t)
	if err := s.StoreGroup("group_c", "Clobber Test", "alice,bob,carol"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}

	if err := s.SetLocalUserGroupAdmin("group_c", true); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Members row should still be "alice,bob,carol"
	all, err := s.GetAllGroups()
	if err != nil {
		t.Fatalf("GetAllGroups: %v", err)
	}
	got, ok := all["group_c"]
	if !ok {
		t.Fatalf("group_c missing from cache")
	}
	if got[0] != "Clobber Test" || got[1] != "alice,bob,carol" {
		t.Errorf("members or name mutated by admin update: got name=%q members=%q", got[0], got[1])
	}
}

// --- RecordGroupEvent / GetGroupEvents / GetRecentGroupEvents ---

func TestRecordGroupEvent_PersistsAndReadsBack(t *testing.T) {
	s := openTestStore(t)

	// Insert a single rename event.
	if err := s.RecordGroupEvent(
		"group_e", "rename", "alice", "alice", "", "New Name", false, 1000,
	); err != nil {
		t.Fatalf("record: %v", err)
	}

	events, err := s.GetGroupEvents("group_e", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.GroupID != "group_e" {
		t.Errorf("GroupID = %q, want group_e", e.GroupID)
	}
	if e.Event != "rename" {
		t.Errorf("Event = %q, want rename", e.Event)
	}
	if e.User != "alice" || e.By != "alice" {
		t.Errorf("user/by mismatch: user=%q by=%q", e.User, e.By)
	}
	if e.Name != "New Name" {
		t.Errorf("Name = %q, want New Name", e.Name)
	}
	if e.Quiet {
		t.Errorf("Quiet = true, want false")
	}
	if e.TS != 1000 {
		t.Errorf("TS = %d, want 1000", e.TS)
	}
}

func TestRecordGroupEvent_QuietFlagRoundTrip(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordGroupEvent(
		"group_q", "promote", "bob", "alice", "", "", true, 2000,
	); err != nil {
		t.Fatalf("record: %v", err)
	}
	events, _ := s.GetGroupEvents("group_q", 10)
	if len(events) != 1 || !events[0].Quiet {
		t.Errorf("quiet flag lost on round-trip: %+v", events)
	}
}

func TestGetGroupEvents_DescOrderByTs(t *testing.T) {
	s := openTestStore(t)
	// Insert three events out of order — the SELECT should return
	// them newest-first regardless of insertion order.
	s.RecordGroupEvent("group_o", "join", "bob", "alice", "", "", false, 100)
	s.RecordGroupEvent("group_o", "promote", "carol", "alice", "", "", false, 300)
	s.RecordGroupEvent("group_o", "demote", "bob", "carol", "", "", false, 200)

	events, _ := s.GetGroupEvents("group_o", 10)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].TS != 300 || events[1].TS != 200 || events[2].TS != 100 {
		t.Errorf("expected DESC order 300,200,100 — got %d,%d,%d",
			events[0].TS, events[1].TS, events[2].TS)
	}
}

func TestGetGroupEvents_LimitBoundsResults(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 5; i++ {
		s.RecordGroupEvent("group_l", "join", "user", "alice", "", "", false, int64(100+i))
	}
	events, _ := s.GetGroupEvents("group_l", 3)
	if len(events) != 3 {
		t.Errorf("expected limit=3 to bound results, got %d", len(events))
	}
}

func TestGetGroupEvents_DifferentGroupsIsolated(t *testing.T) {
	s := openTestStore(t)
	s.RecordGroupEvent("group_a", "join", "alice", "bob", "", "", false, 100)
	s.RecordGroupEvent("group_b", "join", "carol", "dave", "", "", false, 100)

	eventsA, _ := s.GetGroupEvents("group_a", 10)
	eventsB, _ := s.GetGroupEvents("group_b", 10)

	if len(eventsA) != 1 || eventsA[0].User != "alice" {
		t.Errorf("group_a read leaked across boundary: %v", eventsA)
	}
	if len(eventsB) != 1 || eventsB[0].User != "carol" {
		t.Errorf("group_b read leaked across boundary: %v", eventsB)
	}
}

func TestGetRecentGroupEvents_WrapperMatchesGetGroupEvents(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 3; i++ {
		s.RecordGroupEvent("group_w", "promote", "u", "a", "", "", false, int64(100+i))
	}

	got1, _ := s.GetRecentGroupEvents("group_w", 2)
	got2, _ := s.GetGroupEvents("group_w", 2)

	if len(got1) != len(got2) {
		t.Fatalf("wrapper returned %d, direct call returned %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i].ID != got2[i].ID {
			t.Errorf("entry %d differs between wrapper and direct: %v vs %v", i, got1[i], got2[i])
		}
	}
}
