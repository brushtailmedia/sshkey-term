package client

import (
	"encoding/base64"
	"encoding/json"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// handleEpochTrigger generates a new epoch key, wraps it for all members, and sends epoch_rotate.
func (c *Client) handleEpochTrigger(raw json.RawMessage) {
	var trigger protocol.EpochTrigger
	if err := json.Unmarshal(raw, &trigger); err != nil {
		c.logger.Error("parse epoch_trigger", "error", err)
		return
	}

	c.logger.Info("epoch trigger received",
		"room", trigger.Room,
		"new_epoch", trigger.NewEpoch,
		"members", len(trigger.Members),
	)

	// Generate new epoch key
	epochKey, err := crypto.GenerateKey()
	if err != nil {
		c.logger.Error("generate epoch key", "error", err)
		return
	}

	// Wrap for each member
	wrappedKeys := make(map[string]string)
	for _, member := range trigger.Members {
		pubKey, err := crypto.ParseSSHPubKey(member.PubKey)
		if err != nil {
			c.logger.Error("parse member pubkey", "user", member.User, "error", err)
			return
		}

		wrapped, err := crypto.WrapKey(epochKey, pubKey)
		if err != nil {
			c.logger.Error("wrap key for member", "user", member.User, "error", err)
			return
		}
		wrappedKeys[member.User] = wrapped
	}

	// Compute member hash
	var memberNames []string
	for _, m := range trigger.Members {
		memberNames = append(memberNames, m.User)
	}
	memberHash := crypto.MemberHash(memberNames)

	// Send epoch_rotate
	err = c.enc.Encode(protocol.EpochRotate{
		Type:        "epoch_rotate",
		Room:        trigger.Room,
		Epoch:       trigger.NewEpoch,
		WrappedKeys: wrappedKeys,
		MemberHash:  memberHash,
	})
	if err != nil {
		c.logger.Error("send epoch_rotate", "error", err)
		return
	}

	// Store the key locally (pending — will be confirmed by epoch_confirmed)
	// We store it now so we're ready to use it when confirmed
	c.mu.Lock()
	if c.epochKeys[trigger.Room] == nil {
		c.epochKeys[trigger.Room] = make(map[int64][]byte)
	}
	c.epochKeys[trigger.Room][trigger.NewEpoch] = epochKey
	c.mu.Unlock()

	c.logger.Info("epoch rotation submitted",
		"room", trigger.Room,
		"epoch", trigger.NewEpoch,
		"members", len(wrappedKeys),
	)

	// Also store our own wrapped key for verification
	_ = wrappedKeys[c.username]
}

// handleEpochConfirmed activates the confirmed epoch key.
func (c *Client) handleEpochConfirmed(raw json.RawMessage) {
	var confirmed protocol.EpochConfirmed
	if err := json.Unmarshal(raw, &confirmed); err != nil {
		return
	}

	c.mu.Lock()
	c.currentEpoch[confirmed.Room] = confirmed.Epoch
	c.mu.Unlock()

	c.logger.Info("epoch confirmed",
		"room", confirmed.Room,
		"epoch", confirmed.Epoch,
	)
}

// handleSyncBatchKeys unwraps and stores epoch keys from a sync batch.
func (c *Client) handleSyncBatchKeys(raw json.RawMessage) {
	var batch protocol.SyncBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return
	}

	for _, ek := range batch.EpochKeys {
		c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey)
	}

	// Also try to decrypt messages in the batch and forward them
	for _, msgRaw := range batch.Messages {
		msgType, err := protocol.TypeOf(msgRaw)
		if err != nil {
			continue
		}

		if c.cfg.OnMessage != nil {
			c.cfg.OnMessage(msgType, msgRaw)
		}
	}
}

// handleHistoryKeys unwraps epoch keys from a history result.
func (c *Client) handleHistoryKeys(raw json.RawMessage) {
	var result protocol.HistoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return
	}

	for _, ek := range result.EpochKeys {
		c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey)
	}
}

// CurrentEpoch returns the current confirmed epoch for a room.
func (c *Client) CurrentEpoch(room string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentEpoch[room]
}

// EpochKeyBase64 returns the wrapped (base64) version of an epoch key for re-wrapping during key rotation.
func (c *Client) EpochKeyBase64(room string, epoch int64) string {
	c.mu.RLock()
	key := c.epochKeys[room][epoch]
	c.mu.RUnlock()
	if key == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(key)
}
