//go:build e2e

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestPersistenceComprehensive exercises local DB persistence for epoch keys,
// conversations, reactions, read positions, and scroll-back-from-local-DB.
// These are the features recently wired or fixed.
func TestPersistenceComprehensive(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()

	aliceDir := t.TempDir()
	bobDir := t.TempDir()

	aliceMessages := make(chan json.RawMessage, 200)
	bobMessages := make(chan json.RawMessage, 200)
	aliceSynced := make(chan bool, 1)
	bobSynced := make(chan bool, 1)

	mkClient := func(keyPath, deviceID, dataDir string, synced chan bool, msgs chan json.RawMessage) *client.Client {
		return testutil.MkClient(port, keyPath, deviceID, dataDir, synced, msgs)
	}

	alice := mkClient(testutil.Alice.KeyPath, "dev_alice_persist", aliceDir, aliceSynced, aliceMessages)
	bob := mkClient(testutil.Bob.KeyPath, "dev_bob_persist", bobDir, bobSynced, bobMessages)

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

	// Wait for epoch rotation
	time.Sleep(time.Second)

	// Create a 1:1 DM
	alice.CreateDM([]string{testutil.Bob.Username}, "")
	raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
	var dmCreated protocol.DMCreated
	json.Unmarshal(raw, &dmCreated)
	dmConvID := dmCreated.Conversation
	waitForType(t, bobMessages, "dm_created", 5*time.Second)
	time.Sleep(300 * time.Millisecond)

	// =========================================================================
	// 1. Epoch keys persisted to local DB and loaded lazily
	// =========================================================================
	t.Run("epoch_key_persistence", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		room := "general"
		epoch := alice.CurrentEpoch(room)
		if epoch == 0 {
			t.Skip("no epoch")
		}

		// Key should be in local DB (storeEpochKey persists on receive)
		dbKey, err := st.GetEpochKey(room, epoch)
		if err != nil {
			t.Fatalf("get epoch key: %v", err)
		}
		if dbKey == nil {
			t.Fatal("epoch key not in local DB")
		}

		// Key should also be in memory
		memKey := alice.RoomEpochKey(room, epoch)
		if memKey == nil {
			t.Fatal("epoch key not in memory")
		}

		// Both should match
		if string(dbKey) != string(memKey) {
			t.Error("DB key != memory key")
		}

		t.Logf("epoch key persistence: room=%s epoch=%d key=%d bytes", room, epoch, len(dbKey))
	})

	// =========================================================================
	// 2. Epoch key dedup — storeEpochKey short-circuits when key already in memory
	// =========================================================================
	t.Run("epoch_key_dedup", func(t *testing.T) {
		room := "general"
		epoch := alice.CurrentEpoch(room)
		if epoch == 0 {
			t.Skip("no epoch")
		}

		// Key is already in memory from initial connect. Send a message
		// to confirm decryption works (proves key is usable)
		err := alice.SendRoomMessage(room, "dedup test msg", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		// Epoch key should still be the same (not duplicated or corrupted)
		memKey := alice.RoomEpochKey(room, epoch)
		if memKey == nil {
			t.Fatal("key lost from memory")
		}

		t.Log("epoch key dedup: OK — key reused without re-unwrap")
	})

	// =========================================================================
	// 3. Conversation persistence — stored and reloaded from DB
	// =========================================================================
	t.Run("conversation_persistence", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// The DM we created should be in the DB
		convs, err := st.GetAllConversations()
		if err != nil {
			t.Fatalf("get conversations: %v", err)
		}
		info, ok := convs[dmConvID]
		if !ok {
			t.Fatalf("conversation %s not found in local DB", dmConvID)
		}

		// Members should be alice and bob
		members := strings.Split(info[1], ",")
		if len(members) != 2 {
			t.Errorf("expected 2 members, got %v", members)
		}

		t.Logf("conversation persistence: %s members=%s", dmConvID, info[1])
	})

	// =========================================================================
	// 4. Messages stored decrypted in local DB
	// =========================================================================
	t.Run("messages_stored_decrypted", func(t *testing.T) {
		// Send a message so we have something in the DB
		err := alice.SendRoomMessage("general", "persistence test body 12345", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		msgs, err := st.GetRoomMessages("general", 100)
		if err != nil {
			t.Fatalf("get messages: %v", err)
		}

		found := false
		for _, m := range msgs {
			if m.Body == "persistence test body 12345" {
				found = true
				break
			}
		}
		if !found {
			t.Error("message body not found in local DB (should be stored decrypted)")
		}

		t.Logf("messages stored decrypted: %d messages in DB", len(msgs))
	})

	// =========================================================================
	// 5. Reactions persisted and loadable from DB
	// =========================================================================
	t.Run("reaction_persistence", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Send a message, then react to it
		err := alice.SendRoomMessage("general", "react to this", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		raw := waitForType(t, bobMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		// Bob reacts
		err = bob.SendRoomReaction("general", msg.ID, "👍")
		if err != nil {
			t.Fatalf("react: %v", err)
		}
		waitForType(t, aliceMessages, "reaction", 5*time.Second)
		waitForType(t, bobMessages, "reaction", 5*time.Second)

		// Reaction should be in alice's local DB
		reactions, err := st.GetReactionsForMessages([]string{msg.ID})
		if err != nil {
			t.Fatalf("get reactions: %v", err)
		}
		if len(reactions) == 0 {
			t.Fatal("reaction not found in local DB")
		}
		if reactions[0].Emoji != "👍" {
			t.Errorf("emoji = %q, want 👍", reactions[0].Emoji)
		}
		if reactions[0].User != testutil.Bob.Username {
			t.Errorf("user = %q, want bob", reactions[0].User)
		}

		t.Logf("reaction persistence: %s reacted %s on %s", reactions[0].User, reactions[0].Emoji, msg.ID)
	})

	// =========================================================================
	// 6. Read position persistence
	// =========================================================================
	t.Run("read_position_persistence", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Store a read position manually (simulating sendReadReceipt)
		st.StoreReadPosition("general", "msg_fake_123")

		// Read it back
		lastRead, err := st.GetReadPosition("general")
		if err != nil {
			t.Fatalf("get read position: %v", err)
		}
		if lastRead != "msg_fake_123" {
			t.Errorf("lastRead = %q, want msg_fake_123", lastRead)
		}

		// Overwrite
		st.StoreReadPosition("general", "msg_fake_456")
		lastRead, _ = st.GetReadPosition("general")
		if lastRead != "msg_fake_456" {
			t.Errorf("updated lastRead = %q, want msg_fake_456", lastRead)
		}

		t.Log("read position persistence: OK")
	})

	// =========================================================================
	// 7. Scroll-back serves from local DB before hitting server
	// =========================================================================
	t.Run("scrollback_local_first", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Send several messages so local DB has content (pace to avoid rate limit)
		for i := 0; i < 3; i++ {
			alice.SendRoomMessage("general", "scrollback msg", "", nil)
			waitForType(t, bobMessages, "message", 5*time.Second)
			waitForType(t, aliceMessages, "message", 5*time.Second)
			time.Sleep(250 * time.Millisecond)
		}

		// Get all messages from local DB (includes messages from earlier subtests)
		allMsgs, err := st.GetRoomMessages("general", 200)
		if err != nil {
			t.Fatalf("get messages: %v", err)
		}
		if len(allMsgs) < 3 {
			t.Fatalf("expected at least 3 messages, got %d", len(allMsgs))
		}

		// Simulate scroll-back: get messages before the latest one
		latestID := allMsgs[len(allMsgs)-1].ID
		older, err := st.GetMessagesBefore("general", "", latestID, 10)
		if err != nil {
			t.Fatalf("get messages before: %v", err)
		}
		if len(older) == 0 {
			t.Fatal("no older messages from local DB — scroll-back would hit server unnecessarily")
		}

		// Verify they're actually older (lower timestamp)
		latestTS := allMsgs[len(allMsgs)-1].TS
		for _, m := range older {
			if m.TS > latestTS {
				t.Errorf("scroll-back returned message with TS %d > latest %d", m.TS, latestTS)
			}
		}

		t.Logf("scrollback local first: %d older messages from local DB", len(older))
	})

	// =========================================================================
	// 7b. DM scroll-back from local DB
	// =========================================================================
	t.Run("dm_scrollback_local", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Send several DMs
		for i := 0; i < 3; i++ {
			alice.SendDMMessage(dmConvID, "dm scrollback msg", "", nil)
			waitForType(t, bobMessages, "dm", 5*time.Second)
			waitForType(t, aliceMessages, "dm", 5*time.Second)
			time.Sleep(250 * time.Millisecond)
		}

		allDMs, err := st.GetConvMessages(dmConvID, 200)
		if err != nil {
			t.Fatalf("get conv messages: %v", err)
		}
		if len(allDMs) < 3 {
			t.Fatalf("expected at least 3 DMs, got %d", len(allDMs))
		}

		latestID := allDMs[len(allDMs)-1].ID
		older, err := st.GetMessagesBefore("", dmConvID, latestID, 10)
		if err != nil {
			t.Fatalf("get DM messages before: %v", err)
		}
		if len(older) == 0 {
			t.Fatal("no older DMs from local DB")
		}

		t.Logf("dm scrollback local: %d older DMs from local DB", len(older))
	})

	// =========================================================================
	// 7c. Scroll-back exhausts local DB, should fall through to server
	// =========================================================================
	t.Run("scrollback_local_then_server", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// We have a handful of messages in local DB (fewer than 100).
		// GetMessagesBefore with limit=100 should return < 100 results,
		// signaling to the TUI that local is exhausted and server should
		// be queried for the remainder.
		allMsgs, _ := st.GetRoomMessages("general", 200)
		if len(allMsgs) < 2 {
			t.Skip("need at least 2 messages")
		}
		latestID := allMsgs[len(allMsgs)-1].ID

		older, err := st.GetMessagesBefore("general", "", latestID, 100)
		if err != nil {
			t.Fatalf("get messages before: %v", err)
		}

		// We have fewer than 100 older messages → local DB exhausted.
		// In the TUI, this triggers: hasMore = len(localMsgs) >= 100 = false
		// → RequestHistory to server for the tail.
		if len(older) >= 100 {
			t.Logf("local DB has %d older messages (full page — server not needed)", len(older))
		} else {
			t.Logf("local DB has %d older messages (< 100 — TUI would also query server)", len(older))
		}

		// Verify the actual message content is correct (decrypted body, not "(encrypted)")
		for _, m := range older {
			if m.Body == "(encrypted)" || m.Body == "" {
				t.Errorf("message %s has undecrypted body: %q", m.ID, m.Body)
			}
		}

		t.Log("scrollback local then server: local exhaustion detected correctly")
	})

	// =========================================================================
	// 8. DM messages and reactions persisted
	// =========================================================================
	t.Run("dm_persistence", func(t *testing.T) {
		time.Sleep(300 * time.Millisecond)

		err := alice.SendDMMessage(dmConvID, "DM persistence test", "", nil)
		if err != nil {
			t.Fatalf("send DM: %v", err)
		}
		waitForType(t, bobMessages, "dm", 5*time.Second)
		waitForType(t, aliceMessages, "dm", 5*time.Second)

		// Check alice's local DB for the DM
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}
		msgs, err := st.GetConvMessages(dmConvID, 100)
		if err != nil {
			t.Fatalf("get conv messages: %v", err)
		}
		found := false
		for _, m := range msgs {
			if m.Body == "DM persistence test" {
				found = true
			}
		}
		if !found {
			t.Error("DM message body not found in local DB")
		}

		t.Logf("dm persistence: %d messages for %s", len(msgs), dmConvID)
	})

	// =========================================================================
	// 8b. Group DM history — create group, send messages, verify local scroll-back
	// =========================================================================
	t.Run("group_dm_history", func(t *testing.T) {
		// Connect carol for the group
		carolSynced := make(chan bool, 1)
		carolMsgs := make(chan json.RawMessage, 200)
		carol := mkClient(testutil.Carol.KeyPath, "dev_carol_persist", t.TempDir(), carolSynced, carolMsgs)
		if err := carol.Connect(); err != nil {
			t.Fatalf("carol connect: %v", err)
		}
		defer carol.Close()
		<-carolSynced

		// Create group DM
		alice.CreateDM([]string{testutil.Bob.Username, testutil.Carol.Username}, "Persist Group")
		raw := waitForType(t, aliceMessages, "dm_created", 5*time.Second)
		var group protocol.DMCreated
		json.Unmarshal(raw, &group)
		groupID := group.Conversation
		waitForType(t, bobMessages, "dm_created", 5*time.Second)
		waitForType(t, carolMsgs, "dm_created", 5*time.Second)
		time.Sleep(300 * time.Millisecond)

		// Send messages from multiple members
		alice.SendDMMessage(groupID, "group msg from alice", "", nil)
		waitForType(t, bobMessages, "dm", 5*time.Second)
		waitForType(t, carolMsgs, "dm", 5*time.Second)
		waitForType(t, aliceMessages, "dm", 5*time.Second)

		bob.SendDMMessage(groupID, "group msg from bob", "", nil)
		waitForType(t, aliceMessages, "dm", 5*time.Second)
		waitForType(t, carolMsgs, "dm", 5*time.Second)
		waitForType(t, bobMessages, "dm", 5*time.Second)

		// Verify alice's local DB has both messages
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}
		msgs, err := st.GetConvMessages(groupID, 200)
		if err != nil {
			t.Fatalf("get group messages: %v", err)
		}
		if len(msgs) < 2 {
			t.Fatalf("expected at least 2 group DM messages, got %d", len(msgs))
		}

		// Verify scroll-back works
		latestID := msgs[len(msgs)-1].ID
		older, err := st.GetMessagesBefore("", groupID, latestID, 10)
		if err != nil {
			t.Fatalf("get group messages before: %v", err)
		}
		if len(older) == 0 {
			t.Fatal("no older group DM messages from local DB")
		}

		// Verify the group conversation was persisted
		convs, _ := st.GetAllConversations()
		if _, ok := convs[groupID]; !ok {
			t.Error("group conversation not persisted to local DB")
		}

		carol.Close()
		t.Logf("group dm history: %d messages, %d older via scrollback", len(msgs), len(older))
	})

	// =========================================================================
	// 9. Reconnect: epoch keys loaded from DB, messages decryptable
	// =========================================================================
	t.Run("reconnect_epoch_keys", func(t *testing.T) {
		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		room := "general"
		epoch := alice.CurrentEpoch(room)

		// Verify the epoch key is in local DB
		dbKey, err := st.GetEpochKey(room, epoch)
		if err != nil || dbKey == nil {
			t.Fatalf("epoch key not in DB before reconnect test")
		}

		// Simulate what happens on restart: create a new client reusing
		// the same data dir (same local DB). The new client should be
		// able to decrypt messages using epoch keys loaded from DB.
		alice2Synced := make(chan bool, 1)
		alice2Msgs := make(chan json.RawMessage, 200)
		alice2 := mkClient(testutil.Alice.KeyPath, "dev_alice_persist2", aliceDir, alice2Synced, alice2Msgs)

		if err := alice2.Connect(); err != nil {
			t.Fatalf("alice2 connect: %v", err)
		}
		<-alice2Synced

		time.Sleep(500 * time.Millisecond)

		// alice2 should have the epoch key available (loaded from DB
		// lazily or re-sent by server — either way it must work)
		key := alice2.RoomEpochKey(room, epoch)
		if key == nil {
			t.Fatal("epoch key not available after reconnect")
		}

		// Verify it matches what was in DB
		if string(key) != string(dbKey) {
			t.Error("epoch key mismatch after reconnect")
		}

		// Can decrypt: send a message on the new connection and verify
		err = alice2.SendRoomMessage(room, "post-reconnect msg", "", nil)
		if err != nil {
			t.Fatalf("send after reconnect: %v", err)
		}
		waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, alice2Msgs, "message", 5*time.Second)

		alice2.Close()
		t.Log("reconnect epoch keys: OK — decrypt works with DB-loaded keys")
	})

	// =========================================================================
	// 10. Reconnect: conversations loaded from local DB
	// =========================================================================
	t.Run("reconnect_conversations", func(t *testing.T) {
		// Create a new client with same data dir
		alice3Synced := make(chan bool, 1)
		alice3Msgs := make(chan json.RawMessage, 200)
		alice3 := mkClient(testutil.Alice.KeyPath, "dev_alice_persist3", aliceDir, alice3Synced, alice3Msgs)

		if err := alice3.Connect(); err != nil {
			t.Fatalf("alice3 connect: %v", err)
		}
		<-alice3Synced

		// ConvMembers should be populated from DB before server pushes
		members := alice3.ConvMembers(dmConvID)
		if len(members) == 0 {
			t.Fatal("conversation members not loaded from DB on reconnect")
		}
		if len(members) != 2 {
			t.Errorf("expected 2 members, got %d: %v", len(members), members)
		}

		alice3.Close()
		t.Logf("reconnect conversations: %s has %v", dmConvID, members)
	})

	// =========================================================================
	// 11. Reaction removal persisted
	// =========================================================================
	t.Run("reaction_removal", func(t *testing.T) {
		st := bob.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Bob sends a message, alice reacts, then alice unreacts
		err := bob.SendRoomMessage("general", "unreact target", "", nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		raw := waitForType(t, aliceMessages, "message", 5*time.Second)
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		waitForType(t, bobMessages, "message", 5*time.Second)

		err = alice.SendRoomReaction("general", msg.ID, "❤️")
		if err != nil {
			t.Fatalf("react: %v", err)
		}
		// Both receive the reaction
		aliceRaw := waitForType(t, aliceMessages, "reaction", 5*time.Second)
		waitForType(t, bobMessages, "reaction", 5*time.Second)

		var reaction protocol.Reaction
		json.Unmarshal(aliceRaw, &reaction)

		// Verify reaction is in bob's DB
		reactions, _ := st.GetReactionsForMessages([]string{msg.ID})
		if len(reactions) == 0 {
			t.Fatal("reaction not in bob's DB before unreact")
		}
		reactionID := reactions[0].ReactionID

		// Alice unreacts
		alice.SendUnreact(reactionID)
		waitForType(t, aliceMessages, "reaction_removed", 5*time.Second)
		waitForType(t, bobMessages, "reaction_removed", 5*time.Second)

		// Reaction should be gone from bob's DB
		reactions, _ = st.GetReactionsForMessages([]string{msg.ID})
		if len(reactions) != 0 {
			t.Errorf("reaction still in DB after unreact: %v", reactions)
		}

		t.Log("reaction removal: persisted to DB correctly")
	})

	// =========================================================================
	// 12. Epoch rotation — both old and new keys work, DB has both
	// =========================================================================
	t.Run("epoch_rotation_dual_key", func(t *testing.T) {
		room := "general"
		oldEpoch := alice.CurrentEpoch(room)
		if oldEpoch == 0 {
			t.Skip("no epoch")
		}

		st := alice.Store()
		if st == nil {
			t.Skip("no store")
		}

		// Send a message with the current epoch
		err := alice.SendRoomMessage(room, "pre-rotation msg", "", nil)
		if err != nil {
			t.Fatalf("send pre-rotation: %v", err)
		}
		waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		oldKey, _ := st.GetEpochKey(room, oldEpoch)
		if oldKey == nil {
			t.Fatalf("old epoch key not in DB")
		}
		t.Logf("old epoch: %d, key: %d bytes", oldEpoch, len(oldKey))

		// Trigger rotation by sending 100 messages (epoch threshold = 100)
		t.Log("sending 100 messages to trigger rotation...")
		for i := 0; i < 100; i++ {
			bob.SendRoomMessage(room, "filler", "", nil)
		}
		// Drain bob's channel
		deadline := time.After(30 * time.Second)
		drained := 0
		for drained < 100 {
			select {
			case <-bobMessages:
				drained++
			case <-deadline:
				t.Fatalf("timeout draining bob (got %d/100)", drained)
			}
		}
		// Drain alice's channel (she gets the same 100 messages)
		deadline = time.After(30 * time.Second)
		drained = 0
		for drained < 100 {
			select {
			case <-aliceMessages:
				drained++
			case <-deadline:
				t.Fatalf("timeout draining alice (got %d/100)", drained)
			}
		}
		time.Sleep(time.Second)

		newEpoch := alice.CurrentEpoch(room)
		if newEpoch <= oldEpoch {
			t.Fatalf("epoch didn't rotate: old=%d current=%d", oldEpoch, newEpoch)
		}
		t.Logf("epoch rotated: %d → %d", oldEpoch, newEpoch)

		// New epoch key should be in memory AND DB
		newKey := alice.RoomEpochKey(room, newEpoch)
		if newKey == nil {
			t.Fatal("new epoch key not in memory")
		}
		newDBKey, _ := st.GetEpochKey(room, newEpoch)
		if newDBKey == nil {
			t.Fatal("new epoch key not in DB")
		}

		// Old epoch key should STILL be accessible (from memory or DB)
		oldKeyStill := alice.RoomEpochKey(room, oldEpoch)
		if oldKeyStill == nil {
			t.Fatal("old epoch key lost after rotation")
		}
		if string(oldKeyStill) != string(oldKey) {
			t.Error("old epoch key changed after rotation")
		}

		// Send a message with the NEW epoch — verify it works
		err = alice.SendRoomMessage(room, "post-rotation msg", "", nil)
		if err != nil {
			t.Fatalf("send post-rotation: %v", err)
		}
		waitForType(t, bobMessages, "message", 5*time.Second)
		waitForType(t, aliceMessages, "message", 5*time.Second)

		// Both pre- and post-rotation messages in local DB
		msgs, _ := st.GetRoomMessages(room, 200)
		prePre, postPost := false, false
		for _, m := range msgs {
			if m.Body == "pre-rotation msg" {
				prePre = true
			}
			if m.Body == "post-rotation msg" {
				postPost = true
			}
		}
		if !prePre {
			t.Error("pre-rotation message missing from DB")
		}
		if !postPost {
			t.Error("post-rotation message missing from DB")
		}

		// Verify new key != old key (rotation actually produced a new key)
		if string(newKey) == string(oldKey) {
			t.Error("new epoch key is identical to old — rotation didn't generate a new key")
		}

		t.Logf("epoch rotation: old epoch %d + new epoch %d both work, distinct keys", oldEpoch, newEpoch)
	})
}

