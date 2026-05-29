package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Regression tests for the Incoming Result Guard (history-state-model.md): a
// history_result is applied to the visible pane only if it matches both the
// active request's corr_id and the active context. The corr_id is load-bearing
// for the A->B->A race that tuple matching alone cannot catch. Persistence is
// handled by the client layer (handleHistoryKeys), so a non-matching result is
// simply not shown — it must not mutate the current context's state.

func TestHistoryResult_MatchingAppliesAndReleasesCorrID(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_A"
	a.messages.loadingHistory = true

	m1 := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m1", From: "alice", Room: "room_x", TS: 1})
	raw := mustJSONRaw(t, protocol.HistoryResult{
		Type: "history_result", Room: "room_x",
		Messages: []json.RawMessage{m1}, HasMore: true, CorrID: "corr_A",
	})
	a.handleServerMessage(ServerMsg{Type: "history_result", Raw: raw})

	if len(a.messages.messages) != 1 {
		t.Fatalf("matching result must be applied: got %d messages", len(a.messages.messages))
	}
	if a.messages.activeHistoryCorrID != "" {
		t.Errorf("ownership must be released after applying: activeHistoryCorrID=%q", a.messages.activeHistoryCorrID)
	}
	if a.messages.remoteState != HistoryAvailable || !a.messages.hintVisible || a.messages.loadingHistory {
		t.Errorf("has_more=true must set Available/hint/!loading: state=%d hint=%v loading=%v",
			a.messages.remoteState, a.messages.hintVisible, a.messages.loadingHistory)
	}
}

// A->B->A: a late result carrying the *old* request's corr_id must not render
// into — or clear the loading state of — the current (re-entered) context.
func TestHistoryResult_StaleCorrIDIgnored(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_new"
	a.messages.loadingHistory = true

	m := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m_old", From: "alice", Room: "room_x", TS: 1})
	raw := mustJSONRaw(t, protocol.HistoryResult{
		Type: "history_result", Room: "room_x",
		Messages: []json.RawMessage{m}, HasMore: false, CorrID: "corr_old", // stale
	})
	a.handleServerMessage(ServerMsg{Type: "history_result", Raw: raw})

	if len(a.messages.messages) != 0 {
		t.Errorf("stale-corr_id result must not render into the pane: got %d messages", len(a.messages.messages))
	}
	if a.messages.activeHistoryCorrID != "corr_new" {
		t.Errorf("stale result must not release the active corr_id: got %q", a.messages.activeHistoryCorrID)
	}
	if !a.messages.loadingHistory {
		t.Error("stale result must not clear the current context's loading state")
	}
}

// A result whose context tuple does not match the active context (the user
// switched rooms while the request was in flight) must not render into the
// current pane even if its corr_id were to collide.
func TestHistoryResult_WrongContextIgnored(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_A"

	m := mustJSONRaw(t, protocol.Message{Type: "message", ID: "m1", From: "alice", Room: "room_y", TS: 1})
	raw := mustJSONRaw(t, protocol.HistoryResult{
		Type: "history_result", Room: "room_y", // different room
		Messages: []json.RawMessage{m}, HasMore: false, CorrID: "corr_A",
	})
	a.handleServerMessage(ServerMsg{Type: "history_result", Raw: raw})

	if len(a.messages.messages) != 0 {
		t.Errorf("wrong-context result must not render into the active pane: got %d messages", len(a.messages.messages))
	}
	if a.messages.activeHistoryCorrID != "corr_A" {
		t.Errorf("wrong-context result must not release ownership: got %q", a.messages.activeHistoryCorrID)
	}
}
