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

	// Store the key locally (pending — will be confirmed by epoch_confirmed).
	// We store it now so we're ready to use it when confirmed.
	c.mu.Lock()
	if c.epochKeys[trigger.Room] == nil {
		c.epochKeys[trigger.Room] = make(map[int64][]byte)
	}
	c.epochKeys[trigger.Room][trigger.NewEpoch] = epochKey
	store := c.store
	c.mu.Unlock()

	// Persist to local DB so the key survives restart. If the server
	// rejects the rotation (epoch_conflict / stale_member_list), the
	// persisted key is harmless — subsequent connects will receive the
	// winning epoch key and overwrite this one.
	if store != nil {
		if err := store.StoreEpochKey(trigger.Room, trigger.NewEpoch, epochKey); err != nil {
			c.logger.Warn("failed to persist generated epoch key", "room", trigger.Room, "epoch", trigger.NewEpoch, "error", err)
		}
	}

	c.logger.Info("epoch rotation submitted",
		"room", trigger.Room,
		"epoch", trigger.NewEpoch,
		"members", len(wrappedKeys),
	)
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

// handleSyncBatchKeys unwraps and stores epoch keys from a sync batch,
// then persists every inner message and reaction to the local DB before
// forwarding them to the UI.
//
// 2026-05-05 fix — pre-fix this only stored epoch keys and forwarded
// inner messages via OnMessage; the storeRoomMessage / storeGroupMessage
// / storeDMMessage / storeReaction paths in handleInternal were never
// reached for sync_batch contents. So:
//
//   - Sync'd messages lived only in TUI memory (not in the local DB).
//     Next context switch / restart, they were gone.
//   - storeReaction's orphan-check (`parent message in DB?`) failed for
//     every sync'd reaction, since the parent was never stored. The
//     reaction was silently dropped on the client side.
//   - The TUI's sync_batch handler still emitted reactions via
//     AddReactionDecrypted (in-memory) and SyncReactionsForMessage
//     (clears + reloads from DB). The reload found nothing — the
//     newly-added in-memory reactions were immediately wiped.
//
// Net effect: scrolled-back room reactions appeared to "flash on then
// disappear" or just never render.
//
// Fix: handleInternal each inner message AND each inner reaction. That
// runs the storage side-effects (decrypt, signature verify, persist)
// and primes the parent-in-DB check for subsequent reactions.
func (c *Client) handleSyncBatchKeys(raw json.RawMessage) {
	var batch protocol.SyncBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return
	}

	for _, ek := range batch.EpochKeys {
		c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey)
	}

	// Persist each inner message. Persist FIRST so a reaction in the
	// same batch (same iteration of the read loop) finds its parent
	// when storeReaction's orphan check runs.
	//
	// UI dispatch intentionally stays on the outer sync_batch frame.
	// Forwarding each inner message here creates double-dispatch into
	// the TUI (inner + outer replay), which over-applies live side
	// effects like unread increments.
	for _, msgRaw := range batch.Messages {
		msgType, err := protocol.TypeOf(msgRaw)
		if err != nil {
			continue
		}

		c.handleCatchupMessage(msgType, msgRaw)
	}

	// Persist reactions. The TUI's sync_batch handler iterates
	// batch.Reactions itself for display, so we don't need to forward
	// them via OnMessage — only the storage side is missing.
	for _, reactRaw := range batch.Reactions {
		c.handleInternal("reaction", reactRaw)
	}
}

// handleHistoryKeys unwraps epoch keys from a history result and
// persists the inner messages + reactions to the local DB, mirroring
// handleSyncBatchKeys for the same reason. See that function's
// doc-comment for the bug history.
//
// Unlike sync_batch, history_result inner items are NOT forwarded via
// OnMessage — the TUI receives the outer history_result and unmarshals
// the inner Messages/Reactions itself for display. This function only
// adds the storage side of the equation.
func (c *Client) handleHistoryKeys(raw json.RawMessage) {
	var result protocol.HistoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return
	}

	for _, ek := range result.EpochKeys {
		c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey)
	}

	// Persist inner messages first so the orphan check in
	// storeReaction passes when reactions are processed below.
	for _, msgRaw := range result.Messages {
		msgType, err := protocol.TypeOf(msgRaw)
		if err != nil {
			continue
		}
		c.handleCatchupMessage(msgType, msgRaw)
	}

	for _, reactRaw := range result.Reactions {
		c.handleInternal("reaction", reactRaw)
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
