package store

import "testing"

// F6 follow-up — DeleteMessageInContext is the context-scoped durable apply for a
// verified remote `deleted` tombstone on the LIVE path. It must mutate only a row
// whose stored context matches the tombstone's signed context, and no-op otherwise
// (without erroring), so a verified-but-wrong-context tombstone cannot blank a
// same-id row in another context.

func TestDeleteMessageInContext_DeletesMatchingContext(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "m1", Sender: "bob", Body: "delete me", TS: 1, Room: "general",
		Attachments: []StoredAttachment{{FileID: "file_abc", Name: "a.bin", Size: 1, Mime: "application/octet-stream"}},
	})
	s.InsertReaction(StoredReaction{ReactionID: "r1", MessageID: "m1", User: "carol", Emoji: "👍", TS: 2})

	fileIDs, err := s.DeleteMessageInContext("m1", "alice", "general", "", "")
	if err != nil {
		t.Fatalf("matching-context delete: %v", err)
	}
	if len(fileIDs) != 1 || fileIDs[0] != "file_abc" {
		t.Errorf("expected the attachment's file ID back, got %v", fileIDs)
	}
	got, _ := s.GetMessageByID("m1")
	if got == nil || !got.Deleted || got.Body != "" || got.DeletedBy != "alice" {
		t.Errorf("m1 should be soft-deleted by alice with empty body, got %+v", got)
	}
	if reactions, _ := s.GetReactionsForMessages([]string{"m1"}); len(reactions) != 0 {
		t.Errorf("reactions should be purged on a matching delete, got %d", len(reactions))
	}
}

func TestDeleteMessageInContext_WrongContextNoop(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{
		ServerOrder: 1, ID: "m1", Sender: "bob", Body: "keep me", TS: 1, Room: "general",
		Attachments: []StoredAttachment{{FileID: "file_abc", Name: "a.bin", Size: 1, Mime: "application/octet-stream"}},
	})
	s.InsertReaction(StoredReaction{ReactionID: "r1", MessageID: "m1", User: "carol", Emoji: "👍", TS: 2})

	// Same id, but a GROUP context — must not touch the room row.
	fileIDs, err := s.DeleteMessageInContext("m1", "alice", "", "grp_side", "")
	if err != nil {
		t.Fatalf("wrong-context delete must not error: %v", err)
	}
	if fileIDs != nil {
		t.Errorf("no row matched, so no file IDs expected, got %v", fileIDs)
	}
	got, _ := s.GetMessageByID("m1")
	if got == nil || got.Deleted || got.Body != "keep me" {
		t.Errorf("m1 (room) must stay live after a group-context delete, got %+v", got)
	}
	if reactions, _ := s.GetReactionsForMessages([]string{"m1"}); len(reactions) != 1 {
		t.Errorf("reactions must survive a no-op delete, got %d", len(reactions))
	}
}

func TestDeleteMessageInContext_RejectsZeroOrMultiContext(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ServerOrder: 1, ID: "m1", Sender: "bob", Body: "intact", TS: 1, Room: "general"})

	if _, err := s.DeleteMessageInContext("m1", "alice", "", "", ""); err == nil {
		t.Error("zero context must error")
	}
	if _, err := s.DeleteMessageInContext("m1", "alice", "general", "", "dm1"); err == nil {
		t.Error("multi context must error")
	}
	// Neither rejected attempt may mutate the row.
	got, _ := s.GetMessageByID("m1")
	if got == nil || got.Deleted || got.Body != "intact" {
		t.Errorf("m1 must stay live after rejected calls, got %+v", got)
	}
}

// Positive regression (doc §7 item 0, store level): a correct-context delete still
// works in every context. This is the test that actually guards the field-mapping
// risk (protocol room/group/dm -> DB room/group_id/dm_id) — a helper that no-op'd
// everything would pass the wrong-context test above but fail here.
func TestDeleteMessageInContext_AllThreeContexts(t *testing.T) {
	cases := []struct {
		name            string
		room, group, dm string
	}{
		{"room", "general", "", ""},
		{"group", "", "grp1", ""},
		{"dm", "", "", "dm1"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			s.InsertMessage(StoredMessage{
				ServerOrder: int64(i + 1), ID: "m", Sender: "bob", Body: "x", TS: 1,
				Room: tc.room, Group: tc.group, DM: tc.dm,
			})
			if _, err := s.DeleteMessageInContext("m", "alice", tc.room, tc.group, tc.dm); err != nil {
				t.Fatalf("correct-context delete: %v", err)
			}
			got, _ := s.GetMessageByID("m")
			if got == nil || !got.Deleted || got.Body != "" {
				t.Errorf("%s: message should be soft-deleted, got %+v", tc.name, got)
			}
		})
	}
}
