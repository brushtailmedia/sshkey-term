package client

// Phase 17c Step 5 — send queue state machine tests.

import (
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestQueue_EnqueueAssignsCorrID(t *testing.T) {
	q := NewQueue()
	e := q.Enqueue("send", "payload")
	if err := protocol.ValidateCorrID(e.CorrID); err != nil {
		t.Errorf("generated corr_id invalid: %v", err)
	}
	if e.State != StatePending {
		t.Errorf("initial state = %v, want StatePending", e.State)
	}
	if e.Verb != "send" {
		t.Errorf("verb = %q, want send", e.Verb)
	}
}

func TestQueue_MarkSendingAdvancesState(t *testing.T) {
	q := NewQueue()
	e := q.Enqueue("send", nil)
	q.MarkSending(e.CorrID)
	got := q.Get(e.CorrID)
	if got == nil {
		t.Fatal("entry lost after MarkSending")
	}
	if got.State != StateSending {
		t.Errorf("state = %v, want StateSending", got.State)
	}
	if got.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", got.Attempts)
	}
	if got.FirstSentAt.IsZero() {
		t.Error("FirstSentAt not stamped")
	}
}

func TestQueue_AckRemovesEntryAndFiresCallback(t *testing.T) {
	q := NewQueue()
	var acked bool
	e := q.EnqueueWithCallbacks("send", nil,
		func(*Entry) { acked = true },
		nil,
	)
	q.MarkSending(e.CorrID)
	got := q.Ack(e.CorrID)
	if got == nil {
		t.Fatal("Ack returned nil; want entry")
	}
	if !acked {
		t.Error("OnAcked callback not fired")
	}
	if q.Get(e.CorrID) != nil {
		t.Error("entry not removed from queue")
	}
}

func TestQueue_AckUnknownCorrIDIsNoOp(t *testing.T) {
	q := NewQueue()
	if got := q.Ack("corr_does_not_exist"); got != nil {
		t.Errorf("Ack on unknown corr_id = %v, want nil", got)
	}
}

func TestQueue_ErrorCategoryARetries(t *testing.T) {
	q := NewQueue()
	e := q.Enqueue("send", nil)
	q.MarkSending(e.CorrID)
	err := &protocol.Error{Code: "rate_limited", Message: "slow down"}
	q.Error(e.CorrID, err)

	got := q.Get(e.CorrID)
	if got == nil {
		t.Fatal("entry removed on Category A; want kept for retry")
	}
	if got.State != StatePending {
		t.Errorf("state after Category A = %v, want StatePending", got.State)
	}
	if got.LastError == nil || got.LastError.Code != "rate_limited" {
		t.Error("LastError not recorded")
	}
}

func TestQueue_ErrorCategoryBRetries(t *testing.T) {
	q := NewQueue()
	e := q.Enqueue("send", nil)
	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "invalid_epoch", Message: "behind"})

	got := q.Get(e.CorrID)
	if got == nil {
		t.Fatal("Category B entry removed; want kept for retry after state fix")
	}
	if got.State != StatePending {
		t.Errorf("state after Category B = %v, want StatePending", got.State)
	}
}

func TestQueue_ErrorCategoryCSurfaces(t *testing.T) {
	q := NewQueue()
	var errFired bool
	var gotCategory protocol.ErrorCategory
	e := q.EnqueueWithCallbacks("send", nil,
		nil,
		func(entry *Entry, err *protocol.Error, cat protocol.ErrorCategory) {
			errFired = true
			gotCategory = cat
		},
	)
	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "message_too_large", Message: "oversized"})

	if !errFired {
		t.Error("OnError callback not fired for Category C")
	}
	if gotCategory != protocol.CategoryC {
		t.Errorf("callback category = %v, want C", gotCategory)
	}
	if q.Get(e.CorrID) != nil {
		t.Error("Category C entry not removed from queue")
	}
}

func TestQueue_ErrorCategoryDSurfaces(t *testing.T) {
	q := NewQueue()
	var gotCategory protocol.ErrorCategory
	e := q.EnqueueWithCallbacks("send", nil, nil,
		func(*Entry, *protocol.Error, protocol.ErrorCategory) {
			// Capture (uses variadic signature via outer var)
		},
	)
	// Assign a capturing callback that records the category.
	q.mu.Lock()
	ref := q.entries[e.CorrID]
	ref.OnError = func(entry *Entry, err *protocol.Error, cat protocol.ErrorCategory) {
		gotCategory = cat
	}
	q.mu.Unlock()

	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "denied"})

	if gotCategory != protocol.CategoryD {
		t.Errorf("Category D callback = %v, want D", gotCategory)
	}
	if q.Get(e.CorrID) != nil {
		t.Error("Category D entry not removed")
	}
}

func TestQueue_BudgetExhaustionSurfaces(t *testing.T) {
	q := NewQueue()
	q.maxBudget = 2
	var errFired bool
	e := q.EnqueueWithCallbacks("send", nil, nil,
		func(*Entry, *protocol.Error, protocol.ErrorCategory) { errFired = true },
	)
	// Attempt 1 — Category A retry keeps in queue.
	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "rate_limited"})
	if q.Get(e.CorrID) == nil {
		t.Fatal("entry lost after first Category A")
	}
	// Attempt 2 — budget exhausts.
	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "rate_limited"})
	if !errFired {
		t.Error("OnError not fired after budget exhaustion")
	}
	if q.Get(e.CorrID) != nil {
		t.Error("budget-exhausted entry not removed from queue")
	}
}

func TestQueue_PendingCountTracksInFlight(t *testing.T) {
	q := NewQueue()
	e1 := q.Enqueue("send", nil) // Pending
	e2 := q.Enqueue("edit", nil) // Pending
	q.MarkSending(e2.CorrID)     // Sending
	e3 := q.Enqueue("react", nil)
	q.MarkSending(e3.CorrID)
	q.Ack(e3.CorrID) // Acked (removed)
	_ = e1
	if got := q.PendingCount(); got != 2 {
		t.Errorf("PendingCount = %d, want 2 (one Pending + one Sending)", got)
	}
}

func TestQueue_SweepTimeoutsTransitionsStaleSending(t *testing.T) {
	q := NewQueue()
	q.ackTimeout = 10 * time.Millisecond

	e := q.Enqueue("send", nil)
	q.MarkSending(e.CorrID)

	// Rewind LastSentAt so the entry looks stale.
	q.mu.Lock()
	q.entries[e.CorrID].LastSentAt = time.Now().Add(-1 * time.Second)
	q.mu.Unlock()

	timedOut := q.SweepTimeouts()
	if len(timedOut) != 1 {
		t.Fatalf("SweepTimeouts returned %d, want 1", len(timedOut))
	}
	if timedOut[0].State != StateTimedOut {
		t.Errorf("state = %v, want StateTimedOut", timedOut[0].State)
	}
}

func TestQueue_SweepTimeoutsIgnoresFresh(t *testing.T) {
	q := NewQueue()
	q.ackTimeout = 30 * time.Second

	e := q.Enqueue("send", nil)
	q.MarkSending(e.CorrID)

	if got := q.SweepTimeouts(); len(got) != 0 {
		t.Errorf("fresh send timed out: %d entries", len(got))
	}
}

func TestQueue_DropRemovesEntry(t *testing.T) {
	q := NewQueue()
	e := q.Enqueue("send", nil)
	q.Drop(e.CorrID)
	if q.Get(e.CorrID) != nil {
		t.Error("entry still in queue after Drop")
	}
}

func TestQueue_AckViaSuccessBroadcastClearsEntry(t *testing.T) {
	// End-to-end shape: the generic readLoop Ack fires on any received
	// message carrying corr_id. Simulates a success broadcast
	// (protocol.Edited, Reaction, Deleted, etc.) — the payload type
	// doesn't matter to the Ack path; only the corr_id does.
	q := NewQueue()
	corrID := "corr_ABCDEFGHIJKLMNOPQRSTU"
	q.EnqueueWithID(corrID, "edit", nil)
	q.MarkSending(corrID)

	if q.Get(corrID) == nil {
		t.Fatal("entry missing after enqueue")
	}

	// Simulate what the client's readLoop does on receive.
	q.Ack(corrID)

	if q.Get(corrID) != nil {
		t.Error("entry still present after Ack — expected removal")
	}
}

func TestQueue_UnknownCategoryTreatedAsError(t *testing.T) {
	// Server may return a code this client version doesn't know. The
	// conservative default is A-default, but our CategoryForCode
	// returns CategoryUnknown for unknown codes — the queue treats
	// that as permanent/surface (CategoryC path) so the user sees
	// something rather than getting silent retries forever.
	q := NewQueue()
	var errFired bool
	e := q.EnqueueWithCallbacks("send", nil, nil,
		func(*Entry, *protocol.Error, protocol.ErrorCategory) { errFired = true },
	)
	q.MarkSending(e.CorrID)
	q.Error(e.CorrID, &protocol.Error{Code: "future_unknown_code_xyz"})

	if !errFired {
		t.Error("unknown-code error did not fire OnError")
	}
	if q.Get(e.CorrID) != nil {
		t.Error("unknown-code entry not removed")
	}
}
