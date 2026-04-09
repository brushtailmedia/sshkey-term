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
	fileID, uploadEpoch, err := c.UploadFile(filePath, room, "")
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	attachment := protocol.Attachment{
		FileID:    fileID,
		Name:      filepath.Base(filePath),
		Size:      info.Size(),
		Mime:      sniffMimeType(filePath),
		FileEpoch: uploadEpoch, // pin to the epoch used for encryption
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

// SendGroupMessage encrypts and sends a group DM message. Zero-attachments
// convenience wrapper around SendGroupMessageFull.
func (c *Client) SendGroupMessage(group, body string, replyTo string, mentions []string) error {
	return c.SendGroupMessageFull(group, body, replyTo, mentions, nil)
}

// SendGroupMessageFull sends a group DM message with attachments. Generates a
// fresh per-message key K_msg to encrypt the payload and wraps it for each
// member. Attachments must already have been uploaded and must carry their
// own base64 FileKey (K_file) so recipients can decrypt each file
// independently after decrypting the message payload. See PROTOCOL.md
// "DM attachments".
func (c *Client) SendGroupMessageFull(group, body, replyTo string, mentions []string, attachments []protocol.Attachment) error {
	// Build payload
	c.mu.Lock()
	seqKey := "group:" + group
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	payload := protocol.DecryptedPayload{
		Body:        body,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     replyTo,
		Attachments: attachments,
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

	fileIDs := make([]string, 0, len(attachments))
	for _, a := range attachments {
		fileIDs = append(fileIDs, a.FileID)
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, group, wrappedKeys)

	return c.enc.Encode(protocol.SendGroup{
		Type:        "send_group",
		Group:       group,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		FileIDs:     fileIDs,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	})
}

// SendGroupMessageFile uploads a local file into a group DM and sends a
// message referencing it as an attachment. Each file is encrypted with its
// own per-file key K_file, which is stored inside the encrypted message
// payload's attachment entry.
func (c *Client) SendGroupMessageFile(group, body, filePath, replyTo string, mentions []string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	// Generate a fresh per-file key K_file for this attachment.
	fileKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	fileID, err := c.UploadGroupFile(filePath, group, fileKey)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	att := protocol.Attachment{
		FileID:  fileID,
		Name:    filepath.Base(filePath),
		Size:    info.Size(),
		Mime:    sniffMimeType(filePath),
		FileKey: base64.StdEncoding.EncodeToString(fileKey),
	}
	return c.SendGroupMessageFull(group, body, replyTo, mentions, []protocol.Attachment{att})
}

// wrapKeyForGroup wraps a symmetric key for all members of a group DM.
func (c *Client) wrapKeyForGroup(group string, key []byte) (map[string]string, error) {
	c.mu.RLock()
	members := c.groupMembers[group]
	c.mu.RUnlock()

	if len(members) == 0 {
		return nil, fmt.Errorf("no members for group %s", group)
	}

	wrappedKeys := make(map[string]string)

	for _, member := range members {
		var pubKey ed25519.PublicKey

		if member == c.UserID() {
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

// RoomEpochKey returns the raw epoch key for a (room, epoch) pair, or nil
// if not known. Checks local DB as fallback if not in memory.
func (c *Client) RoomEpochKey(room string, epoch int64) []byte {
	c.mu.RLock()
	key := c.epochKeys[room][epoch]
	c.mu.RUnlock()
	if key == nil && c.store != nil {
		if dbKey, err := c.store.GetEpochKey(room, epoch); err == nil && dbKey != nil {
			c.mu.Lock()
			if c.epochKeys[room] == nil {
				c.epochKeys[room] = make(map[int64][]byte)
			}
			c.epochKeys[room][epoch] = dbKey
			c.mu.Unlock()
			key = dbKey
		}
	}
	return key
}

// LoadEpochKeysFromDB loads specific epoch keys from the local DB into the
// in-memory cache. Called when displaying messages that reference epochs not
// yet in memory (e.g., messages loaded from local DB on room switch).
func (c *Client) LoadEpochKeysFromDB(room string, epochs []int64) {
	if c.store == nil || len(epochs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epochKeys[room] == nil {
		c.epochKeys[room] = make(map[int64][]byte)
	}
	for _, epoch := range epochs {
		if c.epochKeys[room][epoch] != nil {
			continue // already in memory
		}
		if key, err := c.store.GetEpochKey(room, epoch); err == nil && key != nil {
			c.epochKeys[room][epoch] = key
		}
	}
}

// DecryptRoomMessage decrypts a room message payload.
func (c *Client) DecryptRoomMessage(room string, epoch int64, payloadBase64 string) (*protocol.DecryptedPayload, error) {
	c.mu.RLock()
	key := c.epochKeys[room][epoch]
	c.mu.RUnlock()

	// Try loading from local DB if not in memory
	if key == nil && c.store != nil {
		if dbKey, err := c.store.GetEpochKey(room, epoch); err == nil && dbKey != nil {
			c.mu.Lock()
			if c.epochKeys[room] == nil {
				c.epochKeys[room] = make(map[int64][]byte)
			}
			c.epochKeys[room][epoch] = dbKey
			c.mu.Unlock()
			key = dbKey
		}
	}

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

// DecryptGroupMessage decrypts a group DM message payload.
func (c *Client) DecryptGroupMessage(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedPayload, error) {
	c.mu.RLock()
	username := c.userID
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

// SendGroupReaction sends an encrypted reaction to a group DM message.
func (c *Client) SendGroupReaction(group, targetMsgID, emoji string) error {
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}

	c.mu.Lock()
	seqKey := "group:" + group
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

	wrappedKeys, err := c.wrapKeyForGroup(group, msgKey)
	if err != nil {
		return err
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, group, wrappedKeys)

	return c.enc.Encode(protocol.React{
		Type:        "react",
		ID:          targetMsgID,
		Group:       group,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
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

// DecryptGroupReaction decrypts a reaction payload from a group DM.
func (c *Client) DecryptGroupReaction(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedReaction, error) {
	c.mu.RLock()
	username := c.userID
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

// SendTyping sends a typing indicator. Exactly one of room / group / dm
// should be non-empty — the server routes the broadcast based on which
// context the caller is typing in.
func (c *Client) SendTyping(room, group, dm string) error {
	return c.enc.Encode(protocol.Typing{
		Type:  "typing",
		Room:  room,
		Group: group,
		DM:    dm,
	})
}

// SendRead sends a read receipt. Exactly one of room / group / dm should
// be non-empty — the server routes the broadcast to the other participants
// of that context (plus the caller's own other sessions for multi-device
// sync).
func (c *Client) SendRead(room, group, dm, lastRead string) error {
	return c.enc.Encode(protocol.Read{
		Type:     "read",
		Room:     room,
		Group:    group,
		DM:       dm,
		LastRead: lastRead,
	})
}

// SendDelete sends a message deletion request.
func (c *Client) SendDelete(id string) error {
	return c.enc.Encode(protocol.Delete{
		Type: "delete",
		ID:   id,
	})
}

// CreateGroup creates a new group DM.
func (c *Client) CreateGroup(members []string, name string) error {
	return c.enc.Encode(protocol.CreateGroup{
		Type:    "create_group",
		Members: members,
		Name:    name,
	})
}

// DeleteGroup sends a delete_group request for a group DM. The server
// will run the leave logic if the user is still a member, record a
// deletion intent for offline-device catchup, and echo group_deleted
// back to all of the user's connected sessions. Idempotent: safe to
// call on a group the user has already left.
func (c *Client) DeleteGroup(groupID string) error {
	return c.enc.Encode(protocol.DeleteGroup{
		Type:  "delete_group",
		Group: groupID,
	})
}

// CreateDM creates a new 1:1 DM with a single other user.
func (c *Client) CreateDM(other string) error {
	return c.enc.Encode(protocol.CreateDM{
		Type:  "create_dm",
		Other: other,
	})
}

// SendDMMessage encrypts and sends a 1:1 DM message.
func (c *Client) SendDMMessage(dmID, body string, replyTo string, mentions []string) error {
	return c.SendDMMessageFull(dmID, body, replyTo, mentions, nil)
}

// SendDMMessageFull sends a 1:1 DM message with attachments.
func (c *Client) SendDMMessageFull(dmID, body, replyTo string, mentions []string, attachments []protocol.Attachment) error {
	c.mu.Lock()
	seqKey := "dm:" + dmID
	c.seqCounters[seqKey]++
	seq := c.seqCounters[seqKey]
	c.mu.Unlock()

	payload := protocol.DecryptedPayload{
		Body:        body,
		Seq:         seq,
		DeviceID:    c.cfg.DeviceID,
		Mentions:    mentions,
		ReplyTo:     replyTo,
		Attachments: attachments,
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

	fileIDs := make([]string, 0, len(attachments))
	for _, a := range attachments {
		fileIDs = append(fileIDs, a.FileID)
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, dmID, wrappedKeys)

	return c.enc.Encode(protocol.SendDM{
		Type:        "send_dm",
		DM:          dmID,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		FileIDs:     fileIDs,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	})
}

// SendDMMessageFile uploads a local file into a 1:1 DM and sends it.
func (c *Client) SendDMMessageFile(dmID, body, filePath, replyTo string, mentions []string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	fileKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	fileID, err := c.UploadDMFile(filePath, dmID, fileKey)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	att := protocol.Attachment{
		FileID:  fileID,
		Name:    filepath.Base(filePath),
		Size:    info.Size(),
		Mime:    sniffMimeType(filePath),
		FileKey: base64.StdEncoding.EncodeToString(fileKey),
	}
	return c.SendDMMessageFull(dmID, body, replyTo, mentions, []protocol.Attachment{att})
}

// wrapKeyForDM wraps a symmetric key for both members of a 1:1 DM.
func (c *Client) wrapKeyForDM(dmID string, key []byte) (map[string]string, error) {
	c.mu.RLock()
	pair := c.dms[dmID]
	c.mu.RUnlock()

	if pair[0] == "" || pair[1] == "" {
		return nil, fmt.Errorf("no members for DM %s", dmID)
	}

	wrappedKeys := make(map[string]string)

	for _, member := range pair {
		var pubKey ed25519.PublicKey

		if member == c.UserID() {
			pubKey = c.privKey.Public().(ed25519.PublicKey)
		} else {
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

// DecryptDMMessage decrypts a 1:1 DM message payload.
func (c *Client) DecryptDMMessage(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedPayload, error) {
	c.mu.RLock()
	username := c.userID
	c.mu.RUnlock()

	wrappedKey, ok := wrappedKeys[username]
	if !ok {
		return nil, fmt.Errorf("no wrapped key for user %s", username)
	}

	msgKey, err := c.UnwrapKey(wrappedKey)
	if err != nil {
		return nil, fmt.Errorf("unwrap: %w", err)
	}

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

// SendDMReaction sends an encrypted reaction to a 1:1 DM message.
func (c *Client) SendDMReaction(dmID, targetMsgID, emoji string) error {
	msgKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}

	c.mu.Lock()
	seqKey := "dm:" + dmID
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

	wrappedKeys, err := c.wrapKeyForDM(dmID, msgKey)
	if err != nil {
		return err
	}

	payloadBytes, _ := base64.StdEncoding.DecodeString(encrypted)
	sig := crypto.SignDM(c.privKey, payloadBytes, dmID, wrappedKeys)

	return c.enc.Encode(protocol.React{
		Type:        "react",
		ID:          targetMsgID,
		DM:          dmID,
		WrappedKeys: wrappedKeys,
		Payload:     encrypted,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	})
}

// DecryptDMReaction decrypts a reaction payload from a 1:1 DM.
func (c *Client) DecryptDMReaction(wrappedKeys map[string]string, payloadBase64 string) (*protocol.DecryptedReaction, error) {
	c.mu.RLock()
	username := c.userID
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

// RequestHistory requests older messages.
func (c *Client) RequestHistory(room, group, before string, limit int) error {
	return c.enc.Encode(protocol.History{
		Type:   "history",
		Room:   room,
		Group:  group,
		Before: before,
		Limit:  limit,
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

// SendListPendingKeys requests the list of pending (unapproved) SSH keys.
// Admin-only — the server rejects non-admin callers with an error.
// The response arrives as a pending_keys_list message.
func (c *Client) SendListPendingKeys() error {
	return c.enc.Encode(protocol.ListPendingKeys{Type: "list_pending_keys"})
}

// HasPendingKeys returns true if any admin_notify events have been received
// since the last pending_keys_list refresh.
func (c *Client) HasPendingKeys() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasPendingKeys
}

// PendingKeys returns the most recent pending keys list from the server.
func (c *Client) PendingKeys() []protocol.PendingKeyEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pendingKeys
}

// ClearPendingAlert resets the pending-keys indicator (called when the
// admin opens the /pending panel).
func (c *Client) ClearPendingAlert() {
	c.mu.Lock()
	c.hasPendingKeys = len(c.pendingKeys) > 0
	c.mu.Unlock()
}

// RequestRoomMembers asks the server for the member list of a room.
func (c *Client) RequestRoomMembers(room string) error {
	return c.enc.Encode(protocol.RoomMembers{Type: "room_members", Room: room})
}

// RoomMembersList returns the most recently received room members list.
func (c *Client) RoomMembersList() (string, []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.roomMembersRoom, c.roomMembers
}
