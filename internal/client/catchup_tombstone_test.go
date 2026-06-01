package client

// S2b wiring: catchup (`sync_batch` / `history_result`) `deleted` rows route
// through handleCatchupMessage -> storeCatchupTombstone -> UpsertDeletedMessage,
// making remote tombstones durable. The live delete path (handleInternal
// "deleted") is unchanged and still uses update-only DeleteMessage.
//
// F6: storeCatchupTombstone now verify-or-drops via VerifyDeleteAuthor, so these
// tests build genuinely-signed tombstones (Bob is the deleter; the harness pins
// Bob's key so pubKeyForUser resolves).

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// signedTombstone builds a Bob-signed `deleted` frame binding (kind, contextID,
// msgID), verifiable on receipt via VerifyDeleteAuthor.
func (h *editVerifyHarness) signedTombstone(kind, contextID, msgID string, serverOrder int64) protocol.Deleted {
	sig := crypto.SignDelete(h.bobPriv, kind, contextID, msgID)
	d := protocol.Deleted{
		Type:        "deleted",
		ID:          msgID,
		DeletedBy:   h.bobID,
		TS:          2,
		ServerOrder: serverOrder,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}
	switch kind {
	case "room":
		d.Room = contextID
	case "group":
		d.Group = contextID
	case "dm":
		d.DM = contextID
	}
	return d
}

// A catchup `deleted` for a message that was never cached locally inserts a
// durable tombstone (the join-after-create-and-delete case). Before S2b this
// routed to DeleteMessage, which no-ops on an absent row, so the tombstone
// vanished on reload.
func TestHandleCatchupMessage_DeletedInsertsDurableTombstone(t *testing.T) {
	h := newEditVerifyHarness(t)

	raw, _ := json.Marshal(h.signedTombstone("room", "general", "ghost", 31))
	h.alice.handleCatchupMessage("deleted", raw)

	got, err := h.alice.store.GetMessageByID("ghost")
	if err != nil {
		t.Fatalf("catchup tombstone should be persisted: %v", err)
	}
	if !got.Deleted || got.DeletedBy != h.bobID || got.ServerOrder != 31 || got.Room != "general" {
		t.Errorf("unexpected tombstone: %+v", got)
	}
}

// A catchup `deleted` for an already-cached message soft-deletes it in place.
func TestHandleCatchupMessage_DeletedKnownRowSoftDeletes(t *testing.T) {
	h := newEditVerifyHarness(t)

	if _, err := h.alice.store.InsertMessage(store.StoredMessage{
		ServerOrder: 4, ID: "known", Sender: h.bobID, Body: "to be deleted", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	raw, _ := json.Marshal(h.signedTombstone("room", "general", "known", 4))
	h.alice.handleCatchupMessage("deleted", raw)

	got, err := h.alice.store.GetMessageByID("known")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Deleted || got.Body != "" {
		t.Errorf("known row should be soft-deleted, got deleted=%v body=%q", got.Deleted, got.Body)
	}
}

// A wrong-context tombstone (signed for a context the message isn't in) is
// dropped by Gate #2 — no durable mutation.
func TestStoreCatchupTombstone_WrongContextDropped(t *testing.T) {
	h := newEditVerifyHarness(t)

	if _, err := h.alice.store.InsertMessage(store.StoredMessage{
		ServerOrder: 7, ID: "rm_msg", Sender: h.bobID, Body: "live", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Bob signs a delete for the message in a GROUP context, but it lives in a
	// room — the signature won't verify against a room-context reconstruction,
	// so the tombstone is dropped and the row stays live.
	bad := h.signedTombstone("group", "grp_x", "rm_msg", 7)
	raw, _ := json.Marshal(bad)
	h.alice.handleCatchupMessage("deleted", raw)

	got, err := h.alice.store.GetMessageByID("rm_msg")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Deleted {
		t.Error("a wrong-context tombstone must not soft-delete the row")
	}
}

// Malformed catchup `deleted` JSON is dropped without panicking or writing.
func TestStoreCatchupTombstone_MalformedJSONNoop(t *testing.T) {
	c := newClientWithStore(t)
	c.storeCatchupTombstone(json.RawMessage(`{not valid`))
	// Nothing to assert beyond "did not panic and wrote nothing"; a follow-up
	// GetMessageByID would error with ErrNoRows, which is the expected state.
}
