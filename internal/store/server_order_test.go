package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// S2a: the local messages cache carries the server's authoritative
// per-conversation commit order (server_order). Every persisted row is
// server-originated, so InsertMessage enforces server_order > 0 and exactly one
// context column before SQLite's CHECK constraints act as a backstop.

func TestInsertMessage_RejectsMissingServerOrder(t *testing.T) {
	s := openTestStore(t)

	_, err := s.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "hi", TS: 1, Room: "general",
		// ServerOrder omitted -> 0
	})
	if err == nil {
		t.Fatal("expected error for missing server_order, got nil")
	}
	if !strings.Contains(err.Error(), "server_order") {
		t.Errorf("error should mention server_order, got: %v", err)
	}

	// Negative is rejected too.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: -5, ID: "m2", Sender: "alice", Body: "hi", TS: 1, Room: "general",
	}); err == nil {
		t.Fatal("expected error for negative server_order, got nil")
	}
}

func TestInsertMessage_RejectsWrongContextCount(t *testing.T) {
	s := openTestStore(t)

	// Zero contexts.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "m1", Sender: "alice", Body: "hi", TS: 1,
	}); err == nil {
		t.Fatal("expected error for zero contexts, got nil")
	}

	// Two contexts (room + dm).
	_, err := s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "m2", Sender: "alice", Body: "hi", TS: 1,
		Room: "general", DM: "dm1",
	})
	if err == nil {
		t.Fatal("expected error for two contexts, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error should mention exactly-one-context, got: %v", err)
	}

	// All three contexts.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "m3", Sender: "alice", Body: "hi", TS: 1,
		Room: "general", Group: "g1", DM: "dm1",
	}); err == nil {
		t.Fatal("expected error for three contexts, got nil")
	}
}

func TestInsertMessage_StoresAndReadsServerOrder(t *testing.T) {
	s := openTestStore(t)

	// Room context.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 42, ID: "r1", Sender: "alice", Body: "room", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	rooms, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get room: %v", err)
	}
	if len(rooms) != 1 || rooms[0].ServerOrder != 42 {
		t.Fatalf("room server_order = %v (want one row with 42)", rooms)
	}

	// Group context.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 7, ID: "g1m", Sender: "bob", Body: "grp", TS: 1, Group: "grp1",
	}); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	groups, err := s.GetGroupMessages("grp1", 10)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if len(groups) != 1 || groups[0].ServerOrder != 7 {
		t.Fatalf("group server_order = %v (want one row with 7)", groups)
	}

	// DM context.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 99, ID: "d1m", Sender: "carol", Body: "dm", TS: 1, DM: "dm1",
	}); err != nil {
		t.Fatalf("insert dm: %v", err)
	}
	dms, err := s.GetDMMessages("dm1", 10)
	if err != nil {
		t.Fatalf("get dm: %v", err)
	}
	if len(dms) != 1 || dms[0].ServerOrder != 99 {
		t.Fatalf("dm server_order = %v (want one row with 99)", dms)
	}
}

// Per-conversation server_order is independent: two conversations may both have
// a row at server_order=1 (the column is non-unique by design; S1's server-side
// AUTOINCREMENT guarantees no duplicate within a single conversation).
func TestInsertMessage_ServerOrderIsPerConversation(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "a1", Sender: "alice", Body: "x", TS: 1, Room: "alpha",
	}); err != nil {
		t.Fatalf("insert alpha: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "b1", Sender: "bob", Body: "y", TS: 1, Room: "beta",
	}); err != nil {
		t.Fatalf("insert beta (same server_order, different room): %v", err)
	}

	alpha, _ := s.GetRoomMessages("alpha", 10)
	beta, _ := s.GetRoomMessages("beta", 10)
	if len(alpha) != 1 || len(beta) != 1 {
		t.Fatalf("expected one row per room, got alpha=%d beta=%d", len(alpha), len(beta))
	}
}

// A re-delivered identical message (live broadcast + sync_batch + history_result
// all re-hit the same id) must converge quietly: ON CONFLICT(id) DO NOTHING, so
// the second insert reports inserted=false with no error and the original row is
// preserved unchanged.
func TestInsertMessage_OnConflictIdempotent(t *testing.T) {
	s := openTestStore(t)

	first, err := s.InsertMessage(StoredMessage{
		ServerOrder: 5, ID: "dup", Sender: "alice", Body: "original", TS: 1, Room: "general",
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !first {
		t.Fatal("first insert should report inserted=true")
	}

	// Same id, different body/server_order: must be ignored, not overwritten.
	second, err := s.InsertMessage(StoredMessage{
		ServerOrder: 6, ID: "dup", Sender: "alice", Body: "changed", TS: 2, Room: "general",
	})
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if second {
		t.Fatal("second insert of same id should report inserted=false")
	}

	msgs, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one row after duplicate insert, got %d", len(msgs))
	}
	if msgs[0].Body != "original" || msgs[0].ServerOrder != 5 {
		t.Errorf("original row should be preserved, got body=%q server_order=%d", msgs[0].Body, msgs[0].ServerOrder)
	}
}

// The clean-rebuild guard drops a pre-server_order messages cache so init
// recreates it with the new schema. The app has no users, so cached rows simply
// re-sync from the server; there is no migration that preserves them.
func TestEnsureMessagesServerOrderSchema_CleanRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// First open creates the current schema; tear it down and replace it with a
	// legacy messages table that predates server_order, then seed a legacy row.
	s1, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	s1.db.Exec(`DROP TRIGGER IF EXISTS messages_ai`)
	s1.db.Exec(`DROP TRIGGER IF EXISTS messages_ad`)
	s1.db.Exec(`DROP TABLE IF EXISTS messages_fts`)
	if _, err := s1.db.Exec(`DROP TABLE IF EXISTS messages`); err != nil {
		t.Fatalf("drop new messages: %v", err)
	}
	if _, err := s1.db.Exec(`
		CREATE TABLE messages (
			id     TEXT PRIMARY KEY,
			sender TEXT NOT NULL,
			body   TEXT NOT NULL,
			ts     INTEGER NOT NULL,
			room   TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create legacy messages: %v", err)
	}
	if _, err := s1.db.Exec(`INSERT INTO messages (id, sender, body, ts, room) VALUES ('old1','alice','legacy',1,'general')`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if s1.messageColumnExists("server_order") {
		t.Fatal("legacy table should not have server_order yet")
	}
	s1.Close()

	// Reopen: init() runs the clean-rebuild guard, which drops the legacy table
	// (and its absent FTS/triggers) so the CREATE recreates it with server_order.
	s2, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	if !s2.messageColumnExists("server_order") {
		t.Fatal("server_order column should exist after clean rebuild")
	}
	// Legacy row is gone (clean rebuild, not a preserving migration).
	msgs, err := s2.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get after rebuild: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("legacy rows should be dropped, got %d", len(msgs))
	}
	// New schema accepts server_order inserts.
	if _, err := s2.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "new1", Sender: "bob", Body: "fresh", TS: 2, Room: "general",
	}); err != nil {
		t.Fatalf("insert into rebuilt table: %v", err)
	}
}
