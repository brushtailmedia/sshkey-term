package store

import (
	"testing"
)

// TestMarkDMLeft_SetsLeftAt verifies that MarkDMLeft updates the left_at
// column on the local direct_messages row to the supplied unix timestamp.
func TestMarkDMLeft_SetsLeftAt(t *testing.T) {
	s := openTestStore(t)

	if err := s.StoreDM("dm_ab", "alice", "bob"); err != nil {
		t.Fatalf("store DM: %v", err)
	}

	if got := s.GetDMLeftAt("dm_ab"); got != 0 {
		t.Errorf("fresh DM should have left_at = 0, got %d", got)
	}

	if err := s.MarkDMLeft("dm_ab", 1700000000); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	if got := s.GetDMLeftAt("dm_ab"); got != 1700000000 {
		t.Errorf("after MarkDMLeft: left_at = %d, want 1700000000", got)
	}
}

// TestMarkDMRejoined_ClearsLeftAt verifies that MarkDMRejoined zeros the
// left_at column. This is used by the client when a previously-deleted DM
// is locally re-materialised after re-contact.
func TestMarkDMRejoined_ClearsLeftAt(t *testing.T) {
	s := openTestStore(t)

	if err := s.StoreDM("dm_ab", "alice", "bob"); err != nil {
		t.Fatalf("store DM: %v", err)
	}
	if err := s.MarkDMLeft("dm_ab", 1700000000); err != nil {
		t.Fatalf("mark left: %v", err)
	}

	if err := s.MarkDMRejoined("dm_ab"); err != nil {
		t.Fatalf("mark rejoined: %v", err)
	}

	if got := s.GetDMLeftAt("dm_ab"); got != 0 {
		t.Errorf("after MarkDMRejoined: left_at = %d, want 0", got)
	}
}

// TestGetDMLeftAt_MissingRow verifies that asking about a DM that doesn't
// exist locally is not an error — returns 0 (the same as "active").
func TestGetDMLeftAt_MissingRow(t *testing.T) {
	s := openTestStore(t)

	if got := s.GetDMLeftAt("dm_does_not_exist"); got != 0 {
		t.Errorf("missing row should return 0, got %d", got)
	}
}

// TestPurgeDMMessages_RemovesMessagesAndReactions verifies that PurgeDMMessages
// drops every message keyed by dm_id, and removes any reactions that were
// hanging off those messages. The direct_messages row itself is preserved.
func TestPurgeDMMessages_RemovesMessagesAndReactions(t *testing.T) {
	s := openTestStore(t)

	if err := s.StoreDM("dm_ab", "alice", "bob"); err != nil {
		t.Fatalf("store DM: %v", err)
	}

	// Insert two messages in the DM, plus one in another DM as a control.
	if err := s.InsertMessage(StoredMessage{
		ID: "msg_dm_1", Sender: "alice", Body: "hi", TS: 1, DM: "dm_ab",
	}); err != nil {
		t.Fatalf("insert msg 1: %v", err)
	}
	if err := s.InsertMessage(StoredMessage{
		ID: "msg_dm_2", Sender: "bob", Body: "hey", TS: 2, DM: "dm_ab",
	}); err != nil {
		t.Fatalf("insert msg 2: %v", err)
	}
	if err := s.InsertMessage(StoredMessage{
		ID: "msg_other", Sender: "alice", Body: "ping", TS: 3, DM: "dm_other",
	}); err != nil {
		t.Fatalf("insert msg other: %v", err)
	}

	// Add a reaction on msg_dm_1
	if _, err := s.db.Exec(
		`INSERT INTO reactions (reaction_id, message_id, user, emoji, ts) VALUES (?, ?, ?, ?, ?)`,
		"react_1", "msg_dm_1", "bob", "👍", 5,
	); err != nil {
		t.Fatalf("insert reaction: %v", err)
	}

	if err := s.PurgeDMMessages("dm_ab"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Messages for dm_ab gone
	msgs, err := s.GetDMMessages("dm_ab", 100)
	if err != nil {
		t.Fatalf("get dm_ab messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no messages after purge, got %d", len(msgs))
	}

	// Reaction for the purged message gone
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM reactions WHERE reaction_id = ?`, "react_1",
	).Scan(&n); err != nil {
		t.Fatalf("count reactions: %v", err)
	}
	if n != 0 {
		t.Errorf("expected reaction to be deleted, got %d remaining", n)
	}

	// Other DM untouched
	other, err := s.GetDMMessages("dm_other", 100)
	if err != nil {
		t.Fatalf("get dm_other messages: %v", err)
	}
	if len(other) != 1 {
		t.Errorf("dm_other should still have 1 message, got %d", len(other))
	}

	// direct_messages row for dm_ab is preserved (so multi-device sync
	// can still recognise the leave on next reconnect).
	dms, err := s.GetAllDMs()
	if err != nil {
		t.Fatalf("get DMs: %v", err)
	}
	found := false
	for _, dm := range dms {
		if dm.ID == "dm_ab" {
			found = true
			break
		}
	}
	if !found {
		t.Error("direct_messages row should be preserved after PurgeDMMessages")
	}
}

// TestPurgeDMMessages_NoMessagesIsNoop verifies that calling PurgeDMMessages
// on a DM with no local messages succeeds without error.
func TestPurgeDMMessages_NoMessagesIsNoop(t *testing.T) {
	s := openTestStore(t)

	if err := s.StoreDM("dm_empty", "alice", "bob"); err != nil {
		t.Fatalf("store DM: %v", err)
	}
	if err := s.PurgeDMMessages("dm_empty"); err != nil {
		t.Errorf("purge of empty DM should succeed, got: %v", err)
	}
}

// TestPurgeGroupMessages_RemovesMessagesAndReactions verifies the group
// equivalent of PurgeDMMessages. Used by the group_deleted handler when
// /delete completes (live or via offline catchup).
func TestPurgeGroupMessages_RemovesMessagesAndReactions(t *testing.T) {
	s := openTestStore(t)

	// Three messages: two in group_a, one in group_b (control).
	if err := s.InsertMessage(StoredMessage{
		ID: "g1", Sender: "alice", Body: "hi", TS: 1, Group: "group_a",
	}); err != nil {
		t.Fatalf("insert g1: %v", err)
	}
	if err := s.InsertMessage(StoredMessage{
		ID: "g2", Sender: "bob", Body: "hey", TS: 2, Group: "group_a",
	}); err != nil {
		t.Fatalf("insert g2: %v", err)
	}
	if err := s.InsertMessage(StoredMessage{
		ID: "g3", Sender: "alice", Body: "ping", TS: 3, Group: "group_b",
	}); err != nil {
		t.Fatalf("insert g3: %v", err)
	}

	// A reaction on g1
	if _, err := s.db.Exec(
		`INSERT INTO reactions (reaction_id, message_id, user, emoji, ts) VALUES (?, ?, ?, ?, ?)`,
		"react_g", "g1", "bob", "👍", 5,
	); err != nil {
		t.Fatalf("insert reaction: %v", err)
	}

	if err := s.PurgeGroupMessages("group_a"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// group_a messages gone
	msgs, _ := s.GetGroupMessages("group_a", 100)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages in group_a after purge, got %d", len(msgs))
	}

	// Reaction on the purged message gone
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM reactions WHERE reaction_id = ?`, "react_g").Scan(&n)
	if n != 0 {
		t.Errorf("expected reaction to be deleted, got %d", n)
	}

	// group_b untouched
	other, _ := s.GetGroupMessages("group_b", 100)
	if len(other) != 1 {
		t.Errorf("group_b should still have 1 message, got %d", len(other))
	}
}

// TestPurgeGroupMessages_NoMessagesIsNoop verifies the empty case.
func TestPurgeGroupMessages_NoMessagesIsNoop(t *testing.T) {
	s := openTestStore(t)
	if err := s.PurgeGroupMessages("group_empty"); err != nil {
		t.Errorf("purge of empty group should succeed, got: %v", err)
	}
}
