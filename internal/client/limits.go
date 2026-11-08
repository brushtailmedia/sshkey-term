package client

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Size rules come from the server. Every server_hello carries a complete
// limits block (protocol.ServerLimits), validated at handshake and kept
// per connection. There is no client-side default for any of them: a
// hello without the block, or with a zero where a cap belongs, fails the
// handshake. Until a handshake has completed the client has no limits, and
// the accessors below say so (no typing, no budget, no cap) — nothing a
// disconnected client can act on anyway.

// Compose-budget arithmetic for the live counter: a heuristic for "how
// much can I type", conservative by construction. The exact answer is
// given by checkFrameSize, which measures the real frame at send time.
const (
	// aeadOverheadBytes is what crypto.Encrypt adds before base64: a 12-byte
	// nonce and a 16-byte GCM tag. Pinned by the expansion test.
	aeadOverheadBytes = 12 + 16
	// payloadWrapperAllowance covers the sealed JSON around the body
	// (protocol.DecryptedPayload: seq, device_id, mentions, reply_to).
	payloadWrapperAllowance = 256
	// envelopeAllowance covers the largest frame envelope the server
	// checks whole — a DM send: two wrapped keys, signature, corr_id, ids.
	envelopeAllowance = 512
)

// ErrNoLimits is returned by a send that runs before the handshake has
// delivered the server's limits. Every send is gated on IsConnected first,
// so reaching it is a programming error, never a user-visible state.
var ErrNoLimits = errors.New("no server limits: handshake incomplete")

// validateLimits is the handshake's check on the block: every cap present
// and positive. The room typing limit alone may be zero (typing off in
// rooms); negative is never valid.
func validateLimits(l protocol.ServerLimits) error {
	switch {
	case l.MaxGroupMembers <= 0:
		return fmt.Errorf("server_hello limits: max_group_members = %d, want > 0", l.MaxGroupMembers)
	case l.MaxBodyBytes <= 0:
		return fmt.Errorf("server_hello limits: max_body_bytes = %d, want > 0", l.MaxBodyBytes)
	case l.WrappedKeyEntryBytes <= 0:
		return fmt.Errorf("server_hello limits: wrapped_key_entry_bytes = %d, want > 0", l.WrappedKeyEntryBytes)
	case l.MaxFileIDsPerMessage <= 0:
		return fmt.Errorf("server_hello limits: max_file_ids_per_message = %d, want > 0", l.MaxFileIDsPerMessage)
	case l.RoomTypingMaxMembers < 0:
		return fmt.Errorf("server_hello limits: room_typing_max_members = %d, want >= 0", l.RoomTypingMaxMembers)
	}
	return nil
}

// Limits returns a copy of the size rules the server sent in this
// connection's hello, or nil before the handshake has delivered them.
func (c *Client) Limits() *protocol.ServerLimits {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.limits == nil {
		return nil
	}
	l := *c.limits
	return &l
}

// ComposeBudget is the plaintext byte budget for one message on this
// server: one number per connection, the same in every room, group and
// DM, because the server holds the sealed payload to its body cap and
// gives group keys their own allowance. About 11,600 bytes at a 16 KB cap.
// The input widget uses it as its limit and the status bar counts against
// it; the exact decision is made at send time on the real frame. Zero
// before the handshake, which the widget reads as "no limit yet".
func (c *Client) ComposeBudget() int {
	l := c.Limits()
	if l == nil {
		return 0
	}
	return composeBudgetFor(l.MaxBodyBytes)
}

// composeBudgetFor derives the plaintext budget from a body cap: strip the
// largest whole-frame envelope, undo the base64 expansion (3 bytes per 4
// characters), then the AEAD overhead and the sealed wrapper.
func composeBudgetFor(maxBodyBytes int) int {
	sealed := (maxBodyBytes - envelopeAllowance) / 4 * 3
	b := sealed - aeadOverheadBytes - payloadWrapperAllowance
	if b < 1 {
		b = 1 // the widget treats 0 as "no limit"
	}
	return b
}

// GroupMemberCap is the server's group membership cap, or 0 before the
// handshake (no group can be created then either).
func (c *Client) GroupMemberCap() int {
	if l := c.Limits(); l != nil {
		return l.MaxGroupMembers
	}
	return 0
}

// RoomTypingLimit is the room size above which the server drops typing
// frames: zero means off in every room, and also what a client without a
// handshake reports.
func (c *Client) RoomTypingLimit() int {
	if l := c.Limits(); l != nil {
		return l.RoomTypingMaxMembers
	}
	return 0
}

// RoomTypingAllowed reports whether the server relays typing in roomID:
// true while the room has RoomTypingMaxMembers members or fewer by the
// live member cache, falling back to the server's per-room flag from
// room_list / room_added_to when the cache has no entry. A limit of zero
// means typing is off in every room, and nothing is allowed before the
// handshake. Groups and DMs are never gated.
func (c *Client) RoomTypingAllowed(roomID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.limits == nil {
		return false
	}
	limit := c.limits.RoomTypingMaxMembers
	if limit <= 0 {
		return false
	}
	if members, ok := c.roomMemberCache[roomID]; ok {
		return len(members) <= limit
	}
	if flag, ok := c.roomTypingFlag[roomID]; ok {
		return flag
	}
	return true
}

// setRoomTypingFlag records the server's per-room typing answer carried by
// room_list and room_added_to.
func (c *Client) setRoomTypingFlag(roomID string, allowed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roomTypingFlag == nil {
		c.roomTypingFlag = make(map[string]bool)
	}
	c.roomTypingFlag[roomID] = allowed
}

// MessageTooLargeError is returned by the send and edit methods when the
// frame they built would be refused by the server as message_too_large:
// the sealed payload over MaxPayload, or the whole frame over MaxFrame.
// Nothing was queued or written, so the caller keeps the draft and tells
// the user.
type MessageTooLargeError struct {
	Verb         string
	PayloadBytes int
	MaxPayload   int
	FrameBytes   int
	MaxFrame     int
}

func (e *MessageTooLargeError) Error() string {
	over := e.PayloadBytes - e.MaxPayload
	if frameOver := e.FrameBytes - e.MaxFrame; frameOver > over {
		over = frameOver
	}
	return fmt.Sprintf("Message is %d bytes over this server's limit, shorten it and send again", over)
}

// checkFrameSize measures the frame exactly as it will go on the wire and
// compares it with the server's rules: the payload against the body cap,
// and the whole frame against the body cap plus the wrapped-key allowance
// for group verbs or against the bare body cap for the verbs the server
// checks whole (rooms and 1:1 DMs). Exact by construction — the bytes are
// the encoder's — so the counter's arithmetic can never drift from it.
func (c *Client) checkFrameSize(verb, payload string, envelope any) error {
	l := c.Limits()
	if l == nil {
		return ErrNoLimits
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", verb, err)
	}
	maxFrame := l.MaxBodyBytes
	switch verb {
	case "send_group", "edit_group":
		maxFrame = l.MaxBodyBytes + l.WrappedKeyEntryBytes*l.MaxGroupMembers
	}
	if len(payload) > l.MaxBodyBytes || len(raw) > maxFrame {
		return &MessageTooLargeError{
			Verb:         verb,
			PayloadBytes: len(payload),
			MaxPayload:   l.MaxBodyBytes,
			FrameBytes:   len(raw),
			MaxFrame:     maxFrame,
		}
	}
	return nil
}

// SetLimitsForTesting installs a server limits block as a validated
// handshake would (nil = no handshake yet). Do not call from production
// code.
func SetLimitsForTesting(c *Client, l *protocol.ServerLimits) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits = l
}
