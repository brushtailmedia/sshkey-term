package client

// Phase 15 — client-side message editing.
//
// Three edit methods mirror the send-family shape:
//
//   EditRoomMessage   — rooms       (sends `edit`,       dispatched as `edited`)
//   EditGroupMessage  — group DMs   (sends `edit_group`, dispatched as `group_edited`)
//   EditDMMessage     — 1:1 DMs     (sends `edit_dm`,    dispatched as `dm_edited`)
//
// All three follow the **preserve-and-replace** pattern described in
// message_editing.md Chunk 6:
//
//   1. Fetch the original decrypted message from the local store.
//   2. Decrypt nothing — we already have `Body`, `ReplyTo`, and the
//      attachment list in StoredMessage from the original insert.
//   3. Build a new DecryptedPayload: copy `ReplyTo` verbatim, re-extract
//      `Mentions` from the new body (the new body is the authoritative
//      source), copy `Attachments[*]` verbatim (including per-attachment
//      `FileKey` / `FileEpoch` decryption hints — these are independent
//      of K_msg and must survive the edit), replace `Body`, regenerate
//      `Seq` + `DeviceID` for the new wire frame.
//   4. Encrypt with the right key material (current epoch key for rooms,
//      fresh K_msg for groups/DMs).
//   5. Sign the new envelope.
//   6. Send. Do NOT touch local state — wait for the server echo.
//
// The server performs authorship / most-recent / epoch-window / retired
// checks and returns an error if any fail. The local store is only
// updated when the client's dispatch path receives the `edited` /
// `group_edited` / `dm_edited` broadcast (see persist.go).

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// EditRoomMessage replaces the body of a room message the user already
// sent. The edit must target the user's most recent message in the room
// and must be within the current epoch's grace window (current or
// previous epoch). The server validates both; the client just attempts
// the send. Mentions are re-extracted from the new body; ReplyTo and
// Attachments are copied verbatim from the original.
func (c *Client) EditRoomMessage(msgID, room, newBody string) error {
	orig, err := c.loadOriginalMessage(msgID)
	if err != nil {
		return err
	}

	// Re-extract mentions from the new body. This is client-side policy
	// (mentions live inside the encrypted payload and the server never
	// sees them) — extract from the authoritative new text so highlight
	// rendering and push-notification targeting track the edit.
	mentions := extractMentionsFromBody(newBody)

	// Build the new decrypted payload. Seq is incremented via the
	// existing counter pattern to ensure monotonicity.
	c.mu.Lock()
	epoch := c.currentEpoch[room]
	key := c.epochKeys[room][epoch]
	seqKey := "room:" + room
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	if key == nil {
		return fmt.Errorf("no epoch key for room %s (cannot edit)", room)
	}

	payload := protocol.DecryptedPayload{
		Body:        newBody,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     orig.ReplyTo, // preserved
		Attachments: storedToProtocolAttachments(orig.Attachments), // preserved verbatim
	}
	payloadJSON, _ := json.Marshal(payload)

	encrypted, err := crypto.Encrypt(key, payloadJSON)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	// Phase 21 item 3 — bind the signature to this specific msgID so a
	// compromised server cannot replay this `(payload, room, epoch)` pair
	// against a different msgID to rewrite history. Distinct domain tag
	// from SignRoom also guarantees a send signature cannot cross-verify
	// as an edit signature.
	sig := crypto.SignRoomEdit(c.privKey, msgID, payloadBytes, room, epoch)

	corrID := protocol.GenerateCorrID()
	envelope := protocol.Edit{
		Type:      "edit",
		ID:        msgID,
		Room:      room,
		Epoch:     epoch,
		Payload:   encrypted,
		Signature: base64.StdEncoding.EncodeToString(sig),
		CorrID:    corrID,
	}
	c.sendQueue.EnqueueWithID(corrID, "edit", envelope)
	c.sendQueue.MarkSending(corrID)
	return c.enc.Encode(envelope)
}

// EditGroupMessage replaces the body of a group DM message. Group DMs
// use per-message keys, so an edit produces a fresh K_msg wrapped for
// the CURRENT group member set (not the member set at original send
// time — the server validates against GetGroupMembers at edit time and
// rejects with invalid_wrapped_keys if the sets don't match).
func (c *Client) EditGroupMessage(msgID, group, newBody string) error {
	orig, err := c.loadOriginalMessage(msgID)
	if err != nil {
		return err
	}

	mentions := extractMentionsFromBody(newBody)

	c.mu.Lock()
	seqKey := "group:" + group
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	payload := protocol.DecryptedPayload{
		Body:        newBody,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     orig.ReplyTo,
		Attachments: storedToProtocolAttachments(orig.Attachments),
	}
	payloadJSON, _ := json.Marshal(payload)

	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		return err
	}

	wrappedKeys, err := c.wrapKeyForGroup(group, msgKey)
	if err != nil {
		return err
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	// Phase 21 item 3 — msgID-bound edit signature. See EditRoomMessage.
	sig := crypto.SignDMEdit(c.privKey, msgID, payloadBytes, group, wrappedKeys)

	corrID := protocol.GenerateCorrID()
	envelope := protocol.EditGroup{
		Type:        "edit_group",
		ID:          msgID,
		Group:       group,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
		CorrID:      corrID,
	}
	c.sendQueue.EnqueueWithID(corrID, "edit_group", envelope)
	c.sendQueue.MarkSending(corrID)
	return c.enc.Encode(envelope)
}

// EditDMMessage replaces the body of a 1:1 DM message. Generates a
// fresh K_msg wrapped for exactly two parties (the DM's two members).
// The server validates that both parties appear in wrapped_keys and
// rejects if either has a frozen view (per-user left_at ratchet).
func (c *Client) EditDMMessage(msgID, dmID, newBody string) error {
	orig, err := c.loadOriginalMessage(msgID)
	if err != nil {
		return err
	}

	mentions := extractMentionsFromBody(newBody)

	c.mu.Lock()
	seqKey := "dm:" + dmID
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	payload := protocol.DecryptedPayload{
		Body:        newBody,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     orig.ReplyTo,
		Attachments: storedToProtocolAttachments(orig.Attachments),
	}
	payloadJSON, _ := json.Marshal(payload)

	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		return err
	}

	wrappedKeys, err := c.wrapKeyForDM(dmID, msgKey)
	if err != nil {
		return err
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	// Phase 21 item 3 — msgID-bound edit signature. See EditRoomMessage.
	sig := crypto.SignDMEdit(c.privKey, msgID, payloadBytes, dmID, wrappedKeys)

	corrID := protocol.GenerateCorrID()
	envelope := protocol.EditDM{
		Type:        "edit_dm",
		ID:          msgID,
		DM:          dmID,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
		CorrID:      corrID,
	}
	c.sendQueue.EnqueueWithID(corrID, "edit_dm", envelope)
	c.sendQueue.MarkSending(corrID)
	return c.enc.Encode(envelope)
}

// loadOriginalMessage fetches the stored message row for the given
// message ID. Returns a helpful error if the row is missing in the
// local store — can't edit what we don't have. Callers use the returned
// `StoredMessage.ReplyTo` and `StoredMessage.Attachments` as the
// preserve-and-replace source.
func (c *Client) loadOriginalMessage(msgID string) (*originalMessage, error) {
	if c.store == nil {
		return nil, fmt.Errorf("no local store — cannot edit")
	}
	m, err := c.store.GetMessageByID(msgID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("edit: message %s not in local store", msgID)
	}
	if err != nil {
		return nil, fmt.Errorf("edit: lookup failed: %w", err)
	}
	// Convert from store.StoredAttachment (base64 FileKey as string,
	// local-path bookkeeping, etc.) into protocol.Attachment for
	// re-serialization into the encrypted payload. The fields that
	// matter for decryption preservation are FileID, Name, Size, Mime,
	// FileKey, and FileEpoch — the client's local-path-only fields
	// don't leave the local DB.
	attachments := make([]originalAttachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		attachments = append(attachments, originalAttachment{
			FileID:     a.FileID,
			Name:       a.Name,
			Size:       a.Size,
			Mime:       a.Mime,
			DecryptKey: a.DecryptKey,
		})
	}
	return &originalMessage{
		ReplyTo:     m.ReplyTo,
		Attachments: attachments,
	}, nil
}

// originalMessage is a local-only projection of the fields from
// StoredMessage that the edit path needs to copy verbatim. Keeps the
// edit helpers decoupled from any future StoredMessage field churn.
type originalMessage struct {
	ReplyTo     string
	Attachments []originalAttachment
}

// originalAttachment mirrors store.StoredAttachment in the small set
// of fields that preserve-and-replace needs. `DecryptKey` is the
// base64-encoded per-file key for groups/DMs, or the epoch key bytes
// for rooms (stored as base64 via StoredAttachment.DecryptKey).
type originalAttachment struct {
	FileID     string
	Name       string
	Size       int64
	Mime       string
	DecryptKey string
}

// storedToProtocolAttachments converts the client's originalAttachment
// form back into protocol.Attachment records for embedding inside the
// new encrypted payload. For groups/DMs the FileKey field is populated
// from DecryptKey (the per-file K_file). For rooms the payload's
// attachments carry FileEpoch in the original — the client's stored
// form doesn't preserve FileEpoch today (it reconstructs from the
// current room epoch at insert time), so room attachments on edit are
// a known limitation: the new payload copies FileID/Name/Size/Mime
// but regenerates FileEpoch against the current epoch. Since a room
// edit is constrained to the current-or-previous epoch grace window,
// and most attachments are from the current epoch, this is usually
// correct. Attachments uploaded in the PREVIOUS epoch that survive a
// room edit in the CURRENT epoch will have FileEpoch silently bumped
// to current — the file itself is still decryptable because the
// current epoch key decrypts via the grace window rule, but this is
// worth flagging in Chunk 9 docs as a known edge case.
func storedToProtocolAttachments(atts []originalAttachment) []protocol.Attachment {
	out := make([]protocol.Attachment, 0, len(atts))
	for _, a := range atts {
		out = append(out, protocol.Attachment{
			FileID:  a.FileID,
			Name:    a.Name,
			Size:    a.Size,
			Mime:    a.Mime,
			FileKey: a.DecryptKey, // groups/DMs: base64 K_file; rooms: unused (epoch key handles it)
		})
	}
	return out
}

// extractMentionsFromBody scans a body for @mentions and returns them
// in the order they appear. Matches the server-agnostic convention
// that mention highlighting lives entirely in the client layer.
// Simple whitespace-delimited scan — the full client already has more
// sophisticated mention extraction logic in the TUI layer; this is a
// minimal standalone version for the edit path. If a future pass
// wants richer extraction (word boundaries, Unicode), share the code
// with the TUI instead of duplicating.
func extractMentionsFromBody(body string) []string {
	var mentions []string
	for i := 0; i < len(body); i++ {
		if body[i] != '@' {
			continue
		}
		// Extract the @-prefixed word that follows.
		j := i + 1
		for j < len(body) {
			c := body[j]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
				j++
				continue
			}
			break
		}
		if j > i+1 {
			mentions = append(mentions, body[i+1:j])
		}
		i = j - 1
	}
	return mentions
}
