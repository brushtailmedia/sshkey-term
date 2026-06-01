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
// §9 step 6 re-enable (2026-05-20): the group info-panel admin hints
// are role-gated — shown ONLY to group admins (the user). This pair of
// tests pins both halves of that contract: admins see `a/r/p/x`
// hints; non-admins see the generic footer with no admin keys
// advertised.
func TestInfoPanel_GroupFooterAdminHintsShownForAdmin(t *testing.T) {
	i := InfoPanelModel{
		visible:      true,
		group:        "group_1",
		isGroup:      true,
		isGroupAdmin: true, // local user IS a group admin
		members:      []memberInfo{{User: "usr_alice", DisplayName: "Alice"}},
	}
	view := i.View(80)
	for _, expected := range []string{"a=add", "r=remove", "p=promote", "x=demote"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("group admin footer should contain %q, got:\n%s", expected, view)
		}
	}
	// Capital `K` must NOT come back — `r` is the locked letter
	// (group-infopanel-picker-rework.md §1).
	if strings.Contains(view, "K=remove") {
		t.Fatalf("'K=remove' is the OLD locked letter; the new contract is `r=remove`, got:\n%s", view)
	}
}

func TestInfoPanel_GroupFooterAdminHintsHiddenForNonAdmin(t *testing.T) {
	i := InfoPanelModel{
		visible:      true,
		group:        "group_1",
		isGroup:      true,
		isGroupAdmin: false, // non-admin in the same group
		members:      []memberInfo{{User: "usr_alice", DisplayName: "Alice"}},
	}
	view := i.View(80)
	for _, banned := range []string{"a=add", "r=remove", "p=promote", "x=demote"} {
		if strings.Contains(view, banned) {
			t.Fatalf("non-admin group footer must NOT advertise admin hint %q (role-gated), got:\n%s", banned, view)
		}
	}
	if !strings.Contains(view, "Esc=close") {
		t.Fatalf("non-admin group footer should still show the generic footer, got:\n%s", view)
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

func TestInfoPanel_UserProfileVerifyFooterMatchesTrustState(t *testing.T) {
	verified := InfoPanelModel{
		visible:      true,
		isUser:       true,
		userID:       "usr_alice",
		userDisplay:  "Alice",
		userVerified: true,
	}
	view := verified.View(80)
	if !strings.Contains(view, "u=unverify") {
		t.Fatalf("verified user footer should offer unverify, got:\n%s", view)
	}
	if strings.Contains(view, "v=verify") {
		t.Fatalf("verified user footer should not offer verify, got:\n%s", view)
	}

	unverified := InfoPanelModel{
		visible:     true,
		isUser:      true,
		userID:      "usr_bob",
		userDisplay: "Bob",
	}
	view = unverified.View(80)
	if !strings.Contains(view, "v=verify") {
		t.Fatalf("unverified user footer should offer verify, got:\n%s", view)
	}
	if strings.Contains(view, "u=unverify") {
		t.Fatalf("unverified user footer should not offer unverify, got:\n%s", view)
	}
}

func TestInfoPanel_UserProfileVerifyKeysMatchTrustState(t *testing.T) {
	verified := InfoPanelModel{
		visible:      true,
		isUser:       true,
		userID:       "usr_alice",
		userDisplay:  "Alice",
		userVerified: true,
	}

	_, cmd := verified.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if cmd != nil {
		t.Fatal("v on an already-verified profile should no-op")
	}

	_, cmd = verified.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("u on a verified profile should emit unverify action")
	}
	msg, ok := cmd().(MemberActionMsg)
	if !ok || msg.Action != "unverify" || msg.User != "usr_alice" {
		t.Fatalf("u emitted %#v, want MemberActionMsg{unverify, usr_alice}", msg)
	}

	unverified := InfoPanelModel{
		visible:     true,
		isUser:      true,
		userID:      "usr_bob",
		userDisplay: "Bob",
	}
	_, cmd = unverified.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if cmd == nil {
		t.Fatal("v on an unverified profile should emit verify action")
	}
	msg, ok = cmd().(MemberActionMsg)
	if !ok || msg.Action != "verify" || msg.User != "usr_bob" {
		t.Fatalf("v emitted %#v, want MemberActionMsg{verify, usr_bob}", msg)
	}

	_, cmd = unverified.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd != nil {
		t.Fatal("u on an unverified profile should no-op")
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
