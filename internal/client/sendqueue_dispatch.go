package client

// Phase 17c Step 5 residual + classification walk — generic corr_id
// dispatch for the readLoop. One place to route every inbound frame
// that carries corr_id into the send queue:
//
//   - Error responses (type="error", "upload_error", "download_error")
//     → Queue.Error — queue classifies via CategoryForCode, caller's
//     OnError callback fires for Category C/D, Category A/B keep the
//     entry pending for retry.
//
//   - Everything else with a corr_id (success broadcasts + responses
//     like "message", "edited", "reaction", "deleted",
//     "history_result", "room_members_list", "device_list",
//     "upload_ready", "upload_complete", "download_start", etc.)
//     → Queue.Ack — entry cleared, OnAcked fires.
//
// Non-error types without corr_id (presence, typing, profile,
// broadcasts for other users' actions) are no-ops — they don't match
// any queue entry so the Ack call returns without effect.

import (
	"encoding/json"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// errorTypes is the set of wire message types that carry a Code + a
// corr_id and should route to Queue.Error rather than Queue.Ack.
//
// `type="error"` is deliberately NOT in this set — the TUI's "error"
// case needs to inspect the queue entry's verb BEFORE Queue.Error
// removes it (for the A-silent verb check in Gap 5). Leaving
// `type="error"` out of the dispatcher means the TUI case handles it
// exclusively.
//
// upload_error and download_error have no such verb-lookup
// requirement, so routing them through the dispatcher here is safe
// and spares the per-case wiring.
var errorTypes = map[string]bool{
	"upload_error":   true,
	"download_error": true,
}

// dispatchCorrID routes an inbound frame to the send-queue based on
// msgType. For error types, unmarshals the {code, message, corr_id}
// shape and calls Queue.Error. For everything else, unmarshals just
// corr_id and calls Queue.Ack.
//
// Called from readLoop BEFORE handleInternal so the queue clears
// before any per-case handler runs — this means handler code can
// assume its queue entry (if any) is already resolved.
func dispatchCorrID(c *Client, msgType string, raw []byte) {
	// `type="error"` is handled exclusively by the TUI's error case
	// (it needs verb lookup BEFORE Queue.Error removes the entry for
	// the Gap 5 A-silent check). Skip it here.
	if msgType == "error" {
		return
	}
	if errorTypes[msgType] {
		dispatchCorrIDError(c, msgType, raw)
		return
	}
	// Success / broadcast path: any corr_id matches an entry to Ack.
	var corrOnly struct {
		CorrID string `json:"corr_id,omitempty"`
	}
	if err := json.Unmarshal(raw, &corrOnly); err != nil {
		return
	}
	if corrOnly.CorrID == "" {
		return
	}
	c.sendQueue.Ack(corrOnly.CorrID)
}

// dispatchCorrIDError extracts the error code + corr_id and routes to
// Queue.Error so the queue's Category dispatch fires. Upload and
// download errors share the Code + Message + CorrID shape with
// protocol.Error, so a single shape unmarshals all three.
func dispatchCorrIDError(c *Client, msgType string, raw []byte) {
	var e struct {
		Code         string `json:"code,omitempty"`
		Message      string `json:"message,omitempty"`
		CorrID       string `json:"corr_id,omitempty"`
		RetryAfterMs int64  `json:"retry_after_ms,omitempty"`
		// Ref comes from protocol.Error on edit-path errors — surface-
		// able to the caller via OnError if the Entry cares.
		Ref string `json:"ref,omitempty"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return
	}
	if e.CorrID == "" {
		return
	}
	c.sendQueue.Error(e.CorrID, &protocol.Error{
		Type:         msgType,
		Code:         e.Code,
		Message:      e.Message,
		Ref:          e.Ref,
		RetryAfterMs: e.RetryAfterMs,
		CorrID:       e.CorrID,
	})
}
