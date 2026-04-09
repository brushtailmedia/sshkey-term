package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// openTestStore creates a temp-dir Store for a test. Uses an unencrypted DB
// to keep tests fast and portable.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	s, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// -- Encrypted round-trip --

func TestOpen_EncryptedRoundTrip(t *testing.T) {
	// Verify that an encrypted DB can be created, written, closed, and reopened.
	// This is the exact flow that was failing before the SetMaxOpenConns(1) fix.
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	dbKey, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "encrypted.db")

	// First open: create + insert
	s1, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.InsertMessage(StoredMessage{
		ID: "enc_m1", Sender: "alice", Body: "secret message", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s1.Close()

	// Second open: reopen with same key and read back
	s2, err := Open(path, dbKey)
	if err != nil {
		t.Fatalf("reopen with correct key: %v", err)
	}
	defer s2.Close()

	msgs, err := s2.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "enc_m1" || msgs[0].Body != "secret message" {
		t.Errorf("encrypted round-trip failed: %+v", msgs)
	}
}

func TestOpen_EncryptedWrongKey(t *testing.T) {
	seed1 := []byte("seed-one-for-ed25519-private-key")
	seed2 := []byte("seed-two-for-ed25519-private-key")
	key1, _ := DeriveDBKey(seed1)
	key2, _ := DeriveDBKey(seed2)

	dir := t.TempDir()
	path := filepath.Join(dir, "encrypted.db")

	// Create with key1
	s1, err := Open(path, key1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s1.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "hi", TS: 1, Room: "general"})
	s1.Close()

	// Reopen with key2 — should fail
	_, err = Open(path, key2)
	if err == nil {
		t.Fatal("opening with wrong key should fail")
	}
}

// -- DeriveDBKey --

func TestDeriveDBKey_Deterministic(t *testing.T) {
	seed := []byte("test-seed-32-bytes-for-ed25519key")
	k1, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive 1: %v", err)
	}
	k2, err := DeriveDBKey(seed)
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	if string(k1) != string(k2) {
		t.Error("same seed should produce same key")
	}
	if len(k1) != 32 {
		t.Errorf("key length = %d, want 32", len(k1))
	}
}

func TestDeriveDBKey_DifferentSeeds(t *testing.T) {
	k1, _ := DeriveDBKey([]byte("seed-one-for-ed25519-private-key"))
	k2, _ := DeriveDBKey([]byte("seed-two-for-ed25519-private-key"))
	if string(k1) == string(k2) {
		t.Error("different seeds should produce different keys")
	}
}

// -- Schema --

func TestOpen_SchemaCreated(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		found = append(found, name)
	}

	expected := []string{"messages", "reactions", "epoch_keys", "pinned_keys", "read_positions", "seq_marks", "groups", "direct_messages", "rooms", "state"}
	for _, want := range expected {
		seen := false
		for _, got := range found {
			if got == want {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("missing table %q (found tables: %v)", want, found)
		}
	}
}

func TestOpen_IdempotentReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	s1, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.InsertMessage(StoredMessage{ID: "m1", Sender: "a", Body: "hi", TS: 1, Room: "general"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s1.Close()

	s2, err := OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	msgs, err := s2.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Errorf("persistence broken: %v", msgs)
	}
}

func TestOpen_RejectsNilKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	_, err := Open(path, nil)
	if err == nil {
		t.Fatal("Open should reject nil key")
	}
}

// -- Messages --

func TestMessages_InsertAndRetrieve(t *testing.T) {
	s := openTestStore(t)

	msg := StoredMessage{
		ID:      "msg_001",
		Sender:  "alice",
		Body:    "hello world",
		TS:      1712345678,
		Room:    "general",
		Epoch:   3,
		ReplyTo: "msg_000",
	}
	if err := s.InsertMessage(msg); err != nil {
		t.Fatalf("insert: %v", err)
	}

	msgs, err := s.GetRoomMessages("general", 10)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	got := msgs[0]
	if got.ID != msg.ID || got.Body != msg.Body || got.Sender != msg.Sender {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, msg)
	}
	if got.Epoch != 3 {
		t.Errorf("epoch = %d, want 3", got.Epoch)
	}
	if got.ReplyTo != "msg_000" {
		t.Errorf("reply_to = %q", got.ReplyTo)
	}
}

func TestMessages_RoomFilter(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "a", Sender: "x", Body: "general msg", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "b", Sender: "x", Body: "eng msg", TS: 2, Room: "engineering"})
	s.InsertMessage(StoredMessage{ID: "c", Sender: "x", Body: "general 2", TS: 3, Room: "general"})

	got, _ := s.GetRoomMessages("general", 10)
	if len(got) != 2 {
		t.Errorf("general should have 2 messages, got %d", len(got))
	}
	got, _ = s.GetRoomMessages("engineering", 10)
	if len(got) != 1 {
		t.Errorf("engineering should have 1 message, got %d", len(got))
	}
}

func TestMessages_GroupFilter(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "a", Sender: "x", Body: "grp 1", TS: 1, Group: "group_1"})
	s.InsertMessage(StoredMessage{ID: "b", Sender: "x", Body: "grp 2", TS: 2, Group: "group_2"})

	got, _ := s.GetGroupMessages("group_1", 10)
	if len(got) != 1 || got[0].Body != "grp 1" {
		t.Errorf("group_1 filter failed: %v", got)
	}
}

func TestMessages_DMFilter(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "a", Sender: "x", Body: "dm 1", TS: 1, DM: "dm_1"})
	s.InsertMessage(StoredMessage{ID: "b", Sender: "x", Body: "dm 2", TS: 2, DM: "dm_2"})

	got, _ := s.GetDMMessages("dm_1", 10)
	if len(got) != 1 || got[0].Body != "dm 1" {
		t.Errorf("dm_1 filter failed: %v", got)
	}
}

func TestMessages_LimitRespected(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 20; i++ {
		s.InsertMessage(StoredMessage{ID: string(rune('a' + i)), Sender: "x", Body: "hi", TS: int64(i), Room: "general"})
	}
	got, _ := s.GetRoomMessages("general", 5)
	if len(got) != 5 {
		t.Errorf("limit=5 should return 5 messages, got %d", len(got))
	}
}

func TestMessages_Delete(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "secret", TS: 1, Room: "general"})
	if _, err := s.DeleteMessage("m1", "alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.GetRoomMessages("general", 10)
	if len(got) != 1 {
		t.Fatalf("soft-deleted message should still be in results, got %d", len(got))
	}
	if !got[0].Deleted {
		t.Error("message should be marked as deleted")
	}
	if got[0].DeletedBy != "alice" {
		t.Errorf("deleted_by = %q, want alice", got[0].DeletedBy)
	}
	if got[0].Body != "" {
		t.Errorf("body should be cleared, got %q", got[0].Body)
	}
}

func TestMessages_InsertIsIdempotent(t *testing.T) {
	// InsertMessage uses INSERT OR IGNORE — re-inserting the same message_id
	// is a no-op. This is intentional: messages are immutable and can be
	// re-delivered via sync + real-time push without duplicating.
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "v1", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "v2-should-be-ignored", TS: 2, Room: "general"})
	got, _ := s.GetRoomMessages("general", 10)
	if len(got) != 1 {
		t.Fatalf("should still have 1 message, got %d", len(got))
	}
	if got[0].Body != "v1" {
		t.Errorf("first insert wins (INSERT OR IGNORE), got body = %q", got[0].Body)
	}
}

func TestMessages_GetMessagesBefore(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "x", Body: "first", TS: 100, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "x", Body: "second", TS: 200, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m3", Sender: "x", Body: "third", TS: 300, Room: "general"})

	got, err := s.GetMessagesBefore("general", "", "", "m3", 10)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 messages before m3, got %d", len(got))
	}
}

func TestMessages_Search(t *testing.T) {
	s := openTestStore(t)
	s.InsertMessage(StoredMessage{ID: "m1", Sender: "alice", Body: "the quick brown fox", TS: 1, Room: "general"})
	s.InsertMessage(StoredMessage{ID: "m2", Sender: "bob", Body: "jumps over the lazy dog", TS: 2, Room: "general"})

	got, err := s.SearchMessages("brown", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, m := range got {
		if m.ID == "m1" {
			found = true
		}
	}
	if !found {
		t.Errorf("should find m1, got %v", got)
	}
}

// -- Epoch keys --

func TestEpochKeys_StoreAndRetrieve(t *testing.T) {
	s := openTestStore(t)
	key := []byte("32-byte-symmetric-key-for-test00")

	if err := s.StoreEpochKey("general", 3, key); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := s.GetEpochKey("general", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(key) {
		t.Errorf("key mismatch")
	}
}

func TestEpochKeys_Missing(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetEpochKey("nonexistent", 99)
	if err != nil {
		t.Errorf("missing key should not error, got: %v", err)
	}
	if got != nil {
		t.Errorf("missing key should return nil, got %v", got)
	}
}

func TestEpochKeys_Replace(t *testing.T) {
	s := openTestStore(t)
	s.StoreEpochKey("general", 3, []byte("key-v1-32-bytes-padding-padding-"))
	s.StoreEpochKey("general", 3, []byte("key-v2-32-bytes-padding-padding-"))
	got, _ := s.GetEpochKey("general", 3)
	if !strings.HasPrefix(string(got), "key-v2") {
		t.Errorf("key should be replaced: %q", got)
	}
}

// -- Pinned keys --

func TestPinnedKeys_FirstSeen(t *testing.T) {
	s := openTestStore(t)
	err := s.PinKey("alice", "SHA256:abc", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("pin: %v", err)
	}

	fp, verified, err := s.GetPinnedKey("alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fp != "SHA256:abc" {
		t.Errorf("fingerprint = %q", fp)
	}
	if verified {
		t.Error("new pin should start unverified")
	}
}

func TestPinnedKeys_MarkVerified(t *testing.T) {
	s := openTestStore(t)
	s.PinKey("alice", "SHA256:abc", "ssh-ed25519 AAAA...")

	if err := s.MarkVerified("alice"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	_, verified, _ := s.GetPinnedKey("alice")
	if !verified {
		t.Error("should be marked verified")
	}
}

func TestPinnedKeys_ClearVerifiedOnKeyChange(t *testing.T) {
	s := openTestStore(t)
	s.PinKey("alice", "SHA256:fp1", "ssh-ed25519 AAAA...")
	s.MarkVerified("alice")

	s.PinKey("alice", "SHA256:fp2", "ssh-ed25519 BBBB...")
	fp, verified, _ := s.GetPinnedKey("alice")
	if fp != "SHA256:fp2" {
		t.Errorf("fingerprint not updated: %q", fp)
	}
	if verified {
		t.Error("verified should be cleared when fingerprint changes")
	}
}

func TestPinnedKeys_ClearVerified(t *testing.T) {
	s := openTestStore(t)
	s.PinKey("alice", "SHA256:abc", "ssh-ed25519 AAAA...")
	s.MarkVerified("alice")
	s.ClearVerified("alice")
	_, verified, _ := s.GetPinnedKey("alice")
	if verified {
		t.Error("verified should be cleared")
	}
}

// -- Seq marks --

func TestSeqMarks_StoreAndRetrieve(t *testing.T) {
	s := openTestStore(t)
	if err := s.StoreSeqMark("alice:dev_abc:room:general", 42); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := s.GetSeqMark("alice:dev_abc:room:general")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != 42 {
		t.Errorf("seq = %d, want 42", got)
	}
}

func TestSeqMarks_Missing(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetSeqMark("unknown")
	if err != nil {
		t.Errorf("unknown seq should not error: %v", err)
	}
	if got != 0 {
		t.Errorf("unknown seq should be 0, got %d", got)
	}
}

func TestSeqMarks_Replace(t *testing.T) {
	s := openTestStore(t)
	s.StoreSeqMark("key", 10)
	s.StoreSeqMark("key", 20)
	got, _ := s.GetSeqMark("key")
	if got != 20 {
		t.Errorf("should be replaced: got %d, want 20", got)
	}
}

// -- Read positions --

func TestReadPositions_StoreAndRetrieve(t *testing.T) {
	s := openTestStore(t)
	if err := s.StoreReadPosition("room:general", "msg_100"); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := s.GetReadPosition("room:general")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "msg_100" {
		t.Errorf("got %q, want msg_100", got)
	}
}

func TestReadPositions_Replace(t *testing.T) {
	s := openTestStore(t)
	s.StoreReadPosition("room:general", "msg_1")
	s.StoreReadPosition("room:general", "msg_50")
	got, _ := s.GetReadPosition("room:general")
	if got != "msg_50" {
		t.Errorf("should update, got %q", got)
	}
}

// -- Groups --

func TestGroups_StoreAndReplace(t *testing.T) {
	s := openTestStore(t)
	if err := s.StoreGroup("group_1", "Project", "alice,bob"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := s.StoreGroup("group_1", "Project Alpha", "alice,bob,carol"); err != nil {
		t.Fatalf("replace: %v", err)
	}
}

// -- DMs --

func TestDMs_StoreAndReplace(t *testing.T) {
	s := openTestStore(t)
	if err := s.StoreDM("dm_1", "alice", "bob"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := s.StoreDM("dm_1", "alice", "bob"); err != nil {
		t.Fatalf("replace: %v", err)
	}
}

// -- State kv --

func TestState_SetAndGet(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetState("last_synced", "2026-04-05T12:00:00Z"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.GetState("last_synced")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "2026-04-05T12:00:00Z" {
		t.Errorf("got %q", got)
	}
}

func TestState_MissingKey(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetState("never_set")
	if err != nil {
		t.Errorf("missing key should not error: %v", err)
	}
	if got != "" {
		t.Errorf("missing key should return empty, got %q", got)
	}
}

func TestState_Overwrite(t *testing.T) {
	s := openTestStore(t)
	s.SetState("k", "v1")
	s.SetState("k", "v2")
	got, _ := s.GetState("k")
	if got != "v2" {
		t.Errorf("got %q, want v2", got)
	}
}

// -- Rooms --

func TestRoomUpsertAndGet(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertRoom("room_abc", "general", "General chat", 3); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if name := s.GetRoomName("room_abc"); name != "general" {
		t.Errorf("GetRoomName = %q, want general", name)
	}
	// Unknown room falls back to raw ID
	if name := s.GetRoomName("room_unknown"); name != "room_unknown" {
		t.Errorf("GetRoomName unknown = %q, want room_unknown", name)
	}
	// Update display name
	s.UpsertRoom("room_abc", "general-renamed", "New topic", 5)
	if name := s.GetRoomName("room_abc"); name != "general-renamed" {
		t.Errorf("GetRoomName after update = %q, want general-renamed", name)
	}
}

// -- FTS5 availability --

func TestHasFTS_Unencrypted(t *testing.T) {
	s := openTestStore(t)
	// SQLCipher built with FTS5 should have it; without, HasFTS returns false.
	// Either result is valid — just ensure the method doesn't panic.
	_ = s.HasFTS()
	t.Logf("HasFTS = %v", s.HasFTS())
}
