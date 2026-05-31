package client

// Audit F1 — tests for the send-path signature verification wired into
// storeRoomMessage / storeGroupMessage / storeDMMessage. These mirror the
// edit-path suite (edit_verify_test.go) but cover *normal* message
// broadcasts: the server is untrusted for authorship, so an inbound message
// whose Ed25519 signature does not verify against the claimed sender's key
// must be dropped, never stored or shown as authentic (verify-or-drop).
//
// The signature primitives (SignRoom/VerifyRoom, SignDM/VerifyDM) are
// unit-tested in the crypto package; these tests lock in the INTEGRATION —
// that persist.go refuses to InsertMessage when verification fails.
//
// Coverage matrix (×3 contexts: room, group, DM):
//   - valid send-path signature        → stored (happy path)
//   - random garbage signature         → dropped
//   - sender pubkey unresolvable        → dropped (verify-or-drop contract)
//   - empty / absent signature          → dropped
//   - signature rebound to a different
//     context (room: epoch; group/DM:
//     conversation id)                  → dropped (binding works)
//
// Negative cases are constructed from an otherwise-valid, decryptable frame
// with only the signature (or sender / binding) broken — so a dropped row
// proves the F1 verify gate fired, not an incidental decrypt failure. This
// matters most for the group path, where DecryptGroupMessage would succeed
// (the key is wrapped for Alice) and thus store the row if verify were absent.

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// --- builders -------------------------------------------------------------

// buildSignedRoomMessage mints a normal room broadcast with a genuine Bob
// SignRoom signature over (ciphertext || room || epoch). The caller seeds
// the epoch key (so the body decrypts) and may mutate fields afterwards to
// drive the negative paths.
func (h *editVerifyHarness) buildSignedRoomMessage(t *testing.T, msgID, room string, epoch int64, epochKey []byte, body string) protocol.Message {
	t.Helper()
	payload := protocol.DecryptedPayload{Body: body, Seq: 1, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(epochKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignRoom(h.bobPriv, payloadBytes, room, epoch)
	return protocol.Message{
		Type:        "message",
		ID:          msgID,
		ServerOrder: 1,
		From:        h.bobID,
		Room:        room,
		TS:          1000,
		Epoch:       epoch,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}
}

// buildSignedGroupMessage mints a normal group broadcast: a fresh K_msg
// wrapped for Alice + Bob, the body encrypted under it, and a genuine Bob
// SignDM signature over (ciphertext || group || wrapped_keys).
func (h *editVerifyHarness) buildSignedGroupMessage(t *testing.T, msgID, group, body string) protocol.GroupMessage {
	t.Helper()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen msg key: %v", err)
	}
	wrappedKeys := wrapKeyForTwoParties(t, msgKey, h.alice.UserID(), alicePub, h.bobID, h.bobPub)
	payload := protocol.DecryptedPayload{Body: body, Seq: 1, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(h.bobPriv, payloadBytes, group, wrappedKeys)
	return protocol.GroupMessage{
		Type:        "group_message",
		ID:          msgID,
		ServerOrder: 1,
		From:        h.bobID,
		Group:       group,
		TS:          1000,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}
}

// buildSignedDMMessage mints a normal 1:1 DM broadcast, mirroring the group
// builder but binding the signature to the DM id via SignDM.
func (h *editVerifyHarness) buildSignedDMMessage(t *testing.T, msgID, dmID, body string) protocol.DM {
	t.Helper()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen msg key: %v", err)
	}
	wrappedKeys := wrapKeyForTwoParties(t, msgKey, h.alice.UserID(), alicePub, h.bobID, h.bobPub)
	payload := protocol.DecryptedPayload{Body: body, Seq: 1, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(h.bobPriv, payloadBytes, dmID, wrappedKeys)
	return protocol.DM{
		Type:        "dm",
		ID:          msgID,
		ServerOrder: 1,
		From:        h.bobID,
		DM:          dmID,
		TS:          1000,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}
}

// storedMessage returns Alice's locally-stored row for msgID, or nil if the
// receive handler dropped it. A missing row surfaces as sql.ErrNoRows from
// GetMessageByID; we map that to nil so callers can assert "dropped".
func (h *editVerifyHarness) storedMessage(t *testing.T, msgID string) *store.StoredMessage {
	t.Helper()
	m, err := h.alice.store.GetMessageByID(msgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("GetMessageByID(%q): %v", msgID, err)
	}
	return m
}

const garbageSig = "bm90IGEgcmVhbCBzaWduYXR1cmU=" // base64("not a real signature")

// --- room -----------------------------------------------------------------

func TestStoreRoomMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, room = "msg_room_ok", "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "hello room")
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, false)

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("valid room message should be stored")
	}
	if m.Body != "hello room" {
		t.Errorf("body = %q, want %q", m.Body, "hello room")
	}
}

func TestStoreRoomMessage_RejectsGarbageSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, room = "msg_room_bad", "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "hello room")
	msg.Signature = garbageSig
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("room message with garbage signature must be dropped")
	}
}

func TestStoreRoomMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, room = "msg_room_unk", "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "hello room")
	// Drop Bob's profile; the harness pins no fallback key, so pubKeyForUser
	// returns nil and verification must fail closed.
	delete(h.alice.profiles, h.bobID)
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("room message from an unresolvable sender must be dropped")
	}
}

func TestStoreRoomMessage_RejectsUnsigned(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, room = "msg_room_uns", "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "hello room")
	msg.Signature = ""
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("unsigned room message must be dropped")
	}
}

// A signature is bound to its epoch; replaying the frame under a different
// epoch must fail verification even though the sender and ciphertext are
// genuine.
func TestStoreRoomMessage_RejectsEpochRebind(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, room = "msg_room_rebind", "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)

	msg := h.buildSignedRoomMessage(t, msgID, room, epoch, key, "hello room")
	msg.Epoch = 2 // signature covers epoch 1; verify now binds epoch 2
	raw, _ := json.Marshal(msg)
	h.alice.storeRoomMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("room message replayed into a different epoch must be dropped")
	}
}

// --- group ----------------------------------------------------------------

func TestStoreGroupMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, group = "msg_grp_ok", "grp_project"

	msg := h.buildSignedGroupMessage(t, msgID, group, "hello group")
	raw, _ := json.Marshal(msg)
	h.alice.storeGroupMessage(raw, false)

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("valid group message should be stored")
	}
	if m.Body != "hello group" {
		t.Errorf("body = %q, want %q", m.Body, "hello group")
	}
}

func TestStoreGroupMessage_RejectsGarbageSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, group = "msg_grp_bad", "grp_project"

	msg := h.buildSignedGroupMessage(t, msgID, group, "hello group")
	msg.Signature = garbageSig
	raw, _ := json.Marshal(msg)
	h.alice.storeGroupMessage(raw, false)

	// The key is wrapped for Alice, so DecryptGroupMessage would succeed and
	// store the row if verify were absent — a nil row proves verify dropped it.
	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("group message with garbage signature must be dropped")
	}
}

func TestStoreGroupMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, group = "msg_grp_unk", "grp_project"

	msg := h.buildSignedGroupMessage(t, msgID, group, "hello group")
	delete(h.alice.profiles, h.bobID)
	raw, _ := json.Marshal(msg)
	h.alice.storeGroupMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("group message from an unresolvable sender must be dropped")
	}
}

func TestStoreGroupMessage_RejectsUnsigned(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, group = "msg_grp_uns", "grp_project"

	msg := h.buildSignedGroupMessage(t, msgID, group, "hello group")
	msg.Signature = ""
	raw, _ := json.Marshal(msg)
	h.alice.storeGroupMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("unsigned group message must be dropped")
	}
}

// The signature binds the group id; re-targeting the frame at a different
// group must fail verification even though the wrapped key still decrypts.
func TestStoreGroupMessage_RejectsGroupRebind(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, group = "msg_grp_rebind", "grp_project"

	msg := h.buildSignedGroupMessage(t, msgID, group, "hello group")
	msg.Group = "grp_other" // signature covers grp_project
	raw, _ := json.Marshal(msg)
	h.alice.storeGroupMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("group message re-targeted at a different group must be dropped")
	}
}

// --- DM --------------------------------------------------------------------

func TestStoreDMMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, dmID = "msg_dm_ok", "dm_alice_bob"

	msg := h.buildSignedDMMessage(t, msgID, dmID, "hello dm")
	raw, _ := json.Marshal(msg)
	h.alice.storeDMMessage(raw, false)

	m := h.storedMessage(t, msgID)
	if m == nil {
		t.Fatal("valid DM message should be stored")
	}
	if m.Body != "hello dm" {
		t.Errorf("body = %q, want %q", m.Body, "hello dm")
	}
}

func TestStoreDMMessage_RejectsGarbageSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, dmID = "msg_dm_bad", "dm_alice_bob"

	msg := h.buildSignedDMMessage(t, msgID, dmID, "hello dm")
	msg.Signature = garbageSig
	raw, _ := json.Marshal(msg)
	h.alice.storeDMMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("DM message with garbage signature must be dropped")
	}
}

func TestStoreDMMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, dmID = "msg_dm_unk", "dm_alice_bob"

	msg := h.buildSignedDMMessage(t, msgID, dmID, "hello dm")
	delete(h.alice.profiles, h.bobID)
	raw, _ := json.Marshal(msg)
	h.alice.storeDMMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("DM message from an unresolvable sender must be dropped")
	}
}

func TestStoreDMMessage_RejectsUnsigned(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, dmID = "msg_dm_uns", "dm_alice_bob"

	msg := h.buildSignedDMMessage(t, msgID, dmID, "hello dm")
	msg.Signature = ""
	raw, _ := json.Marshal(msg)
	h.alice.storeDMMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("unsigned DM message must be dropped")
	}
}

// The signature binds the DM id; re-targeting the frame at a different
// conversation must fail verification even though the wrapped key decrypts.
func TestStoreDMMessage_RejectsDMRebind(t *testing.T) {
	h := newEditVerifyHarness(t)
	const msgID, dmID = "msg_dm_rebind", "dm_alice_bob"

	msg := h.buildSignedDMMessage(t, msgID, dmID, "hello dm")
	msg.DM = "dm_other" // signature covers dm_alice_bob
	raw, _ := json.Marshal(msg)
	h.alice.storeDMMessage(raw, false)

	if m := h.storedMessage(t, msgID); m != nil {
		t.Error("DM message re-targeted at a different conversation must be dropped")
	}
}
