package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestFileTransferComprehensive exercises file transfer across rooms, DMs,
// group DMs, multi-file messages, and concurrent operations. This is the
// stress test for the 3-channel design (Channels 2 and 3).
func TestFileTransferComprehensive(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()

	// ---- Connect alice, bob, carol ----
	aliceMessages := make(chan json.RawMessage, 200)
	bobMessages := make(chan json.RawMessage, 200)
	carolMessages := make(chan json.RawMessage, 200)
	aliceSynced := make(chan bool, 1)
	bobSynced := make(chan bool, 1)
	carolSynced := make(chan bool, 1)

	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_ft", t.TempDir(), aliceSynced, aliceMessages)
	bob := testutil.MkClient(port, testutil.Bob.KeyPath, "dev_bob_ft", t.TempDir(), bobSynced, bobMessages)
	carol := testutil.MkClient(port, testutil.Carol.KeyPath, "dev_carol_ft", t.TempDir(), carolSynced, carolMessages)

	for _, c := range []*client.Client{alice, bob, carol} {
		if err := c.Connect(); err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer c.Close()
	}

	for name, ch := range map[string]chan bool{"alice": aliceSynced, "bob": bobSynced, "carol": carolSynced} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s sync timeout", name)
		}
	}

	// Wait for epoch rotation so file uploads have a key
	time.Sleep(time.Second)

	// Create a 1:1 DM (alice <-> bob) and a group DM (alice, bob, carol)
	alice.CreateDM([]string{testutil.Bob.Username}, "")
	raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
	var oneToOne protocol.DMCreated
	json.Unmarshal(raw, &oneToOne)
	dmConvID := oneToOne.Conversation
	waitForType(t, bobMessages, "dm_created", 5*time.Second)

	alice.CreateDM([]string{testutil.Bob.Username, testutil.Carol.Username}, "Group")
	raw = waitForType(t, aliceMessages, "dm_created", 5*time.Second)
	var group protocol.DMCreated
	json.Unmarshal(raw, &group)
	groupConvID := group.Conversation
	waitForType(t, bobMessages, "dm_created", 5*time.Second)
	waitForType(t, carolMessages, "dm_created", 5*time.Second)

	time.Sleep(300 * time.Millisecond)

	// ---- Helper: write temp file, return path and content ----
	mkTempFile := func(prefix, contents string) string {
		f, err := os.CreateTemp("", prefix+"-*.bin")
		if err != nil {
			t.Fatalf("temp file: %v", err)
		}
		if _, err := f.Write([]byte(contents)); err != nil {
			t.Fatalf("write: %v", err)
		}
		f.Close()
		t.Cleanup(func() { os.Remove(f.Name()) })
		return f.Name()
	}

	// Helper: verify downloaded file bytes match expected
	verifyDownload := func(t *testing.T, localPath, expected string) {
		t.Helper()
		got, err := os.ReadFile(localPath)
		if err != nil {
			t.Fatalf("read downloaded: %v", err)
		}
		if string(got) != expected {
			t.Errorf("content mismatch\n got:  %q\n want: %q", got, expected)
		}
	}

	// =========================================================================
	// Room: single file upload + download
	// =========================================================================
	t.Run("room_single_file", func(t *testing.T) {
		content := "room attachment v1 — hello from alice"
		path := mkTempFile("room-single", content)

		err := alice.SendRoomMessageFile("general", "here's a file", path, "", nil)
		if err != nil {
			t.Fatalf("alice send: %v", err)
		}

		// Bob receives the message
		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)

		payload, err := bob.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		if len(payload.Attachments) != 1 {
			t.Fatalf("attachments = %d, want 1", len(payload.Attachments))
		}
		att := payload.Attachments[0]
		if att.FileEpoch == 0 {
			t.Error("file_epoch must be set for room attachments")
		}

		// Bob downloads using epoch key
		key := bob.RoomEpochKey(msg.Room, att.FileEpoch)
		if key == nil {
			t.Fatalf("bob has no epoch key for room %s epoch %d", msg.Room, att.FileEpoch)
		}
		localPath, err := bob.DownloadFile(att.FileID, key)
		if err != nil {
			t.Fatalf("bob download: %v", err)
		}
		verifyDownload(t, localPath, content)

		// Alice echoes her own message
		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("room single file: OK")
	})

	// =========================================================================
	// Room: multi-file attachment (3 files in one message)
	// =========================================================================
	t.Run("room_multi_file", func(t *testing.T) {
		contents := []string{
			"file alpha — first of three",
			"file beta — the middle one",
			"file gamma — the final attachment",
		}
		paths := make([]string, len(contents))
		for i, c := range contents {
			paths[i] = mkTempFile(fmt.Sprintf("room-multi-%d", i), c)
		}

		// Upload each file separately, then send ONE message referencing all
		attachments := make([]protocol.Attachment, len(paths))
		for i, p := range paths {
			fileID, err := alice.UploadFile(p, "general", "")
			if err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
			info, _ := os.Stat(p)
			attachments[i] = protocol.Attachment{
				FileID: fileID,
				Name:   filepath.Base(p),
				Size:   info.Size(),
				Mime:   "application/octet-stream",
			}
		}

		err := alice.SendRoomMessageFull("general", "3 attachments", "", nil, attachments)
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		// Bob receives message, decrypts, downloads each attachment
		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)

		payload, err := bob.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		if len(payload.Attachments) != 3 {
			t.Fatalf("attachments = %d, want 3", len(payload.Attachments))
		}

		for i, att := range payload.Attachments {
			key := bob.RoomEpochKey(msg.Room, att.FileEpoch)
			if key == nil {
				t.Fatalf("attachment %d: no epoch key", i)
			}
			localPath, err := bob.DownloadFile(att.FileID, key)
			if err != nil {
				t.Fatalf("download %d: %v", i, err)
			}
			verifyDownload(t, localPath, contents[i])
		}

		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("room multi file: OK")
	})

	// =========================================================================
	// DM: multi-file attachment (3 files, each with its own K_file)
	// =========================================================================
	t.Run("dm_multi_file", func(t *testing.T) {
		contents := []string{
			"DM file A — per-file key A",
			"DM file B — per-file key B",
			"DM file C — per-file key C",
		}
		paths := make([]string, len(contents))
		for i, c := range contents {
			paths[i] = mkTempFile(fmt.Sprintf("dm-multi-%d", i), c)
		}

		// Upload each with its own K_file
		attachments := make([]protocol.Attachment, len(paths))
		for i, p := range paths {
			fileKey, err := crypto.GenerateKey()
			if err != nil {
				t.Fatalf("gen key %d: %v", i, err)
			}
			fileID, err := alice.UploadDMFile(p, dmConvID, fileKey)
			if err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
			info, _ := os.Stat(p)
			attachments[i] = protocol.Attachment{
				FileID:  fileID,
				Name:    filepath.Base(p),
				Size:    info.Size(),
				Mime:    "application/octet-stream",
				FileKey: base64.StdEncoding.EncodeToString(fileKey),
			}
		}

		err := alice.SendDMMessageFull(dmConvID, "3 DM attachments", "", nil, attachments)
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		raw := waitForType(t, bobMessages, "dm", 5*time.Second)
		var dm protocol.DM
		json.Unmarshal(raw, &dm)

		if len(dm.FileIDs) != 3 {
			t.Errorf("envelope file_ids = %d, want 3", len(dm.FileIDs))
		}

		payload, err := bob.DecryptDMMessage(dm.WrappedKeys, dm.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		if len(payload.Attachments) != 3 {
			t.Fatalf("attachments = %d, want 3", len(payload.Attachments))
		}

		for i, att := range payload.Attachments {
			if att.FileKey == "" {
				t.Fatalf("attachment %d: missing file_key", i)
			}
			fileKey, err := base64.StdEncoding.DecodeString(att.FileKey)
			if err != nil {
				t.Fatalf("decode key %d: %v", i, err)
			}
			localPath, err := bob.DownloadFile(att.FileID, fileKey)
			if err != nil {
				t.Fatalf("download %d: %v", i, err)
			}
			verifyDownload(t, localPath, contents[i])
		}

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("dm multi file: OK")
	})

	// =========================================================================
	// Group DM: single file (3 recipients each download and verify)
	// =========================================================================
	t.Run("group_dm_single_file", func(t *testing.T) {
		content := "group DM file — shared K_file across 3 members"
		path := mkTempFile("group-single", content)

		err := alice.SendDMMessageFile(groupConvID, "group file", path, "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		// Bob and Carol both receive and download
		for _, pair := range []struct {
			name string
			c    *client.Client
			ch   chan json.RawMessage
		}{
			{"bob", bob, bobMessages},
			{"carol", carol, carolMessages},
		} {
			raw := waitForType(t, pair.ch, "dm", 5*time.Second)
			var dm protocol.DM
			json.Unmarshal(raw, &dm)

			if len(dm.WrappedKeys) != 3 {
				t.Errorf("%s: wrapped_keys = %d, want 3", pair.name, len(dm.WrappedKeys))
			}

			payload, err := pair.c.DecryptDMMessage(dm.WrappedKeys, dm.Payload)
			if err != nil {
				t.Fatalf("%s decrypt: %v", pair.name, err)
			}
			if len(payload.Attachments) != 1 {
				t.Fatalf("%s: attachments = %d, want 1", pair.name, len(payload.Attachments))
			}
			att := payload.Attachments[0]
			fileKey, _ := base64.StdEncoding.DecodeString(att.FileKey)
			localPath, err := pair.c.DownloadFile(att.FileID, fileKey)
			if err != nil {
				t.Fatalf("%s download: %v", pair.name, err)
			}
			verifyDownload(t, localPath, content)
		}

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("group dm single file: OK")
	})

	// =========================================================================
	// Group DM: multi-file (2 files, 3 recipients each download both)
	// =========================================================================
	t.Run("group_dm_multi_file", func(t *testing.T) {
		contents := []string{
			"group multi file ONE",
			"group multi file TWO",
		}
		paths := make([]string, len(contents))
		for i, c := range contents {
			paths[i] = mkTempFile(fmt.Sprintf("group-multi-%d", i), c)
		}

		attachments := make([]protocol.Attachment, len(paths))
		for i, p := range paths {
			fileKey, _ := crypto.GenerateKey()
			fileID, err := alice.UploadDMFile(p, groupConvID, fileKey)
			if err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
			info, _ := os.Stat(p)
			attachments[i] = protocol.Attachment{
				FileID:  fileID,
				Name:    filepath.Base(p),
				Size:    info.Size(),
				Mime:    "application/octet-stream",
				FileKey: base64.StdEncoding.EncodeToString(fileKey),
			}
		}

		err := alice.SendDMMessageFull(groupConvID, "2 files for group", "", nil, attachments)
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		for _, pair := range []struct {
			name string
			c    *client.Client
			ch   chan json.RawMessage
		}{
			{"bob", bob, bobMessages},
			{"carol", carol, carolMessages},
		} {
			raw := waitForType(t, pair.ch, "dm", 5*time.Second)
			var dm protocol.DM
			json.Unmarshal(raw, &dm)

			payload, err := pair.c.DecryptDMMessage(dm.WrappedKeys, dm.Payload)
			if err != nil {
				t.Fatalf("%s decrypt: %v", pair.name, err)
			}
			if len(payload.Attachments) != 2 {
				t.Fatalf("%s: attachments = %d, want 2", pair.name, len(payload.Attachments))
			}

			for i, att := range payload.Attachments {
				fileKey, _ := base64.StdEncoding.DecodeString(att.FileKey)
				localPath, err := pair.c.DownloadFile(att.FileID, fileKey)
				if err != nil {
					t.Fatalf("%s download %d: %v", pair.name, i, err)
				}
				verifyDownload(t, localPath, contents[i])
			}
		}

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("group dm multi file: OK")
	})

	// =========================================================================
	// Concurrent uploads: 5 uploads from alice in parallel goroutines
	// =========================================================================
	t.Run("concurrent_uploads", func(t *testing.T) {
		const N = 5
		contents := make([]string, N)
		paths := make([]string, N)
		for i := 0; i < N; i++ {
			contents[i] = fmt.Sprintf("concurrent upload #%d — distinct payload", i)
			paths[i] = mkTempFile(fmt.Sprintf("conc-up-%d", i), contents[i])
		}

		// Upload all N files concurrently from alice
		fileIDs := make([]string, N)
		errs := make([]error, N)
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				fileIDs[i], errs[i] = alice.UploadFile(paths[i], "general", "")
			}(i)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("upload %d failed: %v", i, err)
			}
			if fileIDs[i] == "" {
				t.Fatalf("upload %d: empty file_id", i)
			}
		}

		// All file_ids must be distinct
		seen := make(map[string]bool)
		for i, id := range fileIDs {
			if seen[id] {
				t.Errorf("duplicate file_id %q at index %d", id, i)
			}
			seen[id] = true
		}

		// Bob downloads each and verifies content maps to the right file
		// We know the mapping: fileIDs[i] contains contents[i]
		for i := 0; i < N; i++ {
			key := alice.RoomEpochKey("general", alice.CurrentEpoch("general"))
			localPath, err := bob.DownloadFile(fileIDs[i], key)
			if err != nil {
				t.Fatalf("download %d: %v", i, err)
			}
			verifyDownload(t, localPath, contents[i])
		}

		t.Logf("concurrent uploads: %d files uploaded in parallel, all verified", N)
	})

	// =========================================================================
	// Concurrent downloads: bob pulls 5 distinct files in parallel
	// =========================================================================
	t.Run("concurrent_downloads", func(t *testing.T) {
		const N = 5
		contents := make([]string, N)
		paths := make([]string, N)
		for i := 0; i < N; i++ {
			contents[i] = fmt.Sprintf("concurrent download #%d — unique content", i)
			paths[i] = mkTempFile(fmt.Sprintf("conc-dn-%d", i), contents[i])
		}

		// Alice uploads them sequentially (we're testing downloads, not uploads)
		fileIDs := make([]string, N)
		for i := 0; i < N; i++ {
			id, err := alice.UploadFile(paths[i], "general", "")
			if err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
			fileIDs[i] = id
		}

		// Bob downloads all in parallel — this is the real stress test
		// for the downloadChanMu serialization
		epochKey := bob.RoomEpochKey("general", bob.CurrentEpoch("general"))
		if epochKey == nil {
			t.Fatalf("bob has no epoch key")
		}

		localPaths := make([]string, N)
		errs := make([]error, N)
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				localPaths[i], errs[i] = bob.DownloadFile(fileIDs[i], epochKey)
			}(i)
		}
		wg.Wait()

		for i := 0; i < N; i++ {
			if errs[i] != nil {
				t.Fatalf("download %d failed: %v", i, errs[i])
			}
			verifyDownload(t, localPaths[i], contents[i])
		}

		t.Logf("concurrent downloads: %d files downloaded in parallel, all verified", N)
	})

	// =========================================================================
	// Mixed: alice uploads while bob downloads (cross-direction parallelism)
	// =========================================================================
	t.Run("mixed_upload_download", func(t *testing.T) {
		// Pre-upload a file for bob to download
		dlContent := "file for download while upload is happening"
		dlPath := mkTempFile("mixed-dl", dlContent)
		dlFileID, err := alice.UploadFile(dlPath, "general", "")
		if err != nil {
			t.Fatalf("pre-upload: %v", err)
		}

		// New content for alice to upload during the download
		ulContent := "upload happening during download"
		ulPath := mkTempFile("mixed-ul", ulContent)

		epochKey := bob.RoomEpochKey("general", bob.CurrentEpoch("general"))

		// Run both operations in parallel goroutines
		var wg sync.WaitGroup
		var dlErr, ulErr error
		var dlLocalPath, ulFileID string

		wg.Add(2)
		go func() {
			defer wg.Done()
			dlLocalPath, dlErr = bob.DownloadFile(dlFileID, epochKey)
		}()
		go func() {
			defer wg.Done()
			ulFileID, ulErr = alice.UploadFile(ulPath, "general", "")
		}()
		wg.Wait()

		if dlErr != nil {
			t.Fatalf("download: %v", dlErr)
		}
		if ulErr != nil {
			t.Fatalf("upload: %v", ulErr)
		}
		verifyDownload(t, dlLocalPath, dlContent)

		// Verify the concurrent upload also landed correctly by
		// downloading it from bob's side
		localPath, err := bob.DownloadFile(ulFileID, epochKey)
		if err != nil {
			t.Fatalf("verify upload: %v", err)
		}
		verifyDownload(t, localPath, ulContent)

		t.Log("mixed upload/download: both completed in parallel")
	})

	// =========================================================================
	// Download error: server rejects request, client fails fast (not hang)
	// =========================================================================
	t.Run("download_not_found", func(t *testing.T) {
		epochKey := bob.RoomEpochKey("general", bob.CurrentEpoch("general"))
		if epochKey == nil {
			t.Fatalf("bob has no epoch key")
		}

		// Request a file_id that doesn't exist. Must return an error quickly
		// instead of blocking on the binary channel forever.
		done := make(chan error, 1)
		go func() {
			_, err := bob.DownloadFile("file_DoesNotExist_ABC123", epochKey)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected download error for nonexistent file, got nil")
			}
			t.Logf("download rejected as expected: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("DownloadFile hung on nonexistent file (should fail fast)")
		}

		// Subsequent downloads should still work (the error path left no
		// residual state)
		content := "post-error download works"
		path := mkTempFile("post-error", content)
		fileID, err := alice.UploadFile(path, "general", "")
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		localPath, err := bob.DownloadFile(fileID, epochKey)
		if err != nil {
			t.Fatalf("download after error: %v", err)
		}
		verifyDownload(t, localPath, content)

		t.Log("download error: fails fast and leaves no residual state")
	})

	// =========================================================================
	// Content hash: upload hashes encrypted bytes, download verifies
	// =========================================================================
	t.Run("content_hash_roundtrip", func(t *testing.T) {
		content := "content hash verification — BLAKE2b-256"
		path := mkTempFile("hash-test", content)

		err := alice.SendRoomMessageFile("general", "hash test", path, "", nil)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}

		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)

		payload, err := bob.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if len(payload.Attachments) != 1 {
			t.Fatalf("attachments = %d", len(payload.Attachments))
		}
		att := payload.Attachments[0]

		key := bob.RoomEpochKey(msg.Room, att.FileEpoch)
		localPath, err := bob.DownloadFile(att.FileID, key)
		if err != nil {
			t.Fatalf("download with hash verification: %v", err)
		}
		verifyDownload(t, localPath, content)

		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("content hash roundtrip: upload hashed, download verified, content matches")
	})

	// =========================================================================
	// Content hash: DM file also hashed and verified
	// =========================================================================
	t.Run("content_hash_dm", func(t *testing.T) {
		content := "DM hash verification"
		path := mkTempFile("hash-dm", content)

		err := alice.SendDMMessageFile(dmConvID, "dm hash test", path, "", nil)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}

		raw := waitForType(t, bobMessages, "dm", 5*time.Second)
		var dm protocol.DM
		json.Unmarshal(raw, &dm)

		payload, err := bob.DecryptDMMessage(dm.WrappedKeys, dm.Payload)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if len(payload.Attachments) != 1 {
			t.Fatalf("attachments = %d", len(payload.Attachments))
		}
		att := payload.Attachments[0]

		fileKey, _ := base64.StdEncoding.DecodeString(att.FileKey)
		localPath, err := bob.DownloadFile(att.FileID, fileKey)
		if err != nil {
			t.Fatalf("download with hash verification: %v", err)
		}
		verifyDownload(t, localPath, content)

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("content hash dm: DM file hashed and verified on download")
	})
}
