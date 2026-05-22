package tui

// Finding 1 — info panel live membership via SetLiveMemberIDs.
//
// The App bridge (refreshInfoPanelLiveRows) calls SetLiveMemberIDs before the
// panel's Update and View, so an open info panel reflects both live
// display-field changes (presence/status/verified/admin) AND live
// membership-set changes (join/leave/promote/demote) without close/reopen.
// These tests drive SetLiveMemberIDs directly (no event delivery needed).

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// Display freshness: rows are rebuilt from live online/status/profile/verified
// on every call, and a room uses the server-wide profile.Admin flag.
func TestSetLiveMemberIDs_RoomDisplayFreshness(t *testing.T) {
	c := client.New(client.Config{})
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_a", DisplayName: "Alice", Admin: true})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})

	online := map[string]bool{"usr_a": true}
	status := map[string]string{"usr_a": StatusAway}

	i := InfoPanelModel{
		visible: true,
		room:    "rm_a",
		// Stale baked fields prove the refresh overwrites them.
		members: []memberInfo{{User: "usr_a", DisplayName: "stale", Online: false}},
	}
	i.SetLiveMemberIDs(c, online, status)

	if len(i.members) != 1 {
		t.Fatalf("members len = %d, want 1", len(i.members))
	}
	if !i.members[0].Online {
		t.Error("Online should derive true from the online map")
	}
	if i.members[0].Status != StatusAway {
		t.Errorf("Status = %q, want %q", i.members[0].Status, StatusAway)
	}
	if i.members[0].DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want Alice (from Profile)", i.members[0].DisplayName)
	}
	if !i.members[0].Admin {
		t.Error("room member Admin should derive from server-wide profile.Admin")
	}

	// Pushed presence/status change on the same maps — next refresh reflects it.
	online["usr_a"] = false
	status["usr_a"] = StatusBusy
	i.SetLiveMemberIDs(c, online, status)
	if i.members[0].Online {
		t.Error("after presence push, Online should re-derive false without reopen")
	}
	if i.members[0].Status != StatusBusy {
		t.Errorf("after status push, Status = %q, want %q", i.members[0].Status, StatusBusy)
	}
}

// Membership freshness: the member-ID set tracks the live cache (join/leave),
// not the set captured at open time.
func TestSetLiveMemberIDs_MembershipFreshness(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b"})

	i := InfoPanelModel{visible: true, room: "rm_a"}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(i.members) != 2 {
		t.Fatalf("initial: want 2 members, got %d", len(i.members))
	}

	// A member joins while the panel is open.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b", "usr_c"})
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(i.members) != 3 {
		t.Fatalf("after join: want 3 members, got %d", len(i.members))
	}

	// Members leave.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(i.members) != 1 {
		t.Fatalf("after leave: want 1 member, got %d", len(i.members))
	}
}

// Selected-user preservation: the cursor follows the selected USER across a
// re-order/replacement, not the bare index.
func TestSetLiveMemberIDs_PreservesSelectedUser(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b", "usr_c"})

	i := InfoPanelModel{visible: true, room: "rm_a"}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})

	const want = "usr_c"
	for idx, m := range i.members {
		if m.User == want {
			i.cursor = idx
		}
	}

	// Replace the set so indices shift but usr_c remains.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_c", "usr_a"})
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if i.cursor < 0 || i.cursor >= len(i.members) {
		t.Fatalf("cursor out of range: %d (len %d)", i.cursor, len(i.members))
	}
	if i.members[i.cursor].User != want {
		t.Errorf("cursor should follow selected user %q, got %q", want, i.members[i.cursor].User)
	}
}

// Cursor clamp: when the set shrinks below the old cursor index, the cursor is
// re-clamped in range (no out-of-range Enter target / panic).
func TestSetLiveMemberIDs_CursorClampsWhenSetShrinks(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b", "usr_c"})

	i := InfoPanelModel{visible: true, room: "rm_a"}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	i.cursor = len(i.members) - 1 // last row

	// Drop to one member that is NOT the previously selected row.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if i.cursor < 0 || i.cursor >= len(i.members) {
		t.Fatalf("cursor must re-clamp in range, got %d (len %d)", i.cursor, len(i.members))
	}
}

// Group admin source + local gate: per-member admin comes from the group admin
// set (group governance, NOT profile.Admin), and the local user's isGroupAdmin
// gate flips live on self promote/demote.
func TestSetLiveMemberIDs_GroupAdminSourceAndLocalGate(t *testing.T) {
	c := client.New(client.Config{})
	client.SetUserIDForTesting(c, "usr_self")
	// usr_a is NOT a server-wide admin — proves groups don't use profile.Admin.
	client.SetProfileForTesting(c, &protocol.Profile{User: "usr_a", DisplayName: "Alice", Admin: false})
	client.SetGroupMembersForTesting(c, "grp_a", []string{"usr_self", "usr_a"})

	g := InfoPanelModel{visible: true, group: "grp_a", isGroup: true}
	g.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if adminOf(g.members, "usr_a") {
		t.Fatal("usr_a should not be admin before promote")
	}
	if g.isGroupAdmin {
		t.Fatal("local user should not be group admin before promote")
	}

	// Live promote of both usr_a and the local user — panel NOT reopened.
	client.SetGroupAdminForTesting(c, "grp_a", "usr_a")
	client.SetGroupAdminForTesting(c, "grp_a", "usr_self")
	g.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if !adminOf(g.members, "usr_a") {
		t.Error("usr_a Admin should flip true after group promote (group governance)")
	}
	if !g.isGroupAdmin {
		t.Error("local group-admin gate should flip true after self promote")
	}

	// Demote self (admin set now excludes usr_self) — gate flips back.
	client.SetGroupAdminsForTesting(c, "grp_a", []string{"usr_a"})
	g.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if g.isGroupAdmin {
		t.Error("local group-admin gate should flip false after self demote")
	}
}

// DM pair hydration: a DM panel opened before the pair is cached fills in the
// "other" participant once it hydrates, without reopening.
func TestSetLiveMemberIDs_DMPairHydration(t *testing.T) {
	c := client.New(client.Config{})
	client.SetUserIDForTesting(c, "usr_self")

	// Panel opened before dm_list hydration — DMOther returns "".
	i := InfoPanelModel{visible: true, isDM: true, dm: "dm_x"}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(i.members) != 1 || i.members[0].User != "usr_self" {
		t.Fatalf("pre-hydration: want only self, got %+v", i.members)
	}

	// DM pair hydrates.
	client.SetDMForTesting(c, "dm_x", [2]string{"usr_self", "usr_other"})
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(i.members) != 2 {
		t.Fatalf("post-hydration: want 2 members, got %d (%+v)", len(i.members), i.members)
	}
	if !hasUser(i.members, "usr_other") {
		t.Error("the other participant should appear after the DM pair hydrates")
	}
}

// Live read-only transition: a room retired WHILE the panel is open flips
// i.retired and clears the member rows (read-only short-circuit).
func TestSetLiveMemberIDs_LiveReadOnlyTransition(t *testing.T) {
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	if err := st.UpsertRoom("rm_a", "Room A", "", 0); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})

	i := InfoPanelModel{visible: true, room: "rm_a"}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if i.retired {
		t.Fatal("room should be active initially")
	}
	if len(i.members) != 1 {
		t.Fatalf("active room: want 1 member, got %d", len(i.members))
	}

	// Admin retires the room while the panel is open.
	if err := st.MarkRoomRetired("rm_a", "Room A", 1000); err != nil {
		t.Fatalf("retire room: %v", err)
	}
	i.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if !i.retired {
		t.Error("i.retired should flip true after the room is retired")
	}
	if len(i.members) != 0 {
		t.Errorf("retired room: members should be cleared, got %d", len(i.members))
	}
}

// Negatives: a hidden panel, user-profile mode, or a nil client are all no-ops
// (the baked rows are left untouched).
func TestSetLiveMemberIDs_NoOpCases(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})
	baked := []memberInfo{{User: "baked"}}

	hidden := InfoPanelModel{visible: false, room: "rm_a", members: baked}
	hidden.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(hidden.members) != 1 || hidden.members[0].User != "baked" {
		t.Error("hidden panel: SetLiveMemberIDs must be a no-op")
	}

	userMode := InfoPanelModel{visible: true, isUser: true, members: baked}
	userMode.SetLiveMemberIDs(c, map[string]bool{}, map[string]string{})
	if len(userMode.members) != 1 || userMode.members[0].User != "baked" {
		t.Error("user-profile mode: SetLiveMemberIDs must be a no-op")
	}

	nilClient := InfoPanelModel{visible: true, room: "rm_a", members: baked}
	nilClient.SetLiveMemberIDs(nil, map[string]bool{}, map[string]string{})
	if len(nilClient.members) != 1 || nilClient.members[0].User != "baked" {
		t.Error("nil client: SetLiveMemberIDs must be a no-op")
	}
}

func adminOf(members []memberInfo, user string) bool {
	for _, m := range members {
		if m.User == user {
			return m.Admin
		}
	}
	return false
}

func hasUser(members []memberInfo, user string) bool {
	for _, m := range members {
		if m.User == user {
			return true
		}
	}
	return false
}
