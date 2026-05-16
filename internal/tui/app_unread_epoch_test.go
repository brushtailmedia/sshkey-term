package tui

// Layer 2a regression tests for the unread pre-join-epoch leak fix.
// See unread-epoch-leak-fix.md.
//
// The live-increment paths in handleServerMessage now gate
// a.sidebar.IncrementUnread on whether the user can actually read
// the message:
//   - room  → a.client.RoomEpochKey(m.Room, m.Epoch) != nil
//   - group → a.client.IsGroupMessageRecipient(m.WrappedKeys)
//   - DM    → unchanged (DMs are bilateral, no pre-join concept)
//
// All tests make the local user the message sender (userID ==
// m.From) so the unrelated notification/decrypt branch is skipped
// — the unread gate itself is sender-independent, so this isolates
// exactly the behavior under test.

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// Room: no epoch key seeded → RoomEpochKey returns nil → the
// pre-join-epoch message must NOT increment the badge.
func TestApp_IncrementUnread_SkipsInaccessibleRoomEpoch(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_unread_room_skip"})
	client.SetUserIDForTesting(c, "alice")
	a := minimalAppForServerMsg(t)
	a.client = c

	raw := mustJSONRaw(t, protocol.Message{
		Type: "message", ID: "m1", From: "alice",
		Room: "room_x", Epoch: 7, TS: 1,
	})
	a.handleServerMessage(ServerMsg{Type: "message", Raw: raw})

	if got := a.sidebar.unread["room_x"]; got != 0 {
		t.Fatalf("inaccessible-epoch room message must not increment unread: unread[room_x] = %d, want 0", got)
	}
}

// Room: epoch key seeded for the message's (room, epoch) → the
// message IS accessible and must increment as before (guards
// against the gate over-filtering the non-bug case).
func TestApp_IncrementUnread_AccessibleRoomEpochStillCounts(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_unread_room_ok"})
	client.SetUserIDForTesting(c, "alice")
	client.SetEpochKeyForTesting(c, "room_x", 7, []byte{1})
	a := minimalAppForServerMsg(t)
	a.client = c

	raw := mustJSONRaw(t, protocol.Message{
		Type: "message", ID: "m1", From: "alice",
		Room: "room_x", Epoch: 7, TS: 1,
	})
	a.handleServerMessage(ServerMsg{Type: "message", Raw: raw})

	if got := a.sidebar.unread["room_x"]; got != 1 {
		t.Fatalf("accessible-epoch room message must increment unread: unread[room_x] = %d, want 1", got)
	}
}

// DM: the DM path is intentionally NOT gated (DMs are bilateral —
// no pre-join history). Confirms the fix didn't accidentally guard
// it with an always-false epoch/wrapped-key check.
func TestApp_IncrementUnread_DMUnchanged(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_unread_dm"})
	client.SetUserIDForTesting(c, "alice")
	a := minimalAppForServerMsg(t)
	a.client = c

	raw := mustJSONRaw(t, protocol.DM{
		Type: "dm", ID: "m1", From: "alice", DM: "dm_x", TS: 1,
	})
	a.handleServerMessage(ServerMsg{Type: "dm", Raw: raw})

	if got := a.sidebar.unread["dm_x"]; got != 1 {
		t.Fatalf("DM path must still increment unread (not gated): unread[dm_x] = %d, want 1", got)
	}
}

// Group: WrappedKeys has no entry for the local user → not a
// designated recipient → must NOT increment. Note this test needs
// no private key / crypto setup at all — proof the gate is a cheap
// presence check, not a decrypt.
func TestApp_IncrementUnread_SkipsGroupMessageWithoutWrappedKey(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_unread_grp_skip"})
	client.SetUserIDForTesting(c, "me")
	a := minimalAppForServerMsg(t)
	a.client = c

	raw := mustJSONRaw(t, protocol.GroupMessage{
		Type: "group_message", ID: "m1", From: "me",
		Group: "group_x", TS: 1,
		WrappedKeys: map[string]string{"someone_else": "wk"},
	})
	a.handleServerMessage(ServerMsg{Type: "group_message", Raw: raw})

	if got := a.sidebar.unread["group_x"]; got != 0 {
		t.Fatalf("group message with no wrapped key for self must not increment unread: unread[group_x] = %d, want 0", got)
	}
}

// Group: WrappedKeys contains the local user's entry → designated
// recipient → must increment. The entry value is intentionally
// junk ("wk"): the gate is presence-only, so a real unwrappable
// key is not needed (a decrypt-gate would have required valid
// X25519 wrapping — extra evidence the cheap check is the better
// testable design).
func TestApp_IncrementUnread_GroupMessageWithWrappedKeyStillCounts(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_unread_grp_ok"})
	client.SetUserIDForTesting(c, "me")
	a := minimalAppForServerMsg(t)
	a.client = c

	raw := mustJSONRaw(t, protocol.GroupMessage{
		Type: "group_message", ID: "m1", From: "me",
		Group: "group_x", TS: 1,
		WrappedKeys: map[string]string{"me": "wk"},
	})
	a.handleServerMessage(ServerMsg{Type: "group_message", Raw: raw})

	if got := a.sidebar.unread["group_x"]; got != 1 {
		t.Fatalf("group message with wrapped key for self must increment unread: unread[group_x] = %d, want 1", got)
	}
}

// Unit test for the primitive itself: presence-only, keyed on the
// authenticated userID; nil / empty / absent all → false.
func TestClient_IsGroupMessageRecipient(t *testing.T) {
	c := client.New(client.Config{DeviceID: "dev_isrecip"})
	client.SetUserIDForTesting(c, "me")

	if !c.IsGroupMessageRecipient(map[string]string{"me": "wk"}) {
		t.Error("present entry for self must be true")
	}
	if !c.IsGroupMessageRecipient(map[string]string{"me": "", "x": "y"}) {
		t.Error("present entry for self (even empty value) must be true")
	}
	if c.IsGroupMessageRecipient(map[string]string{"other": "wk"}) {
		t.Error("entry only for another user must be false")
	}
	if c.IsGroupMessageRecipient(map[string]string{}) {
		t.Error("empty map must be false")
	}
	if c.IsGroupMessageRecipient(nil) {
		t.Error("nil map must be false")
	}
}
