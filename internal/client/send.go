package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"crypto/ed25519"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// SendRoomMessage encrypts and sends a message to a room.
func (c *Client) SendRoomMessage(room, body string, replyTo string, mentions []string) error {
	c.mu.Lock()
	epoch := c.currentEpoch[room]
	key := c.epochKeys[room][epoch]
	seqKey := "room:" + room
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	if key == nil {
		return fmt.Errorf("no epoch key for room %s", room)
	}

	// Build payload
	payload := protocol.DecryptedPayload{
		Body:     body,
		Seq:      seq,
		DeviceID: c.cfg.DeviceID,
		Mentions: mentions,
		ReplyTo:  replyTo,
	}
	payloadJSON, _ := json.Marshal(payload)

	// Encrypt
	encrypted, err := crypto.Encrypt(key, payloadJSON)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	// Decode payload for signing (sign the ciphertext bytes, not the base64)
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)

	// Sign
	sig := crypto.SignRoom(c.privKey, payloadBytes, room, epoch)

	return c.enc.Encode(protocol.Send{
		Type:      "send",
		Room:      room,
		Epoch:     epoch,
		Payload:   encrypted,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
}

// SendDMMessage encrypts and sends a DM message.
func (c *Client) SendDMMessage(conversation, body string, replyTo string, mentions []string) error {
	c.mu.RLock()
	// TODO: get member list for this conversation from local state
	c.mu.RUnlock()

	// Generate per-message key
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}

	// Build payload
	c.mu.Lock()
	seqKey := "conv:" + conversation
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	payload := protocol.DecryptedPayload{
		Body:     body,
		Seq:      seq,
		DeviceID: c.cfg.DeviceID,
		Mentions: mentions,
		ReplyTo:  replyTo,
	}
	payloadJSON, _ := json.Marshal(payload)

	// Encrypt
	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		return err
	}

	// Wrap key for each member
	wrappedKeys, err := c.wrapKeyForConversation(conversation, msgKey)
	if err != nil {
		return err
	}

	// Sign
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, conversation, wrappedKeys)

	return c.enc.Encode(protocol.SendDM{
		Type:         "send_dm",
		Conversation: conversation,
		WrappedKeys:  wrappedKeys,
		Payload:      encrypted,
		Signature:    base64.StdEncoding.EncodeToString(sig),
	})
}

// wrapKeyForConversation wraps a symmetric key for all members of a conversation.
func (c *Client) wrapKeyForConversation(conversation string, key []byte) (map[string]string, error) {
	// TODO: get member list from local conversation state
	// For now, look up profiles for all known members
	c.mu.RLock()
	defer c.mu.RUnlock()

	wrappedKeys := make(map[string]string)

	// Wrap for self (multi-device)
	selfPub := c.privKey.Public().(ed25519.PublicKey)
	wrapped, err := crypto.WrapKey(key, selfPub)
	if err != nil {
		return nil, fmt.Errorf("wrap for self: %w", err)
	}
	wrappedKeys[c.username] = wrapped

	// TODO: wrap for other members using their pubkeys from profiles
	// This requires knowing the conversation member list

	return wrappedKeys, nil
}

// UnwrapKey unwraps a wrapped key using the client's private key.
func (c *Client) UnwrapKey(wrappedBase64 string) ([]byte, error) {
	return crypto.UnwrapKey(wrappedBase64, c.privKey)
}

// DecryptRoomMessage decrypts a room message payload.
func (c *Client) DecryptRoomMessage(room string, epoch int64, payloadBase64 string) (*protocol.DecryptedPayload, error) {
	c.mu.RLock()
	key := c.epochKeys[room][epoch]
	c.mu.RUnlock()

	if key == nil {
		return nil, fmt.Errorf("no epoch key for room %s epoch %d", room, epoch)
	}

	plaintext, err := crypto.Decrypt(key, payloadBase64)
	if err != nil {
		return nil, err
	}

	var payload protocol.DecryptedPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// DecryptDMMessage decrypts a DM message payload.
func (c *Client) DecryptDMMessage(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedPayload, error) {
	c.mu.RLock()
	username := c.username
	c.mu.RUnlock()

	wrappedKey, ok := wrappedKeys[username]
	if !ok {
		return nil, fmt.Errorf("no wrapped key for user %s", username)
	}

	// Unwrap the per-message key
	msgKey, err := c.UnwrapKey(wrappedKey)
	if err != nil {
		return nil, fmt.Errorf("unwrap: %w", err)
	}

	// Decrypt payload
	plaintext, err := crypto.Decrypt(msgKey, payloadBase64)
	if err != nil {
		return nil, err
	}

	var payload protocol.DecryptedPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// SendTyping sends a typing indicator.
func (c *Client) SendTyping(room, conversation string) error {
	return c.enc.Encode(protocol.Typing{
		Type:         "typing",
		Room:         room,
		Conversation: conversation,
	})
}

// SendRead sends a read receipt.
func (c *Client) SendRead(room, conversation, lastRead string) error {
	return c.enc.Encode(protocol.Read{
		Type:         "read",
		Room:         room,
		Conversation: conversation,
		LastRead:     lastRead,
	})
}

// SendDelete sends a message deletion request.
func (c *Client) SendDelete(id string) error {
	return c.enc.Encode(protocol.Delete{
		Type: "delete",
		ID:   id,
	})
}

// CreateDM creates a new DM conversation.
func (c *Client) CreateDM(members []string, name string) error {
	return c.enc.Encode(protocol.CreateDM{
		Type:    "create_dm",
		Members: members,
		Name:    name,
	})
}

// RequestHistory requests older messages.
func (c *Client) RequestHistory(room, conversation, before string, limit int) error {
	return c.enc.Encode(protocol.History{
		Type:         "history",
		Room:         room,
		Conversation: conversation,
		Before:       before,
		Limit:        limit,
	})
}
