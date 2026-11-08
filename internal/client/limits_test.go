package client

// Size rules read from the server (limits.go): the handshake's validation,
// the room typing gate, the compose budget, and the exact pre-send check on
// the real frame. There are no client-side defaults: a client without a
// handshake has no limits and says so.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// serverDefaults is the block a default-configured server sends.
func serverDefaults() *protocol.ServerLimits {
	return &protocol.ServerLimits{MaxGroupMembers: 100, RoomTypingMaxMembers: 100, MaxBodyBytes: 16384, WrappedKeyEntryBytes: 160, MaxFileIDsPerMessage: 20}
}

// id25 is an ID of production shape: prefix plus 21 characters.
func id25(prefix string, fill byte) string {
	return prefix + strings.Repeat(string(fill), 25-len(prefix))
}

// userIDs returns n distinct user IDs of production shape.
func userIDs(tag byte, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("usr_%c%020d", tag, i)
	}
	return out
}

// limitsClient is a connected client with a signing key, a capturing
// encoder and the given limits (nil = no handshake yet).
func limitsClient(t *testing.T, l *protocol.ServerLimits) (*Client, *bytes.Buffer) {
	t.Helper()
	c := New(Config{DeviceID: id25("dev_", 'd')})
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c.privKey = priv
	c.userID = id25("usr_", 'a')
	var buf bytes.Buffer
	SetEncoderForTesting(c, protocol.NewEncoder(&buf))
	SetLimitsForTesting(c, l)
	return c, &buf
}

// profileWithKey returns a profile carrying a fresh Ed25519 key in the
// SSH authorized-key form the wrap path parses.
func profileWithKey(t *testing.T, userID string) *protocol.Profile {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return &protocol.Profile{User: userID, PubKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))}
}

func seedRoomEpoch(c *Client, room string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentEpoch[room] = 1
	c.epochKeys[room] = map[int64][]byte{1: make([]byte, 32)}
}

func seedDM(t *testing.T, c *Client, dm string) {
	t.Helper()
	bob := profileWithKey(t, id25("usr_", 'b'))
	SetProfileForTesting(c, bob)
	c.mu.Lock()
	c.dms[dm] = [2]string{c.userID, bob.User}
	c.mu.Unlock()
}

// seedGroup gives the client a group of n members (itself included) with
// a profile key for every other member.
func seedGroup(t *testing.T, c *Client, group string, n int) {
	t.Helper()
	members := []string{c.userID}
	for _, uid := range userIDs('g', n-1) {
		SetProfileForTesting(c, profileWithKey(t, uid))
		members = append(members, uid)
	}
	SetGroupMembersForTesting(c, group, members)
}

func TestValidateLimits_RequiresACompleteBlock(t *testing.T) {
	if err := validateLimits(*serverDefaults()); err != nil {
		t.Fatalf("a default server block must validate: %v", err)
	}
	off := *serverDefaults()
	off.RoomTypingMaxMembers = 0
	if err := validateLimits(off); err != nil {
		t.Fatalf("typing 0 (off in rooms) is valid: %v", err)
	}
	cases := map[string]func(l *protocol.ServerLimits){
		"missing block":                func(l *protocol.ServerLimits) { *l = protocol.ServerLimits{} },
		"zero max_group_members":       func(l *protocol.ServerLimits) { l.MaxGroupMembers = 0 },
		"zero max_body_bytes":          func(l *protocol.ServerLimits) { l.MaxBodyBytes = 0 },
		"zero wrapped_key_entry":       func(l *protocol.ServerLimits) { l.WrappedKeyEntryBytes = 0 },
		"zero max_file_ids":            func(l *protocol.ServerLimits) { l.MaxFileIDsPerMessage = 0 },
		"negative room typing limit":   func(l *protocol.ServerLimits) { l.RoomTypingMaxMembers = -1 },
		"negative max_group_members":   func(l *protocol.ServerLimits) { l.MaxGroupMembers = -5 },
		"negative body cap":            func(l *protocol.ServerLimits) { l.MaxBodyBytes = -1 },
		"negative per-entry allowance": func(l *protocol.ServerLimits) { l.WrappedKeyEntryBytes = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			l := *serverDefaults()
			mutate(&l)
			if err := validateLimits(l); err == nil {
				t.Fatalf("%+v must fail validation", l)
			} else if !strings.Contains(err.Error(), "server_hello limits") {
				t.Fatalf("error should name the block, got %q", err.Error())
			}
		})
	}
}

func TestRoomTypingAllowed_Rule(t *testing.T) {
	room := id25("room_", 'r')
	c := New(Config{})
	c.setRoomMembers(room, userIDs('m', 2))
	if c.RoomTypingAllowed(room) {
		t.Fatal("no handshake yet: nothing is allowed")
	}

	SetLimitsForTesting(c, &protocol.ServerLimits{RoomTypingMaxMembers: 100})
	c.setRoomMembers(room, userIDs('m', 100))
	if !c.RoomTypingAllowed(room) {
		t.Fatal("100 members with limit 100: allowed, want true (at the limit)")
	}
	c.setRoomMembers(room, userIDs('m', 101))
	if c.RoomTypingAllowed(room) {
		t.Fatal("101 members with limit 100: allowed, want false")
	}

	SetLimitsForTesting(c, &protocol.ServerLimits{RoomTypingMaxMembers: 0})
	c.setRoomMembers(room, userIDs('m', 1))
	if c.RoomTypingAllowed(room) {
		t.Fatal("limit 0 means typing off in every room")
	}

	// No member list cached: the server's per-room flag is the fallback;
	// without that either, allowed.
	SetLimitsForTesting(c, &protocol.ServerLimits{RoomTypingMaxMembers: 100})
	c.mu.Lock()
	delete(c.roomMemberCache, room)
	c.mu.Unlock()
	if !c.RoomTypingAllowed(room) {
		t.Fatal("no cache, no flag: allowed, want true")
	}
	c.setRoomTypingFlag(room, false)
	if c.RoomTypingAllowed(room) {
		t.Fatal("no cache, flag false: allowed, want false")
	}
	c.setRoomTypingFlag(room, true)
	if !c.RoomTypingAllowed(room) {
		t.Fatal("no cache, flag true: allowed, want true")
	}
}

func TestRoomTypingFlag_FromRoomListAndRoomAddedTo(t *testing.T) {
	c := New(Config{})
	SetLimitsForTesting(c, &protocol.ServerLimits{RoomTypingMaxMembers: 100})
	room := id25("room_", 'r')

	raw, _ := json.Marshal(protocol.RoomAddedTo{Type: "room_added_to", Room: room, Members: userIDs('m', 2), Typing: false, AddedBy: "os:1"})
	c.handleInternal("room_added_to", raw)
	if !c.RoomTypingAllowed(room) {
		t.Fatal("two cached members under the limit must win over a false flag")
	}
	c.mu.Lock()
	delete(c.roomMemberCache, room)
	c.mu.Unlock()
	if c.RoomTypingAllowed(room) {
		t.Fatal("with no member list the room_added_to flag (false) must gate")
	}

	raw, _ = json.Marshal(protocol.RoomList{Type: "room_list", Rooms: []protocol.RoomInfo{{ID: room, Members: userIDs('m', 3), Typing: true}}})
	c.handleInternal("room_list", raw)
	c.mu.Lock()
	delete(c.roomMemberCache, room)
	c.mu.Unlock()
	if !c.RoomTypingAllowed(room) {
		t.Fatal("with no member list the room_list flag (true) must allow")
	}
}

func TestSendTyping_GatesRoomsOnly(t *testing.T) {
	c, buf := limitsClient(t, &protocol.ServerLimits{RoomTypingMaxMembers: 2})
	big, small, group := id25("room_", 'b'), id25("room_", 's'), id25("group_", 'g')
	c.setRoomMembers(big, userIDs('m', 3))
	c.setRoomMembers(small, userIDs('m', 2))

	if err := c.SendTyping(big, "", ""); err != nil || buf.Len() != 0 {
		t.Fatalf("typing in a room over the limit: err=%v written=%d, want silent no-op", err, buf.Len())
	}
	if err := c.SendTyping(small, "", ""); err != nil || buf.Len() == 0 {
		t.Fatalf("typing in a room at the limit: err=%v written=%d, want a frame", err, buf.Len())
	}
	buf.Reset()
	if err := c.SendTyping("", group, ""); err != nil || buf.Len() == 0 {
		t.Fatalf("group typing is never gated: err=%v written=%d", err, buf.Len())
	}
	buf.Reset()
	SetLimitsForTesting(c, nil)
	if err := c.SendTyping(small, "", ""); err != nil || buf.Len() != 0 {
		t.Fatalf("no handshake: room typing must not be sent, err=%v written=%d", err, buf.Len())
	}
}

func TestComposeBudget_OneNumberPerConnection(t *testing.T) {
	c := New(Config{})
	if got := c.ComposeBudget(); got != 0 {
		t.Fatalf("no handshake: budget = %d, want 0 (no limit yet)", got)
	}
	SetLimitsForTesting(c, serverDefaults())
	if got := c.ComposeBudget(); got != composeBudgetFor(16384) || got != 11620 {
		t.Fatalf("budget at the default 16 KB cap = %d, want 11620 (16384 − 512 envelope, × 3/4, − 28 AEAD − 256 wrapper)", got)
	}
	SetLimitsForTesting(c, &protocol.ServerLimits{MaxBodyBytes: 32768})
	if got := c.ComposeBudget(); got != 23908 {
		t.Fatalf("budget for a 32 KB cap = %d, want 23908", got)
	}
	SetLimitsForTesting(c, &protocol.ServerLimits{MaxBodyBytes: 100})
	if got := c.ComposeBudget(); got != 1 {
		t.Fatalf("budget for an absurd cap = %d, want 1 (never 0: the widget reads 0 as unlimited)", got)
	}
}

// The counter's arithmetic is pinned to the real encrypt path: base64 of
// (plaintext + nonce + tag), and a sealed wrapper that fits the allowance.
// Then a body exactly at the budget passes the exact check in a room and
// in a DM, the whole-frame verb with the largest envelope.
func TestComposeBudget_ExpansionMeasured(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 2, 3, 4, 100, 1000, 11620} {
		enc, err := crypto.Encrypt(key, bytes.Repeat([]byte("a"), n))
		if err != nil {
			t.Fatal(err)
		}
		if want := base64.StdEncoding.EncodedLen(n + aeadOverheadBytes); len(enc) != want {
			t.Fatalf("Encrypt(%d bytes) = %d characters, want %d (base64 of n + %d)", n, len(enc), want, aeadOverheadBytes)
		}
	}

	mentions := []string{id25("usr_", 'x'), id25("usr_", 'y'), id25("usr_", 'z')}
	wrapper, _ := json.Marshal(protocol.DecryptedPayload{Seq: 1 << 40, DeviceID: id25("dev_", 'd'), ReplyTo: id25("msg_", 'm'), Mentions: mentions})
	if len(wrapper) > payloadWrapperAllowance {
		t.Fatalf("a realistic sealed wrapper is %d bytes, over the %d allowance", len(wrapper), payloadWrapperAllowance)
	}

	limits := serverDefaults()
	c, buf := limitsClient(t, limits)
	room := id25("room_", 'r')
	seedRoomEpoch(c, room)
	body := strings.Repeat("a", c.ComposeBudget())
	if err := c.SendRoomMessage(room, body, id25("msg_", 'm'), mentions); err != nil {
		t.Fatalf("room send at the budget: %v", err)
	}
	if buf.Len() > limits.MaxBodyBytes {
		t.Fatalf("room frame at the budget is %d bytes, over the server's whole-frame cap %d", buf.Len(), limits.MaxBodyBytes)
	}
	buf.Reset()
	dm := id25("dm_", 'd')
	seedDM(t, c, dm)
	if err := c.SendDMMessage(dm, body, id25("msg_", 'm'), mentions); err != nil {
		t.Fatalf("dm send at the budget: %v", err)
	}
	if buf.Len() > limits.MaxBodyBytes {
		t.Fatalf("dm frame at the budget is %d bytes, over the server's whole-frame cap %d", buf.Len(), limits.MaxBodyBytes)
	}
}

func TestSend_TooLarge_RefusedBeforeQueueOrWire(t *testing.T) {
	// Small caps keep the fixtures small; the rules are the server's.
	small := &protocol.ServerLimits{MaxBodyBytes: 2048, WrappedKeyEntryBytes: 160, MaxGroupMembers: 4, RoomTypingMaxMembers: 100, MaxFileIDsPerMessage: 20}
	var tooLarge *MessageTooLargeError

	t.Run("room", func(t *testing.T) {
		c, buf := limitsClient(t, small)
		room := id25("room_", 'r')
		seedRoomEpoch(c, room)
		err := c.SendRoomMessage(room, strings.Repeat("a", 2000), "", nil)
		if !errors.As(err, &tooLarge) {
			t.Fatalf("got %v, want MessageTooLargeError", err)
		}
		if buf.Len() != 0 || c.sendQueue.Len() != 0 {
			t.Fatalf("a refused send must reach neither the queue (%d) nor the wire (%d bytes)", c.sendQueue.Len(), buf.Len())
		}
		if tooLarge.Verb != "send" || tooLarge.MaxPayload != 2048 || tooLarge.MaxFrame != 2048 || tooLarge.PayloadBytes <= 2048 {
			t.Fatalf("error detail = %+v", *tooLarge)
		}
		if !strings.Contains(err.Error(), "bytes over this server's limit") {
			t.Fatalf("message = %q", err.Error())
		}
		if err := c.SendRoomMessage(room, strings.Repeat("a", 800), "", nil); err != nil {
			t.Fatalf("send within the cap: %v", err)
		}
		if buf.Len() == 0 || c.sendQueue.Len() != 1 {
			t.Fatalf("an accepted send must be queued (%d) and written (%d bytes)", c.sendQueue.Len(), buf.Len())
		}
	})

	t.Run("group at the cap gets the key allowance", func(t *testing.T) {
		c, buf := limitsClient(t, small)
		group := id25("group_", 'g')
		seedGroup(t, c, group, 4)
		if err := c.SendGroupMessage(group, strings.Repeat("a", 1200), "", nil); err != nil {
			t.Fatalf("group send within the budget: %v", err)
		}
		if buf.Len() <= 2048 {
			t.Fatalf("frame is %d bytes; this case must exceed the bare body cap to prove the key allowance", buf.Len())
		}
		buf.Reset()
		err := c.SendGroupMessage(group, strings.Repeat("a", 1500), "", nil)
		if !errors.As(err, &tooLarge) {
			t.Fatalf("got %v, want MessageTooLargeError", err)
		}
		if tooLarge.PayloadBytes <= tooLarge.MaxPayload {
			t.Fatalf("the body rule must be what refuses it: %+v", *tooLarge)
		}
		if tooLarge.MaxFrame != 2048+160*4 {
			t.Fatalf("group frame budget = %d, want 2048 + 160 × 4", tooLarge.MaxFrame)
		}
		if buf.Len() != 0 || c.sendQueue.Len() != 1 {
			t.Fatalf("refused group send touched the queue (%d) or the wire (%d bytes)", c.sendQueue.Len(), buf.Len())
		}
	})

	t.Run("dm frame is checked whole", func(t *testing.T) {
		c, buf := limitsClient(t, small)
		dm := id25("dm_", 'd')
		seedDM(t, c, dm)
		err := c.SendDMMessage(dm, strings.Repeat("a", 1200), "", nil)
		if !errors.As(err, &tooLarge) {
			t.Fatalf("got %v, want MessageTooLargeError", err)
		}
		if tooLarge.PayloadBytes > tooLarge.MaxPayload {
			t.Fatalf("payload %d fits the cap %d; the whole frame is what is over: %+v", tooLarge.PayloadBytes, tooLarge.MaxPayload, *tooLarge)
		}
		if tooLarge.FrameBytes <= tooLarge.MaxFrame || tooLarge.MaxFrame != 2048 {
			t.Fatalf("dm frame rule: %+v", *tooLarge)
		}
		if buf.Len() != 0 || c.sendQueue.Len() != 0 {
			t.Fatal("refused dm send touched the queue or the wire")
		}
		if err := c.SendDMMessage(dm, strings.Repeat("a", 800), "", nil); err != nil {
			t.Fatalf("dm send within the cap: %v", err)
		}
	})

	t.Run("no handshake", func(t *testing.T) {
		// Reachable only by bypassing the connected gate; the error names
		// the real cause instead of inventing a limit.
		c, buf := limitsClient(t, nil)
		room := id25("room_", 'r')
		seedRoomEpoch(c, room)
		if err := c.SendRoomMessage(room, "hi", "", nil); !errors.Is(err, ErrNoLimits) {
			t.Fatalf("got %v, want ErrNoLimits", err)
		}
		if buf.Len() != 0 || c.sendQueue.Len() != 0 {
			t.Fatal("a send without limits must reach neither the queue nor the wire")
		}
	})
}

func TestCheckFrameSize_VerbRules(t *testing.T) {
	c := New(Config{})
	SetLimitsForTesting(c, &protocol.ServerLimits{MaxBodyBytes: 1000, WrappedKeyEntryBytes: 100, MaxGroupMembers: 3})
	payload := strings.Repeat("p", 900)
	env := map[string]string{"payload": payload, "pad": strings.Repeat("x", 200)} // ~1130 bytes
	if err := c.checkFrameSize("edit", payload, env); err == nil {
		t.Error("edit: a frame over the body cap must be refused (checked whole)")
	}
	if err := c.checkFrameSize("send_dm", payload, env); err == nil {
		t.Error("send_dm: a frame over the body cap must be refused (checked whole)")
	}
	if err := c.checkFrameSize("edit_group", payload, env); err != nil {
		t.Errorf("edit_group: a frame within body cap + 100 × 3 must pass: %v", err)
	}
	over := strings.Repeat("p", 1001)
	if err := c.checkFrameSize("send_group", over, map[string]string{"payload": over}); err == nil {
		t.Error("send_group: a payload over the body cap must be refused whatever the frame budget")
	}
	if err := c.checkFrameSize("send", payload, map[string]string{"payload": payload}); err != nil {
		t.Errorf("send: a frame within the cap must pass: %v", err)
	}
	SetLimitsForTesting(c, nil)
	if err := c.checkFrameSize("send", "p", map[string]string{"payload": "p"}); !errors.Is(err, ErrNoLimits) {
		t.Errorf("no limits: got %v, want ErrNoLimits", err)
	}
}

func TestLimits_CopyAndAccessors(t *testing.T) {
	c := New(Config{})
	if c.Limits() != nil || c.GroupMemberCap() != 0 || c.RoomTypingLimit() != 0 {
		t.Fatal("no handshake yet: Limits nil, GroupMemberCap 0, RoomTypingLimit 0")
	}
	SetLimitsForTesting(c, &protocol.ServerLimits{MaxGroupMembers: 7, RoomTypingMaxMembers: 42})
	got := c.Limits()
	got.MaxGroupMembers = 99 // a copy: the client's block is untouched
	if c.GroupMemberCap() != 7 || c.RoomTypingLimit() != 42 {
		t.Fatalf("Limits must return a copy; cap=%d typing=%d", c.GroupMemberCap(), c.RoomTypingLimit())
	}
}
