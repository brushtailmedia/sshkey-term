package client

import (
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

	payload, err := c.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
	if err == nil {
		body = payload.Body
		replyTo = payload.ReplyTo
		mentions = payload.Mentions

		// Replay detection
		c.checkReplay(msg.From, payload.DeviceID, msg.Room, "", payload.Seq)
	}

	c.store.InsertMessage(store.StoredMessage{
		ID:       msg.ID,
		Sender:   msg.From,
		Body:     body,
		TS:       msg.TS,
		Room:     msg.Room,
		Epoch:    msg.Epoch,
		ReplyTo:  replyTo,
		Mentions: mentions,
	})
}

// storeDMMessage decrypts and stores a DM message in the local DB.
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

	payload, err := c.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
	if err == nil {
		body = payload.Body
		replyTo = payload.ReplyTo
		mentions = payload.Mentions

		// Replay detection
		c.checkReplay(msg.From, payload.DeviceID, "", msg.Conversation, payload.Seq)
	}

	c.store.InsertMessage(store.StoredMessage{
		ID:           msg.ID,
		Sender:       msg.From,
		Body:         body,
		TS:           msg.TS,
		Conversation: msg.Conversation,
		ReplyTo:      replyTo,
		Mentions:     mentions,
	})
}

// checkReplay checks for replay attacks using seq high-water marks.
func (c *Client) checkReplay(sender, deviceID, room, conv string, seq int64) {
	if c.store == nil || seq == 0 {
		return
	}

	target := room
	if target == "" {
		target = conv
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

// LoadConvMessages loads messages from local DB for a conversation.
func (c *Client) LoadConvMessages(convID string, limit int) ([]store.StoredMessage, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.GetConvMessages(convID, limit)
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
