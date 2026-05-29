package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestHistoryResult_PrependsServerOrderAsIs locks the S3 final ordering model:
// the server now emits history oldest-first (handleHistory pages server_order
// and reverses at the store boundary), so the term history_result handler
// prepends the batch verbatim — it must NOT reverse it. A reintroduced TUI
// reversal would flip this 2-element oldest-first batch to newest-first and a
// double-reverse would silently restore the original reverse-chronological bug.
func TestHistoryResult_PrependsServerOrderAsIs(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_hist1" // the in-flight request this result answers

	// Wire order is oldest-first, exactly as the S3 server now emits it.
	older := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m_old", From: "alice", Room: "room_x", TS: 100, ServerOrder: 1})
	newer := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m_new", From: "alice", Room: "room_x", TS: 200, ServerOrder: 2})

	raw := mustJSONRaw(t, protocol.HistoryResult{
		Type:     "history_result",
		Room:     "room_x",
		Messages: []json.RawMessage{older, newer},
		HasMore:  false,
		CorrID:   "corr_hist1",
	})
	a.handleServerMessage(ServerMsg{Type: "history_result", Raw: raw})

	if got := len(a.messages.messages); got != 2 {
		t.Fatalf("expected 2 prepended history messages, got %d", got)
	}
	// The pane preserves the server's oldest-first order: oldest at the top.
	if a.messages.messages[0].ID != "m_old" || a.messages.messages[1].ID != "m_new" {
		t.Errorf("history prepend order = [%s, %s], want [m_old, m_new] (server oldest-first, prepended as-is)",
			a.messages.messages[0].ID, a.messages.messages[1].ID)
	}
}
