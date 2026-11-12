//go:build e2e

package e2e

// The size rules end to end: a group at the member cap with real keys, the
// typing size rule at the threshold, an oversized frame refused by the
// server and refused earlier by the client, a rate limiter, and the
// pending-key path.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// generalRoom returns the one room the session is in.
func (s *Session) generalRoom() string {
	rooms := s.C.Rooms()
	if len(rooms) != 1 {
		s.t.Fatalf("%s: rooms = %v, want exactly general", s.User.Name, rooms)
	}
	return rooms[0]
}

// waitEpochAbove polls the real client on the harness ticker until it holds
// an epoch for room above epoch, and returns the epoch it holds.
func (s *Session) waitEpochAbove(room string, epoch int64) int64 {
	t := s.t
	t.Helper()
	deadline := time.Now().Add(defaultWait)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		cur := s.C.CurrentEpoch(room)
		if cur > epoch {
			return cur
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: epoch for %s is %d after %s, want above %d; frames seen: %s\n%s", s.User.Name, room, cur, defaultWait, epoch, s.rec.summary(), s.env.tailLog())
		}
		<-tick.C
	}
}

// roomMessageFrom matches a message frame in room sent by user.
func roomMessageFrom(room, user string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var m protocol.Message
		return decodeOK(raw, &m) && m.Room == room && m.From == user
	}
}

// fillGeneral approves n extra users into general so their profiles reach
// every connected member, and returns them with IDs resolved.
func fillGeneral(env *Env, n int) []*User {
	users := make([]*User, 0, n)
	for i := 0; i < n; i++ {
		users = append(users, env.NewUser(fmt.Sprintf("u%03d", i), false, "general"))
	}
	env.ResolveIDs(users...)
	return users
}

func TestLimits_GroupAtCapSendsFullBody(t *testing.T) {
	if testing.Short() {
		t.Skip("approves 100 users through the ctl")
	}
	env := start(t)
	env.Ctl("add-room", "--name", "general")
	alice := env.NewUser("alice", true, "general")
	bob := env.NewUser("bob", false, "general")
	env.ResolveIDs(alice, bob)
	extras := fillGeneral(env, 98) // alice + bob + 98 = 100, the cap

	a := env.Connect(alice, "a1")
	b := env.Connect(bob, "b1")

	members := []string{bob.ID}
	for _, u := range extras {
		members = append(members, u.ID)
	}
	if err := a.C.CreateGroup(members, "at the cap"); err != nil {
		t.Fatalf("create 100-member group: %v", err)
	}
	created := decode[protocol.GroupCreated](t, a.Wait("group_created"))
	group := created.Group
	if len(created.Members) != 100 {
		t.Fatalf("group has %d members, want 100", len(created.Members))
	}
	b.WaitWhere("group_created", groupIs(group))

	// A body at the client's budget: the frame is well over 16 KB from the
	// hundred real wrapped keys alone, and the server must take it.
	body := strings.Repeat("a", a.C.ComposeBudget())
	if err := a.C.SendGroupMessage(group, body, "", nil); err != nil {
		t.Fatalf("send at the budget in a 100-member group: %v", err)
	}
	gm := decode[protocol.GroupMessage](t, b.WaitWhere("group_message", groupIs(group)))
	keyBytes := 0
	for k, v := range gm.WrappedKeys {
		keyBytes += len(k) + len(v) + 6
	}
	if len(gm.WrappedKeys) != 100 || len(gm.Payload)+keyBytes <= 16*1024 {
		t.Fatalf("frame carried %d keys and %d payload + %d key bytes; this case must exceed the old 16 KB whole-frame rule", len(gm.WrappedKeys), len(gm.Payload), keyBytes)
	}
	b.waitStoredBody(group, gm.ID, body)

	// One more member is refused by the server with too_many_members (the
	// harness drives the client directly, so the TUI's own refusal is not
	// in the path — the server's authority is what is tested).
	extra := env.NewUser("u101", false, "general")
	env.ResolveIDs(extra)
	markA := a.Mark()
	if err := a.C.AddToGroup(group, extra.ID, false); err != nil {
		t.Fatalf("add past the cap (client side): %v", err)
	}
	a.WaitAfter(markA, "error", codeIs("too_many_members"))
}

func TestLimits_TypingSizeRule(t *testing.T) {
	if testing.Short() {
		t.Skip("approves 100 users through the ctl")
	}
	env := start(t)
	env.Ctl("add-room", "--name", "general")
	alice := env.NewUser("alice", true, "general")
	bob := env.NewUser("bob", false, "general")
	env.ResolveIDs(alice, bob)
	fillGeneral(env, 98) // general at exactly the limit

	a := env.Connect(alice, "a1")
	b := env.Connect(bob, "b1")
	general := a.generalRoom()

	typingFromAlice := func(raw json.RawMessage) bool {
		var ty protocol.Typing
		return decodeOK(raw, &ty) && ty.Room == general && ty.User == alice.ID
	}
	markB := b.Mark()
	if !a.C.RoomTypingAllowed(general) {
		t.Fatal("client: typing must be allowed in a room at the limit")
	}
	if err := a.C.SendTyping(general, "", ""); err != nil {
		t.Fatal(err)
	}
	b.WaitAfter(markB, "typing", typingFromAlice)

	// The room has a key before the 101st member exists.
	epochBefore := a.waitEpochAbove(general, 0)

	// The 101st member joins general; both clients learn it from the
	// server's room event (the add-to-room queue drains within seconds).
	extra := env.NewUser("u101", false, "general")
	env.ResolveIDs(extra)
	a.WaitWhere("room_event", roomEventIs(general, "join", extra.ID))
	b.WaitWhere("room_event", roomEventIs(general, "join", extra.ID))

	if a.C.RoomTypingAllowed(general) {
		t.Fatal("client: typing must not be allowed in a room over the limit")
	}
	markB = b.Mark()
	if err := a.C.SendTyping(general, "", ""); err != nil { // the client sends nothing
		t.Fatal(err)
	}
	a.Raw(protocol.Typing{Type: "typing", Room: general}) // the server drops this one
	b.ExpectNone(markB, "typing", typingFromAlice, 500*time.Millisecond)

	// A fresh connection is told so in room_list.
	a2 := env.Connect(alice, "a2")
	list := decode[protocol.RoomList](t, a2.Wait("room_list"))
	for _, r := range list.Rooms {
		if r.ID == general && r.Typing {
			t.Fatalf("room_list says typing=true for a %d-member room", len(r.Members))
		}
	}

	// The join asked a connected member for a rotation, which carries 101
	// wrapped keys: one more than the group cap. Rooms have no member limit,
	// so the server takes it. Both members move to a newer epoch, and the
	// 101st member holds a key and reads what alice sends after the join.
	a.waitEpochAbove(general, epochBefore)
	b.waitEpochAbove(general, epochBefore)
	x := env.Connect(extra, "x1")
	epoch := x.waitEpochAbove(general, 0)
	// Alice sends from her second device, which took the room's key from
	// the server when it connected. Her first device and bob's answered a
	// burst of rotation requests, one per queued join; which key such a
	// device keeps is not what this test asserts.
	a2.waitEpochAbove(general, epoch-1)
	markX := x.Mark()
	const body = "sent after the 101st member joined"
	if err := a2.C.SendRoomMessage(general, body, "", nil); err != nil {
		t.Fatalf("send to the 101-member room: %v", err)
	}
	msg := decode[protocol.Message](t, x.WaitAfter(markX, "message", roomMessageFrom(general, alice.ID)))
	got, err := x.C.DecryptRoomMessage(general, msg.Epoch, msg.Payload)
	if err != nil || got.Body != body {
		t.Fatalf("the 101st member could not read a message sent after the join (epoch %d): body %+v, err %v", msg.Epoch, got, err)
	}
}

func TestLimits_OversizedFrameRefused(t *testing.T) {
	env := start(t)
	env.Ctl("add-room", "--name", "general")
	alice := env.NewUser("alice", true, "general")
	bob := env.NewUser("bob", false, "general")
	env.ResolveIDs(alice, bob)
	a := env.Connect(alice, "a1")
	b := env.Connect(bob, "b1")
	if err := a.C.CreateGroup([]string{bob.ID}, "big"); err != nil {
		t.Fatal(err)
	}
	group := decode[protocol.GroupCreated](t, a.Wait("group_created")).Group
	b.WaitWhere("group_created", groupIs(group))

	// The real client refuses a body over the budget before anything goes
	// on the wire.
	markB := b.Mark()
	var tooLarge *client.MessageTooLargeError
	if err := a.C.SendGroupMessage(group, strings.Repeat("a", a.C.ComposeBudget()+4096), "", nil); !errors.As(err, &tooLarge) {
		t.Fatalf("over-budget send: got %v, want MessageTooLargeError", err)
	}
	b.ExpectNone(markB, "group_message", groupIs(group), 500*time.Millisecond)

	// A raw frame with a 17 KB payload is refused by the server.
	markA := a.Mark()
	a.Raw(map[string]any{
		"type":         "send_group",
		"group":        group,
		"wrapped_keys": map[string]string{alice.ID: strings.Repeat("A", 124), bob.ID: strings.Repeat("B", 124)},
		"payload":      strings.Repeat("a", 17*1024),
		"signature":    "c2ln",
	})
	a.WaitAfter(markA, "error", codeIs("message_too_large"))
	b.ExpectNone(markB, "group_message", groupIs(group), 500*time.Millisecond)
}

func TestLimits_RateLimiterEndToEnd(t *testing.T) {
	env := start(t, withConfig("edits_per_minute = 100", "edits_per_minute = 1"))
	env.Ctl("add-room", "--name", "general")
	alice := env.NewUser("alice", true, "general")
	bob := env.NewUser("bob", false, "general")
	env.ResolveIDs(alice, bob)
	a := env.Connect(alice, "a1")
	b := env.Connect(bob, "b1")
	if err := a.C.CreateGroup([]string{bob.ID}, "limited"); err != nil {
		t.Fatal(err)
	}
	group := decode[protocol.GroupCreated](t, a.Wait("group_created")).Group
	b.WaitWhere("group_created", groupIs(group))
	if err := a.C.SendGroupMessage(group, "v1", "", nil); err != nil {
		t.Fatal(err)
	}
	msgID := decode[protocol.GroupMessage](t, a.WaitWhere("group_message", groupIs(group))).ID
	a.waitStoredBody(group, msgID, "v1")

	// The server's token bucket admits a burst of five on any per-minute
	// limit before the rate applies, so at 1/min the sixth edit is the
	// first one refused.
	for i := 2; i <= 6; i++ {
		markB := b.Mark()
		if err := a.C.EditGroupMessage(msgID, group, fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("edit %d (client side): %v", i, err)
		}
		b.WaitAfter(markB, "group_edited", msgIs(msgID))
		a.waitStoredBody(group, msgID, fmt.Sprintf("v%d", i))
	}

	markA := a.Mark()
	if err := a.C.EditGroupMessage(msgID, group, "v7"); err != nil {
		t.Fatalf("seventh edit (client side): %v", err)
	}
	limited := decode[protocol.Error](t, a.WaitAfter(markA, "error", codeIs("rate_limited")))
	if limited.RetryAfterMs <= 0 {
		t.Fatalf("rate_limited without retry_after_ms: %+v", limited)
	}
	a.waitStoredBody(group, msgID, "v6") // the refused edit changed nothing
}

func TestBootstrap_PendingKeyThenApprove(t *testing.T) {
	env := start(t)
	env.Ctl("add-room", "--name", "general")
	dave := env.NewKey("dave") // never approved

	if err := env.TryConnect(dave, "d1"); err == nil {
		t.Fatal("an unapproved key connected; the server must refuse it and queue the key")
	}
	// Approval without --name: the requested name the client sent as the
	// SSH user is the pending hint.
	env.Ctl("approve", "--key", dave.PubLine, "--rooms", "general")
	env.ResolveIDs(dave)

	d := env.Connect(dave, "d1")
	if d.C.UserID() != dave.ID {
		t.Fatalf("user id after approval = %s, want %s", d.C.UserID(), dave.ID)
	}
	if out := env.Ctl("list-users"); !strings.Contains(out, `display_name="dave"`) {
		t.Fatalf("approved name should be the requested hint dave:\n%s", out)
	}
}
