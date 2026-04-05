package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"crypto/ed25519"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// SendRoomMessage encrypts and sends a message to a room. Zero-attachments
// convenience wrapper around SendRoomMessageFull.
func (c *Client) SendRoomMessage(room, body string, replyTo string, mentions []string) error {
	return c.SendRoomMessageFull(room, body, replyTo, mentions, nil)
}

// SendRoomMessageFull is the full-featured room sender with attachments.
// Attachments reference file_ids from prior UploadFile calls; their contents
// are NOT encrypted here (the file bytes were already encrypted with the
// current epoch key during upload). The attachment metadata travels inside
// the encrypted message payload.
func (c *Client) SendRoomMessageFull(room, body string, replyTo string, mentions []string, attachments []protocol.Attachment) error {
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

	// Stamp file_epoch on each attachment (which epoch's key decrypts the
	// file bytes — same as the message epoch for freshly-uploaded files).
	for i := range attachments {
		if attachments[i].FileEpoch == 0 {
			attachments[i].FileEpoch = epoch
		}
	}

	// Build payload
	payload := protocol.DecryptedPayload{
		Body:        body,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     replyTo,
		Attachments: attachments,
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

	// Collect file_ids for the envelope (server needs these for retention
	// bookkeeping even though it can't see the attachment metadata)
	var fileIDs []string
	for _, a := range attachments {
		fileIDs = append(fileIDs, a.FileID)
	}

	return c.enc.Encode(protocol.Send{
		Type:      "send",
		Room:      room,
		Epoch:     epoch,
		Payload:   encrypted,
		FileIDs:   fileIDs,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
}

// SendRoomMessageFile uploads a local file and sends a message that
// references it as an attachment in a single call. Returns an error if any
// step fails.
func (c *Client) SendRoomMessageFile(room, body, filePath, replyTo string, mentions []string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	fileID, err := c.UploadFile(filePath, room, "")
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	attachment := protocol.Attachment{
		FileID: fileID,
		Name:   filepath.Base(filePath),
		Size:   info.Size(),
		Mime:   sniffMimeType(filePath),
	}
	return c.SendRoomMessageFull(room, body, replyTo, mentions, []protocol.Attachment{attachment})
}

// sniffMimeType returns a best-guess content type from the file extension.
// Lightweight table covering the common cases; falls back to application/
// octet-stream. The server never sees this value (it's inside the encrypted
// payload) — it's purely for the receiving client's display.
func sniffMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".txt", ".md":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".html":
		return "text/html"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
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
	c.mu.RLock()
	members := c.convMembers[conversation]
	c.mu.RUnlock()

	if len(members) == 0 {
		return nil, fmt.Errorf("no members for conversation %s", conversation)
	}

	wrappedKeys := make(map[string]string)

	for _, member := range members {
		var pubKey ed25519.PublicKey

		if member == c.Username() {
			// Self — use our own public key
			pubKey = c.privKey.Public().(ed25519.PublicKey)
		} else {
			// Other member — get pubkey from profile
			c.mu.RLock()
			profile := c.profiles[member]
			c.mu.RUnlock()

			if profile == nil {
				return nil, fmt.Errorf("no profile for member %s", member)
			}

			var err error
			pubKey, err = crypto.ParseSSHPubKey(profile.PubKey)
			if err != nil {
				return nil, fmt.Errorf("parse pubkey for %s: %w", member, err)
			}
		}

		wrapped, err := crypto.WrapKey(key, pubKey)
		if err != nil {
			return nil, fmt.Errorf("wrap for %s: %w", member, err)
		}
		wrappedKeys[member] = wrapped
	}

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

// SendRoomReaction sends an encrypted reaction to a room message.
func (c *Client) SendRoomReaction(room, targetMsgID, emoji string) error {
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

	// Build reaction payload
	reactionPayload := protocol.DecryptedReaction{
		Emoji:    emoji,
		Target:   targetMsgID,
		Seq:      seq,
		DeviceID: c.cfg.DeviceID,
	}
	payloadJSON, _ := json.Marshal(reactionPayload)

	// Encrypt
	encrypted, err := crypto.Encrypt(key, payloadJSON)
	if err != nil {
		return err
	}

	// Sign
	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignRoom(c.privKey, payloadBytes, room, epoch)

	return c.enc.Encode(protocol.React{
		Type:      "react",
		ID:        targetMsgID,
		Room:      room,
		Epoch:     epoch,
		Payload:   encrypted,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
}

// SendDMReaction sends an encrypted reaction to a DM message.
func (c *Client) SendDMReaction(conversation, targetMsgID, emoji string) error {
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}

	c.mu.Lock()
	seqKey := "conv:" + conversation
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	reactionPayload := protocol.DecryptedReaction{
		Emoji:    emoji,
		Target:   targetMsgID,
		Seq:      seq,
		DeviceID: c.cfg.DeviceID,
	}
	payloadJSON, _ := json.Marshal(reactionPayload)

	encrypted, err := crypto.Encrypt(msgKey, payloadJSON)
	if err != nil {
		return err
	}

	wrappedKeys, err := c.wrapKeyForConversation(conversation, msgKey)
	if err != nil {
		return err
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, conversation, wrappedKeys)

	return c.enc.Encode(protocol.React{
		Type:         "react",
		ID:           targetMsgID,
		Conversation: conversation,
		WrappedKeys:  wrappedKeys,
		Payload:      encrypted,
		Signature:    base64.StdEncoding.EncodeToString(sig),
	})
}

// SendUnreact removes a reaction by its server-assigned reaction_id. Used
// by the explicit "Remove my reaction" UX — the client looks up the
// reaction_id from its local (message_id, user, emoji) index and sends it.
func (c *Client) SendUnreact(reactionID string) error {
	return c.enc.Encode(protocol.Unreact{
		Type:       "unreact",
		ReactionID: reactionID,
	})
}

// DecryptRoomReaction decrypts a reaction payload from a room.
func (c *Client) DecryptRoomReaction(room string, epoch int64, payloadBase64 string) (*protocol.DecryptedReaction, error) {
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

	var reaction protocol.DecryptedReaction
	if err := json.Unmarshal(plaintext, &reaction); err != nil {
		return nil, err
	}
	return &reaction, nil
}

// DecryptDMReaction decrypts a reaction payload from a DM.
func (c *Client) DecryptDMReaction(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedReaction, error) {
	c.mu.RLock()
	username := c.username
	c.mu.RUnlock()

	wrappedKey, ok := wrappedKeys[username]
	if !ok {
		return nil, fmt.Errorf("no wrapped key for user %s", username)
	}

	msgKey, err := c.UnwrapKey(wrappedKey)
	if err != nil {
		return nil, err
	}

	plaintext, err := crypto.Decrypt(msgKey, payloadBase64)
	if err != nil {
		return nil, err
	}

	var reaction protocol.DecryptedReaction
	if err := json.Unmarshal(plaintext, &reaction); err != nil {
		return nil, err
	}
	return &reaction, nil
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

// SendRetireMe permanently retires the current user's account. After the
// server processes this, the session is closed and the key will no longer
// authenticate. This is irreversible — a new account must be created via
// admin action to regain access to this server.
//
// Valid reasons: "self_compromise" (key suspected compromised), "switching_key"
// (user upgrading to a new key), "other".
func (c *Client) SendRetireMe(reason string) error {
	return c.enc.Encode(protocol.RetireMe{
		Type:   "retire_me",
		Reason: reason,
	})
}

// SendListDevices requests the list of devices registered for this user.
// The response arrives as a device_list message and should be handled by the
// OnMessage callback.
func (c *Client) SendListDevices() error {
	return c.enc.Encode(protocol.ListDevices{Type: "list_devices"})
}

// SendRevokeDevice asks the server to revoke one of the user's own devices.
// The response arrives as a device_revoke_result. The server validates
// ownership and rejects attempts to revoke devices belonging to other users.
func (c *Client) SendRevokeDevice(deviceID string) error {
	return c.enc.Encode(protocol.RevokeDevice{
		Type:     "revoke_device",
		DeviceID: deviceID,
	})
}
