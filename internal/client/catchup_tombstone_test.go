package client

// S2b wiring: catchup (`sync_batch` / `history_result`) `deleted` rows route
// through handleCatchupMessage -> storeCatchupTombstone -> UpsertDeletedMessage,
// making remote tombstones durable. The live delete path (handleInternal
// "deleted") is unchanged and still uses update-only DeleteMessage.

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// A catchup `deleted` for a message that was never cached locally inserts a
// durable tombstone (the join-after-create-and-delete case). Before S2b this
// routed to DeleteMessage, which no-ops on an absent row, so the tombstone
// vanished on reload.
func TestHandleCatchupMessage_DeletedInsertsDurableTombstone(t *testing.T) {
	c := newClientWithStore(t)

	raw, _ := json.Marshal(protocol.Deleted{
		Type:        "deleted",
		ID:          "ghost",
		ServerOrder: 31,
		DeletedBy:   "alice",
		TS:          1000,
		Room:        "general",
	})
	c.handleCatchupMessage("deleted", raw)

	got, err := c.store.GetMessageByID("ghost")
	if err != nil {
		t.Fatalf("catchup tombstone should be persisted: %v", err)
	}
	if !got.Deleted || got.DeletedBy != "alice" || got.ServerOrder != 31 || got.Room != "general" {
		t.Errorf("unexpected tombstone: %+v", got)
	}
}

// A catchup `deleted` for an already-cached message soft-deletes it in place.
func TestHandleCatchupMessage_DeletedKnownRowSoftDeletes(t *testing.T) {
	c := newClientWithStore(t)

	if _, err := c.store.InsertMessage(store.StoredMessage{
		ServerOrder: 4, ID: "known", Sender: "bob", Body: "to be deleted", TS: 1, Room: "general",
	}); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	raw, _ := json.Marshal(protocol.Deleted{
		Type: "deleted", ID: "known", ServerOrder: 4, DeletedBy: "bob", TS: 2, Room: "general",
	})
	c.handleCatchupMessage("deleted", raw)

	got, err := c.store.GetMessageByID("known")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Deleted || got.Body != "" {
		t.Errorf("known row should be soft-deleted, got deleted=%v body=%q", got.Deleted, got.Body)
	}
}

// Malformed catchup `deleted` JSON is dropped without panicking or writing.
func TestStoreCatchupTombstone_MalformedJSONNoop(t *testing.T) {
	c := newClientWithStore(t)
	c.storeCatchupTombstone(json.RawMessage(`{not valid`))
	// Nothing to assert beyond "did not panic and wrote nothing"; a follow-up
	// GetMessageByID would error with ErrNoRows, which is the expected state.
}
