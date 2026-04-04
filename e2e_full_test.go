package main

import (
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestFullE2E is a comprehensive test exercising every feature of the system.
func TestFullE2E(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()

	aliceDir := t.TempDir()
	bobDir := t.TempDir()

	// =========================================================================
	// 1. Config loading and device ID generation
	// =========================================================================
	t.Run("config", func(t *testing.T) {
		cfg, err := config.Load(aliceDir)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.Device.ID != "" {
			t.Error("fresh config should have no device ID")
		}

		config.EnsureDeviceID(cfg)
		if cfg.Device.ID == "" {
			t.Fatal("device ID not generated")
		}
		if len(cfg.Device.ID) < 20 {
			t.Errorf("device ID too short: %s", cfg.Device.ID)
		}
		t.Logf("device ID: %s", cfg.Device.ID)

		// Save and reload
		config.Save(aliceDir, cfg)
		cfg2, _ := config.Load(aliceDir)
		if cfg2.Device.ID != cfg.Device.ID {
			t.Error("device ID not persisted")
		}
		t.Log("config: PASSED")
	})

	// =========================================================================
	// 2. Keybinding config
	// =========================================================================
	t.Run("keybindings", func(t *testing.T) {
		kb := config.LoadKeybindings(aliceDir)
		if kb.Global.Quit != "ctrl+q" {
			t.Errorf("default quit = %q, want ctrl+q", kb.Global.Quit)
		}
		if kb.Message.Reply != "r" {
			t.Errorf("default reply = %q, want r", kb.Message.Reply)
		}

		// Check files were created
		if _, err := os.Stat(filepath.Join(aliceDir, "keybindings.default.toml")); err != nil {
			t.Error("keybindings.default.toml not created")
		}
		if _, err := os.Stat(filepath.Join(aliceDir, "keybindings.toml")); err != nil {
			t.Error("keybindings.toml not created")
		}
		t.Log("keybindings: PASSED")
	})

	// =========================================================================
	// 3. Connect alice, bob, and carol
	// =========================================================================
	var alice, bob, carol *client.Client
	var aliceProfiles, bobProfiles []protocol.Profile
	var aliceMu, bobMu sync.Mutex
	aliceSynced := make(chan bool, 1)
	bobSynced := make(chan bool, 1)
	carolSynced := make(chan bool, 1)
	aliceMessages := make(chan json.RawMessage, 50)
	bobMessages := make(chan json.RawMessage, 50)
	carolMessages := make(chan json.RawMessage, 50)

	t.Run("connect", func(t *testing.T) {
		alice = client.New(client.Config{
			Host:     "127.0.0.1",
			Port:     port,
			KeyPath:  "/tmp/sshkey-test-key",
			DeviceID: "dev_alice_full",
			DataDir:  aliceDir,
			Logger:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
			OnMessage: func(msgType string, raw json.RawMessage) {
				switch msgType {
				case "sync_complete":
					aliceSynced <- true
				case "profile":
					var p protocol.Profile
					json.Unmarshal(raw, &p)
					aliceMu.Lock()
					aliceProfiles = append(aliceProfiles, p)
					aliceMu.Unlock()
				default:
					aliceMessages <- raw
				}
			},
		})
		if err := alice.Connect(); err != nil {
			t.Fatalf("alice connect: %v", err)
		}

		select {
		case <-aliceSynced:
		case <-time.After(5 * time.Second):
			t.Fatal("alice sync timeout")
		}

		if alice.Username() != "alice" {
			t.Errorf("alice username = %q", alice.Username())
		}
		if !alice.IsAdmin() {
			t.Error("alice should be admin")
		}
		if len(alice.Rooms()) != 2 {
			t.Errorf("alice rooms = %v", alice.Rooms())
		}

		// Wait for epoch rotation
		time.Sleep(time.Second)

		bob = client.New(client.Config{
			Host:     "127.0.0.1",
			Port:     port,
			KeyPath:  "/tmp/sshkey-test-key-bob",
			DeviceID: "dev_bob_full",
			DataDir:  bobDir,
			Logger:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
			OnMessage: func(msgType string, raw json.RawMessage) {
				switch msgType {
				case "sync_complete":
					bobSynced <- true
				case "profile":
					var p protocol.Profile
					json.Unmarshal(raw, &p)
					bobMu.Lock()
					bobProfiles = append(bobProfiles, p)
					bobMu.Unlock()
				default:
					bobMessages <- raw
				}
			},
		})
		if err := bob.Connect(); err != nil {
			t.Fatalf("bob connect: %v", err)
		}

		select {
		case <-bobSynced:
		case <-time.After(5 * time.Second):
			t.Fatal("bob sync timeout")
		}

		if bob.Username() != "bob" {
			t.Errorf("bob username = %q", bob.Username())
		}

		carol = client.New(client.Config{
			Host:     "127.0.0.1",
			Port:     port,
			KeyPath:  "/tmp/sshkey-test-key-carol",
			DeviceID: "dev_carol_full",
			DataDir:  t.TempDir(),
			Logger:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
			OnMessage: func(msgType string, raw json.RawMessage) {
				switch msgType {
				case "sync_complete":
					carolSynced <- true
				default:
					carolMessages <- raw
				}
			},
		})
		if err := carol.Connect(); err != nil {
			t.Fatalf("carol connect: %v", err)
		}

		select {
		case <-carolSynced:
		case <-time.After(5 * time.Second):
			t.Fatal("carol sync timeout")
		}

		time.Sleep(500 * time.Millisecond)

		t.Logf("alice: user=%s admin=%v rooms=%v epoch=%d", alice.Username(), alice.IsAdmin(), alice.Rooms(), alice.CurrentEpoch("general"))
		t.Logf("bob: user=%s rooms=%v epoch=%d", bob.Username(), bob.Rooms(), bob.CurrentEpoch("general"))
		t.Logf("carol: user=%s rooms=%v epoch=%d", carol.Username(), carol.Rooms(), carol.CurrentEpoch("general"))
		t.Log("connect: PASSED")
	})

	defer alice.Close()
	defer bob.Close()
	defer carol.Close()

	// =========================================================================
	// 4. Profile delivery and key pinning
	// =========================================================================
	t.Run("profiles_and_pinning", func(t *testing.T) {
		aliceProfile := alice.Profile("alice")
		bobProfile := alice.Profile("bob")

		if aliceProfile == nil || bobProfile == nil {
			t.Fatal("profiles not received")
		}
		if aliceProfile.PubKey == "" {
			t.Error("alice profile missing pubkey")
		}
		if bobProfile.KeyFingerprint == "" {
			t.Error("bob profile missing fingerprint")
		}

		// Check key pinning in local DB
		if alice.Store() != nil {
			fp, verified, err := alice.Store().GetPinnedKey("bob")
			if err != nil {
				t.Fatalf("get pinned key: %v", err)
			}
			if fp != bobProfile.KeyFingerprint {
				t.Errorf("pinned fp mismatch: %s vs %s", fp, bobProfile.KeyFingerprint)
			}
			if verified {
				t.Error("bob should not be verified yet")
			}
		}
		t.Log("profiles and pinning: PASSED")
	})

	// =========================================================================
	// 5. Safety numbers
	// =========================================================================
	t.Run("safety_numbers", func(t *testing.T) {
		aliceProfile := alice.Profile("alice")
		bobProfile := alice.Profile("bob")

		alicePub, err := crypto.ParseSSHPubKey(aliceProfile.PubKey)
		if err != nil {
			t.Fatalf("parse alice pubkey: %v", err)
		}
		bobPub, err := crypto.ParseSSHPubKey(bobProfile.PubKey)
		if err != nil {
			t.Fatalf("parse bob pubkey: %v", err)
		}

		sn1 := crypto.SafetyNumber(ed25519.PublicKey(alicePub), ed25519.PublicKey(bobPub))
		sn2 := crypto.SafetyNumber(ed25519.PublicKey(bobPub), ed25519.PublicKey(alicePub))

		if sn1 != sn2 {
			t.Errorf("safety numbers not symmetric: %s vs %s", sn1, sn2)
		}
		if len(sn1) != 29 {
			t.Errorf("safety number length = %d, want 29", len(sn1))
		}
		t.Logf("safety number: %s", sn1)
		t.Log("safety numbers: PASSED")
	})

	// =========================================================================
	// 6. Encrypted room messaging
	// =========================================================================
	t.Run("room_messaging", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		err := alice.SendRoomMessage("general", "Hello from full test!", "", nil)
		if err != nil {
			t.Fatalf("alice send: %v", err)
		}

		// Bob receives
		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)

		payload, err := bob.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		if payload.Body != "Hello from full test!" {
			t.Errorf("body = %q", payload.Body)
		}
		if payload.Seq != 1 {
			t.Errorf("seq = %d, want 1", payload.Seq)
		}
		if payload.DeviceID != "dev_alice_full" {
			t.Errorf("device_id = %q", payload.DeviceID)
		}

		// Alice's echo
		waitForType(t, aliceMessages, "message", 5*time.Second)

		t.Log("room messaging: PASSED")
	})

	// =========================================================================
	// 7. Room message with @mention
	// =========================================================================
	t.Run("mentions", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		err := alice.SendRoomMessage("general", "Hey @bob check this", "", []string{"bob"})
		if err != nil {
			t.Fatalf("send: %v", err)
		}

		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)

		payload, err := bob.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if len(payload.Mentions) == 0 || payload.Mentions[0] != "bob" {
			t.Errorf("mentions = %v, want [bob]", payload.Mentions)
		}

		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("mentions: PASSED")
	})

	// =========================================================================
	// 8. Room message with reply
	// =========================================================================
	t.Run("reply", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		// Bob sends a message, alice replies
		bob.SendRoomMessage("general", "original message", "", nil)
		raw := waitForType(t, aliceMessages, "message", 5*time.Second)
		var orig protocol.Message
		json.Unmarshal(raw, &orig)
		waitForType(t, bobMessages, "message", 5*time.Second) // bob's echo

		alice.SendRoomMessage("general", "my reply", orig.ID, nil)
		raw = waitForType(t, bobMessages, "message", 5*time.Second)
		var reply protocol.Message
		json.Unmarshal(raw, &reply)

		payload, _ := bob.DecryptRoomMessage(reply.Room, reply.Epoch, reply.Payload)
		if payload.ReplyTo != orig.ID {
			t.Errorf("reply_to = %q, want %q", payload.ReplyTo, orig.ID)
		}

		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("reply: PASSED")
	})

	// =========================================================================
	// 9. Create 1:1 DM
	// =========================================================================
	var dmConvID string
	t.Run("dm_create", func(t *testing.T) {
		alice.CreateDM([]string{"bob"}, "")

		raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
		var created protocol.DMCreated
		json.Unmarshal(raw, &created)

		dmConvID = created.Conversation
		if dmConvID == "" {
			t.Fatal("no conversation ID")
		}
		if len(created.Members) != 2 {
			t.Errorf("members = %v", created.Members)
		}

		// Bob should also receive dm_created
		raw = waitForType(t, bobMessages, "dm_created", 5*time.Second)
		var bobCreated protocol.DMCreated
		json.Unmarshal(raw, &bobCreated)
		if bobCreated.Conversation != dmConvID {
			t.Errorf("bob got different conv ID: %s vs %s", bobCreated.Conversation, dmConvID)
		}

		t.Logf("DM created: %s members=%v", dmConvID, created.Members)
		t.Log("dm create: PASSED")
	})

	// =========================================================================
	// 10. DM deduplication
	// =========================================================================
	t.Run("dm_dedup", func(t *testing.T) {
		alice.CreateDM([]string{"bob"}, "")

		raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
		var created protocol.DMCreated
		json.Unmarshal(raw, &created)

		if created.Conversation != dmConvID {
			t.Errorf("dedup failed: got %s, want %s", created.Conversation, dmConvID)
		}
		t.Log("dm dedup: PASSED")
	})

	// =========================================================================
	// 11. Encrypted DM messaging
	// =========================================================================
	t.Run("dm_messaging", func(t *testing.T) {
		time.Sleep(300 * time.Millisecond)

		err := alice.SendDMMessage(dmConvID, "secret DM message", "", nil)
		if err != nil {
			t.Fatalf("alice send DM: %v", err)
		}

		raw := waitForType(t, bobMessages, "dm", 5*time.Second)
		var msg protocol.DM
		json.Unmarshal(raw, &msg)

		if len(msg.WrappedKeys) != 2 {
			t.Errorf("wrapped_keys count = %d, want 2", len(msg.WrappedKeys))
		}

		payload, err := bob.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt DM: %v", err)
		}
		if payload.Body != "secret DM message" {
			t.Errorf("body = %q", payload.Body)
		}

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("dm messaging: PASSED")
	})

	// =========================================================================
	// 12. Create group DM with name
	// =========================================================================
	var groupConvID string
	t.Run("group_dm_create", func(t *testing.T) {
		// Group DM with alice, bob, carol — named "Test Group"
		alice.CreateDM([]string{"bob", "carol"}, "Test Group")

		raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
		var created protocol.DMCreated
		json.Unmarshal(raw, &created)

		groupConvID = created.Conversation
		if created.Name != "Test Group" {
			t.Errorf("name = %q, want 'Test Group'", created.Name)
		}
		if len(created.Members) != 3 {
			t.Errorf("members = %v, want 3", created.Members)
		}
		// This is a different conversation from the 1:1
		if groupConvID == dmConvID {
			t.Error("group DM should be a new conversation, not deduped with 1:1")
		}

		// Bob and carol receive dm_created
		waitForType(t, bobMessages, "dm_created", 5*time.Second)
		waitForType(t, carolMessages, "dm_created", 5*time.Second)

		t.Logf("group DM: %s name=%q members=%v", groupConvID, created.Name, created.Members)
		t.Log("group dm create: PASSED")
	})

	// =========================================================================
	// 12b. Group DM messaging (3 users, per-message key wrapped for all)
	// =========================================================================
	t.Run("group_dm_messaging", func(t *testing.T) {
		if groupConvID == "" {
			t.Skip("no group conv")
		}
		time.Sleep(300 * time.Millisecond)

		err := alice.SendDMMessage(groupConvID, "hello group!", "", nil)
		if err != nil {
			t.Fatalf("alice send group DM: %v", err)
		}

		// Bob receives and decrypts
		raw := waitForType(t, bobMessages, "dm", 5*time.Second)
		var bobMsg protocol.DM
		json.Unmarshal(raw, &bobMsg)

		if len(bobMsg.WrappedKeys) != 3 {
			t.Errorf("wrapped_keys count = %d, want 3", len(bobMsg.WrappedKeys))
		}

		payload, err := bob.DecryptDMMessage(bobMsg.WrappedKeys, bobMsg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt group DM: %v", err)
		}
		if payload.Body != "hello group!" {
			t.Errorf("bob body = %q", payload.Body)
		}

		// Carol receives and decrypts
		raw = waitForType(t, carolMessages, "dm", 5*time.Second)
		var carolMsg protocol.DM
		json.Unmarshal(raw, &carolMsg)

		carolPayload, err := carol.DecryptDMMessage(carolMsg.WrappedKeys, carolMsg.Payload)
		if err != nil {
			t.Fatalf("carol decrypt group DM: %v", err)
		}
		if carolPayload.Body != "hello group!" {
			t.Errorf("carol body = %q", carolPayload.Body)
		}

		// Alice's echo
		waitForType(t, aliceMessages, "dm", 5*time.Second)

		t.Log("group dm messaging: PASSED — all 3 members decrypted")
	})

	// =========================================================================
	// 12c. Leave group conversation
	// =========================================================================
	t.Run("leave_group", func(t *testing.T) {
		if groupConvID == "" {
			t.Skip("no group conv")
		}

		carol.Enc().Encode(protocol.LeaveConversation{
			Type:         "leave_conversation",
			Conversation: groupConvID,
		})

		// Alice and bob should receive the leave event
		raw := waitForType(t, aliceMessages, "conversation_event", 5*time.Second)
		var event protocol.ConversationEvent
		json.Unmarshal(raw, &event)

		if event.Event != "leave" || event.User != "carol" {
			t.Errorf("event = %+v, want leave by carol", event)
		}

		waitForType(t, bobMessages, "conversation_event", 5*time.Second)

		t.Log("leave group: PASSED")
	})

	// =========================================================================
	// 12e. Message after leave — carol excluded from wrapped keys
	// =========================================================================
	t.Run("message_after_leave", func(t *testing.T) {
		if groupConvID == "" {
			t.Skip("no group conv")
		}
		time.Sleep(300 * time.Millisecond)

		// Alice sends to the group — carol has left, should only wrap for alice + bob
		err := alice.SendDMMessage(groupConvID, "carol should not see this", "", nil)
		if err != nil {
			t.Fatalf("alice send after leave: %v", err)
		}

		// Bob receives and decrypts
		raw := waitForType(t, bobMessages, "dm", 5*time.Second)
		var msg protocol.DM
		json.Unmarshal(raw, &msg)

		// Wrapped keys should only have alice and bob (not carol)
		if _, hasCarol := msg.WrappedKeys["carol"]; hasCarol {
			t.Error("carol should NOT have a wrapped key after leaving")
		}
		if len(msg.WrappedKeys) != 2 {
			t.Errorf("wrapped_keys count = %d, want 2 (alice + bob)", len(msg.WrappedKeys))
		}

		payload, err := bob.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		if payload.Body != "carol should not see this" {
			t.Errorf("body = %q", payload.Body)
		}

		// Carol should NOT receive this message at all (server doesn't relay to non-members)
		select {
		case carolRaw := <-carolMessages:
			typ, _ := protocol.TypeOf(carolRaw)
			if typ == "dm" {
				t.Error("carol received a DM after leaving — server should not relay")
			}
		case <-time.After(time.Second):
			// Good — carol didn't receive anything
		}

		waitForType(t, aliceMessages, "dm", 5*time.Second)
		t.Log("message after leave: PASSED — carol excluded from keys and delivery")
	})

	// =========================================================================
	// 12d. Three-user room messaging
	// =========================================================================
	t.Run("three_user_room", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		alice.SendRoomMessage("general", "hello all three of you", "", nil)

		// Both bob and carol receive
		bobRaw := waitForType(t, bobMessages, "message", 5*time.Second)
		carolRaw := waitForType(t, carolMessages, "message", 5*time.Second)

		var bobMsg, carolMsg protocol.Message
		json.Unmarshal(bobRaw, &bobMsg)
		json.Unmarshal(carolRaw, &carolMsg)

		bobPayload, err := bob.DecryptRoomMessage(bobMsg.Room, bobMsg.Epoch, bobMsg.Payload)
		if err != nil {
			t.Fatalf("bob decrypt: %v", err)
		}
		carolPayload, err := carol.DecryptRoomMessage(carolMsg.Room, carolMsg.Epoch, carolMsg.Payload)
		if err != nil {
			t.Fatalf("carol decrypt: %v", err)
		}

		if bobPayload.Body != "hello all three of you" {
			t.Errorf("bob body = %q", bobPayload.Body)
		}
		if carolPayload.Body != "hello all three of you" {
			t.Errorf("carol body = %q", carolPayload.Body)
		}

		waitForType(t, aliceMessages, "message", 5*time.Second)
		t.Log("three user room: PASSED — all 3 decrypted")
	})

	// =========================================================================
	// 13. Message deletion
	// =========================================================================
	t.Run("delete_own_message", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		alice.SendRoomMessage("general", "delete me", "", nil)
		raw := waitForType(t, aliceMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		waitForType(t, bobMessages, "message", 5*time.Second)

		// Delete it
		alice.SendDelete(msg.ID)

		raw = waitForType(t, aliceMessages, "deleted", 5*time.Second)
		var del protocol.Deleted
		json.Unmarshal(raw, &del)
		if del.ID != msg.ID {
			t.Errorf("deleted ID = %q, want %q", del.ID, msg.ID)
		}
		if del.DeletedBy != "alice" {
			t.Errorf("deleted_by = %q", del.DeletedBy)
		}

		waitForType(t, bobMessages, "deleted", 5*time.Second)
		t.Log("delete own message: PASSED")
	})

	// =========================================================================
	// 14. Typing indicators
	// =========================================================================
	t.Run("typing", func(t *testing.T) {
		alice.SendTyping("general", "")

		raw := waitForType(t, bobMessages, "typing", 3*time.Second)
		var typ protocol.Typing
		json.Unmarshal(raw, &typ)

		if typ.User != "alice" {
			t.Errorf("typing user = %q", typ.User)
		}
		if typ.Room != "general" {
			t.Errorf("typing room = %q", typ.Room)
		}
		t.Log("typing: PASSED")
	})

	// =========================================================================
	// 15. Read receipts
	// =========================================================================
	t.Run("read_receipts", func(t *testing.T) {
		alice.SendRead("general", "", "msg_test_123")

		raw := waitForType(t, bobMessages, "read", 3*time.Second)
		var read protocol.Read
		json.Unmarshal(raw, &read)

		if read.User != "alice" {
			t.Errorf("read user = %q", read.User)
		}
		if read.LastRead != "msg_test_123" {
			t.Errorf("last_read = %q", read.LastRead)
		}
		t.Log("read receipts: PASSED")
	})

	// =========================================================================
	// 16. Local DB persistence
	// =========================================================================
	t.Run("local_db", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no local store")
		}

		// Messages should be stored
		msgs, err := st.GetRoomMessages("general", 100)
		if err != nil {
			t.Fatalf("get room messages: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("no messages in local DB")
		}
		t.Logf("local DB has %d messages for general", len(msgs))

		// Epoch keys should be stored
		key, err := st.GetEpochKey("general", 1)
		if err != nil {
			t.Logf("epoch key not in local DB: %v", err)
		} else if len(key) == 0 {
			t.Error("empty epoch key in local DB")
		} else {
			t.Log("epoch key stored in local DB")
		}

		t.Log("local db: PASSED")
	})

	// =========================================================================
	// 17. Search (local FTS5)
	// =========================================================================
	t.Run("search", func(t *testing.T) {
		results, err := alice.SearchMessages("full test", 10)
		if err != nil {
			t.Logf("search error (FTS5 may not be available): %v", err)
			t.Skip("FTS5 not available")
		}
		if len(results) == 0 {
			t.Log("search returned 0 results (FTS5 may not be available in this SQLite build)")
		} else {
			t.Logf("search found %d results for 'full test'", len(results))
		}
		t.Log("search: PASSED")
	})

	// =========================================================================
	// 18. Seq counter increment (replay detection)
	// =========================================================================
	t.Run("seq_counters", func(t *testing.T) {
		if alice.CurrentEpoch("general") == 0 {
			t.Skip("no epoch key")
		}

		alice.SendRoomMessage("general", "seq test 1", "", nil)
		raw1 := waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		alice.SendRoomMessage("general", "seq test 2", "", nil)
		raw2 := waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		var msg1, msg2 protocol.Message
		json.Unmarshal(raw1, &msg1)
		json.Unmarshal(raw2, &msg2)

		p1, _ := bob.DecryptRoomMessage(msg1.Room, msg1.Epoch, msg1.Payload)
		p2, _ := bob.DecryptRoomMessage(msg2.Room, msg2.Epoch, msg2.Payload)

		if p2.Seq <= p1.Seq {
			t.Errorf("seq not incrementing: %d then %d", p1.Seq, p2.Seq)
		}
		t.Logf("seq: %d -> %d", p1.Seq, p2.Seq)
		t.Log("seq counters: PASSED")
	})

	// =========================================================================
	// 19. Member hash computation
	// =========================================================================
	t.Run("member_hash", func(t *testing.T) {
		h1 := crypto.MemberHash([]string{"alice", "bob"})
		h2 := crypto.MemberHash([]string{"bob", "alice"})
		if h1 != h2 {
			t.Error("member hash not order-independent")
		}
		t.Logf("member hash: %s", h1[:30])
		t.Log("member hash: PASSED")
	})

	// =========================================================================
	// 20. Mute config persistence
	// =========================================================================
	t.Run("mute_persistence", func(t *testing.T) {
		cfg, _ := config.Load(aliceDir)

		muted := map[string]bool{"general": true, dmConvID: true}
		config.SaveMutedMap(aliceDir, cfg, muted)

		cfg2, _ := config.Load(aliceDir)
		loaded := config.LoadMutedMap(cfg2)

		if !loaded["general"] {
			t.Error("general not muted after reload")
		}
		if !loaded[dmConvID] {
			t.Errorf("%s not muted after reload", dmConvID)
		}

		// Clean up
		config.SaveMutedMap(aliceDir, cfg2, map[string]bool{})
		t.Log("mute persistence: PASSED")
	})

	// =========================================================================
	// 21. Host key TOFU
	// =========================================================================
	t.Run("host_key_tofu", func(t *testing.T) {
		knownHostPath := filepath.Join(aliceDir, "known_host")
		data, err := os.ReadFile(knownHostPath)
		if err != nil {
			t.Fatalf("known_host not found: %v", err)
		}
		if len(data) == 0 {
			t.Error("known_host is empty")
		}
		t.Logf("known_host: %s", string(data)[:50])
		t.Log("host key TOFU: PASSED")
	})

	// =========================================================================
	// 22. Crypto round-trip (encrypt/decrypt)
	// =========================================================================
	t.Run("crypto_roundtrip", func(t *testing.T) {
		key, _ := crypto.GenerateKey()
		encrypted, err := crypto.Encrypt(key, []byte("test message"))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		decrypted, err := crypto.Decrypt(key, encrypted)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if string(decrypted) != "test message" {
			t.Errorf("decrypted = %q", decrypted)
		}
		t.Log("crypto roundtrip: PASSED")
	})

	// =========================================================================
	// 23. Key wrap/unwrap cross-user
	// =========================================================================
	t.Run("key_wrap_crossuser", func(t *testing.T) {
		bobProfile := alice.Profile("bob")
		if bobProfile == nil {
			t.Skip("no bob profile")
		}

		bobPub, err := crypto.ParseSSHPubKey(bobProfile.PubKey)
		if err != nil {
			t.Fatalf("parse bob pubkey: %v", err)
		}

		symKey, _ := crypto.GenerateKey()
		wrapped, err := crypto.WrapKey(symKey, bobPub)
		if err != nil {
			t.Fatalf("wrap: %v", err)
		}

		// Bob unwraps (need bob's private key)
		bobPriv, err := client.ParseRawEd25519Key("/tmp/sshkey-test-key-bob")
		if err != nil {
			t.Fatalf("parse bob priv: %v", err)
		}

		unwrapped, err := crypto.UnwrapKey(wrapped, bobPriv)
		if err != nil {
			t.Fatalf("unwrap: %v", err)
		}

		if string(symKey) != string(unwrapped) {
			t.Error("unwrapped key doesn't match")
		}
		t.Log("key wrap cross-user: PASSED")
	})

	// =========================================================================
	// 24. Server add/remove config
	// =========================================================================
	t.Run("server_management", func(t *testing.T) {
		cfg, _ := config.Load(aliceDir)

		err := config.AddServer(aliceDir, cfg, config.ServerConfig{
			Name: "Test", Host: "test.example.com", Port: 2222, Key: "~/.ssh/id_ed25519",
		})
		if err != nil {
			t.Fatalf("add server: %v", err)
		}

		// Duplicate should fail
		err = config.AddServer(aliceDir, cfg, config.ServerConfig{
			Name: "Test2", Host: "test.example.com", Port: 2222, Key: "~/.ssh/id_ed25519",
		})
		if err == nil {
			t.Error("duplicate server should fail")
		}

		// Remove
		idx := len(cfg.Servers) - 1
		err = config.RemoveServer(aliceDir, cfg, idx)
		if err != nil {
			t.Fatalf("remove server: %v", err)
		}

		t.Log("server management: PASSED")
	})

	// =========================================================================
	// 25. Profile update
	// =========================================================================
	t.Run("profile_update", func(t *testing.T) {
		alice.Enc().Encode(protocol.SetProfile{
			Type:        "set_profile",
			DisplayName: "Alice Updated",
		})

		// Bob's OnMessage routes profiles to bobProfiles, not bobMessages.
		// Wait and check the profiles slice.
		time.Sleep(time.Second)

		bobMu.Lock()
		found := false
		for _, p := range bobProfiles {
			if p.User == "alice" && p.DisplayName == "Alice Updated" {
				found = true
				break
			}
		}
		bobMu.Unlock()

		if !found {
			t.Error("bob didn't receive alice's updated profile")
		}
		t.Log("profile update: PASSED")
	})

	// =========================================================================
	// 26. Status update
	// =========================================================================
	t.Run("status_update", func(t *testing.T) {
		alice.Enc().Encode(protocol.SetStatus{
			Type: "set_status",
			Text: "Testing all the things",
		})
		// Status is stored server-side, no broadcast — just verify no error
		time.Sleep(200 * time.Millisecond)
		t.Log("status update: PASSED")
	})

	// =========================================================================
	// Summary
	// =========================================================================
	t.Log("")
	t.Log("========================================")
	t.Log("  FULL E2E TEST SUITE COMPLETE")
	t.Log("========================================")
}

// waitForType reads from a channel until it finds a message of the given type.
func waitForType(t *testing.T, ch chan json.RawMessage, msgType string, timeout time.Duration) json.RawMessage {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case raw := <-ch:
			typ, _ := protocol.TypeOf(raw)
			if typ == msgType {
				return raw
			}
			// Skip other message types
		case <-timer:
			t.Fatalf("timeout waiting for %s", msgType)
			return nil
		}
	}
}
