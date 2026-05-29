package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// S2b: remote `deleted` tombstones must be durable even when the original
// message was never cached locally (a client that joins after a message was
// created *and* deleted). UpsertDeletedMessage inserts a minimal tombstone when
// the row is absent and soft-deletes (returning attachment file IDs) when
// present.

func TestUpsertDeletedMessage_AbsentRowInsertsTombstone(t *testing.T) {
	s := openTestStore(t)

	// No prior InsertMessage for this id — the original was never cached.
	fileIDs, err := s.UpsertDeletedMessage("ghost", "alice", 1234, 77, "general", "", "")
	if err != nil {
		t.Fatalf("upsert absent: %v", err)
	}
	if fileIDs != nil {
		t.Errorf("absent-row tombstone has no attachments to clean, got %v", fileIDs)
	}

	got, err := s.GetMessageByID("ghost")
	if err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	if !got.Deleted {
		t.Error("inserted row should be a tombstone (deleted=1)")
	}
	if got.DeletedBy != "alice" {
		t.Errorf("deleted_by = %q, want alice", got.DeletedBy)
	}
	if got.Body != "" {
		t.Errorf("tombstone body should be empty, got %q", got.Body)
	}
	if got.Sender != "" {
		t.Errorf("tombstone sender should be empty (renderer uses deleted_by), got %q", got.Sender)
	}
	if got.ServerOrder != 77 {
		t.Errorf("tombstone server_order = %d, want 77 (preserved)", got.ServerOrder)
	}
	if got.Room != "general" {
		t.Errorf("tombstone room = %q, want general", got.Room)
	}
}

func TestUpsertDeletedMessage_KnownRowSoftDeletesAndReturnsFileIDs(t *testing.T) {
	s := openTestStore(t)

	// Cache an original message with an attachment, plus a reaction.
	if _, err := s.InsertMessage(StoredMessage{
		ServerOrder: 5, ID: "known", Sender: "bob", Body: "with file", TS: 1, Room: "general",
		Attachments: []StoredAttachment{{FileID: "file_abc", Name: "x.png", Size: 10, Mime: "image/png"}},
	}); err != nil {
		t.Fatalf("insert original: %v", err)
	}
	if err := s.InsertReaction(StoredReaction{
		ReactionID: "r1", MessageID: "known", User: "carol", Emoji: "👍", TS: 2,
	}); err != nil {
		t.Fatalf("insert reaction: %v", err)
	}

	fileIDs, err := s.UpsertDeletedMessage("known", "bob", 3, 5, "general", "", "")
	if err != nil {
		t.Fatalf("upsert known: %v", err)
	}
	if len(fileIDs) != 1 || fileIDs[0] != "file_abc" {
		t.Fatalf("known-row tombstone should return its attachment file IDs, got %v", fileIDs)
	}

	got, err := s.GetMessageByID("known")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Deleted || got.Body != "" || got.DeletedBy != "bob" {
		t.Errorf("known row should be soft-deleted (deleted=%v body=%q deleted_by=%q)", got.Deleted, got.Body, got.DeletedBy)
	}
	if reacts, _ := s.GetReactionsForMessages([]string{"known"}); len(reacts) != 0 {
		t.Errorf("reactions should be purged on tombstone, got %d", len(reacts))
	}
}

// Live, sync_batch, and history_result can all deliver the same tombstone;
// duplicate delivery must converge, not raise a primary-key error.
func TestUpsertDeletedMessage_Idempotent(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.UpsertDeletedMessage("dup", "alice", 1, 9, "", "grp1", ""); err != nil {
		t.Fatalf("first upsert (absent): %v", err)
	}
	// Second delivery: the row now exists, so this takes the update path; must
	// not error and must not create a duplicate.
	if _, err := s.UpsertDeletedMessage("dup", "alice", 1, 9, "", "grp1", ""); err != nil {
		t.Fatalf("second upsert (now known): %v", err)
	}

	msgs, err := s.GetGroupMessages("grp1", 10)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one tombstone row after duplicate delivery, got %d", len(msgs))
	}
	if !msgs[0].Deleted {
		t.Error("row should remain a tombstone")
	}
}

// The insert (absent-row) path must enforce the same server-origin invariants as
// InsertMessage: exactly one context and a positive server_order. The update
// (known-row) path does not re-validate, since the existing row already holds a
// valid context/order.
func TestUpsertDeletedMessage_InsertPathValidation(t *testing.T) {
	s := openTestStore(t)

	// Zero / wrong server_order on an absent row.
	if _, err := s.UpsertDeletedMessage("a", "x", 1, 0, "general", "", ""); err == nil {
		t.Error("expected error for server_order=0 on absent row")
	} else if !strings.Contains(err.Error(), "server_order") {
		t.Errorf("error should mention server_order, got: %v", err)
	}

	// Zero contexts.
	if _, err := s.UpsertDeletedMessage("b", "x", 1, 5, "", "", ""); err == nil {
		t.Error("expected error for zero contexts on absent row")
	} else if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error should mention exactly-one-context, got: %v", err)
	}

	// Two contexts.
	if _, err := s.UpsertDeletedMessage("c", "x", 1, 5, "general", "", "dm1"); err == nil {
		t.Error("expected error for two contexts on absent row")
	}

	// None of the rejected ids should have been written.
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.GetMessageByID(id); err == nil {
			t.Errorf("rejected tombstone %q should not have been written", id)
		}
	}
}

// The headline S2b guarantee: an absent-row tombstone survives a DB
// close/reopen, where the pre-S2b update-only path would have left nothing on
// disk and the tombstone would vanish on reload.
func TestUpsertDeletedMessage_DurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tombstone.db")

	s1, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if _, err := s1.UpsertDeletedMessage("ghost", "alice", 10, 42, "", "", "dm1"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	s1.Close()

	s2, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	got, err := s2.GetMessageByID("ghost")
	if err != nil {
		t.Fatalf("tombstone should survive reload: %v", err)
	}
	if !got.Deleted || got.DeletedBy != "alice" || got.ServerOrder != 42 || got.DM != "dm1" {
		t.Errorf("tombstone did not round-trip: %+v", got)
	}
}
