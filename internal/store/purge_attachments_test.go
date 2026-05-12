package store

// Tests for the Phase 0 attachment-cleanup contract on the three
// bulk-purge functions (PurgeRoomMessages, PurgeGroupMessages,
// PurgeDMMessages). Each function returns ([]string, error) with
// the file IDs from every message's stored attachments — the caller
// uses these to remove the matching files from <DataDir>/files/ on
// disk so deleted conversations don't leak their attachments. See
// path-centralization.md §"Phase 0".

import (
	"testing"
)

// TestPurgeRoomMessages_ReturnsAttachmentFileIDs verifies that the
// fileIDs from every message's Attachments slice are returned by
// PurgeRoomMessages so the caller can clean up the on-disk
// attachment cache. Multiple messages with multiple attachments
// each are exercised.
func TestPurgeRoomMessages_ReturnsAttachmentFileIDs(t *testing.T) {
	s := openTestStore(t)

	// msg_a has two attachments, msg_b has one, msg_c has none.
	if _, err := s.InsertMessage(StoredMessage{
		ID: "msg_a", Sender: "alice", Body: "two attachments", TS: 1,
		Room: "room_target",
		Attachments: []StoredAttachment{
			{FileID: "file_1", Name: "one.png", Mime: "image/png"},
			{FileID: "file_2", Name: "two.png", Mime: "image/png"},
		},
	}); err != nil {
		t.Fatalf("insert msg_a: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{
		ID: "msg_b", Sender: "bob", Body: "one attachment", TS: 2,
		Room: "room_target",
		Attachments: []StoredAttachment{
			{FileID: "file_3", Name: "three.pdf", Mime: "application/pdf"},
		},
	}); err != nil {
		t.Fatalf("insert msg_b: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{
		ID: "msg_c", Sender: "carol", Body: "no attachments", TS: 3,
		Room: "room_target",
	}); err != nil {
		t.Fatalf("insert msg_c: %v", err)
	}
	// Control: another room with an attachment that must NOT be returned.
	if _, err := s.InsertMessage(StoredMessage{
		ID: "msg_other", Sender: "alice", Body: "other room", TS: 4,
		Room: "room_other",
		Attachments: []StoredAttachment{
			{FileID: "file_other", Name: "other.png", Mime: "image/png"},
		},
	}); err != nil {
		t.Fatalf("insert msg_other: %v", err)
	}

	fileIDs, err := s.PurgeRoomMessages("room_target")
	if err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}

	// Should return file_1, file_2, file_3 (in some order — DB
	// row iteration order is unspecified).
	want := map[string]bool{"file_1": true, "file_2": true, "file_3": true}
	if len(fileIDs) != len(want) {
		t.Fatalf("got %d fileIDs, want %d: %v", len(fileIDs), len(want), fileIDs)
	}
	for _, fid := range fileIDs {
		if !want[fid] {
			t.Errorf("unexpected fileID returned: %q (want subset of %v)", fid, want)
		}
		if fid == "file_other" {
			t.Errorf("PurgeRoomMessages leaked file_other from a different room")
		}
	}
}

// TestPurgeGroupMessages_ReturnsAttachmentFileIDs — group equivalent
// of the room test above.
func TestPurgeGroupMessages_ReturnsAttachmentFileIDs(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.InsertMessage(StoredMessage{
		ID: "gmsg_a", Sender: "alice", Body: "x", TS: 1,
		Group: "group_target",
		Attachments: []StoredAttachment{
			{FileID: "g_file_1", Name: "a.png"},
		},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{
		ID: "gmsg_b", Sender: "bob", Body: "y", TS: 2,
		Group: "group_target",
		Attachments: []StoredAttachment{
			{FileID: "g_file_2", Name: "b.png"},
			{FileID: "g_file_3", Name: "c.png"},
		},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Control message in a different group.
	if _, err := s.InsertMessage(StoredMessage{
		ID: "gmsg_other", Sender: "alice", Body: "z", TS: 3,
		Group: "group_other",
		Attachments: []StoredAttachment{{FileID: "g_file_other"}},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	fileIDs, err := s.PurgeGroupMessages("group_target")
	if err != nil {
		t.Fatalf("PurgeGroupMessages: %v", err)
	}

	want := map[string]bool{"g_file_1": true, "g_file_2": true, "g_file_3": true}
	if len(fileIDs) != len(want) {
		t.Fatalf("got %d fileIDs, want %d: %v", len(fileIDs), len(want), fileIDs)
	}
	for _, fid := range fileIDs {
		if !want[fid] {
			t.Errorf("unexpected fileID: %q", fid)
		}
	}
}

// TestPurgeDMMessages_ReturnsAttachmentFileIDs — DM equivalent.
func TestPurgeDMMessages_ReturnsAttachmentFileIDs(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.InsertMessage(StoredMessage{
		ID: "dmsg_a", Sender: "alice", Body: "x", TS: 1,
		DM: "dm_target",
		Attachments: []StoredAttachment{
			{FileID: "d_file_1", Name: "a.png"},
		},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{
		ID: "dmsg_b", Sender: "bob", Body: "y", TS: 2,
		DM: "dm_target",
		Attachments: []StoredAttachment{
			{FileID: "d_file_2", Name: "b.png"},
		},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Control: another DM.
	if _, err := s.InsertMessage(StoredMessage{
		ID: "dmsg_other", Sender: "alice", Body: "z", TS: 3,
		DM: "dm_other",
		Attachments: []StoredAttachment{{FileID: "d_file_other"}},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	fileIDs, err := s.PurgeDMMessages("dm_target")
	if err != nil {
		t.Fatalf("PurgeDMMessages: %v", err)
	}

	want := map[string]bool{"d_file_1": true, "d_file_2": true}
	if len(fileIDs) != len(want) {
		t.Fatalf("got %d fileIDs, want %d: %v", len(fileIDs), len(want), fileIDs)
	}
	for _, fid := range fileIDs {
		if !want[fid] {
			t.Errorf("unexpected fileID: %q", fid)
		}
	}
}

// TestPurge_NoAttachmentsReturnsEmpty verifies the no-attachment
// case across all three Purge functions: rows exist but none carry
// any attachments → returned fileIDs slice is empty (not nil-vs-
// empty distinction; just zero entries).
func TestPurge_NoAttachmentsReturnsEmpty(t *testing.T) {
	s := openTestStore(t)

	// Seed one message per scope, no attachments.
	if _, err := s.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "x", TS: 1, Room: "room_x"}); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{ID: "m2", Sender: "a", Body: "x", TS: 2, Group: "group_x"}); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := s.InsertMessage(StoredMessage{ID: "m3", Sender: "a", Body: "x", TS: 3, DM: "dm_x"}); err != nil {
		t.Fatalf("insert dm: %v", err)
	}

	if ids, err := s.PurgeRoomMessages("room_x"); err != nil {
		t.Errorf("PurgeRoomMessages err: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("PurgeRoomMessages with no attachments returned %v, want empty", ids)
	}
	if ids, err := s.PurgeGroupMessages("group_x"); err != nil {
		t.Errorf("PurgeGroupMessages err: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("PurgeGroupMessages with no attachments returned %v, want empty", ids)
	}
	if ids, err := s.PurgeDMMessages("dm_x"); err != nil {
		t.Errorf("PurgeDMMessages err: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("PurgeDMMessages with no attachments returned %v, want empty", ids)
	}
}

// TestPurge_EmptyScopeReturnsEmpty verifies that calling Purge on a
// scope with NO messages returns an empty slice and no error
// (idempotency under empty state).
func TestPurge_EmptyScopeReturnsEmpty(t *testing.T) {
	s := openTestStore(t)

	if ids, err := s.PurgeRoomMessages("nonexistent_room"); err != nil {
		t.Errorf("PurgeRoomMessages on empty: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("empty room returned fileIDs: %v", ids)
	}
	if ids, err := s.PurgeGroupMessages("nonexistent_group"); err != nil {
		t.Errorf("PurgeGroupMessages on empty: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("empty group returned fileIDs: %v", ids)
	}
	if ids, err := s.PurgeDMMessages("nonexistent_dm"); err != nil {
		t.Errorf("PurgeDMMessages on empty: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("empty dm returned fileIDs: %v", ids)
	}
}
