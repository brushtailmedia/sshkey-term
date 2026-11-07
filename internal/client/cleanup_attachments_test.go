package client

// Tests for the cleanupAttachmentFiles helper and the closed-list
// removeCachedAttachment it loops over. See path-centralization.md
// §"Phase 0" and attachments-v2.md §7 (the delete-cleanup bug).

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/config"
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

// testFileID returns a valid server-format id — config.FileIDPrefix plus
// 21 characters of [A-Za-z0-9_-] — whose body starts with tag, so a
// failure message still reads as the id it names. validFileID accepts
// exactly this shape and nothing shorter (PLAN step 3b), so fixtures
// cannot use bare tags like "fid_a" any more.
func testFileID(t *testing.T, tag string) string {
	t.Helper()
	const body = 21
	if len(tag) > body {
		t.Fatalf("testFileID: tag %q longer than %d", tag, body)
	}
	id := config.FileIDPrefix + tag + strings.Repeat("A", body-len(tag))
	if !validFileID(id) {
		t.Fatalf("testFileID(%q) produced an invalid id %q", tag, id)
	}
	return id
}

// seedFile writes a placeholder file under filesDir named name (an id or
// any derived/neighbouring file name).
func seedFile(t *testing.T, filesDir, name string) {
	t.Helper()
	path := filepath.Join(filesDir, name)
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

// fileExists is a small assert helper.
func fileExists(t *testing.T, filesDir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(filesDir, name))
	return err == nil
}

// TestCleanupAttachmentFiles_RemovesAllListedFiles is the happy path —
// every fileID in the slice that exists on disk gets removed.
func TestCleanupAttachmentFiles_RemovesAllListedFiles(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	a, b, cc := testFileID(t, "a"), testFileID(t, "b"), testFileID(t, "c")
	bystander := testFileID(t, "bystander")
	seedFile(t, filesDir, a)
	seedFile(t, filesDir, b)
	seedFile(t, filesDir, cc)
	// Bystander — not in the cleanup list, must survive.
	seedFile(t, filesDir, bystander)

	c.cleanupAttachmentFiles([]string{a, b, cc})

	for _, fid := range []string{a, b, cc} {
		if fileExists(t, filesDir, fid) {
			t.Errorf("%s should have been removed", fid)
		}
	}
	if !fileExists(t, filesDir, bystander) {
		t.Error("bystander file should NOT have been removed")
	}
}

// TestCleanupAttachmentFiles_MissingFileIsSilent verifies the
// expected-case where a fileID points at a file that was never
// downloaded — os.IsNotExist is silent (no log, no error).
func TestCleanupAttachmentFiles_MissingFileIsSilent(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	present, missing := testFileID(t, "present"), testFileID(t, "missing")
	seedFile(t, filesDir, present)

	// missing was never downloaded — its remove will fail with
	// IsNotExist, which the helper swallows silently.
	c.cleanupAttachmentFiles([]string{present, missing})

	if fileExists(t, filesDir, present) {
		t.Error("present file should have been removed")
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
	c.cleanupAttachmentFiles([]string{testFileID(t, "x"), testFileID(t, "y")})
}

// TestCleanupAttachmentFiles_EmptyListNoOp verifies the empty-input
// case is a clean no-op (mirroring the bulk-delete path where no
// messages had attachments).
func TestCleanupAttachmentFiles_EmptyListNoOp(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	stays := testFileID(t, "stays")
	seedFile(t, filesDir, stays)

	c.cleanupAttachmentFiles(nil)
	c.cleanupAttachmentFiles([]string{})

	if !fileExists(t, filesDir, stays) {
		t.Error("empty-input cleanup should not touch any files")
	}
}

// TestRemoveCachedAttachment_RemovesTheClosedListAndSparesNeighbours is
// the attachments-v2 §7 bug fix: a deletion removes every file derived
// from the id — the original, `.part`, `.lthumb` and both legacy
// thumbnails — and nothing else. Two neighbours that merely share the
// prefix (`<id>x`, `<id>.png`) must survive, which is what makes the
// closed list safer than a glob.
func TestRemoveCachedAttachment_RemovesTheClosedListAndSparesNeighbours(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	id := testFileID(t, "derived")
	p := filepath.Join(filesDir, id)
	derived := []string{
		id,
		id + ".part",
		id + ".lthumb",
		filepath.Base(ThumbnailPath(p)),
		filepath.Base(ThumbnailPathRasterm(p)),
	}
	neighbours := []string{id + "x", id + ".png"}
	for _, name := range derived {
		seedFile(t, filesDir, name)
	}
	for _, name := range neighbours {
		seedFile(t, filesDir, name)
	}
	if len(derived) != 5 {
		t.Fatalf("closed list has %d entries, want 5", len(derived))
	}

	c.cleanupAttachmentFiles([]string{id})

	for _, name := range derived {
		if fileExists(t, filesDir, name) {
			t.Errorf("derived file %s should have been removed", name)
		}
	}
	for _, name := range neighbours {
		if !fileExists(t, filesDir, name) {
			t.Errorf("neighbour %s should NOT have been removed", name)
		}
	}
}

// TestRemoveCachedAttachment_InvalidIDIsANoOp: an id that is not the
// server's exact shape removes nothing, even when files with that very
// name and its derived names exist (F12 — a hostile server must not be
// able to point cleanup at an arbitrary file).
func TestRemoveCachedAttachment_InvalidIDIsANoOp(t *testing.T) {
	c, filesDir := newCleanupTestClient(t)
	for _, bad := range []string{"evil", "file_abc123", "file_V1StGXR8_Z5jdHi6B-myT.part"} {
		seedFile(t, filesDir, bad)
		seedFile(t, filesDir, bad+".part")
		c.cleanupAttachmentFiles([]string{bad})
		if !fileExists(t, filesDir, bad) || !fileExists(t, filesDir, bad+".part") {
			t.Errorf("invalid id %q removed a file", bad)
		}
	}
}

// TestRemoveCachedAttachment_AbsentFilesAreSilent: nothing on disk for a
// valid id is the common case (never downloaded) and must neither error
// nor panic — including with a nil logger.
func TestRemoveCachedAttachment_AbsentFilesAreSilent(t *testing.T) {
	_, filesDir := newCleanupTestClient(t)
	removeCachedAttachment(filepath.Dir(filesDir), testFileID(t, "absent"), nil)
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

	x1, x2, y := testFileID(t, "x1"), testFileID(t, "x2"), testFileID(t, "y")
	bystander := testFileID(t, "bystander")

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
	insert("msg_x", "room_target", x1, x2)
	insert("msg_y", "room_target", y)
	// Bystander from a different room.
	insert("msg_bystander", "room_other", bystander)

	// Seed the matching files on disk, each with a legacy thumbnail
	// beside it so the purge exercises the closed list too.
	for _, fid := range []string{x1, x2, y, bystander} {
		seedFile(t, filesDir, fid)
		seedFile(t, filesDir, filepath.Base(ThumbnailPath(filepath.Join(filesDir, fid))))
	}

	// Purge the room and clean up its attachments.
	fileIDs, err := st.PurgeRoomMessages("room_target")
	if err != nil {
		t.Fatalf("PurgeRoomMessages: %v", err)
	}
	c.cleanupAttachmentFiles(fileIDs)

	// All three target files and their thumbnails gone.
	for _, fid := range []string{x1, x2, y} {
		if fileExists(t, filesDir, fid) {
			t.Errorf("%s should be removed after PurgeRoomMessages + cleanup", fid)
		}
		if thumb := filepath.Base(ThumbnailPath(filepath.Join(filesDir, fid))); fileExists(t, filesDir, thumb) {
			t.Errorf("%s should be removed with its original", thumb)
		}
	}
	// Bystander and its thumbnail survive.
	if !fileExists(t, filesDir, bystander) {
		t.Error("bystander (different room) should NOT have been removed")
	}
	if thumb := filepath.Base(ThumbnailPath(filepath.Join(filesDir, bystander))); !fileExists(t, filesDir, thumb) {
		t.Error("bystander thumbnail should NOT have been removed")
	}
}
