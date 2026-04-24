package client

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func assertNoReactionsForMessage(t *testing.T, c *Client, msgID string) {
	t.Helper()
	reactions, err := c.store.GetReactionsForMessages([]string{msgID})
	if err != nil {
		t.Fatalf("GetReactionsForMessages: %v", err)
	}
	if len(reactions) != 0 {
		t.Fatalf("expected reactions to be cleared for %s, got %d", msgID, len(reactions))
	}
}

func TestDispatch_StoreEditedRoomMessage_UpdatesDBAndClearsReactions(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dispatch_room"
	const room = "rm_dispatch"
	const epoch = int64(1)
	key := h.seedRoomEpochKey(t, room, epoch)
	h.seedOriginalRoomMessage(t, msgID, room, "original body")
	if err := h.alice.store.InsertReaction(store.StoredReaction{
		ReactionID: "react_room_1",
		MessageID:  msgID,
		User:       "usr_alice",
		Emoji:      "👍",
		TS:         10,
	}); err != nil {
		t.Fatalf("InsertReaction: %v", err)
	}

	edited := h.buildSignedRoomEdit(t, msgID, room, epoch, key, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.handleInternal("edited", raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Fatalf("body after edited dispatch = %q, want %q", got, "edited body")
	}
	assertNoReactionsForMessage(t, h.alice, msgID)
}

func TestDispatch_StoreEditedGroupMessage_UpdatesDBAndClearsReactions(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dispatch_group"
	const group = "group_dispatch"
	h.seedOriginalGroupMessage(t, msgID, group, "original body")
	if err := h.alice.store.InsertReaction(store.StoredReaction{
		ReactionID: "react_group_1",
		MessageID:  msgID,
		User:       "usr_alice",
		Emoji:      "🔥",
		TS:         10,
	}); err != nil {
		t.Fatalf("InsertReaction: %v", err)
	}

	edited := h.buildSignedGroupEdit(t, msgID, group, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.handleInternal("group_edited", raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Fatalf("body after group_edited dispatch = %q, want %q", got, "edited body")
	}
	assertNoReactionsForMessage(t, h.alice, msgID)
}

func TestDispatch_StoreEditedDMMessage_UpdatesDBAndClearsReactions(t *testing.T) {
	h := newEditVerifyHarness(t)

	const msgID = "msg_dispatch_dm"
	const dmID = "dm_dispatch"
	h.seedOriginalDMMessage(t, msgID, dmID, "original body")
	if err := h.alice.store.InsertReaction(store.StoredReaction{
		ReactionID: "react_dm_1",
		MessageID:  msgID,
		User:       "usr_alice",
		Emoji:      "✅",
		TS:         10,
	}); err != nil {
		t.Fatalf("InsertReaction: %v", err)
	}

	edited := h.buildSignedDMEdit(t, msgID, dmID, "edited body")
	raw, _ := json.Marshal(edited)
	h.alice.handleInternal("dm_edited", raw)

	if got := h.getBodyByID(t, msgID); got != "edited body" {
		t.Fatalf("body after dm_edited dispatch = %q, want %q", got, "edited body")
	}
	assertNoReactionsForMessage(t, h.alice, msgID)
}
