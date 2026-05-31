package client

// Audit F6 — integration tests for un-react (reaction-removal) author
// verification wired into the "reaction_removed" receive arm (client.go
// handleInternal). A removal now requires a crypto.SignUnreact signature over
// the reaction_id, verified against the claimed actor's pinned key
// (VerifyUnreactAuthor) before the stored reaction is deleted — so a malicious
// relay can't forge an un-react to erase a genuine reaction or attribute one to
// a user who never sent it. Reuses editVerifyHarness (Alice receives; Bob is the
// author) + countReactions from the sibling _test.go files.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// seedReaction inserts a stored reaction (authored by Bob) on a message so the
// un-react path has a row to delete.
func (h *editVerifyHarness) seedReaction(t *testing.T, reactionID, msgID string) {
	t.Helper()
	if err := h.alice.store.InsertReaction(store.StoredReaction{
		ReactionID: reactionID,
		MessageID:  msgID,
		User:       h.bobID,
		Emoji:      "👍",
		TS:         2,
	}); err != nil {
		t.Fatalf("InsertReaction: %v", err)
	}
}

// buildSignedReactionRemoved mints a `reaction_removed` broadcast with a genuine
// Bob-authored SignUnreact signature over the reaction_id.
func (h *editVerifyHarness) buildSignedReactionRemoved(reactionID, msgID, room string) protocol.ReactionRemoved {
	sig := crypto.SignUnreact(h.bobPriv, reactionID)
	return protocol.ReactionRemoved{
		Type:       "reaction_removed",
		ReactionID: reactionID,
		ID:         msgID,
		Room:       room,
		User:       h.bobID,
		Signature:  base64.StdEncoding.EncodeToString(sig),
	}
}

func TestUnreact_ValidSignatureDeletesReaction(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_unreact_ok"
	const reactionID = "react_ok"
	h.seedOriginalRoomMessage(t, msgID, room, "hello")
	h.seedReaction(t, reactionID, msgID)
	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("precondition: seeded reaction count = %d, want 1", n)
	}

	rm := h.buildSignedReactionRemoved(reactionID, msgID, room)
	raw, _ := json.Marshal(rm)
	h.alice.handleInternal("reaction_removed", raw)

	if n := countReactions(t, h.alice, msgID); n != 0 {
		t.Fatalf("valid un-react should delete the reaction; count = %d, want 0", n)
	}
}

func TestUnreact_ForgedSignatureKeepsReaction(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_unreact_forged"
	const reactionID = "react_forged"
	h.seedOriginalRoomMessage(t, msgID, room, "hello")
	h.seedReaction(t, reactionID, msgID)

	rm := h.buildSignedReactionRemoved(reactionID, msgID, room)
	garbage := make([]byte, 64)
	_, _ = rand.Read(garbage)
	rm.Signature = base64.StdEncoding.EncodeToString(garbage)
	raw, _ := json.Marshal(rm)
	h.alice.handleInternal("reaction_removed", raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("forged un-react must NOT delete the reaction; count = %d, want 1", n)
	}
}

func TestUnreact_UnknownSenderKeepsReaction(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_unreact_unk"
	const reactionID = "react_unk"
	h.seedOriginalRoomMessage(t, msgID, room, "hello")
	h.seedReaction(t, reactionID, msgID)

	rm := h.buildSignedReactionRemoved(reactionID, msgID, room)
	delete(h.alice.profiles, h.bobID) // pubKeyForUser(bob) → nil → verify fails closed
	raw, _ := json.Marshal(rm)
	h.alice.handleInternal("reaction_removed", raw)

	if n := countReactions(t, h.alice, msgID); n != 1 {
		t.Fatalf("unknown-sender un-react must NOT delete the reaction; count = %d, want 1", n)
	}
}

// TestUnreact_RetargetedSignatureKeepsOtherReaction — a genuine un-react signed
// for reaction A cannot be replayed to remove reaction B; the signature binds
// the reaction_id, so swapping it fails verification.
func TestUnreact_RetargetedSignatureKeepsOtherReaction(t *testing.T) {
	h := newEditVerifyHarness(t)
	const room = "rm_general"
	const msgID = "msg_unreact_rt"
	h.seedOriginalRoomMessage(t, msgID, room, "hello")
	h.seedReaction(t, "react_A", msgID)
	h.seedReaction(t, "react_B", msgID)

	// Bob validly signs removal of react_A; a relay swaps the reaction_id to
	// react_B but keeps Bob's signature for react_A.
	rm := h.buildSignedReactionRemoved("react_A", msgID, room)
	rm.ReactionID = "react_B"
	raw, _ := json.Marshal(rm)
	h.alice.handleInternal("reaction_removed", raw)

	// Both reactions must survive (the sig was for react_A, verified against
	// react_B → fails → no deletion).
	if n := countReactions(t, h.alice, msgID); n != 2 {
		t.Fatalf("retargeted un-react must not delete react_B; count = %d, want 2", n)
	}
}
