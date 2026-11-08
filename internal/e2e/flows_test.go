//go:build e2e

package e2e

// The baseline group lifecycle on today's protocol: create → add → send →
// react → edit → kick → leave → retire, with three real clients on one real
// server. Every step asserts what each side receives, what the local
// stores hold after decryption, and what must not arrive.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestFlows_GroupLifecycle(t *testing.T) {
	env := start(t)
	env.Ctl("add-room", "--name", "general", "--topic", "e2e")
	alice := env.NewUser("alice", true, "general")
	bob := env.NewUser("bob", false, "general")
	carol := env.NewUser("carol", false, "general")
	env.ResolveIDs(alice, bob, carol)

	a := env.Connect(alice, "a1")
	b := env.Connect(bob, "b1")
	c := env.Connect(carol, "c1")
	if a.C.UserID() != alice.ID || b.C.UserID() != bob.ID {
		t.Fatalf("welcome user ids: alice=%s bob=%s, want %s %s", a.C.UserID(), b.C.UserID(), alice.ID, bob.ID)
	}
	// The server advertised its limits and the client validated them.
	l := a.C.Limits()
	if l == nil || l.MaxGroupMembers != 100 || l.MaxBodyBytes != 16384 || l.WrappedKeyEntryBytes != 160 || l.RoomTypingMaxMembers != 100 {
		t.Fatalf("limits = %+v, want the server's defaults", l)
	}

	// 1. create: alice + bob.
	if err := a.C.CreateGroup([]string{bob.ID}, "e2e"); err != nil {
		t.Fatalf("create group: %v", err)
	}
	created := decode[protocol.GroupCreated](t, a.Wait("group_created"))
	group := created.Group
	if group == "" || len(created.Members) != 2 {
		t.Fatalf("group_created = %+v", created)
	}
	b.WaitWhere("group_created", groupIs(group))

	// 2. add carol: bob sees a join event, carol is told she was added.
	markB, markC := b.Mark(), c.Mark()
	if err := a.C.AddToGroup(group, carol.ID, false); err != nil {
		t.Fatalf("add carol: %v", err)
	}
	b.WaitAfter(markB, "group_event", groupEventIs(group, "join", carol.ID))
	c.WaitAfter(markC, "group_added_to", groupIs(group))

	// 3. send: both members receive and decrypt; alice gets her own echo.
	body := "hello from alice " + strings.Repeat("x", 200)
	if err := a.C.SendGroupMessage(group, body, "", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	gm := decode[protocol.GroupMessage](t, b.WaitWhere("group_message", groupIs(group)))
	msgID := gm.ID
	if gm.From != alice.ID {
		t.Fatalf("group_message.from = %s, want alice %s", gm.From, alice.ID)
	}
	c.WaitWhere("group_message", msgIs(msgID))
	a.WaitWhere("group_message", msgIs(msgID))
	b.waitStoredBody(group, msgID, body)
	c.waitStoredBody(group, msgID, body)

	// 4. react: bob reacts, alice and carol see it.
	if err := b.C.SendGroupReaction(group, msgID, "👍"); err != nil {
		t.Fatalf("react: %v", err)
	}
	reactionFromBob := func(raw json.RawMessage) bool {
		var r protocol.Reaction
		return decodeOK(raw, &r) && r.ID == msgID && r.User == bob.ID
	}
	a.WaitWhere("reaction", reactionFromBob)
	c.WaitWhere("reaction", reactionFromBob)

	// 5. edit: alice edits, both stores update.
	newBody := body + " (edited)"
	if err := a.C.EditGroupMessage(msgID, group, newBody); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b.WaitWhere("group_edited", msgIs(msgID))
	c.WaitWhere("group_edited", msgIs(msgID))
	b.waitStoredBody(group, msgID, newBody)
	c.waitStoredBody(group, msgID, newBody)

	// 6. kick carol: she is told, bob sees the leave, and she gets nothing
	// sent afterwards.
	markB, markC = b.Mark(), c.Mark()
	if err := a.C.RemoveFromGroup(group, carol.ID); err != nil {
		t.Fatalf("kick: %v", err)
	}
	left := decode[protocol.GroupLeft](t, c.WaitAfter(markC, "group_left", groupIs(group)))
	if left.Reason != "removed" {
		t.Fatalf("carol's group_left reason = %q, want removed", left.Reason)
	}
	b.WaitAfter(markB, "group_event", groupEventIs(group, "leave", carol.ID))
	markC = c.Mark()
	if err := a.C.SendGroupMessage(group, "after the kick", "", nil); err != nil {
		t.Fatalf("send after kick: %v", err)
	}
	second := decode[protocol.GroupMessage](t, b.WaitWhere("group_message", func(raw json.RawMessage) bool {
		var m protocol.GroupMessage
		return decodeOK(raw, &m) && m.Group == group && m.ID != msgID
	}))
	c.ExpectNone(markC, "group_message", msgIs(second.ID), 500*time.Millisecond)

	// 7. bob leaves (the raw verb, as the term sends it today).
	markA, markB := a.Mark(), b.Mark()
	b.Raw(protocol.LeaveGroup{Type: "leave_group", Group: group})
	b.WaitAfter(markB, "group_left", groupIs(group))
	a.WaitAfter(markA, "group_event", groupEventIs(group, "leave", bob.ID))

	// 8. bob retires: alice is told, and bob's key can no longer connect.
	markA = a.Mark()
	if err := b.C.SendRetireMe("e2e"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	a.WaitAfter(markA, "user_retired", func(raw json.RawMessage) bool {
		var u protocol.UserRetired
		return decodeOK(raw, &u) && u.User == bob.ID
	})
	if err := env.TryConnect(bob, "b2"); err == nil {
		t.Fatal("a retired user's key reconnected; the server must refuse it")
	}
}

// Shutting the server down leaves the data dir reopenable by the ctl.
func TestServer_CleanShutdownLeavesDataDirInspectable(t *testing.T) {
	env := start(t)
	env.Ctl("add-room", "--name", "general")
	alice := env.NewUser("alice", true, "general")
	env.ResolveIDs(alice)
	a := env.Connect(alice, "a1")
	_ = a.C.Close()

	env.StopServer() // asserts a clean exit inside

	// The shutdown ran to completion before the process exited: the
	// lockfile is gone and every database was closed cleanly (SQLite
	// deletes a database's -wal file when its last connection closes
	// cleanly). Checked before any ctl command, which would open and close
	// the stores itself.
	if _, err := os.Stat(filepath.Join(env.dataDir, "sshkey-server.pid")); !os.IsNotExist(err) {
		t.Fatalf("lockfile still present after SIGTERM exit (stat err=%v): the process exited mid-shutdown", err)
	}
	if wals, _ := filepath.Glob(filepath.Join(env.dataDir, "data", "*-wal")); len(wals) != 0 {
		t.Fatalf("write-ahead logs left behind after SIGTERM exit: %v (stores not closed cleanly)", wals)
	}
	if out := env.Ctl("list-users"); !strings.Contains(out, alice.ID) {
		t.Fatalf("list-users after shutdown does not show alice:\n%s", out)
	}
}
