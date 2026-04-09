//go:build e2e

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
	"github.com/brushtailmedia/sshkey-term/internal/tui"
)

// TestTUIDisplayNameResolution verifies the TUI correctly resolves nanoid
// usernames to display names across messages, typing, and system events.
func TestTUIDisplayNameResolution(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceMsgs := make(chan json.RawMessage, 200)
	bobMsgs := make(chan json.RawMessage, 200)
	aliceSynced := make(chan bool, 1)
	bobSynced := make(chan bool, 1)

	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_tui", t.TempDir(), aliceSynced, aliceMsgs)
	bob := testutil.MkClient(port, testutil.Bob.KeyPath, "dev_bob_tui", t.TempDir(), bobSynced, bobMsgs)

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
	time.Sleep(time.Second)

	generalID := roomIDByName(t, alice, "general")

	// --- Test 1: DisplayName resolver returns display name, not nanoid ---
	t.Run("resolver_returns_display_name", func(t *testing.T) {
		aliceDisplay := alice.DisplayName(alice.UserID())
		bobDisplay := alice.DisplayName(bob.UserID())

		if aliceDisplay == alice.UserID() {
			t.Errorf("alice display name IS the nanoid: %q", aliceDisplay)
		}
		if bobDisplay == bob.UserID() {
			t.Errorf("bob display name IS the nanoid: %q", bobDisplay)
		}
		if !strings.HasPrefix(alice.UserID(), "usr_") {
			t.Errorf("alice username doesn't look like nanoid: %q", alice.UserID())
		}

		t.Logf("alice: %s → %s", alice.UserID(), aliceDisplay)
		t.Logf("bob: %s → %s", bob.UserID(), bobDisplay)
	})

	// --- Test 2: AddRoomMessage resolves From to display name ---
	t.Run("message_from_is_display_name", func(t *testing.T) {
		err := alice.SendRoomMessage(generalID, "display name test", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		raw := waitForType(t, bobMsgs, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		waitForType(t, aliceMsgs, "message", 5*time.Second)

		// msg.From in protocol is the nanoid
		if !strings.HasPrefix(msg.From, "usr_") {
			t.Errorf("protocol From should be nanoid, got: %q", msg.From)
		}

		// Simulate TUI AddRoomMessage
		m := tui.NewMessages()
		m.SetContext(generalID, "")
		m.AddRoomMessage(msg, bob)

		// The displayed message should use display name
		sel := m.MessageAt(0)
		if sel == nil {
			t.Fatal("no message added")
		}
		if sel.FromID != msg.From {
			t.Errorf("FromID = %q, want %q (nanoid)", sel.FromID, msg.From)
		}
		if sel.From == msg.From {
			t.Errorf("From should be display name, not nanoid: %q", sel.From)
		}

		t.Logf("message: FromID=%s From=%s Body=%s", sel.FromID, sel.From, sel.Body)
	})

	// --- Test 3: Mention completion uses display names ---
	t.Run("mention_completion_display_name", func(t *testing.T) {
		aliceDisplay := alice.DisplayName(alice.UserID())
		bobDisplay := bob.DisplayName(bob.UserID())

		members := []tui.MemberEntry{
			{UserID: alice.UserID(), DisplayName: aliceDisplay},
			{UserID: bob.UserID(), DisplayName: bobDisplay},
		}

		// Type @<first letter of bob's display name>
		query := "@" + strings.ToLower(bobDisplay[:2])
		comp := tui.Complete(query, len(query), members)

		if comp == nil {
			t.Fatalf("no completion for %q", query)
		}

		// Completion should show display name, not nanoid
		found := false
		for _, item := range comp.Items() {
			if strings.Contains(item.Display, bobDisplay) {
				found = true
			}
			if strings.Contains(item.Display, "usr_") {
				t.Errorf("completion shows nanoid: %q", item.Display)
			}
		}
		if !found {
			t.Errorf("completion doesn't show %q", bobDisplay)
		}

		t.Logf("mention completion for %q: found %s", query, bobDisplay)
	})

	// --- Test 4: ExtractMentions returns nanoid from @displayName ---
	t.Run("extract_mentions_returns_nanoid", func(t *testing.T) {
		bobDisplay := bob.DisplayName(bob.UserID())
		body := "hey @" + bobDisplay + " check this"

		input := tui.NewInput()
		input.SetMembers([]tui.MemberEntry{
			{UserID: alice.UserID(), DisplayName: alice.DisplayName(alice.UserID())},
			{UserID: bob.UserID(), DisplayName: bobDisplay},
		})

		mentions := input.ExtractMentions(body)
		if len(mentions) != 1 {
			t.Fatalf("expected 1 mention, got %d", len(mentions))
		}
		if mentions[0] != bob.UserID() {
			t.Errorf("mention = %q, want %q (nanoid)", mentions[0], bob.UserID())
		}

		t.Logf("extracted @%s → %s", bobDisplay, mentions[0])
	})
}

// TestTUIWithPreSeededDB verifies the TUI correctly loads and displays
// data from a pre-seeded encrypted database.
func TestTUIWithPreSeededDB(t *testing.T) {
	testutil.EnsureFixtures(t)

	dbDir := testutil.CreateTestDB(t, testutil.Alice.KeyPath)

	// Open the DB with the real key derivation
	privKey, _ := client.ParseRawEd25519Key(testutil.Alice.KeyPath)
	dbKey, _ := store.DeriveDBKey(privKey.Seed())
	dbPath := filepath.Join(dbDir, "messages.db")

	st, err := store.Open(dbPath, dbKey)
	if err != nil {
		t.Fatalf("reopen encrypted DB: %v", err)
	}
	defer st.Close()

	// Verify seeded messages have nanoid senders
	msgs, err := st.GetRoomMessages(testutil.TestRoomID, 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected 2+ seeded messages, got %d", len(msgs))
	}

	for _, m := range msgs {
		if !strings.HasPrefix(m.Sender, "usr_") {
			t.Errorf("seeded message sender should be nanoid: %q", m.Sender)
		}
		if m.Body == "" {
			t.Errorf("seeded message %s has empty body", m.ID)
		}
	}

	// Verify epoch key was seeded
	key, _ := st.GetEpochKey(testutil.TestRoomID, 1)
	if key == nil {
		t.Error("epoch key not seeded")
	}

	t.Logf("pre-seeded DB: %d messages, senders are nanoids, epoch key present", len(msgs))
}

// TestTUIErrorRendering verifies error messages from the server
// display correctly (not showing raw nanoids in user-facing text).
func TestTUIErrorRendering(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceSynced := make(chan bool, 1)
	aliceMsgs := make(chan json.RawMessage, 200)
	alice := testutil.MkClient(port, testutil.Alice.KeyPath, "dev_alice_errtui", t.TempDir(), aliceSynced, aliceMsgs)

	if err := alice.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer alice.Close()
	<-aliceSynced

	generalID := roomIDByName(t, alice, "general")

	// --- Test: username_taken error message is user-friendly ---
	t.Run("username_taken_error", func(t *testing.T) {
		// Try to set display name to Bob's name (should fail)
		bobDisplay := alice.DisplayName(testutil.Bob.UserID)
		alice.Enc().Encode(protocol.SetProfile{
			Type:        "set_profile",
			DisplayName: bobDisplay,
		})

		raw := waitForType(t, aliceMsgs, "error", 5*time.Second)
		var errMsg protocol.Error
		json.Unmarshal(raw, &errMsg)

		// Error message should contain the display name, not a nanoid
		if strings.Contains(errMsg.Message, "usr_") {
			t.Errorf("error message contains nanoid: %q", errMsg.Message)
		}
		if !strings.Contains(errMsg.Message, bobDisplay) {
			t.Errorf("error message should reference display name %q: %q", bobDisplay, errMsg.Message)
		}

		t.Logf("username_taken error: %s", errMsg.Message)
	})

	// --- Test: upload_error includes upload_id not nanoid ---
	t.Run("upload_error_format", func(t *testing.T) {
		uploadID := "up_test_err_render"
		alice.Enc().Encode(protocol.UploadStart{
			Type:     "upload_start",
			UploadID: uploadID,
			Size:     100,
			Room:     generalID,
			// Missing content_hash
		})

		raw := waitForType(t, aliceMsgs, "upload_error", 5*time.Second)
		var errMsg protocol.UploadError
		json.Unmarshal(raw, &errMsg)

		if errMsg.UploadID != uploadID {
			t.Errorf("upload_id = %q, want %q", errMsg.UploadID, uploadID)
		}
		// Error message should be human-readable
		if errMsg.Message == "" {
			t.Error("error message is empty")
		}

		t.Logf("upload error: code=%s msg=%s", errMsg.Code, errMsg.Message)
	})
}
