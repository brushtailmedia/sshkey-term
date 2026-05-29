package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Regression tests for the history abort lifecycle (history-state-model.md
// step 11 / step 13): a history request that will never get a result — send
// failure, nil client, or a correlated history error — must clear the stuck
// "loading history" state, and a history error must drop its send-queue entry
// so the background retry driver can't resend an abandoned scroll-back request.

func TestAbortHistoryRequest_ClearsLoadAndProbeKeepsRemote(t *testing.T) {
	m := NewMessages()
	m.loadingHistory = true
	m.activeHistoryCorrID = "corr_A"
	m.probeDone = true
	m.remoteState = HistoryAvailable

	m.abortHistoryRequest()

	if m.loadingHistory || m.activeHistoryCorrID != "" || m.probeDone {
		t.Errorf("abort must clear load/corr_id/probe: loading=%v corr=%q probe=%v",
			m.loadingHistory, m.activeHistoryCorrID, m.probeDone)
	}
	if m.remoteState != HistoryAvailable {
		t.Errorf("abort must leave remoteState unchanged (proves nothing), got %d", m.remoteState)
	}
}

// A correlated history error for the active request aborts the visible load and
// drops the queue entry.
func TestHistoryError_ActiveCorrelatedAbortsAndDrops(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_hist_err"})
	a := minimalAppForServerMsg(t)
	a.client = c
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_A"
	a.messages.loadingHistory = true

	c.SendQueue().EnqueueWithID("corr_A", "history", protocol.History{Type: "history", Room: "room_x", CorrID: "corr_A"})
	c.SendQueue().MarkSending("corr_A")

	raw := mustJSONRaw(t, protocol.Error{Type: "error", Code: "invalid_cursor", CorrID: "corr_A"})
	a.handleServerMessage(ServerMsg{Type: "error", Raw: raw})

	if a.messages.loadingHistory || a.messages.activeHistoryCorrID != "" {
		t.Errorf("active history error must abort the load: loading=%v corr=%q",
			a.messages.loadingHistory, a.messages.activeHistoryCorrID)
	}
	if c.SendQueue().Get("corr_A") != nil {
		t.Error("history error must drop the queue entry so it is not retried")
	}
}

// A stale correlated history error (an older request the user moved on from)
// must still drop its own queue entry — so the retry driver can't resend it —
// but must NOT abort the current active request.
func TestHistoryError_StaleCorrelatedDropsButKeepsActive(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_hist_err2"})
	a := minimalAppForServerMsg(t)
	a.client = c
	a.messages.SetContext("room_x", "", "")
	a.messages.activeHistoryCorrID = "corr_new" // the current active request
	a.messages.loadingHistory = true

	c.SendQueue().EnqueueWithID("corr_old", "history", protocol.History{Type: "history", CorrID: "corr_old"})
	c.SendQueue().MarkSending("corr_old")

	raw := mustJSONRaw(t, protocol.Error{Type: "error", Code: "invalid_cursor", CorrID: "corr_old"})
	a.handleServerMessage(ServerMsg{Type: "error", Raw: raw})

	if c.SendQueue().Get("corr_old") != nil {
		t.Error("stale history error must still drop its own queue entry")
	}
	if !a.messages.loadingHistory || a.messages.activeHistoryCorrID != "corr_new" {
		t.Errorf("stale history error must not abort the active request: loading=%v corr=%q",
			a.messages.loadingHistory, a.messages.activeHistoryCorrID)
	}
}

func TestAbandonActiveHistoryRequest_DropsOnlyHistoryEntryAndAborts(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_hist_abandon"})
	a := minimalAppForServerMsg(t)
	a.client = c
	a.messages.activeHistoryCorrID = "corr_A"
	a.messages.loadingHistory = true
	a.messages.probeDone = true

	c.SendQueue().EnqueueWithID("corr_A", "history", protocol.History{Type: "history", CorrID: "corr_A"})
	c.SendQueue().MarkSending("corr_A")

	a.abandonActiveHistoryRequest()

	if c.SendQueue().Get("corr_A") != nil {
		t.Fatal("abandon must drop the matching history queue entry")
	}
	if a.messages.loadingHistory || a.messages.activeHistoryCorrID != "" || a.messages.probeDone {
		t.Fatalf("abandon must abort visible history state: loading=%v corr=%q probe=%v",
			a.messages.loadingHistory, a.messages.activeHistoryCorrID, a.messages.probeDone)
	}
}

func TestAbandonActiveHistoryRequest_DoesNotDropNonHistoryEntry(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_hist_abandon_nonhist"})
	a := minimalAppForServerMsg(t)
	a.client = c
	a.messages.activeHistoryCorrID = "corr_A"
	a.messages.loadingHistory = true

	c.SendQueue().EnqueueWithID("corr_A", "send", protocol.Send{Type: "send", CorrID: "corr_A"})
	c.SendQueue().MarkSending("corr_A")

	a.abandonActiveHistoryRequest()

	if c.SendQueue().Get("corr_A") == nil {
		t.Fatal("abandon must not drop a non-history queue entry")
	}
	if a.messages.loadingHistory || a.messages.activeHistoryCorrID != "" {
		t.Fatalf("abandon must still clear visible history state: loading=%v corr=%q",
			a.messages.loadingHistory, a.messages.activeHistoryCorrID)
	}
}

func TestSwitchMessageContext_AbandonsHistoryAndRunsContextSideEffects(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_hist_switch"})
	a := minimalAppForServerMsg(t)
	a.client = c
	a.input = NewInput()
	a.messages.SetContext("room_a", "", "")
	a.messages.activeHistoryCorrID = "corr_A"
	a.messages.loadingHistory = true
	a.input.textInput.SetValue("draft")
	a.pinnedBar.expanded = true

	c.SendQueue().EnqueueWithID("corr_A", "history", protocol.History{Type: "history", Room: "room_a", CorrID: "corr_A"})
	c.SendQueue().MarkSending("corr_A")

	a.switchMessageContext("room_b", "", "")

	if c.SendQueue().Get("corr_A") != nil {
		t.Fatal("context switch must drop abandoned history queue entry")
	}
	if a.messages.room != "room_b" || a.messages.group != "" || a.messages.dm != "" {
		t.Fatalf("context = room:%q group:%q dm:%q, want room_b only", a.messages.room, a.messages.group, a.messages.dm)
	}
	if got := a.input.Value(); got != "" {
		t.Fatalf("context switch side effects should clear draft, got %q", got)
	}
	if a.pinnedBar.expanded {
		t.Fatal("context switch side effects should collapse the pinned bar")
	}
}
