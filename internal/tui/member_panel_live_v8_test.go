package tui

// Finding 1 — right-side member panel live membership.
//
// RefreshPreservingSelection rebuilds member rows from the live cache like
// Refresh, but keeps the cursor on the selected USER instead of resetting to
// row 0, so the App bridge can refresh it before every Update/View/mouse-hit
// without the panel jumping. SetRoomMembers (explicit room_members_list
// refresh) now also clamps the cursor.

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// Membership freshness + selected-user preservation: a join/leave/re-order is
// reflected, and the cursor follows the selected user across the change.
func TestMemberPanel_RefreshPreservingSelection_TracksMembershipKeepsUser(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b", "usr_c"})

	var m MemberPanelModel
	m.Refresh("rm_a", "", "", c, map[string]bool{}, map[string]string{})
	if len(m.members) != 3 {
		t.Fatalf("initial: want 3 members, got %d", len(m.members))
	}
	for idx, e := range m.members {
		if e.User == "usr_b" {
			m.cursor = idx
		}
	}

	// usr_a leaves and the set is re-ordered; usr_b remains.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_c", "usr_b"})
	m.RefreshPreservingSelection("rm_a", "", "", c, map[string]bool{}, map[string]string{})
	if len(m.members) != 2 {
		t.Fatalf("after change: want 2 members, got %d", len(m.members))
	}
	if m.SelectedUser() != "usr_b" {
		t.Errorf("cursor should follow selected user usr_b, got %q", m.SelectedUser())
	}
}

// Cursor clamp: when the selected user is removed and the set shrinks, the
// cursor is re-clamped in range (no out-of-range action target).
func TestMemberPanel_RefreshPreservingSelection_ClampsWhenSelectedRemoved(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b", "usr_c"})

	var m MemberPanelModel
	m.Refresh("rm_a", "", "", c, map[string]bool{}, map[string]string{})
	m.cursor = 2 // last row

	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})
	m.RefreshPreservingSelection("rm_a", "", "", c, map[string]bool{}, map[string]string{})
	if m.cursor < 0 || m.cursor >= len(m.members) {
		t.Fatalf("cursor must re-clamp in range, got %d (len %d)", m.cursor, len(m.members))
	}
}

// RefreshPreservingSelection must keep the V8 read-only room semantics
// (notice + readOnly, no rows) and must not hide or blur the panel.
func TestMemberPanel_RefreshPreservingSelection_ReadOnlyRoom(t *testing.T) {
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
	if err := st.MarkRoomRetired("rm_a", "Room A", 1000); err != nil {
		t.Fatalf("retire room: %v", err)
	}

	m := MemberPanelModel{visible: true, focused: true, cursor: 3}
	m.RefreshPreservingSelection("rm_a", "", "", c, map[string]bool{}, map[string]string{})
	if !m.readOnly {
		t.Error("retired room should set readOnly")
	}
	if m.noticeMessage != "room retired" {
		t.Errorf("notice = %q, want \"room retired\"", m.noticeMessage)
	}
	if len(m.members) != 0 {
		t.Errorf("read-only room renders no rows, got %d", len(m.members))
	}
	if !m.visible || !m.focused {
		t.Error("refresh must not hide or blur the panel")
	}
}

// An active room with no cache entry keeps the cache-miss notice (not
// read-only), so RefreshPreservingSelection matches Refresh's signal.
func TestMemberPanel_RefreshPreservingSelection_CacheMissNotice(t *testing.T) {
	c := client.New(client.Config{}) // no store, no cache → active + cache miss
	m := MemberPanelModel{visible: true, focused: true}
	m.RefreshPreservingSelection("rm_active", "", "", c, map[string]bool{}, map[string]string{})
	if m.readOnly {
		t.Error("cache-miss active room is not read-only")
	}
	if m.noticeMessage != "(members unavailable — press r to refresh)" {
		t.Errorf("notice = %q", m.noticeMessage)
	}
}

// SetRoomMembers (explicit room_members_list refresh) must clamp the cursor so
// a shorter response cannot leave it past the new end.
func TestMemberPanel_SetRoomMembers_ClampsCursor(t *testing.T) {
	c := client.New(client.Config{})
	var m MemberPanelModel
	m.SetRoomMembers([]string{"usr_a", "usr_b", "usr_c"}, c, map[string]bool{}, map[string]string{})
	m.cursor = 2

	m.SetRoomMembers([]string{"usr_a"}, c, map[string]bool{}, map[string]string{})
	if m.cursor < 0 || m.cursor >= len(m.members) {
		t.Fatalf("SetRoomMembers must clamp cursor, got %d (len %d)", m.cursor, len(m.members))
	}
}

// App-level persistent path: refreshMemberPanelLiveRowsAndCompletion updates
// both the visible member panel AND the @-completion source (activeMemberEntries
// reads the refreshed panel).
func TestApp_RefreshMemberPanelLiveRowsAndCompletion(t *testing.T) {
	c := client.New(client.Config{})
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a"})

	a := App{
		client:    c,
		messages:  NewMessages(),
		statusBar: NewStatusBar(),
		sidebar:   NewSidebar(),
		input:     NewInput(),
	}
	a.messages.SetContext("rm_a", "", "")
	a.memberPanel.visible = true

	a.refreshMemberPanelLiveRowsAndCompletion()
	if len(a.memberPanel.members) != 1 {
		t.Fatalf("initial: member panel want 1, got %d", len(a.memberPanel.members))
	}
	if len(a.activeMemberEntries()) != 1 {
		t.Fatalf("initial: completion source want 1, got %d", len(a.activeMemberEntries()))
	}

	// A member joins; the persistent refresh must reflect it in both surfaces.
	client.SetRoomMembersForTesting(c, "rm_a", []string{"usr_a", "usr_b"})
	a.refreshMemberPanelLiveRowsAndCompletion()
	if len(a.memberPanel.members) != 2 {
		t.Errorf("after join: member panel want 2, got %d", len(a.memberPanel.members))
	}
	if len(a.activeMemberEntries()) != 2 {
		t.Errorf("after join: @-completion source want 2, got %d", len(a.activeMemberEntries()))
	}
}
