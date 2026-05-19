package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Admin-action footer hints (a=add/K=remove/p=promote/x=demote) are
// DISABLED 2026-05-19 pending the picker-hand-off rework — the keys were
// mis-wired (K/x froze the app behind the modal info panel; a/p were
// no-ops). The group footer must NOT advertise them; it shows the
// generic footer until the picker is implemented. See missing.md §2a.
// When the picker lands, update this test to assert the re-wired hints.
func TestInfoPanel_GroupFooterAdminHintsDisabled(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		group:   "group_1",
		isGroup: true,
		members: []memberInfo{{User: "usr_alice", DisplayName: "Alice"}},
	}
	view := i.View(80)
	for _, banned := range []string{"a=add", "K=remove", "p=promote", "x=demote"} {
		if strings.Contains(view, banned) {
			t.Fatalf("group info footer must NOT contain disabled admin hint %q (pending picker rework, missing.md §2a), got:\n%s", banned, view)
		}
	}
	if !strings.Contains(view, "Esc=close") {
		t.Fatalf("group info footer should still show the generic footer, got:\n%s", view)
	}
}

func TestInfoPanel_RoomAndDMFootersDoNotShowAdminHints(t *testing.T) {
	room := InfoPanelModel{
		visible: true,
		room:    "room_general",
		members: []memberInfo{{User: "usr_alice", DisplayName: "Alice"}},
	}
	roomView := room.View(80)
	if strings.Contains(roomView, "a=add") || strings.Contains(roomView, "K=remove") {
		t.Fatalf("room info footer should not contain group-admin hints, got:\n%s", roomView)
	}

	dm := InfoPanelModel{
		visible: true,
		dm:      "dm_1",
		isDM:    true,
		members: []memberInfo{{User: "me", DisplayName: "Me"}, {User: "usr_alice", DisplayName: "Alice"}},
	}
	dmView := dm.View(80)
	if strings.Contains(dmView, "a=add") || strings.Contains(dmView, "K=remove") {
		t.Fatalf("dm info footer should not contain group-admin hints, got:\n%s", dmView)
	}
}

func TestInfoPanel_UserProfileScrollKeys(t *testing.T) {
	i := InfoPanelModel{
		visible:         true,
		isUser:          true,
		userID:          "usr_alice",
		userDisplay:     "Alice",
		userFingerprint: "SHA256:test",
		userPubKey:      strings.Repeat("A", 1200),
	}
	i.SetViewport(60, 10)

	var cmd tea.Cmd
	i, cmd = i.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Fatalf("pgdown should not emit cmd in user mode")
	}
	if i.scroll <= 0 {
		t.Fatalf("expected user-profile pgdown to increase scroll, got %d", i.scroll)
	}

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyHome})
	if i.scroll != 0 {
		t.Fatalf("home should reset scroll to 0, got %d", i.scroll)
	}

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if i.scroll <= 0 {
		t.Fatalf("end should move to max scroll (>0), got %d", i.scroll)
	}
}

func TestInfoPanel_ContextSelectionAutoScroll(t *testing.T) {
	members := make([]memberInfo, 0, 40)
	for n := 0; n < 40; n++ {
		members = append(members, memberInfo{User: fmt.Sprintf("usr_%02d", n), DisplayName: fmt.Sprintf("user%02d", n)})
	}
	i := InfoPanelModel{
		visible: true,
		group:   "group_1",
		isGroup: true,
		members: members,
	}
	i.SetViewport(80, 12)

	for n := 0; n < 20; n++ {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if i.cursor != 20 {
		t.Fatalf("cursor = %d, want 20", i.cursor)
	}
	if i.scroll <= 0 {
		t.Fatalf("expected auto-scroll to move when cursor moves down, got scroll=%d", i.scroll)
	}
	view := i.ViewWithHeight(80, 12)
	if !strings.Contains(view, "user20") {
		t.Fatalf("expected selected member to remain visible after scrolling, got:\n%s", view)
	}
}

func TestInfoPanel_ContextPageKeysMoveCursor(t *testing.T) {
	members := make([]memberInfo, 0, 30)
	for n := 0; n < 30; n++ {
		members = append(members, memberInfo{User: fmt.Sprintf("usr_%02d", n), DisplayName: fmt.Sprintf("user%02d", n)})
	}
	i := InfoPanelModel{visible: true, group: "group_1", isGroup: true, members: members}
	i.SetViewport(80, 12)

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if i.cursor == 0 {
		t.Fatalf("pgdown should move cursor forward")
	}

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if i.cursor != len(members)-1 {
		t.Fatalf("end should move cursor to last member, got %d", i.cursor)
	}

	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyHome})
	if i.cursor != 0 {
		t.Fatalf("home should move cursor to first member, got %d", i.cursor)
	}
}
