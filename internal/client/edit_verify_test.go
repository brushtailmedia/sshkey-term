package client

// Phase 21 item 3 — tests for the edit-path signature verification
// wired into storeEditedRoomMessage / storeEditedGroupMessage /
// storeEditedDMMessage. The verification itself lives in crypto.go
// (VerifyRoomEdit / VerifyDMEdit) and is unit-tested there; these
// tests lock in the INTEGRATION: that persist.go's edit handlers
// refuse to call UpdateMessageEdited when verification fails.
//
// Coverage matrix (×3 contexts: room, group, DM):
//   - valid msgID-bound signature → applies (happy path)
//   - reused send-path signature → rejected (domain-separation works)
//   - signature bound to a different msgID → rejected (replay blocked)
//   - sender pubkey unknown → rejected (verify-or-drop contract)
//   - random garbage signature → rejected
//
// These tests build the full crypto chain end-to-end (wrap keys,
// encrypt, sign, send) so regression in any layer surfaces here.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// editVerifyHarness bundles an Alice-receiving-from-Bob test setup.
type editVerifyHarness struct {
	alice *Client         // the client under test (receives edits)
	bobID string          // "usr_bob"
	bobPriv ed25519.PrivateKey
	bobPub  ed25519.PublicKey
}

// newEditVerifyHarness creates a Client (Alice) with a store, a live
// Bob profile in c.profiles, and a Bob ed25519 keypair the test can
// use to mint signed payloads.
func newEditVerifyHarness(t *testing.T) *editVerifyHarness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Alice's keypair (client under test). Needed because pubKeyForUser
	// returns c.privKey.Public() for self-lookup.
	alicePub, alicePriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen alice key: %v", err)
	}
	_ = alicePub

	c := New(Config{})
	c.store = st
	c.privKey = alicePriv
	c.userID = "usr_alice"

	// Bob's keypair (message author). Used to mint signed edit
	// broadcasts for Alice's receive path to verify.
	bobPub, bobPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen bob key: %v", err)
	}

	// Register Bob's profile in c.profiles so pubKeyForUser can resolve.
	sshPub, err := ssh.NewPublicKey(bobPub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	c.profiles["usr_bob"] = &protocol.Profile{
		User:   "usr_bob",
		PubKey: pubLine,
	}

	return &editVerifyHarness{
		alice:   c,
		bobID:   "usr_bob",
		bobPriv: bobPriv,
		bobPub:  bobPub,
	}
}

// seedRoomEpochKey stores a fresh epoch key in Alice's client for a
// (room, epoch) pair so the receive handler can decrypt the new
// payload. Returns the key bytes so the caller can encrypt with it.
func (h *editVerifyHarness) seedRoomEpochKey(t *testing.T, room string, epoch int64) []byte {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen epoch key: %v", err)
	}
	h.alice.mu.Lock()
	if h.alice.epochKeys[room] == nil {
		h.alice.epochKeys[room] = make(map[int64][]byte)
	}
	h.alice.epochKeys[room][epoch] = key
	h.alice.currentEpoch[room] = epoch
	h.alice.mu.Unlock()
	return key
}

// seedOriginalRoomMessage inserts a stored message in Alice's local
// DB so storeEditedRoomMessage has a row to target with
// UpdateMessageEdited.
func (h *editVerifyHarness) seedOriginalRoomMessage(t *testing.T, msgID, room, body string) {
	t.Helper()
	err := h.alice.store.InsertMessage(store.StoredMessage{
		ID:     msgID,
		Sender: h.bobID,
		Body:   body,
		TS:     1000,
		Room:   room,
		Epoch:  1,
	})
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
}

// buildSignedRoomEdit mints an `edited` envelope with a genuine
// Bob-authored SignRoomEdit signature over (msgID, payload, room,
// epoch). The caller can then mutate fields before delivery to
// exercise negative paths.
func (h *editVerifyHarness) buildSignedRoomEdit(t *testing.T, msgID, room string, epoch int64, epochKey []byte, newBody string) protocol.Edited {
	t.Helper()
	payload := protocol.DecryptedPayload{Body: newBody, Seq: 2, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(epochKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignRoomEdit(h.bobPriv, msgID, payloadBytes, room, epoch)
	return protocol.Edited{
		Type:      "edited",
		ID:        msgID,
		From:      h.bobID,
		Room:      room,
		TS:        1000,
		Epoch:     epoch,
		Payload:   encrypted,
		Signature: base64.StdEncoding.EncodeToString(sig),
		EditedAt:  2000,
	}
}

// getBodyByID pulls the current body for a stored message. Used to
// assert whether UpdateMessageEdited fired.
func (h *editVerifyHarness) getBodyByID(t *testing.T, msgID string) string {
	t.Helper()
	m, err := h.alice.store.GetMessageByID(msgID)
	if err != nil {
		t.Fatalf("GetMessageByID(%q): %v", msgID, err)
	}
	if m == nil {
		return ""
	}
	return m.Body
}

// TestStoreEditedRoomMessage_AppliesValidSignature — happy path. A
// genuine Bob-signed edit with the new msgID-bound canonical form
// passes verification, decrypts cleanly, and updates the local row.
func TestStoreEditedRoomMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_room_1"
	const room = "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "original body")

	edited := h.buildSignedRoomEdit(t, msgID, room, epoch, key, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.storeEditedRoomMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Errorf("body after valid edit = %q, want %q", got, "edited body")
	}
}

// TestStoreEditedRoomMessage_RejectsReusedSendSignature — a SignRoom
// signature (send path) must not cross-verify as an edit. This is
// the domain-separation protection in the canonical form.
func TestStoreEditedRoomMessage_RejectsReusedSendSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_room_2"
	const room = "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "original body")

	// Build a valid envelope…
	edited := h.buildSignedRoomEdit(t, msgID, room, epoch, key, "substituted body")
	// …then overwrite Signature with a valid SEND-path signature for
	// the same (payload, room, epoch). Without domain separation this
	// would pass verification. With domain separation it must fail.
	payloadBytes, _ := base64.StdEncoding.DecodeString(edited.Payload)
	sendSig := crypto.SignRoom(h.bobPriv, payloadBytes, room, epoch)
	edited.Signature = base64.StdEncoding.EncodeToString(sendSig)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedRoomMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("reused send signature should NOT have applied; body = %q, want unchanged %q", got, "original body")
	}
}

// TestStoreEditedRoomMessage_RejectsMsgIDSubstitution — a valid
// signature for msg_X cannot be replayed against msg_Y. This is the
// core Phase 21 item 3 protection against history rewrite via
// signature replay.
func TestStoreEditedRoomMessage_RejectsMsgIDSubstitution(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgX = "msg_room_x"
	const msgY = "msg_room_y"
	const room = "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgX, room, "x body")
	h.seedOriginalRoomMessage(t, msgY, room, "y body")

	// Bob genuinely signs an edit for msg_X. Attacker intercepts and
	// re-targets at msg_Y — changes the `id` field but keeps Bob's
	// real signature for msg_X.
	editedX := h.buildSignedRoomEdit(t, msgX, room, epoch, key, "malicious substitution")
	editedX.ID = msgY // swap target
	raw, _ := json.Marshal(editedX)
	h.alice.storeEditedRoomMessage(raw)

	// msg_Y must be untouched.
	if got := h.getBodyByID(t, msgY); got != "y body" {
		t.Errorf("msgID substitution should NOT have applied to msg_Y; body = %q, want unchanged %q", got, "y body")
	}
}

// TestStoreEditedRoomMessage_RejectsUnknownSender — verify-or-drop.
// If we can't resolve the sender's pubkey, verification must fail
// closed. Prevents forgery attempts during the cold-start window
// before profile events arrive.
func TestStoreEditedRoomMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_room_3"
	const room = "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "original body")

	edited := h.buildSignedRoomEdit(t, msgID, room, epoch, key, "edited body")
	// Drop Bob's profile so pubKeyForUser returns nil.
	delete(h.alice.profiles, h.bobID)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedRoomMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("unknown-sender edit should NOT have applied; body = %q, want unchanged %q", got, "original body")
	}
}

// TestStoreEditedRoomMessage_RejectsGarbageSignature — a totally
// invalid signature (random bytes) is rejected. Sanity check that
// verification isn't accidentally accepting anything.
func TestStoreEditedRoomMessage_RejectsGarbageSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_room_4"
	const room = "rm_general"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "original body")

	edited := h.buildSignedRoomEdit(t, msgID, room, epoch, key, "edited body")
	garbage := make([]byte, 64)
	_, _ = rand.Read(garbage)
	edited.Signature = base64.StdEncoding.EncodeToString(garbage)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedRoomMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("garbage signature should NOT have applied; body = %q, want unchanged %q", got, "original body")
	}
}

// ---------- Group edit variant ----------

// wrapKeyForTwoParties wraps a K_msg for Alice + Bob under each
// party's ed25519 pubkey. Small test helper that mirrors the
// production wrapKeyForGroup/wrapKeyForDM paths without needing the
// full Client state setup.
func wrapKeyForTwoParties(t *testing.T, key []byte, aliceID string, alicePub ed25519.PublicKey, bobID string, bobPub ed25519.PublicKey) map[string]string {
	t.Helper()
	aliceWrapped, err := crypto.WrapKey(key, alicePub)
	if err != nil {
		t.Fatalf("wrap for alice: %v", err)
	}
	bobWrapped, err := crypto.WrapKey(key, bobPub)
	if err != nil {
		t.Fatalf("wrap for bob: %v", err)
	}
	return map[string]string{aliceID: aliceWrapped, bobID: bobWrapped}
}

func (h *editVerifyHarness) seedOriginalGroupMessage(t *testing.T, msgID, group, body string) {
	t.Helper()
	err := h.alice.store.InsertMessage(store.StoredMessage{
		ID:     msgID,
		Sender: h.bobID,
		Body:   body,
		TS:     1000,
		Group:  group,
	})
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
}

func (h *editVerifyHarness) buildSignedGroupEdit(t *testing.T, msgID, group, newBody string) protocol.GroupEdited {
	t.Helper()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen msg key: %v", err)
	}
	wrappedKeys := wrapKeyForTwoParties(t, msgKey, h.alice.UserID(), alicePub, h.bobID, h.bobPub)

	payload := protocol.DecryptedPayload{Body: newBody, Seq: 2, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDMEdit(h.bobPriv, msgID, payloadBytes, group, wrappedKeys)
	return protocol.GroupEdited{
		Type:        "group_edited",
		ID:          msgID,
		From:        h.bobID,
		Group:       group,
		TS:          1000,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
		EditedAt:    2000,
	}
}

func TestStoreEditedGroupMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_grp_1"
	const group = "grp_project"
	h.seedOriginalGroupMessage(t, msgID, group, "original body")

	edited := h.buildSignedGroupEdit(t, msgID, group, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.storeEditedGroupMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Errorf("body after valid group edit = %q, want %q", got, "edited body")
	}
}

func TestStoreEditedGroupMessage_RejectsReusedSendSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_grp_2"
	const group = "grp_project"
	h.seedOriginalGroupMessage(t, msgID, group, "original body")

	edited := h.buildSignedGroupEdit(t, msgID, group, "substituted body")
	// Replace with a send-path (SignDM) signature over the same inputs.
	payloadBytes, _ := base64.StdEncoding.DecodeString(edited.Payload)
	sendSig := crypto.SignDM(h.bobPriv, payloadBytes, group, edited.WrappedKeys)
	edited.Signature = base64.StdEncoding.EncodeToString(sendSig)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedGroupMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("reused send signature should NOT have applied; body = %q", got)
	}
}

func TestStoreEditedGroupMessage_RejectsMsgIDSubstitution(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgX = "msg_grp_x"
	const msgY = "msg_grp_y"
	const group = "grp_project"
	h.seedOriginalGroupMessage(t, msgX, group, "x body")
	h.seedOriginalGroupMessage(t, msgY, group, "y body")

	editedX := h.buildSignedGroupEdit(t, msgX, group, "malicious substitution")
	editedX.ID = msgY
	raw, _ := json.Marshal(editedX)
	h.alice.storeEditedGroupMessage(raw)

	if got := h.getBodyByID(t, msgY); got != "y body" {
		t.Errorf("msgID substitution should NOT have applied to msg_Y; body = %q", got)
	}
}

func TestStoreEditedGroupMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_grp_3"
	const group = "grp_project"
	h.seedOriginalGroupMessage(t, msgID, group, "original body")

	edited := h.buildSignedGroupEdit(t, msgID, group, "edited body")
	delete(h.alice.profiles, h.bobID)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedGroupMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("unknown-sender edit should NOT have applied; body = %q", got)
	}
}

// ---------- 1:1 DM edit variant ----------

func (h *editVerifyHarness) seedOriginalDMMessage(t *testing.T, msgID, dmID, body string) {
	t.Helper()
	err := h.alice.store.InsertMessage(store.StoredMessage{
		ID:     msgID,
		Sender: h.bobID,
		Body:   body,
		TS:     1000,
		DM:     dmID,
	})
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
}

func (h *editVerifyHarness) buildSignedDMEdit(t *testing.T, msgID, dmID, newBody string) protocol.DMEdited {
	t.Helper()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen msg key: %v", err)
	}
	wrappedKeys := wrapKeyForTwoParties(t, msgKey, h.alice.UserID(), alicePub, h.bobID, h.bobPub)

	payload := protocol.DecryptedPayload{Body: newBody, Seq: 2, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDMEdit(h.bobPriv, msgID, payloadBytes, dmID, wrappedKeys)
	return protocol.DMEdited{
		Type:        "dm_edited",
		ID:          msgID,
		From:        h.bobID,
		DM:          dmID,
		TS:          1000,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
		EditedAt:    2000,
	}
}

func TestStoreEditedDMMessage_AppliesValidSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dm_1"
	const dmID = "dm_ab"
	h.seedOriginalDMMessage(t, msgID, dmID, "original body")

	edited := h.buildSignedDMEdit(t, msgID, dmID, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.storeEditedDMMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Errorf("body after valid dm edit = %q, want %q", got, "edited body")
	}
}

func TestStoreEditedDMMessage_RejectsReusedSendSignature(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dm_2"
	const dmID = "dm_ab"
	h.seedOriginalDMMessage(t, msgID, dmID, "original body")

	edited := h.buildSignedDMEdit(t, msgID, dmID, "substituted body")
	payloadBytes, _ := base64.StdEncoding.DecodeString(edited.Payload)
	sendSig := crypto.SignDM(h.bobPriv, payloadBytes, dmID, edited.WrappedKeys)
	edited.Signature = base64.StdEncoding.EncodeToString(sendSig)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedDMMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("reused send signature should NOT have applied; body = %q", got)
	}
}

func TestStoreEditedDMMessage_RejectsMsgIDSubstitution(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgX = "msg_dm_x"
	const msgY = "msg_dm_y"
	const dmID = "dm_ab"
	h.seedOriginalDMMessage(t, msgX, dmID, "x body")
	h.seedOriginalDMMessage(t, msgY, dmID, "y body")

	editedX := h.buildSignedDMEdit(t, msgX, dmID, "malicious substitution")
	editedX.ID = msgY
	raw, _ := json.Marshal(editedX)
	h.alice.storeEditedDMMessage(raw)

	if got := h.getBodyByID(t, msgY); got != "y body" {
		t.Errorf("msgID substitution should NOT have applied to msg_Y; body = %q", got)
	}
}

func TestStoreEditedDMMessage_RejectsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dm_3"
	const dmID = "dm_ab"
	h.seedOriginalDMMessage(t, msgID, dmID, "original body")

	edited := h.buildSignedDMEdit(t, msgID, dmID, "edited body")
	delete(h.alice.profiles, h.bobID)

	raw, _ := json.Marshal(edited)
	h.alice.storeEditedDMMessage(raw)

	if got := h.getBodyByID(t, msgID); got != "original body" {
		t.Errorf("unknown-sender edit should NOT have applied; body = %q", got)
	}
}
