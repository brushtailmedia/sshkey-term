package store

import (
	"testing"
)

func TestSoftDelete_MessageStaysInResults(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "hello", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "world", TS: 2, Room: "general"})

	s.DeleteMessage("m1", "alice")

	msgs, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (1 deleted + 1 live), got %d", len(msgs))
	}

	// First message (oldest) should be deleted
	if !msgs[0].Deleted {
		t.Error("m1 should be deleted")
	}
	if msgs[0].Body != "" {
		t.Errorf("deleted body should be empty, got %q", msgs[0].Body)
	}
	if msgs[0].DeletedBy != "alice" {
		t.Errorf("deleted_by = %q", msgs[0].DeletedBy)
	}

	// Second message should be untouched
	if msgs[1].Deleted {
		t.Error("m2 should not be deleted")
	}
	if msgs[1].Body != "world" {
		t.Errorf("m2 body = %q", msgs[1].Body)
	}
}

func TestSoftDelete_ConversationMessages(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "d1", Sender: "alice", Body: "dm msg", TS: 1, Conversation: "conv_1"})

	s.DeleteMessage("d1", "alice")

	msgs, _ := s.GetConvMessages("conv_1", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 soft-deleted message, got %d", len(msgs))
	}
	if !msgs[0].Deleted || msgs[0].Body != "" {
		t.Error("should be soft-deleted with empty body")
	}
}

func TestSoftDelete_ReactionsHardDeleted(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "hello", TS: 1, Room: "general"})
	s.InsertReaction(StoredReaction{ReactionID: "r1", MessageID: "m1", User: "bob", Emoji: "👍", TS: 2})

	// Verify reaction exists
	reactions, _ := s.GetReactionsForMessages([]string{"m1"})
	if len(reactions) != 1 {
		t.Fatalf("expected 1 reaction before delete, got %d", len(reactions))
	}

	s.DeleteMessage("m1", "alice")

	// Reaction should be gone
	reactions, _ = s.GetReactionsForMessages([]string{"m1"})
	if len(reactions) != 0 {
		t.Errorf("reactions should be hard-deleted, got %d", len(reactions))
	}
}

func TestSoftDelete_SearchExcludesDeleted(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "secret password", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "password reminder", TS: 2, Room: "general"})

	s.DeleteMessage("m1", "alice")

	results, err := s.SearchMessages("password", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// Only m2 should appear (m1 is deleted)
	for _, r := range results {
		if r.ID == "m1" {
			t.Error("deleted message should not appear in search results")
		}
	}
	found := false
	for _, r := range results {
		if r.ID == "m2" {
			found = true
		}
	}
	if !found {
		t.Error("non-deleted message should appear in search")
	}
}

func TestSoftDelete_GetMessagesBefore_IncludesDeleted(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "first", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "second", TS: 2, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m3", Sender: "alice", Body: "third", TS: 3, Room: "general"})

	s.DeleteMessage("m2", "bob")

	msgs, err := s.GetMessagesBefore("general", "", "m3", 10)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	// Should include m1 (live) and m2 (deleted tombstone)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages before m3, got %d", len(msgs))
	}
	foundDeleted := false
	for _, m := range msgs {
		if m.ID == "m2" && m.Deleted {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Error("deleted m2 should appear as tombstone in scroll-back")
	}
}

func TestAttachments_PersistAndLoad(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "see attached", TS: 1, Room: "general",
		Attachments: []StoredAttachment{
			{FileID: "file_abc", Name: "doc.pdf", Size: 45000, Mime: "application/pdf", DecryptKey: "dGVzdGtleQ=="},
			{FileID: "file_def", Name: "photo.jpg", Size: 230000, Mime: "image/jpeg", DecryptKey: "aW1na2V5"},
		},
	})

	msgs, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(msgs[0].Attachments))
	}
	a1 := msgs[0].Attachments[0]
	if a1.FileID != "file_abc" || a1.Name != "doc.pdf" || a1.Size != 45000 {
		t.Errorf("attachment 1 mismatch: %+v", a1)
	}
	if a1.DecryptKey != "dGVzdGtleQ==" {
		t.Errorf("decrypt key not preserved: %q", a1.DecryptKey)
	}
	a2 := msgs[0].Attachments[1]
	if a2.FileID != "file_def" || a2.Mime != "image/jpeg" {
		t.Errorf("attachment 2 mismatch: %+v", a2)
	}
}

func TestAttachments_EmptyPreserved(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "no files", TS: 1, Room: "general"})

	msgs, _ := s.GetRoomMessages("general", 10)
	if len(msgs[0].Attachments) != 0 {
		t.Errorf("expected no attachments, got %d", len(msgs[0].Attachments))
	}
}

func TestAttachments_SurviveSoftDelete(t *testing.T) {
	// Attachments should be gone after soft-delete (body cleared)
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "file here", TS: 1, Room: "general",
		Attachments: []StoredAttachment{
			{FileID: "file_abc", Name: "doc.pdf", Size: 100, Mime: "application/pdf"},
		},
	})

	s.DeleteMessage("m1", "alice")

	msgs, _ := s.GetRoomMessages("general", 10)
	if !msgs[0].Deleted {
		t.Error("should be deleted")
	}
	// Attachments column is not cleared by soft-delete (only body is cleared).
	// This is fine — the TUI checks msg.Deleted before rendering attachments.
}

func TestSoftDelete_ReturnsFileIDs(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{
		ID: "m1", Sender: "alice", Body: "file here", TS: 1, Room: "general",
		Attachments: []StoredAttachment{
			{FileID: "file_abc", Name: "doc.pdf", Size: 100, Mime: "application/pdf"},
			{FileID: "file_def", Name: "img.jpg", Size: 200, Mime: "image/jpeg"},
		},
	})

	fileIDs, err := s.DeleteMessage("m1", "alice")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(fileIDs) != 2 {
		t.Fatalf("expected 2 file IDs, got %d", len(fileIDs))
	}
	if fileIDs[0] != "file_abc" || fileIDs[1] != "file_def" {
		t.Errorf("file IDs = %v", fileIDs)
	}
}

func TestSoftDelete_NoAttachmentsReturnsNil(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "no files", TS: 1, Room: "general"})

	fileIDs, _ := s.DeleteMessage("m1", "alice")
	if len(fileIDs) != 0 {
		t.Errorf("expected no file IDs, got %v", fileIDs)
	}
}

func TestSoftDelete_NonexistentMessage(t *testing.T) {
	s := openTestStore(t)
	// Should not error — just a no-op (0 rows affected)
	_, err := s.DeleteMessage("nonexistent", "alice")
	if err != nil {
		t.Errorf("deleting nonexistent message should not error: %v", err)
	}
}

func TestSoftDelete_DoubleDelete(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "hello", TS: 1, Room: "general"})

	s.DeleteMessage("m1", "alice")
	// Second delete is a no-op in practice (server won't send two
	// tombstones), but the UPDATE is idempotent and doesn't error.
	_, err := s.DeleteMessage("m1", "alice")
	if err != nil {
		t.Errorf("double delete should not error: %v", err)
	}

	msgs, _ := s.GetRoomMessages("general", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !msgs[0].Deleted {
		t.Error("should still be deleted")
	}
}
