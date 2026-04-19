package client

// Phase 17c Step 5 classification walk — dispatchCorrID behavior.

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func makeTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{sendQueue: NewQueue()}
}

func TestDispatchCorrID_SuccessTypeAcks(t *testing.T) {
	c := makeTestClient(t)
	corrID := "corr_ABCDEFGHIJKLMNOPQRSTU"
	c.sendQueue.EnqueueWithID(corrID, "send", nil)
	c.sendQueue.MarkSending(corrID)

	raw, _ := json.Marshal(protocol.Message{
		Type:   "message",
		ID:     "msg_x",
		CorrID: corrID,
	})
	dispatchCorrID(c, "message", raw)

	if c.sendQueue.Get(corrID) != nil {
		t.Error("success dispatch did not Ack the entry")
	}
}

func TestDispatchCorrID_UploadErrorRoutesToError(t *testing.T) {
	c := makeTestClient(t)
	corrID := "corr_ABCDEFGHIJKLMNOPQRSTU"
	var errCbCategory protocol.ErrorCategory
	e := c.sendQueue.EnqueueWithCallbacks("upload_start", nil, nil,
		func(_ *Entry, _ *protocol.Error, cat protocol.ErrorCategory) {
			errCbCategory = cat
		},
	)
	c.sendQueue.mu.Lock()
	e.CorrID = corrID
	delete(c.sendQueue.entries, e.CorrID)
	e.CorrID = corrID
	c.sendQueue.entries[corrID] = e
	c.sendQueue.mu.Unlock()
	c.sendQueue.MarkSending(corrID)

	raw, _ := json.Marshal(protocol.UploadError{
		Type:     "upload_error",
		UploadID: "up_x",
		Code:     "upload_too_large",
		Message:  "exceeds limit",
		CorrID:   corrID,
	})
	dispatchCorrID(c, "upload_error", raw)

	if c.sendQueue.Get(corrID) != nil {
		t.Error("Category C error did not remove entry")
	}
	if errCbCategory != protocol.CategoryC {
		t.Errorf("callback category = %v, want C", errCbCategory)
	}
}

func TestDispatchCorrID_DownloadErrorRoutesToError(t *testing.T) {
	c := makeTestClient(t)
	corrID := "corr_ABCDEFGHIJKLMNOPQRSTU"
	var errCbCategory protocol.ErrorCategory
	_ = c.sendQueue.EnqueueWithCallbacks("download", nil, nil,
		func(_ *Entry, _ *protocol.Error, cat protocol.ErrorCategory) {
			errCbCategory = cat
		},
	)
	// Rewire the callback entry to use our corrID (EnqueueWithCallbacks
	// generates its own CorrID).
	c.sendQueue.mu.Lock()
	for k, v := range c.sendQueue.entries {
		delete(c.sendQueue.entries, k)
		v.CorrID = corrID
		c.sendQueue.entries[corrID] = v
		break
	}
	c.sendQueue.mu.Unlock()
	c.sendQueue.MarkSending(corrID)

	raw, _ := json.Marshal(protocol.DownloadError{
		Type:    "download_error",
		FileID:  "file_x",
		Code:    "not_found",
		Message: "missing",
		CorrID:  corrID,
	})
	dispatchCorrID(c, "download_error", raw)

	if c.sendQueue.Get(corrID) != nil {
		t.Error("Category D error did not remove entry")
	}
	if errCbCategory != protocol.CategoryD {
		t.Errorf("callback category = %v, want D", errCbCategory)
	}
}

func TestDispatchCorrID_PlainErrorNotRouted(t *testing.T) {
	// `type="error"` is intentionally not in errorTypes — the TUI
	// handles it exclusively so its A-silent verb lookup can inspect
	// the queue entry BEFORE Queue.Error removes it. Verify the
	// dispatcher leaves such frames untouched.
	c := makeTestClient(t)
	corrID := "corr_ABCDEFGHIJKLMNOPQRSTU"
	c.sendQueue.EnqueueWithID(corrID, "send", nil)
	c.sendQueue.MarkSending(corrID)

	raw, _ := json.Marshal(protocol.Error{
		Type:    "error",
		Code:    "rate_limited",
		Message: "slow",
		CorrID:  corrID,
	})
	dispatchCorrID(c, "error", raw)

	if c.sendQueue.Get(corrID) == nil {
		t.Error("dispatcher touched the entry for type=\"error\"; should be TUI's exclusive path")
	}
}

func TestDispatchCorrID_NoCorrIDIsNoOp(t *testing.T) {
	c := makeTestClient(t)
	c.sendQueue.EnqueueWithID("corr_ABCDEFGHIJKLMNOPQRSTU", "send", nil)
	c.sendQueue.MarkSending("corr_ABCDEFGHIJKLMNOPQRSTU")

	raw, _ := json.Marshal(protocol.Message{Type: "message", ID: "msg_x"})
	dispatchCorrID(c, "message", raw)

	// Entry still present — no corr_id to match against.
	if c.sendQueue.Get("corr_ABCDEFGHIJKLMNOPQRSTU") == nil {
		t.Error("dispatcher acked an entry with no matching corr_id on the frame")
	}
}

func TestDispatchCorrID_NoMatchingEntryIsNoOp(t *testing.T) {
	c := makeTestClient(t)
	raw, _ := json.Marshal(protocol.Message{
		Type:   "message",
		CorrID: "corr_ABCDEFGHIJKLMNOPQRSTU",
	})
	dispatchCorrID(c, "message", raw)
	// No panic. No queue entry was present to begin with; we're just
	// asserting the call is safe on empty queues.
}

func TestDispatchCorrID_MalformedJSONIsNoOp(t *testing.T) {
	c := makeTestClient(t)
	c.sendQueue.EnqueueWithID("corr_ABCDEFGHIJKLMNOPQRSTU", "send", nil)
	dispatchCorrID(c, "message", []byte(`{broken`))
	// Entry still present; malformed input shouldn't destabilize state.
	if c.sendQueue.Get("corr_ABCDEFGHIJKLMNOPQRSTU") == nil {
		t.Error("malformed frame cleared a queue entry")
	}
}
