package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestHistoryResult_RendersDeletedTombstone is the regression test for the S2b
// render half. A history_result can carry `deleted` tombstone rows for messages
// this client never cached (created+deleted before joining). The TUI builder
// previously handled only message/group_message/dm and silently dropped
// `deleted` rows, so a remote scrollback tombstone was invisible on first fetch.
// It must now render as a generic tombstone — no inferred author, no body.
func TestHistoryResult_RendersDeletedTombstone(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_hist1" // the in-flight request this result answers

	// Wire order is oldest-first (as the S3 server emits): an older tombstone for
	// a message this client never saw, then a live message.
	tomb := mustJSONRaw(t, protocol.Deleted{Type: "deleted", ID: "m_gone", DeletedBy: "carol", Room: "room_x", TS: 100, ServerOrder: 7})
	live := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m_live", From: "alice", Room: "room_x", TS: 200, ServerOrder: 8})

	raw := mustJSONRaw(t, protocol.HistoryResult{
		Type:     "history_result",
		Room:     "room_x",
		Messages: []json.RawMessage{tomb, live},
		HasMore:  false,
		CorrID:   "corr_hist1",
	})
	a.handleServerMessage(ServerMsg{Type: "history_result", Raw: raw})

	if got := len(a.messages.messages); got != 2 {
		t.Fatalf("expected 2 history rows (live + tombstone), got %d", got)
	}
	// Oldest-first: the older tombstone is at the top.
	tombstone := a.messages.messages[0]
	if tombstone.ID != "m_gone" {
		t.Fatalf("expected tombstone first (oldest), got %q", tombstone.ID)
	}
	if !tombstone.Deleted {
		t.Error("deleted history row should render as a tombstone (Deleted=true)")
	}
	if tombstone.DeletedBy != "carol" {
		t.Errorf("tombstone DeletedBy = %q, want carol", tombstone.DeletedBy)
	}
	if tombstone.Body != "" {
		t.Errorf("tombstone body should be empty, got %q", tombstone.Body)
	}
	// Server-side deleted history carries the deleter, not the original author,
	// so the tombstone must not infer authorship.
	if tombstone.FromID != "" || tombstone.From != "" {
		t.Errorf("tombstone should not infer an author, got FromID=%q From=%q", tombstone.FromID, tombstone.From)
	}
}
