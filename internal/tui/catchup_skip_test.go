package tui

// F7 Phase D (§5e.10) — the sync/history TUI display must SKIP an undecryptable
// room message instead of rendering a transient "(encrypted)" ghost, because
// persistence drops that same row (storeRoomMessageFromCatchup). Otherwise the
// row "appears then disappears" on the next LoadFromDB. The LIVE path keeps
// rendering "(encrypted)" (a missing-current-key signal, matching live
// store-empty behavior).

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestAddRoomMessage_SyncSkipsUndecryptable(t *testing.T) {
	c := client.New(client.Config{}) // no epoch keys → decrypt fails

	msg := protocol.Message{
		ID:      "msg_sync_undec",
		From:    "bob_id",
		Room:    "rm_test",
		Epoch:   3,
		Payload: "AAAA", // unreachable — key lookup fails first
		TS:      1000,
	}

	// Sync/history replay (fromSync=true): undecryptable row is skipped.
	mSync := NewMessages()
	mSync.room = "rm_test"
	mSync.AddRoomMessage(msg, c, true)
	if len(mSync.messages) != 0 {
		t.Errorf("sync replay must skip undecryptable row; len = %d, want 0", len(mSync.messages))
	}

	// Live (fromSync=false): undecryptable row is still rendered "(encrypted)".
	mLive := NewMessages()
	mLive.room = "rm_test"
	mLive.AddRoomMessage(msg, c, false)
	if len(mLive.messages) != 1 {
		t.Fatalf("live must render undecryptable row; len = %d, want 1", len(mLive.messages))
	}
	if mLive.messages[0].Body != "(encrypted)" {
		t.Errorf("live undecryptable body = %q, want (encrypted)", mLive.messages[0].Body)
	}
}

func TestBuildDisplayMsg_SkipsUndecryptable(t *testing.T) {
	c := client.New(client.Config{}) // no epoch keys → decrypt fails
	m := NewMessages()

	msg := protocol.Message{
		ID:      "h1",
		From:    "bob_id",
		Room:    "rm_test",
		Epoch:   3,
		Payload: "AAAA",
		TS:      1000,
	}
	if _, ok := m.buildDisplayMsg(msg, c); ok {
		t.Error("buildDisplayMsg must return ok=false for an undecryptable history row")
	}
}
