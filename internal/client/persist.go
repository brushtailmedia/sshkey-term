package client

import (
	"encoding/base64"
	"encoding/json"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// storeRoomMessage decrypts and stores a room message in the local DB.
func (c *Client) storeRoomMessage(raw json.RawMessage) {
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
		c.checkReplay(msg.From, payload.DeviceID, msg.Room, "", payload.Seq)

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

	c.store.InsertMessage(store.StoredMessage{
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
func (c *Client) storeGroupMessage(raw json.RawMessage) {
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

	c.checkReplay(msg.From, payload.DeviceID, "", msg.Group, payload.Seq)

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

	c.store.InsertMessage(store.StoredMessage{
		ID:          msg.ID,
		Sender:      msg.From,
		Body:        payload.Body,
		TS:          msg.TS,
		Group:       msg.Group,
		ReplyTo:     payload.ReplyTo,
		Mentions:    payload.Mentions,
		Attachments: attachments,
	})
}

// storeDMMessage decrypts and stores a 1:1 DM message in the local DB.
func (c *Client) storeDMMessage(raw json.RawMessage) {
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
		c.checkReplay(msg.From, payload.DeviceID, "", msg.DM, payload.Seq)

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

	c.store.InsertMessage(store.StoredMessage{
		ID:          msg.ID,
		Sender:      msg.From,
		Body:        body,
		TS:          msg.TS,
		DM:          msg.DM,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
}

// storeReaction decrypts and persists a reaction to the local DB.
func (c *Client) storeReaction(raw json.RawMessage) {
	if c.store == nil {
		return
	}

	var r protocol.Reaction
	if err := json.Unmarshal(raw, &r); err != nil {
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
func (c *Client) checkReplay(sender, deviceID, room, group string, seq int64) {
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
		c.logger.Warn("possible replay detected",
			"sender", sender,
			"device", deviceID,
			"seq", seq,
			"high_water", existing,
		)
		return
	}

	c.store.StoreSeqMark(key, seq)
}

// StoreProfile pins a user's key on first encounter, warns on change.
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
