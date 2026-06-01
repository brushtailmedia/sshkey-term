package client

// F7 Phase D — catchup wiring integration tests. These exercise the real
// storeRoomMessageFromCatchup / storeReactionFromCatchup entry points end to
// end: a genuinely-historical (epoch < currentEpoch) room message/reaction
// decrypts via the history-only scope and is stored, an attachment's DecryptKey
// is baked via the history resolver, a not-yet-historical row is dropped, and
// the LIVE paths never read the history store.

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// seedHistoricalRoomEpochKey seeds a HISTORY-ONLY epoch key (historyEpochKeys
// map + historical_epoch_keys table) for (room, epoch) and advances currentEpoch
// PAST it (epoch+1), so the resolver treats `epoch` as genuinely historical.
// Deliberately leaves the adopted epochKeys/epoch_keys empty for that epoch.
func (h *editVerifyHarness) seedHistoricalRoomEpochKey(t *testing.T, room string, epoch int64) []byte {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen epoch key: %v", err)
	}
	if err := h.alice.store.StoreHistoricalEpochKey(room, epoch, key); err != nil {
		t.Fatalf("StoreHistoricalEpochKey: %v", err)
	}
	h.alice.mu.Lock()
	if h.alice.historyEpochKeys[room] == nil {
		h.alice.historyEpochKeys[room] = make(map[int64][]byte)
	}
	h.alice.historyEpochKeys[room][epoch] = key
	h.alice.currentEpoch[room] = epoch + 1 // epoch is now genuinely historical
	h.alice.mu.Unlock()
	return key
}

func TestStoreRoomMessageFromCatchup_HistoricalDecryptsAndStores(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_hist_ok"
	const oldEpoch = int64(3)
	key := h.seedHistoricalRoomEpochKey(t, room, oldEpoch) // currentEpoch -> 4

	msg := h.buildSignedRoomMessage(t, msgID, room, oldEpoch, key, "history body")
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessageFromCatchup(raw)

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("historical catchup message should be stored")
	}
	if m.Body != "history body" {
		t.Errorf("body = %q, want %q", m.Body, "history body")
	}
}

// A catchup message whose epoch is NOT yet historical (currentEpoch hasn't
// advanced past it) must be dropped — even though a history key exists. This is
// the gate enforced through the real catchup entry point.
func TestStoreRoomMessageFromCatchup_NotYetHistoricalDropped(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_not_hist"
	const epoch = int64(3)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	// History key present, but currentEpoch stays 0 → epoch 3 is NOT historical.
	h.alice.mu.Lock()
	if h.alice.historyEpochKeys[room] == nil {
		h.alice.historyEpochKeys[room] = make(map[int64][]byte)
	}
	h.alice.historyEpochKeys[room][epoch] = key
	h.alice.mu.Unlock()

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "should drop")
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessageFromCatchup(raw)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Errorf("not-yet-historical catchup message must be dropped; got body=%q", m.Body)
	}
}

// The LIVE path must not read the history store: an undecryptable live message
// is stored empty (current behavior), never decrypted via a history-only key.
func TestStoreRoomMessage_LiveDoesNotUseHistoryKey(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_live_nohist"
	const oldEpoch = int64(3)
	key := h.seedHistoricalRoomEpochKey(t, room, oldEpoch) // history only; adopted slot empty

	msg := h.buildSignedRoomMessage(t, msgID, room, oldEpoch, key, "live body")
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, true) // LIVE path

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("live path stores the row (empty body) even when undecryptable")
	}
	if m.Body == "live body" {
		t.Error("live path must NOT decrypt via the history store")
	}
}

// §5d.7b: a historical attachment's DecryptKey must be baked via the history
// resolver — adopted-only RoomEpochKey would yield an empty key and leave the
// attachment permanently undownloadable.
func TestStoreRoomMessageFromCatchup_HistoricalAttachmentKeyBaked(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_hist_attach"
	const oldEpoch = int64(3)
	key := h.seedHistoricalRoomEpochKey(t, room, oldEpoch) // currentEpoch -> 4

	payload := protocol.DecryptedPayload{
		Body:     "with attachment",
		Seq:      1,
		DeviceID: "dev_bob_test",
		Attachments: []protocol.Attachment{{
			FileID:    "file_hist",
			Name:      "old.png",
			Size:      10,
			Mime:      "image/png",
			FileEpoch: oldEpoch,
		}},
	}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(key, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignRoom(h.bobPriv, payloadBytes, room, oldEpoch)
	msg := protocol.Message{
		Type: "message", ID: msgID, ServerOrder: 1, From: h.bobID, Room: room,
		TS: 1000, Epoch: oldEpoch, Payload: encrypted,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessageFromCatchup(raw)

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("historical attachment message should be stored")
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(m.Attachments))
	}
	wantKey := base64.StdEncoding.EncodeToString(key)
	if m.Attachments[0].DecryptKey != wantKey {
		t.Errorf("attachment DecryptKey = %q, want the history epoch key (non-empty)", m.Attachments[0].DecryptKey)
	}
}

func TestStoreReactionFromCatchup_HistoricalDecrypts(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_for_hist_react"
	const oldEpoch = int64(3)
	h.seedOriginalRoomMessage(t, msgID, room, "parent") // parent must exist (orphan check)
	key := h.seedHistoricalRoomEpochKey(t, room, oldEpoch)

	r := h.buildSignedRoomReaction(t, "react_hist", msgID, room, oldEpoch, key, "👍")
	raw, _ := json.Marshal(r)
	h.alice.storeReactionFromCatchup(raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("historical catchup reaction should be stored; rows = %d, want 1", n)
	}
}

// The LIVE reaction path must not read the history store.
func TestStoreReaction_LiveDoesNotUseHistoryKey(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room, msgID = "rm_general", "msg_live_react"
	const oldEpoch = int64(3)
	h.seedOriginalRoomMessage(t, msgID, room, "parent")
	key := h.seedHistoricalRoomEpochKey(t, room, oldEpoch) // history only; adopted slot empty

	r := h.buildSignedRoomReaction(t, "react_live", msgID, room, oldEpoch, key, "👍")
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw) // LIVE path → adopted-only → cannot decrypt → drop

	if n := countReactions(t, h.alice, msgID); n != 0 {
		t.Errorf("live reaction must not decrypt via the history store; rows = %d, want 0", n)
	}
}
