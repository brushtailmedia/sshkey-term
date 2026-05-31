package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func TestApp_EmojiSelectedMsg_DMSendsReaction(t *testing.T) {
	a, _ := newEditAppHarness(t)

	target := DisplayMessage{
		ID:   "msg_dm_react",
		DM:   "dm_react",
		From: "bob",
		Body: "hello",
	}

	model, _ := a.Update(EmojiSelectedMsg{Emoji: "👍", Target: target})
	updated := model.(App)

	// No DM members are seeded in this harness; reaching SendDMReaction
	// should fail in wrapKeyForDM with this exact error shape.
	if !strings.Contains(updated.statusBar.errorMsg, "no members for DM dm_react") {
		t.Fatalf("status error = %q, want DM send-path error", updated.statusBar.errorMsg)
	}
}

func TestApp_UnpinActionSendsOnlyForPinnedRoomMessage(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_pin", "", "")
	a.pinnedBar.SetPins("room_pin", []string{"msg_pinned"}, []DisplayMessage{{ID: "msg_pinned"}})

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MessageAction{
		Action: "unpin",
		Msg:    DisplayMessage{ID: "msg_pinned", Room: "room_pin"},
	})
	updated := model.(App)
	if updated.statusBar.errorMsg != "" {
		t.Fatalf("unexpected status error: %q", updated.statusBar.errorMsg)
	}

	var unpin protocol.Unpin
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &unpin); err != nil {
		t.Fatalf("decode unpin frame: %v\n%s", err, out.String())
	}
	if unpin.Type != "unpin" || unpin.Room != "room_pin" || unpin.ID != "msg_pinned" {
		t.Fatalf("unpin frame = %+v, want room_pin/msg_pinned", unpin)
	}
}

func TestApp_UnpinActionNoopsForUnpinnedRoomMessage(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_pin", "", "")

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MessageAction{
		Action: "unpin",
		Msg:    DisplayMessage{ID: "msg_unpinned", Room: "room_pin"},
	})
	updated := model.(App)
	if out.Len() != 0 {
		t.Fatalf("unpinned message should not send unpin frame, got %q", out.String())
	}
	if !strings.Contains(updated.statusBar.errorMsg, "not pinned") {
		t.Fatalf("status error = %q, want not pinned", updated.statusBar.errorMsg)
	}
}

func TestApp_PinActionNoopsForAlreadyPinnedRoomMessage(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_pin", "", "")
	a.pinnedBar.SetPins("room_pin", []string{"msg_pinned"}, []DisplayMessage{{ID: "msg_pinned"}})

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MessageAction{
		Action: "pin",
		Msg:    DisplayMessage{ID: "msg_pinned", Room: "room_pin"},
	})
	updated := model.(App)
	if out.Len() != 0 {
		t.Fatalf("already-pinned message should not send pin/unpin frame, got %q", out.String())
	}
	if !strings.Contains(updated.statusBar.errorMsg, "already pinned") {
		t.Fatalf("status error = %q, want already pinned", updated.statusBar.errorMsg)
	}
}

func TestApp_ServerReaction_ReconcilesFromStore(t *testing.T) {
	clearReactionTracker()

	a, st := newEditAppHarness(t)
	if _, err := st.InsertMessage(store.StoredMessage{ServerOrder: 1,
		ID:     "msg_live_react",
		Sender: "usr_bob",
		Body:   "hello",
		TS:     1,
		Room:   "room_live",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := st.InsertReaction(store.StoredReaction{
		ReactionID: "react_live",
		MessageID:  "msg_live_react",
		User:       "usr_bob",
		Emoji:      "👍",
		TS:         2,
	}); err != nil {
		t.Fatalf("InsertReaction: %v", err)
	}

	// Loaded message row in view.
	a.messages.messages = []DisplayMessage{
		{
			ID:     "msg_live_react",
			FromID: "usr_bob",
			From:   "bob",
			Body:   "hello",
			TS:     1,
			Room:   "room_live",
		},
	}

	// Deliberately undecryptable live payload: AddReactionDecrypted would
	// otherwise fall back to "?" for this frame. The app's reaction path now
	// reconciles from store so the rendered state stays canonical immediately.
	raw, _ := json.Marshal(protocol.Reaction{
		Type:       "reaction",
		ReactionID: "react_live",
		ID:         "msg_live_react",
		Room:       "room_live",
		User:       "usr_bob",
		TS:         2,
		Epoch:      999,
		Payload:    "not-base64",
		Signature:  "sig",
	})

	model, _ := a.Update(ServerMsg{Type: "reaction", Raw: raw})
	updated := model.(App)

	counts := updated.messages.messages[0].DisplayReactions()
	if counts["👍"] != 1 {
		t.Fatalf("reactions = %v, want 👍 count of 1", counts)
	}
	if counts["?"] != 0 {
		t.Fatalf("reactions = %v, unexpected fallback '?' reaction after reconciliation", counts)
	}
}
