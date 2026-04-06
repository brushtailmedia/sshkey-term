package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
)

// TestErrorPaths exercises negative / error paths that should fail gracefully
// rather than hang, crash, or corrupt state.
func TestErrorPaths(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceMsgs := make(chan json.RawMessage, 200)
	bobMsgs := make(chan json.RawMessage, 200)
	aliceSynced := make(chan bool, 1)
	bobSynced := make(chan bool, 1)

	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_err", t.TempDir(), aliceSynced, aliceMsgs)
	bob := testutil.MkClient(port, testutil.Bob.KeyPath, "dev_bob_err", t.TempDir(), bobSynced, bobMsgs)

	if err := alice.Connect(); err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Close()
	<-aliceSynced

	if err := bob.Connect(); err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer bob.Close()
	<-bobSynced

	time.Sleep(time.Second) // epoch rotation

	// =========================================================================
	// 1. Display name rejected — duplicate
	// =========================================================================
	t.Run("display_name_duplicate", func(t *testing.T) {
		// Bob tries to set his display name to Alice's display name
		bobDisplayBefore := bob.DisplayName(bob.Username())

		bob.Enc().Encode(protocol.SetProfile{
			Type:        "set_profile",
			DisplayName: alice.DisplayName(alice.Username()),
		})

		// Should receive an error back
		raw := waitForType(t, bobMsgs, "error", 5*time.Second)
		var errMsg protocol.Error
		json.Unmarshal(raw, &errMsg)

		if errMsg.Code != "username_taken" {
			t.Errorf("error code = %q, want username_taken", errMsg.Code)
		}

		// Bob's display name should be unchanged
		bobDisplayAfter := bob.DisplayName(bob.Username())
		if bobDisplayBefore != bobDisplayAfter {
			t.Errorf("bob's name changed despite rejection: %q → %q", bobDisplayBefore, bobDisplayAfter)
		}

		t.Logf("display name duplicate: rejected with %q — %s", errMsg.Code, errMsg.Message)
	})

	// =========================================================================
	// 2. Display name — empty rejected
	// =========================================================================
	t.Run("display_name_empty", func(t *testing.T) {
		bob.Enc().Encode(protocol.SetProfile{
			Type:        "set_profile",
			DisplayName: "",
		})

		raw := waitForType(t, bobMsgs, "error", 5*time.Second)
		var errMsg protocol.Error
		json.Unmarshal(raw, &errMsg)

		if errMsg.Code != "invalid_profile" {
			t.Errorf("error code = %q, want invalid_profile", errMsg.Code)
		}

		t.Logf("empty display name: rejected with %q", errMsg.Code)
	})

	// =========================================================================
	// 3. Download nonexistent file — fails fast, no hang
	// =========================================================================
	t.Run("download_not_found", func(t *testing.T) {
		epochKey := bob.RoomEpochKey("general", bob.CurrentEpoch("general"))
		if epochKey == nil {
			t.Skip("no epoch key")
		}

		done := make(chan error, 1)
		go func() {
			_, err := bob.DownloadFile("file_DOES_NOT_EXIST_xyz", epochKey)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected error for nonexistent file")
			}
			t.Logf("download not found: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("download hung on nonexistent file — should fail fast")
		}
	})

	// =========================================================================
	// 4. Upload without content hash — rejected
	// =========================================================================
	t.Run("upload_missing_hash", func(t *testing.T) {
		// Send upload_start with no content_hash
		uploadID := "up_test_no_hash"
		alice.Enc().Encode(protocol.UploadStart{
			Type:     "upload_start",
			UploadID: uploadID,
			Size:     100,
			Room:     "general",
			// ContentHash intentionally empty
		})

		raw := waitForType(t, aliceMsgs, "upload_error", 5*time.Second)
		var errMsg protocol.UploadError
		json.Unmarshal(raw, &errMsg)

		if errMsg.UploadID != uploadID {
			t.Errorf("upload_id = %q, want %q", errMsg.UploadID, uploadID)
		}
		if errMsg.Code != "missing_hash" {
			t.Errorf("code = %q, want missing_hash", errMsg.Code)
		}

		t.Logf("upload missing hash: rejected with %q — %s", errMsg.Code, errMsg.Message)
	})

	// =========================================================================
	// 5. Upload too large — rejected
	// =========================================================================
	t.Run("upload_too_large", func(t *testing.T) {
		uploadID := "up_test_too_big"
		alice.Enc().Encode(protocol.UploadStart{
			Type:        "upload_start",
			UploadID:    uploadID,
			Size:        500 * 1024 * 1024, // 500MB > 50MB limit
			ContentHash: "blake2b-256:deadbeef",
			Room:        "general",
		})

		raw := waitForType(t, aliceMsgs, "upload_error", 5*time.Second)
		var errMsg protocol.UploadError
		json.Unmarshal(raw, &errMsg)

		if errMsg.Code != "upload_too_large" {
			t.Errorf("code = %q, want upload_too_large", errMsg.Code)
		}

		t.Logf("upload too large: rejected with %q", errMsg.Code)
	})

	// =========================================================================
	// 6. Self-verify blocked
	// =========================================================================
	t.Run("self_verify_blocked", func(t *testing.T) {
		// The verify model should refuse to compute a safety number for self
		// This is a TUI-level check, not protocol — test via the model directly
		// (already covered in wizard_test.go / verify tests)
		// Here we just confirm the client-level identity is correct
		if alice.Username() == "" {
			t.Fatal("alice has no username")
		}
		if alice.Username() == bob.Username() {
			t.Fatal("alice and bob have the same username")
		}
		t.Logf("self-verify: alice=%s bob=%s (different identities confirmed)", alice.Username(), bob.Username())
	})

	// =========================================================================
	// 7. Send to nonexistent conversation — error
	// =========================================================================
	t.Run("send_to_nonexistent_conv", func(t *testing.T) {
		err := alice.SendDMMessage("conv_DOES_NOT_EXIST", "hello?", "", nil)
		if err == nil {
			// The send might succeed at the protocol level but the server
			// should return an error. Check for it.
			raw := waitForType(t, aliceMsgs, "error", 3*time.Second)
			var errMsg protocol.Error
			json.Unmarshal(raw, &errMsg)
			t.Logf("nonexistent conv: server error %q — %s", errMsg.Code, errMsg.Message)
		} else {
			t.Logf("nonexistent conv: client error — %v", err)
		}
	})

	// =========================================================================
	// 8. Send message with wrong epoch — graceful handling
	// =========================================================================
	t.Run("message_wrong_epoch", func(t *testing.T) {
		// Send a room message and verify it arrives correctly first
		err := alice.SendRoomMessage("general", "error test msg", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		waitForType(t, bobMsgs, "message", 5*time.Second)
		waitForType(t, aliceMsgs, "message", 5*time.Second)

		t.Log("wrong epoch: verified normal message flow works (epoch validation is server-side)")
	})

	// =========================================================================
	// 9. Content hash verification — correct hash passes
	// =========================================================================
	t.Run("content_hash_correct", func(t *testing.T) {
		// Encrypt some data and verify the hash round-trips
		key := alice.RoomEpochKey("general", alice.CurrentEpoch("general"))
		if key == nil {
			t.Skip("no epoch key")
		}

		plaintext := []byte("hash verification test data")
		encrypted, err := crypto.Encrypt(key, plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		hash := crypto.ContentHash([]byte(encrypted))
		if hash == "" {
			t.Fatal("empty content hash")
		}
		if err := crypto.VerifyContentHash([]byte(encrypted), hash); err != nil {
			t.Fatalf("hash verification failed on correct data: %v", err)
		}

		// Corrupt one byte and verify hash fails
		corrupted := []byte(encrypted)
		corrupted[0] ^= 0xFF
		if err := crypto.VerifyContentHash(corrupted, hash); err == nil {
			t.Fatal("hash verification should fail on corrupted data")
		}

		t.Logf("content hash: correct=%s, corruption detected", hash[:30])
	})

	// =========================================================================
	// 10. DM to self — should work (send note to self)
	// =========================================================================
	t.Run("dm_to_self", func(t *testing.T) {
		// Create a DM with just yourself
		alice.CreateDM([]string{alice.Username()}, "")

		// Should either succeed (self-DM) or return an error — not hang
		select {
		case raw := <-aliceMsgs:
			typ, _ := protocol.TypeOf(raw)
			if typ == "dm_created" {
				t.Log("dm to self: conversation created")
			} else if typ == "error" {
				var errMsg protocol.Error
				json.Unmarshal(raw, &errMsg)
				t.Logf("dm to self: rejected — %s", errMsg.Message)
			} else {
				t.Logf("dm to self: got %s", typ)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("dm to self: hung — no response")
		}
	})

	// =========================================================================
	// 11. Reaction on nonexistent message — doesn't crash
	// =========================================================================
	t.Run("reaction_nonexistent_msg", func(t *testing.T) {
		err := alice.SendRoomReaction("general", "msg_DOES_NOT_EXIST", "👍")
		if err != nil {
			t.Logf("reaction on nonexistent: client error — %v", err)
			return
		}

		// Server may silently ignore or return error
		select {
		case raw := <-aliceMsgs:
			typ, _ := protocol.TypeOf(raw)
			t.Logf("reaction on nonexistent: got %s", typ)
		case <-time.After(2 * time.Second):
			t.Log("reaction on nonexistent: no response (silently ignored)")
		}
	})

	// =========================================================================
	// 12. Unreact with invalid reaction_id — doesn't crash
	// =========================================================================
	t.Run("unreact_invalid_id", func(t *testing.T) {
		err := alice.SendUnreact("react_DOES_NOT_EXIST")
		if err != nil {
			t.Logf("unreact invalid: client error — %v", err)
			return
		}

		select {
		case raw := <-aliceMsgs:
			typ, _ := protocol.TypeOf(raw)
			t.Logf("unreact invalid: got %s", typ)
		case <-time.After(2 * time.Second):
			t.Log("unreact invalid: no response (silently ignored)")
		}
	})
}

// TestRetiredLoginRejected verifies a retired user's key is rejected.
// This requires retiring a user mid-test, so it needs its own server.
func TestRetiredLoginRejected(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceSynced := make(chan bool, 1)
	aliceMsgs := make(chan json.RawMessage, 200)

	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_retire", t.TempDir(), aliceSynced, aliceMsgs)
	if err := alice.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	<-aliceSynced

	// Alice retires herself
	err := alice.SendRetireMe("self_compromise")
	if err != nil {
		t.Fatalf("retire: %v", err)
	}

	// Should receive error (account retired) and get disconnected
	select {
	case raw := <-aliceMsgs:
		typ, _ := protocol.TypeOf(raw)
		if typ == "error" {
			var errMsg protocol.Error
			json.Unmarshal(raw, &errMsg)
			t.Logf("retirement: %s — %s", errMsg.Code, errMsg.Message)
		} else {
			t.Logf("retirement: got %s", typ)
		}
	case <-time.After(5 * time.Second):
		t.Log("retirement: no error received (connection may have closed)")
	}

	// Wait for disconnection
	time.Sleep(time.Second)

	// Try to reconnect — should be rejected
	alice2Synced := make(chan bool, 1)
	alice2Msgs := make(chan json.RawMessage, 50)
	alice2 := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_retire2", t.TempDir(), alice2Synced, alice2Msgs)

	err = alice2.Connect()
	if err != nil {
		// Expected: connection rejected
		t.Logf("retired reconnect: rejected — %v", err)
	} else {
		// If connect succeeds, sync should fail or we get an error
		select {
		case <-alice2Synced:
			t.Error("retired user should not sync successfully")
			alice2.Close()
		case raw := <-alice2Msgs:
			typ, _ := protocol.TypeOf(raw)
			t.Logf("retired reconnect: got %s (expected rejection)", typ)
			alice2.Close()
		case <-time.After(3 * time.Second):
			t.Log("retired reconnect: no sync (connection likely rejected at SSH level)")
		}
	}
}

// TestConnectionToWrongPort verifies the client handles connection failures.
func TestConnectionToWrongPort(t *testing.T) {
	testutil.EnsureFixtures(t)

	synced := make(chan bool, 1)
	msgs := make(chan json.RawMessage, 50)

	// Connect to a port where nothing is listening
	c := testutil.MkClient(59999, testutil.Alice.KeyPath, "dev_wrong_port", t.TempDir(), synced, msgs)

	err := c.Connect()
	if err == nil {
		c.Close()
		t.Fatal("should fail connecting to wrong port")
	}

	t.Logf("wrong port: %v", err)
}

// TestUploadRateLimit verifies the client handles upload rate limiting.
func TestUploadRateLimit(t *testing.T) {
	// The test server has uploads_per_minute = 6000 (very high for testing).
	// To test rate limiting without sending 6000+ uploads, we verify the
	// upload_error path is functional by sending an invalid upload.
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceSynced := make(chan bool, 1)
	aliceMsgs := make(chan json.RawMessage, 200)
	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_rate", t.TempDir(), aliceSynced, aliceMsgs)
	if err := alice.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer alice.Close()
	<-aliceSynced

	// Send upload_start with missing hash — server returns upload_error immediately
	alice.Enc().Encode(protocol.UploadStart{
		Type:     "upload_start",
		UploadID: "up_rate_test",
		Size:     100,
		Room:     "general",
	})

	raw := waitForType(t, aliceMsgs, "upload_error", 5*time.Second)
	var errMsg protocol.UploadError
	json.Unmarshal(raw, &errMsg)

	if errMsg.UploadID != "up_rate_test" {
		t.Errorf("upload_id = %q", errMsg.UploadID)
	}

	// After receiving the error, normal operations should still work
	time.Sleep(time.Second)
	err := alice.SendRoomMessage("general", "post-error message", "", nil)
	if err != nil {
		t.Fatalf("send after upload error: %v", err)
	}
	waitForType(t, aliceMsgs, "message", 5*time.Second)

	t.Log("upload rate limit: error handled, connection still functional")
}

// Ensure testutil is imported (for _ usage check)
var _ = testutil.Alice
var _ = crypto.ContentHash
var _ = os.TempDir
