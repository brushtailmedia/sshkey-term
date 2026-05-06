package client

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// inflightAutoPreview tracks fileIDs that already have a goroutine
// downloading them. Lookup is keyed by fileID. Used by
// maybeAutoPreviewAttachments to skip spawning a duplicate download
// when N store paths fire for the same fileID before any of them
// have written to the cache (the os.Stat check before spawn is a
// TOCTOU window — without this guard, N concurrent goroutines all
// miss the cache, all queue behind c.downloadChanMu, and all
// re-fetch the same bytes, each rewriting the on-disk file and
// invalidating the image-render cache's mod-time check).
var inflightAutoPreview sync.Map

// pubKeyForUser resolves a user's Ed25519 public key for signature
// verification on inbound broadcasts. Lookup order:
//  1. Self — use our own key directly (we trust our send path).
//  2. Live profile cache (`c.profiles`) — most authoritative, populated
//     on every connect handshake via `profile` events.
//  3. Pinned-keys store fallback — covers the offline/cold-start window
//     before the profile broadcast arrives for a given user.
//
// Returns nil if the user is not known. Callers MUST treat nil as a
// verification failure (drop the broadcast) — verify-or-drop is the
// contract on which Phase 21 item 3's edit-path protection rests.
func (c *Client) pubKeyForUser(userID string) ed25519.PublicKey {
	if userID == c.UserID() {
		return c.privKey.Public().(ed25519.PublicKey)
	}
	c.mu.RLock()
	profile := c.profiles[userID]
	c.mu.RUnlock()
	if profile != nil && profile.PubKey != "" {
		if pub, err := crypto.ParseSSHPubKey(profile.PubKey); err == nil {
			return pub
		}
	}
	if c.store != nil {
		_, _, pubkeyStr := c.store.GetPinnedKeyFull(userID)
		if pubkeyStr != "" {
			if pub, err := crypto.ParseSSHPubKey(pubkeyStr); err == nil {
				return pub
			}
		}
	}
	return nil
}

// storeRoomMessage decrypts and stores a room message in the local DB.
// When warnReplay is false, replay high-water checks still run but do not
// emit WARN logs (used for sync/history catchup where old frames are normal).
func (c *Client) storeRoomMessage(raw json.RawMessage, warnReplay bool) {
	if c.store == nil {
		return
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	body := ""
	replyTo := ""
	var mentions []string

	var attachments []store.StoredAttachment

	payload, err := c.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
	if err == nil {
		body = payload.Body
		replyTo = payload.ReplyTo
		mentions = payload.Mentions
		c.checkReplay(msg.From, payload.DeviceID, msg.Room, "", payload.Seq, warnReplay)

		for _, a := range payload.Attachments {
			fileEpoch := a.FileEpoch
			if fileEpoch == 0 {
				fileEpoch = msg.Epoch
			}
			key := c.RoomEpochKey(msg.Room, fileEpoch)
			attachments = append(attachments, store.StoredAttachment{
				FileID:     a.FileID,
				Name:       a.Name,
				Size:       a.Size,
				Mime:       a.Mime,
				DecryptKey: base64.StdEncoding.EncodeToString(key),
			})
		}
	}

	inserted, _ := c.store.InsertMessage(store.StoredMessage{
		ID:          msg.ID,
		Sender:      msg.From,
		Body:        body,
		TS:          msg.TS,
		Room:        msg.Room,
		Epoch:       msg.Epoch,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
	// Only fire auto-preview when we actually inserted a new row.
	// Same message arriving via live broadcast + sync_batch +
	// history_result is the common case (every reconnect re-hits
	// the same recent rows); without this gate, multiple goroutines
	// fire for the same fileID, each rewriting the cached file and
	// invalidating the image-render cache.
	if inserted {
		c.maybeAutoPreviewAttachments(attachments)
	}
}

// storeGroupMessage decrypts and stores a group DM message in the local DB.
//
// Defense in depth for the new-member pre-join history gate: if decrypt
// fails, drop the row entirely rather than persist an empty-body ghost.
// The server-side filter (syncGroup + handleHistory joined_at gate) is
// the source fix — this client-side drop catches any path where the
// server ever sends a row we can't decrypt, which for a post-fix server
// only happens if something regresses. A pre-join message has no
// wrapped_key for the new member, so DecryptGroupMessage returns an
// error and we skip InsertMessage entirely. Mirrors storeReaction's
// "can't decrypt — don't persist garbage" pattern.
func (c *Client) storeGroupMessage(raw json.RawMessage, warnReplay bool) {
	if c.store == nil {
		return
	}

	var msg protocol.GroupMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	payload, err := c.DecryptGroupMessage(msg.WrappedKeys, msg.Payload)
	if err != nil {
		// No wrapped key for us, or key material missing — drop the
		// row rather than leak metadata via an empty-body insert.
		return
	}

	c.checkReplay(msg.From, payload.DeviceID, "", msg.Group, payload.Seq, warnReplay)

	var attachments []store.StoredAttachment
	for _, a := range payload.Attachments {
		attachments = append(attachments, store.StoredAttachment{
			FileID:     a.FileID,
			Name:       a.Name,
			Size:       a.Size,
			Mime:       a.Mime,
			DecryptKey: a.FileKey, // group DMs: already base64-encoded per-file K_file
		})
	}

	inserted, _ := c.store.InsertMessage(store.StoredMessage{
		ID:          msg.ID,
		Sender:      msg.From,
		Body:        payload.Body,
		TS:          msg.TS,
		Group:       msg.Group,
		ReplyTo:     payload.ReplyTo,
		Mentions:    payload.Mentions,
		Attachments: attachments,
	})
	if inserted {
		c.maybeAutoPreviewAttachments(attachments)
	}
}

// storeDMMessage decrypts and stores a 1:1 DM message in the local DB.
func (c *Client) storeDMMessage(raw json.RawMessage, warnReplay bool) {
	if c.store == nil {
		return
	}

	var msg protocol.DM
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	body := ""
	replyTo := ""
	var mentions []string

	var attachments []store.StoredAttachment

	payload, err := c.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
	if err == nil {
		body = payload.Body
		replyTo = payload.ReplyTo
		mentions = payload.Mentions
		c.checkReplay(msg.From, payload.DeviceID, "", msg.DM, payload.Seq, warnReplay)

		for _, a := range payload.Attachments {
			attachments = append(attachments, store.StoredAttachment{
				FileID:     a.FileID,
				Name:       a.Name,
				Size:       a.Size,
				Mime:       a.Mime,
				DecryptKey: a.FileKey, // 1:1 DMs: already base64-encoded per-file K_file
			})
		}
	}

	inserted, _ := c.store.InsertMessage(store.StoredMessage{
		ID:          msg.ID,
		Sender:      msg.From,
		Body:        body,
		TS:          msg.TS,
		DM:          msg.DM,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
	if inserted {
		c.maybeAutoPreviewAttachments(attachments)
	}
}

// maybeAutoPreviewAttachments kicks off background downloads for image
// attachments small enough to preview inline. One goroutine per matching
// attachment; no-op when auto-preview is disabled (cap <= 0), when the
// mime is not in the accept-list, when the attachment is already cached
// on disk, or when the decrypt key can't be decoded.
//
// Each goroutine calls DownloadFile which does the full fetch + decrypt
// + write-to-cache. On completion we fire the OnAttachmentReady callback
// so the TUI can re-render the message (render path checks the cache
// file on disk by file_id, so the callback carries the file_id only).
// Download failures log at Debug and are otherwise silent — the render
// path falls back to the 🖼 placeholder.
//
// The size threshold is the primary defense against crafted-image
// decoder exploits: anything above it cannot auto-fire the decoder,
// only manual `o` can. Decoder panics are additionally recovered in
// RenderImageInline.
func (c *Client) maybeAutoPreviewAttachments(attachments []store.StoredAttachment) {
	if c.cfg.ImageAutoPreviewMaxBytes <= 0 {
		return
	}
	dataDir := c.cfg.DataDir
	if dataDir == "" {
		return
	}
	cb := c.cfg.OnAttachmentReady
	for _, a := range attachments {
		if !isAutoPreviewMime(a.Mime) {
			continue
		}
		if a.Size <= 0 || a.Size > c.cfg.ImageAutoPreviewMaxBytes {
			continue
		}
		cachedPath := filepath.Join(dataDir, "files", a.FileID)
		if _, err := os.Stat(cachedPath); err == nil {
			// Already cached from a previous session or manual open — no
			// download needed, but still nudge the TUI to re-render in
			// case this message is visible and the previous render
			// happened before the cache check fired.
			if cb != nil {
				go cb(a.FileID)
			}
			continue
		}
		key, err := base64.StdEncoding.DecodeString(a.DecryptKey)
		if err != nil {
			continue
		}
		fileID := a.FileID // capture for closure
		go func() {
			// In-flight dedup: if another goroutine is already
			// downloading this fileID, skip. LoadOrStore is atomic —
			// exactly one goroutine sees `loaded=false` and proceeds.
			// Re-checks the on-disk cache after acquiring the
			// "lock" because the previous goroutine may have just
			// finished writing.
			if _, loaded := inflightAutoPreview.LoadOrStore(fileID, struct{}{}); loaded {
				return
			}
			defer inflightAutoPreview.Delete(fileID)

			if _, err := os.Stat(cachedPath); err == nil {
				// Another goroutine finished while we were waiting on
				// LoadOrStore. File is already on disk; just nudge a
				// re-render (same as the cache-hit fast path above).
				if cb != nil {
					cb(fileID)
				}
				return
			}

			if _, err := c.DownloadFile(fileID, key); err != nil {
				if c.logger != nil {
					c.logger.Debug("auto-preview download failed",
						"file_id", fileID, "error", err)
				}
				return
			}
			if cb != nil {
				cb(fileID)
			}
		}()
	}
}

// isAutoPreviewMime is the narrow accept-list for auto-download. Kept
// intentionally tighter than any general image-mime check: auto-decoding
// on receive (without user action) is the one path where a crafted
// payload could fire, so we restrict it to the four formats native to
// Go's image decoders. Other mime types stay manual-open-only.
func isAutoPreviewMime(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// storeEditedRoomMessage applies an `edited` broadcast to the local DB.
// Phase 15. Decrypts the new payload with the epoch key, extracts the
// new body, and calls UpdateMessageEdited which replaces the body +
// edited_at and clears any locally cached reactions on that message ID
// (per Decision log Q12: client-side unconditional clear on receipt).
//
// Phase 21 item 3 — verifies the edit signature against the new
// msgID-bound canonical form (crypto.VerifyRoomEdit) BEFORE applying.
// A compromised server cannot replay a past signed payload of sender A
// against a different msgID to rewrite history: the signature is now
// cryptographically bound to `(msgID, payload, room, epoch)` and a
// mismatch drops the broadcast silently. Verify-or-drop contract — if
// we can't resolve the sender's pubkey (rare — profile broadcast
// normally precedes traffic on every connect) we also drop.
//
// If decryption fails, the row is left untouched (no corruption of
// the stored body). Matches the defensive "can't decrypt — don't
// persist garbage" pattern elsewhere in this file.
func (c *Client) storeEditedRoomMessage(raw json.RawMessage) {
	if c.store == nil {
		return
	}
	var msg protocol.Edited
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// Verify signature before applying — prevents compromised-server
	// history rewrite via signature-replay. Phase 21 item 3.
	pubKey := c.pubKeyForUser(msg.From)
	if pubKey == nil {
		c.logger.Warn("edit signature drop — unknown sender pubkey",
			"context", "room", "id", msg.ID, "from", msg.From)
		return
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil {
		c.logger.Warn("edit signature drop — payload not base64",
			"context", "room", "id", msg.ID)
		return
	}
	sigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		c.logger.Warn("edit signature drop — signature not base64",
			"context", "room", "id", msg.ID)
		return
	}
	if !crypto.VerifyRoomEdit(pubKey, msg.ID, payloadBytes, msg.Room, msg.Epoch, sigBytes) {
		c.logger.Warn("edit signature drop — verification failed",
			"context", "room", "id", msg.ID, "from", msg.From)
		return
	}

	payload, err := c.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
	if err != nil {
		// Decryption failed — either we don't have the epoch key for
		// this edit (unlikely since the original was readable) or the
		// server sent a malformed payload. Leave the stored row alone.
		return
	}
	if _, err := c.store.UpdateMessageEdited(msg.ID, payload.Body, msg.EditedAt); err != nil {
		c.logger.Warn("UpdateMessageEdited (room) failed", "id", msg.ID, "error", err)
	}
}

// storeEditedGroupMessage applies a `group_edited` broadcast. The
// payload is decrypted using the caller's wrapped_keys entry from the
// edit envelope (the new K_msg the author wrapped for the current
// member set). Verifies the msgID-bound edit signature before applying
// (Phase 21 item 3).
func (c *Client) storeEditedGroupMessage(raw json.RawMessage) {
	if c.store == nil {
		return
	}
	var msg protocol.GroupEdited
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	pubKey := c.pubKeyForUser(msg.From)
	if pubKey == nil {
		c.logger.Warn("edit signature drop — unknown sender pubkey",
			"context", "group", "id", msg.ID, "from", msg.From)
		return
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil {
		c.logger.Warn("edit signature drop — payload not base64",
			"context", "group", "id", msg.ID)
		return
	}
	sigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		c.logger.Warn("edit signature drop — signature not base64",
			"context", "group", "id", msg.ID)
		return
	}
	if !crypto.VerifyDMEdit(pubKey, msg.ID, payloadBytes, msg.Group, msg.WrappedKeys, sigBytes) {
		c.logger.Warn("edit signature drop — verification failed",
			"context", "group", "id", msg.ID, "from", msg.From)
		return
	}

	payload, err := c.DecryptGroupMessage(msg.WrappedKeys, msg.Payload)
	if err != nil {
		return
	}
	if _, err := c.store.UpdateMessageEdited(msg.ID, payload.Body, msg.EditedAt); err != nil {
		c.logger.Warn("UpdateMessageEdited (group) failed", "id", msg.ID, "error", err)
	}
}

// storeEditedDMMessage applies a `dm_edited` broadcast. The payload
// is decrypted using the caller's wrapped_keys entry. Verifies the
// msgID-bound edit signature before applying (Phase 21 item 3).
func (c *Client) storeEditedDMMessage(raw json.RawMessage) {
	if c.store == nil {
		return
	}
	var msg protocol.DMEdited
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	pubKey := c.pubKeyForUser(msg.From)
	if pubKey == nil {
		c.logger.Warn("edit signature drop — unknown sender pubkey",
			"context", "dm", "id", msg.ID, "from", msg.From)
		return
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil {
		c.logger.Warn("edit signature drop — payload not base64",
			"context", "dm", "id", msg.ID)
		return
	}
	sigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		c.logger.Warn("edit signature drop — signature not base64",
			"context", "dm", "id", msg.ID)
		return
	}
	if !crypto.VerifyDMEdit(pubKey, msg.ID, payloadBytes, msg.DM, msg.WrappedKeys, sigBytes) {
		c.logger.Warn("edit signature drop — verification failed",
			"context", "dm", "id", msg.ID, "from", msg.From)
		return
	}

	payload, err := c.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
	if err != nil {
		return
	}
	if _, err := c.store.UpdateMessageEdited(msg.ID, payload.Body, msg.EditedAt); err != nil {
		c.logger.Warn("UpdateMessageEdited (dm) failed", "id", msg.ID, "error", err)
	}
}

// storeReaction decrypts and persists a reaction to the local DB.
//
// Phase 15 follow-up: drop reactions whose parent message is not in
// the local store OR has been tombstoned. Three paths can produce
// such orphans absent the check:
//
//  1. Race with delete: the server-side guard in handleReact
//     (`isReactableMessage`) closes the race on the authoritative
//     side, but clients processing an earlier snapshot may still
//     receive a reaction broadcast for a message they've just
//     tombstoned locally.
//  2. Reaction on an out-of-cache message: the client never loaded
//     the parent (narrow history window, message predates this
//     device's join, etc.) but a react broadcast arrives anyway
//     because other clients with wider context know the parent.
//  3. Malformed server broadcast: future bug produces a reaction
//     with a msgID that doesn't resolve locally.
//
// In all three cases the TUI would silently drop the reaction at
// render time (`addReactionRecord` walks the loaded-messages list
// and no-ops when no row matches), but the reaction row still lands
// in the local `reactions` table and orphans there forever. The
// guard here drops the reaction at insert time so the local store
// stays clean. Matches the "can't decrypt — don't persist garbage"
// defensive pattern below and the server-side tombstone guard.
func (c *Client) storeReaction(raw json.RawMessage) {
	if c.store == nil {
		return
	}

	var r protocol.Reaction
	if err := json.Unmarshal(raw, &r); err != nil {
		return
	}

	// Drop orphan reactions before any expensive work (decrypt,
	// profile lookup). If the parent message isn't in the local
	// store or is already tombstoned, the reaction has nowhere to
	// land — skip silently. The store.ErrNoRows case is the
	// "parent not cached" scenario and is just as invalid as a
	// tombstoned parent for reaction-attachment purposes.
	parent, err := c.store.GetMessageByID(r.ID)
	if err != nil || parent == nil || parent.Deleted {
		return
	}

	// Decrypt to get the emoji
	var emoji string
	if r.Room != "" {
		dr, err := c.DecryptRoomReaction(r.Room, r.Epoch, r.Payload)
		if err == nil {
			emoji = dr.Emoji
		}
	} else if r.Group != "" {
		dr, err := c.DecryptGroupReaction(r.WrappedKeys, r.Payload)
		if err == nil {
			emoji = dr.Emoji
		}
	} else if r.DM != "" {
		dr, err := c.DecryptDMReaction(r.WrappedKeys, r.Payload)
		if err == nil {
			emoji = dr.Emoji
		}
	}

	if emoji == "" {
		return // can't decrypt — don't persist garbage
	}

	c.store.InsertReaction(store.StoredReaction{
		ReactionID: r.ReactionID,
		MessageID:  r.ID,
		User:       r.User,
		Emoji:      emoji,
		TS:         r.TS,
	})
}

// checkReplay checks for replay attacks using seq high-water marks.
// warn controls whether stale/non-monotonic frames should emit a WARN log.
func (c *Client) checkReplay(sender, deviceID, room, group string, seq int64, warn bool) {
	if c.store == nil || seq == 0 {
		return
	}

	target := room
	if target == "" {
		target = group
	}
	key := sender + ":" + deviceID + ":" + target

	existing, err := c.store.GetSeqMark(key)
	if err == nil && seq <= existing {
		if warn && c.logger != nil {
			c.logger.Warn("possible replay detected",
				"sender", sender,
				"device", deviceID,
				"seq", seq,
				"high_water", existing,
			)
		}
		return
	}

	c.store.StoreSeqMark(key, seq)
}

// handleCatchupMessage persists a message carried inside sync/history catchup.
// Catchup frames are expected to include older seq values, so replay warnings
// are suppressed while still enforcing high-water updates.
func (c *Client) handleCatchupMessage(msgType string, raw json.RawMessage) {
	switch msgType {
	case "message":
		c.storeRoomMessage(raw, false)
	case "group_message":
		c.storeGroupMessage(raw, false)
	case "dm":
		c.storeDMMessage(raw, false)
	default:
		c.handleInternal(msgType, raw)
	}
}

// StoreProfile pins a user's key on first encounter, warns on change.
//
// Under the no-rotation protocol invariant (see PROTOCOL.md "Keys as
// Identities"), the "change" branch here only fires on anomalous
// inputs — a compromised server substituting a key, a server bug
// emitting a corrupted fingerprint, or local DB tampering. It never
// fires on legitimate operation. Both the ClearVerified call AND the
// OnKeyWarning dispatch below are attack-path code (see
// audit_v0.2.0.md#F32): they exist to surface and mitigate an event
// class the protocol does not produce in normal operation. Stripping
// either as "redundant" code halves the detection coverage.
func (c *Client) StoreProfile(p *protocol.Profile) {
	if c.store == nil {
		return
	}

	existing, _, err := c.store.GetPinnedKey(p.User)
	if err == nil && existing != "" && existing != p.KeyFingerprint {
		c.logger.Warn("KEY CHANGE DETECTED",
			"user", p.User,
			"old_fingerprint", existing,
			"new_fingerprint", p.KeyFingerprint,
		)
		c.store.ClearVerified(p.User)

		// Phase 21 F3.a closure 2026-04-19 — dispatch to the TUI so
		// the user sees a blocking modal with old vs. new fingerprints
		// and an explicit Accept / Disconnect choice. The callback
		// runs on the readLoop goroutine; TUI handlers push to a
		// channel and return.
		if c.cfg.OnKeyWarning != nil {
			c.cfg.OnKeyWarning(p.User, existing, p.KeyFingerprint)
		}
	}

	c.store.PinKey(p.User, p.KeyFingerprint, p.PubKey)
}

// LoadRoomMessages loads messages from local DB for a room.
func (c *Client) LoadRoomMessages(room string, limit int) ([]store.StoredMessage, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.GetRoomMessages(room, limit)
}

// LoadGroupMessages loads messages from local DB for a group DM.
func (c *Client) LoadGroupMessages(groupID string, limit int) ([]store.StoredMessage, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.GetGroupMessages(groupID, limit)
}

// LoadDMMessages loads messages from local DB for a 1:1 DM.
func (c *Client) LoadDMMessages(dmID string, limit int) ([]store.StoredMessage, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.GetDMMessages(dmID, limit)
}

// SearchMessages searches local DB.
func (c *Client) SearchMessages(query string, limit int) ([]store.StoredMessage, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.SearchMessages(query, limit)
}

// Store returns the local store (for direct access if needed).
func (c *Client) Store() *store.Store {
	return c.store
}
