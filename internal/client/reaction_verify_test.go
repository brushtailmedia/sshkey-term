package client

// Audit F6 — integration tests for the reaction author-signature verification
// wired into storeReaction (persist.go). Reactions are signed on send
// (crypto.SignRoom / SignDM over the encrypted payload, see send.go) but were
// historically never verified on receipt — so a malicious relay could forge a
// reaction or attribute one to a user who never sent it. storeReaction now
// verify-or-drops via Client.VerifyReactionAuthor before InsertReaction, exactly
// as the message/edit receive paths do (see edit_verify_test.go, whose
// editVerifyHarness + helpers this file reuses).
//
// Coverage:
//   - valid Bob-signed room reaction        → stored (happy path, VerifyRoom)
//   - valid Bob-signed group + DM reactions  → stored (VerifyDM branch)
//   - unknown sender (no profile/pinned key) → dropped (verify-or-drop contract)
//   - garbage signature                      → dropped
//   - reaction re-targeted onto another msg  → dropped (durable dr.Target check)

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// buildSignedRoomReaction mints a `reaction` envelope with a genuine
// Bob-authored SignRoom signature over the encrypted reaction payload, exactly
// as SendRoomReaction does. Callers mutate fields before delivery to exercise
// the negative paths.
func (h *editVerifyHarness) buildSignedRoomReaction(t *testing.T, reactionID, targetMsgID, room string, epoch int64, epochKey []byte, emoji string) protocol.Reaction {
	t.Helper()
	payload := protocol.DecryptedReaction{Emoji: emoji, Target: targetMsgID, Seq: 2, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(epochKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignRoom(h.bobPriv, payloadBytes, room, epoch)
	return protocol.Reaction{
		Type:       "reaction",
		ReactionID: reactionID,
		ID:         targetMsgID,
		Room:       room,
		User:       h.bobID,
		TS:         2000,
		Epoch:      epoch,
		Payload:    encrypted,
		Signature:  base64.StdEncoding.EncodeToString(sig),
	}
}

// buildSignedConvReaction mints a group/DM `reaction` envelope with a genuine
// Bob-authored SignDM signature. Set group!="" for a group reaction or dm!=""
// for a 1:1 DM reaction (the conversation id the signature binds).
func (h *editVerifyHarness) buildSignedConvReaction(t *testing.T, reactionID, targetMsgID, group, dm, emoji string) protocol.Reaction {
	t.Helper()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen msg key: %v", err)
	}
	wrappedKeys := wrapKeyForTwoParties(t, msgKey, h.alice.UserID(), alicePub, h.bobID, h.bobPub)
	conversation := group
	if conversation == "" {
		conversation = dm
	}
	payload := protocol.DecryptedReaction{Emoji: emoji, Target: targetMsgID, Seq: 2, DeviceID: "dev_bob_test"}
	payloadJSON, _ := json.Marshal(payload)
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(h.bobPriv, payloadBytes, conversation, wrappedKeys)
	return protocol.Reaction{
		Type:        "reaction",
		ReactionID:  reactionID,
		ID:          targetMsgID,
		Group:       group,
		DM:          dm,
		User:        h.bobID,
		TS:          2000,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}
}

func TestStoreReaction_StoresValidRoomSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_react_ok"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "hello")

	r := h.buildSignedRoomReaction(t, "react_ok", msgID, room, epoch, key, "👍")
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("valid room reaction should be stored; rows = %d, want 1", n)
	}
}

func TestStoreReaction_StoresValidGroupSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const group = "grp_project"
	const msgID = "msg_grp_react"
	h.seedOriginalGroupMessage(t, msgID, group, "hello")

	r := h.buildSignedConvReaction(t, "react_grp_ok", msgID, group, "", "🎉")
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("valid group reaction should be stored; rows = %d, want 1", n)
	}
}

func TestStoreReaction_StoresValidDMSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const dmID = "dm_ab"
	const msgID = "msg_dm_react"
	h.seedOriginalDMMessage(t, msgID, dmID, "hello")

	r := h.buildSignedConvReaction(t, "react_dm_ok", msgID, "", dmID, "❤️")
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("valid DM reaction should be stored; rows = %d, want 1", n)
	}
}

func TestStoreReaction_DropsUnknownSender(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_react_unk"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "hello")

	r := h.buildSignedRoomReaction(t, "react_unk", msgID, room, epoch, key, "👍")
	delete(h.alice.profiles, h.bobID) // pubKeyForUser(bob) → nil → verify fails closed
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgID); n != 0 {
		t.Fatalf("unknown-sender reaction should be dropped; rows = %d, want 0", n)
	}
}

func TestStoreReaction_DropsGarbageSignature(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_react_garbage"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "hello")

	r := h.buildSignedRoomReaction(t, "react_g", msgID, room, epoch, key, "👍")
	garbage := make([]byte, 64)
	_, _ = rand.Read(garbage)
	r.Signature = base64.StdEncoding.EncodeToString(garbage)
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgID); n != 0 {
		t.Fatalf("garbage-signature reaction should be dropped; rows = %d, want 0", n)
	}
}

// TestStoreReaction_DropsRetargetedReaction — Bob validly signs a reaction for
// msgX; a malicious relay keeps the signature + ciphertext but swaps the
// envelope ID to msgY. The signature still verifies (it covers the payload +
// room + epoch, not the envelope ID), but the decrypted Target (msgX) no longer
// matches the envelope ID (msgY), so the durable dr.Target check drops it. This
// is the anti-retarget guard that previously lived only in the TUI path.
func TestStoreReaction_DropsRetargetedReaction(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgX = "msg_react_x"
	const msgY = "msg_react_y"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgX, room, "x")
	h.seedOriginalRoomMessage(t, msgY, room, "y")

	r := h.buildSignedRoomReaction(t, "react_rt", msgX, room, epoch, key, "👍")
	r.ID = msgY // re-target onto a different (valid, present) parent
	raw, _ := json.Marshal(r)
	h.alice.storeReaction(raw)

	if n := countReactions(t, h.alice, msgY); n != 0 {
		t.Fatalf("re-targeted reaction should be dropped; rows on msgY = %d, want 0", n)
	}
}
