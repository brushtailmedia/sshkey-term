package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestSyncBatchReplayDoesNotIncrementUnread(t *testing.T) {
	tests := []struct {
		name       string
		unreadMsg  protocol.Unread
		messageRaw json.RawMessage
		key        string
	}{
		{
			name:      "room",
			unreadMsg: protocol.Unread{Type: "unread", Room: "room_support", Count: 4},
			messageRaw: mustJSONRaw(t, protocol.Message{
				Type: "message",
				ID:   "msg_room_1",
				From: "alice",
				Room: "room_support",
				TS:   1,
			}),
			key: "room_support",
		},
		{
			name:      "group",
			unreadMsg: protocol.Unread{Type: "unread", Group: "group_team", Count: 5},
			messageRaw: mustJSONRaw(t, protocol.GroupMessage{
				Type:  "group_message",
				ID:    "msg_group_1",
				From:  "alice",
				Group: "group_team",
				TS:    1,
			}),
			key: "group_team",
		},
		{
			name:      "dm",
			unreadMsg: protocol.Unread{Type: "unread", DM: "dm_alice", Count: 6},
			messageRaw: mustJSONRaw(t, protocol.DM{
				Type: "dm",
				ID:   "msg_dm_1",
				From: "alice",
				DM:   "dm_alice",
				TS:   1,
			}),
			key: "dm_alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := minimalAppForServerMsg(t)

			unreadRaw := mustJSONRaw(t, tt.unreadMsg)
			a.handleServerMessage(ServerMsg{Type: "unread", Raw: unreadRaw})

			batchRaw := mustJSONRaw(t, protocol.SyncBatch{
				Type:     "sync_batch",
				Messages: []json.RawMessage{tt.messageRaw},
			})
			a.handleServerMessage(ServerMsg{Type: "sync_batch", Raw: batchRaw})

			if got := a.sidebar.unread[tt.key]; got != tt.unreadMsg.Count {
				t.Fatalf("unread[%q] = %d, want %d", tt.key, got, tt.unreadMsg.Count)
			}
		})
	}
}

func TestLiveMessageStillIncrementsUnreadAfterSyncReplay(t *testing.T) {
	a := minimalAppForServerMsg(t)

	batchRaw := mustJSONRaw(t, protocol.SyncBatch{
		Type: "sync_batch",
		Messages: []json.RawMessage{
			mustJSONRaw(t, protocol.Message{
				Type: "message",
				ID:   "msg_sync_1",
				From: "alice",
				Room: "room_support",
				TS:   1,
			}),
		},
	})
	a.handleServerMessage(ServerMsg{Type: "sync_batch", Raw: batchRaw})
	if got := a.sidebar.unread["room_support"]; got != 0 {
		t.Fatalf("after sync replay unread = %d, want 0", got)
	}

	liveRaw := mustJSONRaw(t, protocol.Message{
		Type: "message",
		ID:   "msg_live_1",
		From: "alice",
		Room: "room_support",
		TS:   2,
	})
	a.handleServerMessage(ServerMsg{Type: "message", Raw: liveRaw})

	if got := a.sidebar.unread["room_support"]; got != 1 {
		t.Fatalf("after live message unread = %d, want 1", got)
	}
}

func mustJSONRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
