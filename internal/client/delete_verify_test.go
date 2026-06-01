package client

// F6 delete — client-side verify-or-drop. VerifyDeleteAuthor is the authenticity
// gate (the security boundary; the server is untrusted). These tests use real
// Ed25519 signing via the harness (Bob's key is pinned in Alice).

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func TestVerifyDeleteAuthor_ValidAndBinding(t *testing.T) {
	h := newEditVerifyHarness(t)
	valid := h.signedTombstone("room", "general", "msg1", 1)

	if !h.alice.VerifyDeleteAuthor(valid) {
		t.Fatal("valid Bob-signed delete must verify")
	}

	// Wrong context — signature bound to (room, general); delivered as a group.
	wrongCtx := valid
	wrongCtx.Room = ""
	wrongCtx.Group = "general"
	if h.alice.VerifyDeleteAuthor(wrongCtx) {
		t.Error("wrong-context tombstone must fail")
	}

	// Retargeted — signature for msg1 delivered as msg2.
	retarget := valid
	retarget.ID = "msg2"
	if h.alice.VerifyDeleteAuthor(retarget) {
		t.Error("retargeted tombstone must fail")
	}

	// Forged attribution — signed by Bob but claims Alice (verifies against
	// Alice's key → fails).
	forged := valid
	forged.DeletedBy = h.alice.UserID()
	if h.alice.VerifyDeleteAuthor(forged) {
		t.Error("forged attribution must fail")
	}

	// Unknown / unpinned sender.
	unknown := valid
	unknown.DeletedBy = "usr_nobody"
	if h.alice.VerifyDeleteAuthor(unknown) {
		t.Error("unknown sender must fail")
	}

	// Zero context.
	zero := valid
	zero.Room = ""
	if h.alice.VerifyDeleteAuthor(zero) {
		t.Error("zero-context tombstone must fail")
	}

	// Multiple contexts.
	multi := valid
	multi.Group = "grp_x"
	if h.alice.VerifyDeleteAuthor(multi) {
		t.Error("multi-context tombstone must fail")
	}

	// Garbage signature.
	garbage := valid
	garbage.Signature = "bm90IGEgcmVhbCBzaWc="
	if h.alice.VerifyDeleteAuthor(garbage) {
		t.Error("garbage signature must fail")
	}
}

// Gate #1 — the live durable receive path (handleInternal "deleted"):
// verify-or-drop before store.DeleteMessage.
func TestGate1_LiveDeletedVerifyOrDrop(t *testing.T) {
	h := newEditVerifyHarness(t)
	if _, err := h.alice.store.InsertMessage(store.StoredMessage{
		ServerOrder: 1, ID: "m1", Sender: h.bobID, Body: "hi", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("seed m1: %v", err)
	}
	if _, err := h.alice.store.InsertMessage(store.StoredMessage{
		ServerOrder: 2, ID: "m2", Sender: h.bobID, Body: "hi2", TS: 2, Room: "general",
	}); err != nil {
		t.Fatalf("seed m2: %v", err)
	}

	// Valid signed delete soft-deletes m1.
	raw, _ := json.Marshal(h.signedTombstone("room", "general", "m1", 1))
	h.alice.handleInternal("deleted", raw)
	if got, _ := h.alice.store.GetMessageByID("m1"); got == nil || !got.Deleted {
		t.Error("valid live delete should soft-delete m1")
	}

	// Forged delete (garbage sig) leaves m2 live.
	forged := h.signedTombstone("room", "general", "m2", 2)
	forged.Signature = "bm90IGEgcmVhbCBzaWc="
	fraw, _ := json.Marshal(forged)
	h.alice.handleInternal("deleted", fraw)
	if got, _ := h.alice.store.GetMessageByID("m2"); got == nil || got.Deleted {
		t.Error("forged live delete must be dropped — m2 stays live")
	}
}

// Gate #1's durable apply is context-scoped (DeleteMessageInContext): a GENUINELY
// Bob-signed tombstone for the WRONG context still verifies (the signature is
// real), but must not mutate a same-id row stored under a different context — and
// the correct-context tombstone still deletes (so we didn't just no-op every
// delete, which is the only regression that would actually matter).
func TestGate1_LiveDeletedWrongContextDoesNotMutate(t *testing.T) {
	h := newEditVerifyHarness(t)
	if _, err := h.alice.store.InsertMessage(store.StoredMessage{
		ServerOrder: 1, ID: "m1", Sender: h.bobID, Body: "keep me", TS: 1, Room: "room_main",
	}); err != nil {
		t.Fatalf("seed m1: %v", err)
	}

	// Genuinely signed, but bound to a GROUP context. VerifyDeleteAuthor passes
	// (real signature); the local row lives in room_main, so the context-scoped
	// apply must find no matching row and no-op.
	wrong := h.signedTombstone("group", "grp_side", "m1", 1)
	if !h.alice.VerifyDeleteAuthor(wrong) {
		t.Fatal("precondition: the wrong-context tombstone must itself verify (it is genuinely signed)")
	}
	wraw, _ := json.Marshal(wrong)
	h.alice.handleInternal("deleted", wraw)
	if got, _ := h.alice.store.GetMessageByID("m1"); got == nil || got.Deleted || got.Body != "keep me" {
		t.Errorf("a verified wrong-context delete must not mutate m1, got %+v", got)
	}

	// The correct-context tombstone still soft-deletes it.
	right := h.signedTombstone("room", "room_main", "m1", 1)
	rraw, _ := json.Marshal(right)
	h.alice.handleInternal("deleted", rraw)
	if got, _ := h.alice.store.GetMessageByID("m1"); got == nil || !got.Deleted {
		t.Errorf("a verified correct-context delete must soft-delete m1, got %+v", got)
	}
}
