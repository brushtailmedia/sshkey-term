package client

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func TestHandleInternalDMList_HiddenPurgesMessagesAndCachesState(t *testing.T) {
	c := newClientWithRoomStore(t)

	if err := c.store.StoreDM("dm_ab", "alice", "bob"); err != nil {
		t.Fatalf("StoreDM: %v", err)
	}
	if _, err := c.store.InsertMessage(store.StoredMessage{
		ID: "msg_dm_1", Sender: "alice", Body: "hi", TS: 1, DM: "dm_ab",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	raw, _ := json.Marshal(protocol.DMList{
		Type: "dm_list",
		DMs: []protocol.DMInfo{{
			ID:              "dm_ab",
			Members:         []string{"alice", "bob"},
			HiddenForCaller: true,
			LeftAtForCaller: 1700001111,
		}},
	})
	c.handleInternal("dm_list", raw)

	if !c.store.IsDMHidden("dm_ab") {
		t.Fatal("dm_ab should be hidden after dm_list hidden_for_caller=true")
	}
	if got := c.store.GetDMLeftAt("dm_ab"); got != 1700001111 {
		t.Fatalf("left_at = %d, want 1700001111", got)
	}
	msgs, err := c.store.GetDMMessages("dm_ab", 100)
	if err != nil {
		t.Fatalf("GetDMMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("hidden DM should have local messages purged, got %d", len(msgs))
	}
}

func TestHandleInternalDMList_VisibleKeepsCutoffButClearsHidden(t *testing.T) {
	c := newClientWithRoomStore(t)

	if err := c.store.StoreDM("dm_ab", "alice", "bob"); err != nil {
		t.Fatalf("StoreDM: %v", err)
	}
	if err := c.store.MarkDMLeft("dm_ab", 123); err != nil {
		t.Fatalf("MarkDMLeft: %v", err)
	}

	raw, _ := json.Marshal(protocol.DMList{
		Type: "dm_list",
		DMs: []protocol.DMInfo{{
			ID:              "dm_ab",
			Members:         []string{"alice", "bob"},
			HiddenForCaller: false,
			LeftAtForCaller: 777,
		}},
	})
	c.handleInternal("dm_list", raw)

	if c.store.IsDMHidden("dm_ab") {
		t.Fatal("dm_ab should be visible after dm_list hidden_for_caller=false")
	}
	if got := c.store.GetDMLeftAt("dm_ab"); got != 777 {
		t.Fatalf("left_at = %d, want 777", got)
	}
}
