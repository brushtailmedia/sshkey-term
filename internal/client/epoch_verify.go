package client

import (
	"encoding/base64"
	"encoding/json"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// F7 client-side room member attestation verification — the "no shadow reader"
// guarantee. See docs/planning/open/f7-room-member-attestation.md §6.5.
//
// A room epoch key delivered on the CURRENT-epoch path — the live epoch_key
// message (live rotation, on-connect sendEpochKeys, or state-fix) — must be
// verified before it is adopted as current: confirm the rotator's signed
// attestation against the rotator's pinned key, then confirm the attested
// member set matches the roster we can see. Otherwise a malicious server could
// wrap the epoch key for a shadow reader and the room would never notice.
//
// Historical keys (sync_batch / history_result) are deliberately NOT routed
// here — they go through storeEpochKeyHistorical (decryption-only, never
// advancing currentEpoch), which is the sync-path bypass guard.

// verifyAndAdoptEpochKey runs the verify-or-fail-closed decision tree for an
// epoch_key and, on success, adopts it (storeEpochKey advances currentEpoch).
func (c *Client) verifyAndAdoptEpochKey(ek protocol.EpochKey) {
	// 1. Missing attestation → fail-closed. A current-epoch key must carry one
	//    (zero users / no back-compat — the whole fleet ships F7 together).
	if ek.Generator == "" || ek.MemberHash == "" || ek.MemberSig == "" {
		c.failClosedEpoch(ek, "missing attestation")
		return
	}
	// 2. Resolve the generator's key (live profile → pinned fallback via
	//    pubKeyForUser; a key change is caught by StoreProfile's F4 warning).
	genKey := c.pubKeyForUser(ek.Generator)
	if genKey == nil {
		c.failClosedEpoch(ek, "unresolvable generator key")
		return
	}
	// 3. Verify the signature binds (room, epoch, member_hash) under that key —
	//    so the relay can't have rewritten the hash.
	sigBytes, err := base64.StdEncoding.DecodeString(ek.MemberSig)
	if err != nil || !crypto.VerifyEpochRoster(genKey, ek.Room, ek.Epoch, ek.MemberHash, sigBytes) {
		c.failClosedEpoch(ek, "attestation signature verification failed")
		return
	}
	// 4+5. Compare the attested member set to our own roster.
	c.compareRosterAndAdopt(ek, false)
}

// compareRosterAndAdopt does the roster comparison for a signature-verified ek.
// terminal=false is the first attempt (may stash + request a fresh roster);
// terminal=true is the post-refresh drain (adopt or fail-closed, no more retry).
func (c *Client) compareRosterAndAdopt(ek protocol.EpochKey, terminal bool) {
	roster, ok := c.RoomMembers(ek.Room)
	if !ok {
		if terminal {
			c.failClosedEpoch(ek, "room roster unavailable")
			return
		}
		c.stashAndRefresh(ek) // cold roster — fetch it, then re-verify (not an alarm)
		return
	}
	if crypto.MemberHash(roster) == ek.MemberHash {
		c.storeEpochKey(ek.Room, ek.Epoch, ek.WrappedKey) // adopt — advances currentEpoch
		c.TriggerEpochRetry(ek.Room)
		return
	}
	if terminal {
		// Mismatch persists against a freshly-fetched roster — the real alarm:
		// the epoch key was wrapped for a member set we cannot see.
		c.failClosedEpoch(ek, "epoch key wrapped for a different member set than the room roster")
		return
	}
	// First-seen mismatch may be a lagging roster (a dropped room_event). Fetch
	// a fresh room_members_list and re-verify once before alarming.
	c.stashAndRefresh(ek)
}

// stashAndRefresh stores the pending (signature-verified) ek and requests a
// fresh room_members_list; drainPendingEpochAttestation re-runs the comparison
// terminally when that list arrives.
func (c *Client) stashAndRefresh(ek protocol.EpochKey) {
	c.mu.Lock()
	c.pendingEpochAtt[ek.Room] = ek
	c.mu.Unlock()
	_ = c.RequestRoomMembers(ek.Room)
}

// drainPendingEpochAttestation is called from the room_members_list handler
// after the roster is refreshed. It terminally re-verifies any stashed epoch
// key for that room (single-shot: adopt on match, else fail-closed).
func (c *Client) drainPendingEpochAttestation(room string) {
	c.mu.Lock()
	ek, ok := c.pendingEpochAtt[room]
	if ok {
		delete(c.pendingEpochAtt, room)
	}
	c.mu.Unlock()
	if ok {
		c.compareRosterAndAdopt(ek, true)
	}
}

// failClosedEpoch refuses to adopt an epoch key — the room stays un-decryptable
// for that epoch (the user-visible signal), and the client keeps sending under
// the previous epoch per the two-epoch grace window, so a single rejected epoch
// does not instantly mute the user; a subsequent honest rotation supersedes it.
// The warning is surfaced via a synthetic OnMessage frame (the same UI path as
// device_revoked / device_added — no dedicated channel).
func (c *Client) failClosedEpoch(ek protocol.EpochKey, reason string) {
	c.logger.Warn("room epoch attestation failed — key NOT adopted (fail-closed)",
		"room", ek.Room, "epoch", ek.Epoch, "generator", ek.Generator, "reason", reason)
	if c.cfg.OnMessage != nil {
		raw, _ := json.Marshal(struct {
			Type      string `json:"type"`
			Room      string `json:"room"`
			Generator string `json:"generator"`
			Reason    string `json:"reason"`
		}{"room_attestation_warning", ek.Room, ek.Generator, reason})
		c.cfg.OnMessage("room_attestation_warning", raw)
	}
}
