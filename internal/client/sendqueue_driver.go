package client

// Phase 17c Step 5 Gap 2 / 3 / 4 — background send-queue driver.
//
// Responsibilities:
//
//   - Gap 2 (auto-retry for Category A): scan StatePending entries with
//     a non-nil LastError of Category A. When the backoff window
//     elapses, re-encode the payload (MarkSending stamps the new
//     attempt). Budget is enforced inside Queue.Error; once the
//     budget is exhausted, entries move to StateError and callbacks
//     fire — not the driver's concern.
//
//   - Gap 3 (timeout sweep): call Queue.SweepTimeouts() each tick.
//     Timed-out entries (StateTimedOut) are dropped silently — we
//     can't distinguish "server ack was slow" from "server ack was
//     lost", so assume-success is safer than spurious error toasts.
//     The per-entry ack timeout is chosen generously (30s default) so
//     genuine transient ack delays don't get swept.
//
//   - Gap 4 (Category B state-fix apply): exposed as TriggerEpochRetry
//     — called by the epoch_key receive handler when a fresh epoch_key
//     arrives for a room. The driver finds pending entries for that
//     room whose LastError was invalid_epoch/epoch_conflict/
//     stale_member_list, and retries them immediately (bypassing
//     backoff because the state fix IS the "reason to retry now").
//
// Tick cadence: 200ms. Fast enough for 200ms-budget Category A retry
// responsiveness; slow enough that idle CPU cost is negligible.

import (
	"time"
)

// sendQueueTickInterval is how often the driver polls the queue.
// 200ms — smaller than the initial Category A backoff so retries
// fire promptly, but not so small that idle-driver CPU shows up.
const sendQueueTickInterval = 200 * time.Millisecond

// backoffForAttempts returns the retry delay for the Nth attempt.
// 200ms, 400ms, 800ms, 1.6s, 3.2s — capped at 5s. Matches a
// conventional exponential-backoff curve.
func backoffForAttempts(attempts int) time.Duration {
	if attempts <= 0 {
		return sendQueueTickInterval
	}
	d := time.Duration(200<<(attempts-1)) * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// isRetriableCategoryA returns true if the entry's LastError's code
// is a Category A retriable transient. Used to filter which Pending
// entries the driver re-sends — Category B entries wait for the
// explicit TriggerEpochRetry call instead.
func (e *Entry) isRetriableCategoryA() bool {
	if e.LastError == nil {
		return false
	}
	switch e.LastError.Code {
	case "rate_limited", "internal_error", "server_busy":
		return true
	}
	return false
}

// isCategoryBInvalidEpoch returns true if the entry's LastError is an
// epoch-related Category B code that a fresh epoch_key push should
// trigger a retry for.
func (e *Entry) isCategoryBInvalidEpoch() bool {
	if e.LastError == nil {
		return false
	}
	switch e.LastError.Code {
	case "invalid_epoch", "epoch_conflict", "stale_member_list":
		return true
	}
	return false
}

// runSendQueueDriver is the background loop. Exits when c.done closes.
// Should run exactly once per Client; spawned from Connect after the
// session is established.
func (c *Client) runSendQueueDriver() {
	ticker := time.NewTicker(sendQueueTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.driverTick()
		}
	}
}

// driverTick is one iteration of the driver loop: sweep timeouts then
// scan for Category A retries due.
func (c *Client) driverTick() {
	// Gap 3: sweep timeouts. Timed-out entries are dropped silently.
	for _, e := range c.sendQueue.SweepTimeouts() {
		c.sendQueue.Drop(e.CorrID)
	}

	// Gap 2: retry Category A entries whose backoff window has
	// elapsed.
	now := time.Now()
	pending := c.sendQueue.PendingForCategoryARetry(now)
	for _, e := range pending {
		// Re-encode + mark-sending. Failure of the encode means the
		// channel is broken; there's nothing useful the driver can
		// do. Leave the entry in StatePending and try again next tick.
		c.sendQueue.MarkSending(e.CorrID)
		_ = c.enc.Encode(e.Payload)
	}
}

// TriggerEpochRetry is called when a fresh epoch_key arrives for
// roomID. Pending entries for this room whose LastError was a
// Category B invalid_epoch/epoch_conflict/stale_member_list are
// re-sent immediately — the state-fix has been applied, the retry
// should succeed against the fresh epoch.
//
// Called from the epoch_key receive handler (see persist.go or wherever
// the client applies received epoch keys). Safe to call at any time;
// no-op when there are no matching entries.
func (c *Client) TriggerEpochRetry(roomID string) {
	now := time.Now()
	toRetry := c.sendQueue.PendingForCategoryBRoomRetry(roomID, now)
	for _, e := range toRetry {
		c.sendQueue.MarkSending(e.CorrID)
		_ = c.enc.Encode(e.Payload)
	}
}
