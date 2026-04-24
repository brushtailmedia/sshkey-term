package client

// Phase 17c Step 5 — client-side send queue.
//
// Purpose: replace fire-and-forget with an ack-tracked queue that
// retries Category A (transient) errors transparently and surfaces
// Category C/D (permanent) errors to the UI. Lives in memory only —
// a clean app close or crash loses any pending-but-unacked entries,
// which matches the design decision in refactor_plan.md §Phase 17c:
// "terminal chat apps don't persist send queues across close".
//
// State machine per entry:
//
//   pending  — sitting in the queue, no in-flight send
//   sending  — send attempted, waiting for ack (success broadcast)
//              or error response
//   acked    — server confirmed; entry dropped on next sweep
//   error    — permanent Category C/D failure surfaced to UI
//   timeout  — no ack arrived within ackTimeout; retry or surface
//
// Correlation: each entry has a unique corr_id. The send envelope
// carries that corr_id; the server echoes it in error responses and
// in the authoritative success broadcast (for send/send_group/send_dm)
// so the client can match ack → entry. For verbs that don't have a
// natural success broadcast (delete, react, unreact, pin, unpin,
// refresh verbs), the entry is dropped after one successful round
// trip of the server's reply (or error).
//
// Thread-safety: all public methods take the mutex. The ack/error
// callbacks are intended to be invoked from the receive goroutine.

import (
	"sync"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// EntryState is the send-queue state machine.
type EntryState int

const (
	StatePending EntryState = iota
	StateSending
	StateAcked
	StateError
	StateTimedOut
)

// Entry is one queued outbound request. Clients retain a stable
// reference to its CorrID to look up state; the rest of the fields
// are internal.
type Entry struct {
	CorrID      string
	Verb        string // "send", "edit", "react", etc. — for retry + category routing
	Payload     any    // the typed protocol struct (protocol.Send, protocol.Edit, ...)
	State       EntryState
	Attempts    int       // how many times we've actually sent
	FirstSentAt time.Time // first send time (for timeout)
	LastSentAt  time.Time // most recent send time (for backoff)
	LastError   *protocol.Error
	// OnAcked and OnError are optional UI callbacks. They run synchronously
	// under the queue mutex — keep them fast.
	OnAcked func(entry *Entry)
	OnError func(entry *Entry, err *protocol.Error, category protocol.ErrorCategory)
}

// Queue is the client's in-memory send queue.
type Queue struct {
	mu        sync.Mutex
	entries   map[string]*Entry // keyed by CorrID
	maxBudget int               // max retries per entry before surfacing (Category A)
	// ackTimeout is the wall-clock duration after which a "sending"
	// entry without ack is treated as timed out. Category A timeouts
	// route through the retry path; other categories surface.
	ackTimeout time.Duration
}

// NewQueue constructs a send queue with sensible defaults.
func NewQueue() *Queue {
	return &Queue{
		entries:    make(map[string]*Entry),
		maxBudget:  5,
		ackTimeout: 30 * time.Second,
	}
}

// Enqueue creates a new entry in StatePending with a freshly-generated
// CorrID. Returns the entry (caller typically just keeps the CorrID).
//
// Callers: use verb to route retry / category handling. Payload is
// the typed protocol struct that will be sent; the caller has already
// encrypted it and is just holding a serializable object for resend.
func (q *Queue) Enqueue(verb string, payload any) *Entry {
	return q.EnqueueWithID(protocol.GenerateCorrID(), verb, payload)
}

// EnqueueWithID is Enqueue with a caller-supplied CorrID. Use when the
// corr_id is already stamped on a wire envelope and the caller needs
// the queue entry to match. The ID must be a well-formed corr_xxx;
// caller is responsible for validation.
func (q *Queue) EnqueueWithID(corrID, verb string, payload any) *Entry {
	e := &Entry{
		CorrID:  corrID,
		Verb:    verb,
		Payload: payload,
		State:   StatePending,
	}
	q.mu.Lock()
	q.entries[e.CorrID] = e
	q.mu.Unlock()
	return e
}

// EnqueueWithCallbacks is Enqueue + attaches on-ack / on-error
// callbacks that fire when the entry transitions. Used when the caller
// wants UI notification.
func (q *Queue) EnqueueWithCallbacks(verb string, payload any,
	onAcked func(*Entry),
	onError func(*Entry, *protocol.Error, protocol.ErrorCategory),
) *Entry {
	e := q.Enqueue(verb, payload)
	q.mu.Lock()
	e.OnAcked = onAcked
	e.OnError = onError
	q.mu.Unlock()
	return e
}

// MarkSending transitions an entry to StateSending and stamps
// timing. Called right before the encoder writes the frame to the
// wire.
func (q *Queue) MarkSending(corrID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.entries[corrID]
	if !ok {
		return
	}
	e.State = StateSending
	now := time.Now()
	if e.Attempts == 0 {
		e.FirstSentAt = now
	}
	e.LastSentAt = now
	e.Attempts++
}

// Ack marks an entry as acknowledged and removes it from the queue.
// Returns the Entry (for the caller's UI callback to inspect) or nil
// if no entry exists for this corrID.
func (q *Queue) Ack(corrID string) *Entry {
	q.mu.Lock()
	e, ok := q.entries[corrID]
	if !ok {
		q.mu.Unlock()
		return nil
	}
	e.State = StateAcked
	delete(q.entries, corrID)
	cb := e.OnAcked
	q.mu.Unlock()
	if cb != nil {
		cb(e)
	}
	return e
}

// Error transitions an entry based on the error's category. For
// Category A (retriable transient), the entry returns to StatePending
// — the caller's retry loop picks it up. For B, the entry stays in
// StatePending too; the caller is expected to apply the server's
// state-fix push (fresh epoch_key etc.) before the next retry. For
// C/D (permanent), the entry transitions to StateError, fires the
// OnError callback, and is removed from the queue.
//
// Returns the entry (for caller inspection) or nil if unknown corrID.
func (q *Queue) Error(corrID string, err *protocol.Error) *Entry {
	if err == nil {
		return nil
	}
	category := protocol.CategoryForCode(err.Code)
	q.mu.Lock()
	e, ok := q.entries[corrID]
	if !ok {
		q.mu.Unlock()
		return nil
	}
	e.LastError = err
	var cb func(*Entry, *protocol.Error, protocol.ErrorCategory)
	switch category {
	case protocol.CategoryADefault, protocol.CategoryASilent, protocol.CategoryB:
		// Retriable: back to pending. Caller's retry loop fires.
		// A-silent is treated like A-default here for queue shape;
		// the UI-silent behavior is enforced at the TUI error handler
		// (no status-bar toast) rather than at the queue level.
		if e.Attempts >= q.maxBudget {
			// Budget exhausted — escalate to permanent.
			e.State = StateError
			delete(q.entries, corrID)
			cb = e.OnError
		} else {
			e.State = StatePending
		}
	case protocol.CategoryC, protocol.CategoryD, protocol.CategoryUnknown:
		e.State = StateError
		delete(q.entries, corrID)
		cb = e.OnError
	}
	q.mu.Unlock()
	if cb != nil {
		cb(e, err, category)
	}
	return e
}

// Len returns the number of entries currently in the queue (including
// pending, sending, and any that haven't been swept). Used by the quit-
// confirmation UI to warn about unflushed sends.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// PendingCount counts entries in StatePending or StateSending — the
// "still in flight" count that matters for quit confirmation.
func (q *Queue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	var n int
	for _, e := range q.entries {
		if e.State == StatePending || e.State == StateSending {
			n++
		}
	}
	return n
}

// SweepTimeouts scans entries in StateSending for ack-timeout
// violations and transitions them to StateTimedOut. Returns the
// affected entries so the caller can decide to retry or surface.
//
// The caller is expected to run this periodically (e.g., on a ticker)
// or on every receive-loop iteration. Safe to call with no timeouts
// (returns nil).
func (q *Queue) SweepTimeouts() []*Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	var timedOut []*Entry
	now := time.Now()
	for _, e := range q.entries {
		if e.State == StateSending && now.Sub(e.LastSentAt) > q.ackTimeout {
			e.State = StateTimedOut
			timedOut = append(timedOut, e)
		}
	}
	return timedOut
}

// Get returns the entry for the given corrID, or nil if absent.
// Copy-semantics — the returned pointer IS the entry, so callers
// should not mutate fields outside of the queue's own methods.
func (q *Queue) Get(corrID string) *Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.entries[corrID]
}

// Drop removes an entry from the queue by corrID. Used by callers
// that want to give up on an entry explicitly (e.g., user cancelled
// the send in the TUI before the server responded).
func (q *Queue) Drop(corrID string) {
	q.mu.Lock()
	delete(q.entries, corrID)
	q.mu.Unlock()
}

// PendingForCategoryARetry returns entries in StatePending whose
// LastError is a Category A transient AND whose backoff window has
// elapsed given `now`. Caller is expected to re-encode each returned
// entry's payload. The returned slice is a snapshot — mutating it
// doesn't affect queue state, and concurrent Ack/Error calls may
// make some returned pointers race-adjacent by the time the caller
// acts (handled by caller's own non-nil checks).
func (q *Queue) PendingForCategoryARetry(now time.Time) []*Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []*Entry
	for _, e := range q.entries {
		if e.State != StatePending {
			continue
		}
		if !e.isRetriableCategoryA() {
			continue
		}
		// backoff based on Attempts count (set by prior MarkSending
		// calls); LastSentAt = most recent send time.
		backoff := backoffForAttempts(e.Attempts)
		if now.Sub(e.LastSentAt) < backoff {
			continue
		}
		out = append(out, e)
	}
	return out
}

// PendingForCategoryBRoomRetry returns entries in StatePending that
// belong to the given roomID and had a Category B invalid_epoch /
// epoch_conflict / stale_member_list error. Used when a fresh
// epoch_key arrives for that room — the client retries immediately,
// bypassing Category A backoff because the state-fix IS the reason to
// retry.
func (q *Queue) PendingForCategoryBRoomRetry(roomID string, now time.Time) []*Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []*Entry
	for _, e := range q.entries {
		if e.State != StatePending {
			continue
		}
		if !e.isCategoryBInvalidEpoch() {
			continue
		}
		if roomFromKnownPayload(e.Payload) != roomID {
			continue
		}
		out = append(out, e)
	}
	return out
}
