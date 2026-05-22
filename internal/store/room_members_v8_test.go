package store

// V8 — tests for the rooms.member_ids cache column and its helpers, plus
// the retired-room sidebar helpers (GetRetiredRooms / EnsureRetiredRoom).

import (
	"reflect"
	"testing"
)

// TestEnsureRoomMembersSchema_Idempotent verifies the migration is a no-op
// when the column already exists (it always does after Open).
func TestEnsureRoomMembersSchema_Idempotent(t *testing.T) {
	s := openTestStore(t)
	if !s.roomsColumnExists("member_ids") {
		t.Fatal("member_ids column should exist after Open")
	}
	if err := s.ensureRoomMembersSchema(); err != nil {
		t.Fatalf("ensureRoomMembersSchema (2nd call): %v", err)
	}
	if !s.roomsColumnExists("member_ids") {
		t.Fatal("member_ids column should still exist after idempotent re-run")
	}
}

// TestEnsureRoomMembersSchema_UpgradePath simulates a pre-V8 DB by dropping
// the member_ids column, then verifies the migration re-adds it and that
// pre-existing rows come back as not-loaded (NULL).
func TestEnsureRoomMembersSchema_UpgradePath(t *testing.T) {
	s := openTestStore(t)

	// Pre-existing room row from a "pre-V8" session.
	if err := s.UpsertRoom("rm_old", "general", "topic", 3); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	// Simulate a pre-V8 schema (no member_ids column) by rebuilding the
	// rooms table without it. This SQLite build does not support
	// ALTER TABLE ... DROP COLUMN, so we rebuild + rename.
	for _, stmt := range []string{
		`CREATE TABLE rooms_prev8 (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			topic TEXT NOT NULL DEFAULT '',
			members INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			left_at INTEGER NOT NULL DEFAULT 0,
			retired_at INTEGER NOT NULL DEFAULT 0,
			leave_reason TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO rooms_prev8 (id, name, topic, members, updated_at, left_at, retired_at, leave_reason)
			SELECT id, name, topic, members, updated_at, left_at, retired_at, leave_reason FROM rooms`,
		`DROP TABLE rooms`,
		`ALTER TABLE rooms_prev8 RENAME TO rooms`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("rebuild pre-V8 rooms table: %v\nstmt: %s", err, stmt)
		}
	}
	if s.roomsColumnExists("member_ids") {
		t.Fatal("member_ids should be absent in the simulated pre-V8 schema")
	}

	// Run the migration.
	if err := s.ensureRoomMembersSchema(); err != nil {
		t.Fatalf("ensureRoomMembersSchema upgrade: %v", err)
	}
	if !s.roomsColumnExists("member_ids") {
		t.Fatal("member_ids should exist after migration")
	}

	// Existing row has NULL member_ids → not loaded.
	members, loaded, err := s.GetRoomMembers("rm_old")
	if err != nil {
		t.Fatalf("GetRoomMembers: %v", err)
	}
	if loaded {
		t.Fatalf("pre-existing row should be not-loaded after migration, got loaded with %v", members)
	}
}

// TestRoomMembers_TriState verifies the NULL / "" / CSV distinction.
func TestRoomMembers_TriState(t *testing.T) {
	s := openTestStore(t)

	// (1) Room exists but member_ids never set → not loaded.
	if err := s.UpsertRoom("rm_a", "general", "", 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, loaded, err := s.GetRoomMembers("rm_a"); err != nil || loaded {
		t.Fatalf("rm_a should be not-loaded (NULL); loaded=%v err=%v", loaded, err)
	}

	// (2) Loaded with members.
	if err := s.SetRoomMembers("rm_a", []string{"usr_b", "usr_a", "usr_c"}); err != nil {
		t.Fatalf("set members: %v", err)
	}
	got, loaded, err := s.GetRoomMembers("rm_a")
	if err != nil || !loaded {
		t.Fatalf("rm_a should be loaded; loaded=%v err=%v", loaded, err)
	}
	if want := []string{"usr_b", "usr_a", "usr_c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v (server order preserved)", got, want)
	}

	// (3) Loaded with zero members → "" CSV, loaded=true, empty slice.
	if err := s.SetRoomMembers("rm_a", []string{}); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	got, loaded, err = s.GetRoomMembers("rm_a")
	if err != nil || !loaded {
		t.Fatalf("rm_a should be loaded-empty; loaded=%v err=%v", loaded, err)
	}
	if len(got) != 0 {
		t.Fatalf("loaded-empty should be zero members, got %v", got)
	}

	// (4) Missing room → not loaded, no error.
	if _, loaded, err := s.GetRoomMembers("rm_missing"); err != nil || loaded {
		t.Fatalf("missing room should be not-loaded; loaded=%v err=%v", loaded, err)
	}
}

// TestSetRoomMembers_Normalization verifies empty-drop, de-dupe (first
// occurrence), order preservation, and that mutating the input slice after
// the call does not affect stored state.
func TestSetRoomMembers_Normalization(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertRoom("rm_a", "general", "", 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	input := []string{"usr_a", "", "   ", "usr_b", "usr_a", " usr_c ", "usr_b", "\t"}
	if err := s.SetRoomMembers("rm_a", input); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Mutate the caller's slice afterward — must not affect stored state.
	input[0] = "MUTATED"

	got, loaded, err := s.GetRoomMembers("rm_a")
	if err != nil || !loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
	want := []string{"usr_a", "usr_b", "usr_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}
}

// TestRoomMembers_DefensiveCopy verifies that mutating a returned slice does
// not corrupt the next read.
func TestRoomMembers_DefensiveCopy(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertRoom("rm_a", "general", "", 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetRoomMembers("rm_a", []string{"usr_a", "usr_b"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	first, _, _ := s.GetRoomMembers("rm_a")
	first[0] = "MUTATED"
	second, _, _ := s.GetRoomMembers("rm_a")
	if second[0] != "usr_a" {
		t.Fatalf("defensive copy violated: second read = %v", second)
	}
}

// TestUpsertRoomWithMembers_CountConsistency verifies the count and the CSV
// are written from the same normalized slice (no drift).
func TestUpsertRoomWithMembers_CountConsistency(t *testing.T) {
	s := openTestStore(t)
	// Duplicates + empties: normalized to 2 members.
	if err := s.UpsertRoomWithMembers("rm_a", "general", "topic", []string{"usr_a", "usr_a", "", "usr_b"}); err != nil {
		t.Fatalf("upsert with members: %v", err)
	}
	got, loaded, err := s.GetRoomMembers("rm_a")
	if err != nil || !loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
	if want := []string{"usr_a", "usr_b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	// The members count column must equal len(member_ids).
	var count int
	if err := s.db.QueryRow(`SELECT members FROM rooms WHERE id = ?`, "rm_a").Scan(&count); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	if count != 2 {
		t.Fatalf("members count = %d, want 2 (consistent with member_ids)", count)
	}
}

// TestGetAllLoadedRoomMembers_OnlyNonNull verifies hydration skips not-loaded
// rooms.
func TestGetAllLoadedRoomMembers_OnlyNonNull(t *testing.T) {
	s := openTestStore(t)
	// rm_loaded: loaded; rm_unloaded: NULL member_ids.
	if err := s.UpsertRoomWithMembers("rm_loaded", "g", "", []string{"usr_a", "usr_b"}); err != nil {
		t.Fatalf("upsert loaded: %v", err)
	}
	if err := s.UpsertRoom("rm_unloaded", "h", "", 0); err != nil {
		t.Fatalf("upsert unloaded: %v", err)
	}
	m, err := s.GetAllLoadedRoomMembers()
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if _, ok := m["rm_unloaded"]; ok {
		t.Fatal("unloaded room should not appear in hydration map")
	}
	if got := m["rm_loaded"]; !reflect.DeepEqual(got, []string{"usr_a", "usr_b"}) {
		t.Fatalf("rm_loaded = %v, want [usr_a usr_b]", got)
	}
}

// TestClearRoomMembers_SetsNull verifies clear returns the room to not-loaded.
func TestClearRoomMembers_SetsNull(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertRoomWithMembers("rm_a", "g", "", []string{"usr_a"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, loaded, _ := s.GetRoomMembers("rm_a"); !loaded {
		t.Fatal("precondition: rm_a should be loaded")
	}
	if err := s.ClearRoomMembers("rm_a"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, loaded, _ := s.GetRoomMembers("rm_a"); loaded {
		t.Fatal("rm_a should be not-loaded after ClearRoomMembers")
	}
}

// TestGetRetiredRooms verifies only retired_at>0 rows are returned.
func TestGetRetiredRooms(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertRoom("rm_active", "general", "", 2); err != nil {
		t.Fatalf("upsert active: %v", err)
	}
	if err := s.UpsertRoom("rm_retired", "eng", "", 3); err != nil {
		t.Fatalf("upsert retired: %v", err)
	}
	if err := s.MarkRoomRetired("rm_retired", "eng (retired)", 1700000000); err != nil {
		t.Fatalf("retire: %v", err)
	}
	got, err := s.GetRetiredRooms()
	if err != nil {
		t.Fatalf("GetRetiredRooms: %v", err)
	}
	if len(got) != 1 || got[0].ID != "rm_retired" {
		t.Fatalf("GetRetiredRooms = %+v, want exactly rm_retired", got)
	}
	if got[0].RetiredAt != 1700000000 {
		t.Fatalf("RetiredAt = %d, want 1700000000", got[0].RetiredAt)
	}
}

// TestEnsureRetiredRoom verifies insert-on-fresh and update-on-existing,
// without populating member_ids.
func TestEnsureRetiredRoom(t *testing.T) {
	s := openTestStore(t)

	// Fresh DB, no row yet: MarkRoomRetired (UPDATE-only) would be a no-op.
	if err := s.MarkRoomRetired("rm_fresh", "x (retired)", 1700000000); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if s.IsRoomRetired("rm_fresh") {
		t.Fatal("MarkRoomRetired should be a no-op on a fresh DB (no row to update)")
	}

	// EnsureRetiredRoom inserts the row.
	if err := s.EnsureRetiredRoom("rm_fresh", "x (retired)", 1700000000); err != nil {
		t.Fatalf("ensure (insert): %v", err)
	}
	if !s.IsRoomRetired("rm_fresh") {
		t.Fatal("EnsureRetiredRoom should have inserted a retired row")
	}
	// member_ids must remain NULL (not loaded) — retired rooms have no member UI.
	if _, loaded, _ := s.GetRoomMembers("rm_fresh"); loaded {
		t.Fatal("EnsureRetiredRoom must not populate member_ids")
	}

	// On an existing active row, EnsureRetiredRoom updates retired metadata.
	if err := s.UpsertRoom("rm_existing", "active", "", 5); err != nil {
		t.Fatalf("upsert existing: %v", err)
	}
	if err := s.EnsureRetiredRoom("rm_existing", "active (retired)", 1700000001); err != nil {
		t.Fatalf("ensure (update): %v", err)
	}
	if !s.IsRoomRetired("rm_existing") {
		t.Fatal("EnsureRetiredRoom should have flagged the existing row retired")
	}
}
