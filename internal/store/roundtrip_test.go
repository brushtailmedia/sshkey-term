package store

import (
	"path/filepath"
	"testing"
)

// TestSoftDelete_RoundTrip verifies the full cycle: insert → delete → close →
// reopen → load. The tombstone must survive a DB round-trip with deleted flag,
// deleted_by, and cleared body.
func TestSoftDelete_RoundTrip(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")

	// First open: insert + delete
	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	s1.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "hello world", TS: 1000, Room: "general"})
	s1.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "reply", TS: 1001, Room: "general", ReplyTo: "m1"})
	s1.DeleteMessage("m1", "alice")
	s1.Close()

	// Second open: verify tombstone survived
	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	msgs, err := s2.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (1 tombstone + 1 live), got %d", len(msgs))
	}

	// m1 should be a tombstone
	if !msgs[0].Deleted {
		t.Error("m1 should be deleted")
	}
	if msgs[0].DeletedBy != "alice" {
		t.Errorf("m1 deleted_by = %q", msgs[0].DeletedBy)
	}
	if msgs[0].Body != "" {
		t.Errorf("m1 body should be empty, got %q", msgs[0].Body)
	}

	// m2 should be live with reply_to intact
	if msgs[1].Deleted {
		t.Error("m2 should not be deleted")
	}
	if msgs[1].Body != "reply" {
		t.Errorf("m2 body = %q", msgs[1].Body)
	}
	if msgs[1].ReplyTo != "m1" {
		t.Errorf("m2 reply_to = %q, should still point to deleted m1", msgs[1].ReplyTo)
	}
}

// TestAttachment_RoundTrip verifies attachments survive close → reopen.
func TestAttachment_RoundTrip(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")

	// First open: insert message with attachments
	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	s1.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "see attached", TS: 1000, Room: "general",
		Attachments: []StoredAttachment{
			{FileID: "file_abc", Name: "report.pdf", Size: 45000, Mime: "application/pdf", DecryptKey: "dGVzdGtleTE="},
			{FileID: "file_def", Name: "photo.jpg", Size: 230000, Mime: "image/jpeg", DecryptKey: "dGVzdGtleTI="},
		},
	})
	s1.InsertMessage(StoredMessage{
		ID: "m2", Sender: "bob", Body: "no attachments", TS: 1001, Room: "general",
	})
	s1.Close()

	// Second open: verify attachments survived
	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	msgs, err := s2.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// m1: 2 attachments with decrypt keys
	m1 := msgs[0]
	if len(m1.Attachments) != 2 {
		t.Fatalf("m1 expected 2 attachments, got %d", len(m1.Attachments))
	}
	if m1.Attachments[0].FileID != "file_abc" {
		t.Errorf("att[0] file_id = %q", m1.Attachments[0].FileID)
	}
	if m1.Attachments[0].Name != "report.pdf" {
		t.Errorf("att[0] name = %q", m1.Attachments[0].Name)
	}
	if m1.Attachments[0].Size != 45000 {
		t.Errorf("att[0] size = %d", m1.Attachments[0].Size)
	}
	if m1.Attachments[0].Mime != "application/pdf" {
		t.Errorf("att[0] mime = %q", m1.Attachments[0].Mime)
	}
	if m1.Attachments[0].DecryptKey != "dGVzdGtleTE=" {
		t.Errorf("att[0] decrypt_key = %q", m1.Attachments[0].DecryptKey)
	}
	if m1.Attachments[1].FileID != "file_def" {
		t.Errorf("att[1] file_id = %q", m1.Attachments[1].FileID)
	}
	if m1.Attachments[1].DecryptKey != "dGVzdGtleTI=" {
		t.Errorf("att[1] decrypt_key = %q", m1.Attachments[1].DecryptKey)
	}

	// m2: no attachments
	if len(msgs[1].Attachments) != 0 {
		t.Errorf("m2 should have no attachments, got %d", len(msgs[1].Attachments))
	}
}

// TestDeletedAttachment_RoundTrip verifies that deleting a message with
// attachments returns the file IDs and survives reopen as a tombstone.
func TestDeletedAttachment_RoundTrip(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")

	// First open: insert with attachments, then delete
	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	s1.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "file here", TS: 1000, Room: "general",
		Attachments: []StoredAttachment{
			{FileID: "file_xyz", Name: "doc.txt", Size: 100, Mime: "text/plain", DecryptKey: "a2V5"},
		},
	})
	fileIDs, err := s1.DeleteMessage("m1", "alice")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fileIDs) != 1 || fileIDs[0] != "file_xyz" {
		t.Errorf("delete should return file IDs, got %v", fileIDs)
	}
	s1.Close()

	// Second open: tombstone should exist
	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	msgs, _ := s2.GetRoomMessages("general", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 tombstone, got %d", len(msgs))
	}
	if !msgs[0].Deleted {
		t.Error("should be deleted")
	}
	if msgs[0].Body != "" {
		t.Errorf("body should be empty, got %q", msgs[0].Body)
	}
}

// TestSearch_ExcludesDeleted_RoundTrip verifies deleted messages stay out of
// search results after a DB round-trip.
func TestSearch_ExcludesDeleted_RoundTrip(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")

	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s1.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "secret password", TS: 1, Room: "general"})
	s1.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "password reminder", TS: 2, Room: "general"})
	s1.DeleteMessage("m1", "alice")
	s1.Close()

	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	results, err := s2.SearchMessages("password", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.ID == "m1" {
			t.Error("deleted message should not appear in search after reopen")
		}
	}
}

// TestConvAttachment_RoundTrip verifies DM attachment persistence.
func TestConvAttachment_RoundTrip(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")

	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s1.InsertMessage(StoredMessage{
		ID: "d1", Sender: "alice", Body: "dm file", TS: 1000, Conversation: "conv_abc",
		Attachments: []StoredAttachment{
			{FileID: "file_dm1", Name: "secret.pdf", Size: 5000, Mime: "application/pdf", DecryptKey: "ZG1rZXk="},
		},
	})
	s1.Close()

	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	msgs, _ := s2.GetConvMessages("conv_abc", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msgs[0].Attachments))
	}
	if msgs[0].Attachments[0].DecryptKey != "ZG1rZXk=" {
		t.Errorf("DM decrypt key not preserved: %q", msgs[0].Attachments[0].DecryptKey)
	}
}
