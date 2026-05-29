package client

// Tests for the Phase 0 cleanupAttachmentFiles helper. See
// path-centralization.md §"Phase 0".

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// newCleanupTestClient builds a minimal Client with DataDir set to a
// tempdir and a discard logger. Returns the client + the files
// directory path (already pre-created so tests can write into it).
func newCleanupTestClient(t *testing.T) (*Client, string) {
	t.Helper()
	dataDir := t.TempDir()
	filesDir := filepath.Join(dataDir, "files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	return &Client{
		cfg: Config{
			DataDir: dataDir,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, filesDir
}

// seedFile writes a placeholder file under filesDir for fileID.
func seedFile(t *testing.T, filesDir, fileID string) {
	t.Helper()
	path := filepath.Join(filesDir, fileID)
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("seed %s: %v", fileID, err)
	}
}

// fileExists is a small assert helper.
func fileExists(t *testing.T, filesDir, fileID string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(filesDir, fileID))
	return err == nil
}

// TestCleanupAttachmentFiles_RemovesAllListedFiles is the happy path —
// every fileID in the slice that exists on disk gets removed.
func TestCleanupAttachmentFiles_RemovesAllListedFiles(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	seedFile(t, filesDir, "fid_a")
	seedFile(t, filesDir, "fid_b")
	seedFile(t, filesDir, "fid_c")
	// Bystander — not in the cleanup list, must survive.
	seedFile(t, filesDir, "fid_bystander")

	c.cleanupAttachmentFiles([]string{"fid_a", "fid_b", "fid_c"})

	for _, fid := range []string{"fid_a", "fid_b", "fid_c"} {
		if fileExists(t, filesDir, fid) {
			t.Errorf("%s should have been removed", fid)
		}
	}
	if !fileExists(t, filesDir, "fid_bystander") {
		t.Error("bystander file should NOT have been removed")
	}
}

// TestCleanupAttachmentFiles_MissingFileIsSilent verifies the
// expected-case where a fileID points at a file that was never
// downloaded — os.IsNotExist is silent (no log, no error).
func TestCleanupAttachmentFiles_MissingFileIsSilent(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	seedFile(t, filesDir, "fid_present")

	// fid_missing was never downloaded — its remove will fail with
	// IsNotExist, which the helper swallows silently.
	c.cleanupAttachmentFiles([]string{"fid_present", "fid_missing"})

	if fileExists(t, filesDir, "fid_present") {
		t.Error("fid_present should have been removed")
	}
	// Should not panic, should not error. Test passes by reaching
	// this line.
}

// TestCleanupAttachmentFiles_EmptyDataDirNoOp verifies the helper
// is a no-op when DataDir is empty (test/disabled-storage context).
// Specifically, it must NOT try to construct a path from an empty
// dataDir (which would attempt to remove files from "files/<fid>"
// relative to the cwd — bad surprise).
func TestCleanupAttachmentFiles_EmptyDataDirNoOp(t *testing.T) {
	c := &Client{
		cfg:    Config{DataDir: ""},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// Must not panic; must not touch the filesystem.
	c.cleanupAttachmentFiles([]string{"fid_x", "fid_y"})
}

// TestCleanupAttachmentFiles_EmptyListNoOp verifies the empty-input
// case is a clean no-op (mirroring the bulk-delete path where no
// messages had attachments).
func TestCleanupAttachmentFiles_EmptyListNoOp(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	seedFile(t, filesDir, "fid_stays")

	c.cleanupAttachmentFiles(nil)
	c.cleanupAttachmentFiles([]string{})

	if !fileExists(t, filesDir, "fid_stays") {
		t.Error("empty-input cleanup should not touch any files")
	}
}

// TestCleanupAttachmentFiles_IntegrationWithPurgeRoom seeds a room
// with messages carrying attachments + the matching files on disk,
// runs the bulk purge, and asserts the files are gone. End-to-end
// shape that matches what client.go does for room_deleted /
// deleted_rooms catchup.
func TestCleanupAttachmentFiles_IntegrationWithPurgeRoom(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)

	// Open a store and wire it into the client.
	storePath := filepath.Join(t.TempDir(), "messages.db")
	st, err := store.OpenUnencrypted(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	c.store = st

	// Seed two messages with attachments + one bystander.
	insert := func(id, room string, fileIDs ...string) {
		atts := make([]store.StoredAttachment, 0, len(fileIDs))
		for _, fid := range fileIDs {
			atts = append(atts, store.StoredAttachment{FileID: fid, Name: fid + ".png", Mime: "image/png"})
		}
		if _, err := st.InsertMessage(store.StoredMessage{ServerOrder: 1,
			ID: id, Sender: "alice", Body: "x", TS: 1,
			Room:        room,
			Attachments: atts,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("msg_x", "room_target", "file_x1", "file_x2")
	insert("msg_y", "room_target", "file_y")
	// Bystander from a different room.
	insert("msg_bystander", "room_other", "file_bystander")

	// Seed the matching files on disk.
	seedFile(t, filesDir, "file_x1")
	seedFile(t, filesDir, "file_x2")
	seedFile(t, filesDir, "file_y")
	seedFile(t, filesDir, "file_bystander")

	// Purge the room and clean up its attachments.
	fileIDs, err := st.PurgeRoomMessages("room_target")
	if err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}
	c.cleanupAttachmentFiles(fileIDs)

	// All three target files gone.
	for _, fid := range []string{"file_x1", "file_x2", "file_y"} {
		if fileExists(t, filesDir, fid) {
			t.Errorf("%s should be removed after PurgeRoomMessages + cleanup", fid)
		}
	}
	// Bystander survives.
	if !fileExists(t, filesDir, "file_bystander") {
		t.Error("file_bystander (different room) should NOT have been removed")
	}
}
