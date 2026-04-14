package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Phase 14 Chunk 5 tests for the five new in-group admin verb
// confirmation dialogs: add, kick, promote, demote, transfer.
//
// Each dialog follows the same state-machine shape as
// DeleteGroupConfirmModel (initial-hidden / Show / Hide / y-enter-emit /
// n-esc-cancel / View-when-hidden), so these tests mirror that file's
// structure. Shared assertions about the y-then-msg-emit pattern.

// --- AddConfirmModel ---

func TestAddConfirm_InitialHidden(t *testing.T) {
	m := AddConfirmModel{}
	if m.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestAddConfirm_ShowPopulatesFields(t *testing.T) {
	m := AddConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	if !m.IsVisible() {
		t.Error("after Show should be visible")
	}
	if m.group != "group_x" || m.groupName != "Project X" {
		t.Errorf("group/name fields wrong: %+v", m)
	}
	if m.targetID != "usr_bob" || m.targetName != "Bob" {
		t.Errorf("target fields wrong: %+v", m)
	}
}

func TestAddConfirm_HideClearsState(t *testing.T) {
	m := AddConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	m.Hide()
	if m.IsVisible() || m.group != "" || m.targetID != "" {
		t.Errorf("Hide should clear state, got %+v", m)
	}
}

func TestAddConfirm_YEmitsMsgWithFields(t *testing.T) {
	m := AddConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.IsVisible() {
		t.Error("after y, dialog should hide")
	}
	if cmd == nil {
		t.Fatal("y should emit AddConfirmMsg")
	}
	msg, ok := cmd().(AddConfirmMsg)
	if !ok {
		t.Fatalf("expected AddConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_x" || msg.TargetID != "usr_bob" {
		t.Errorf("emitted msg wrong: %+v", msg)
	}
}

func TestAddConfirm_NCancels(t *testing.T) {
	m := AddConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.IsVisible() {
		t.Error("after n, dialog should hide")
	}
	if cmd != nil {
		t.Error("n should NOT emit a confirm message")
	}
}

func TestAddConfirm_ViewContainsTargetAndGroup(t *testing.T) {
	m := AddConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	view := m.View(80)
	for _, frag := range []string{"Add member", "Bob", "Project X", "[y] Add", "[n] Cancel"} {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing fragment %q", frag)
		}
	}
}

// --- KickConfirmModel ---

func TestKickConfirm_YEmitsMsgWithFields(t *testing.T) {
	m := KickConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should emit KickConfirmMsg")
	}
	msg, ok := cmd().(KickConfirmMsg)
	if !ok {
		t.Fatalf("expected KickConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_x" || msg.TargetID != "usr_bob" {
		t.Errorf("emitted msg wrong: %+v", msg)
	}
}

func TestKickConfirm_EscCancelsNoEmit(t *testing.T) {
	m := KickConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsVisible() || cmd != nil {
		t.Errorf("Esc should hide and not emit, got visible=%v cmd=%v", m.IsVisible(), cmd)
	}
}

func TestKickConfirm_ViewContainsRemoveLanguage(t *testing.T) {
	m := KickConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	view := m.View(80)
	for _, frag := range []string{"Remove member", "Bob", "[y] Remove", "[n] Cancel"} {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing fragment %q", frag)
		}
	}
}

// --- PromoteConfirmModel ---

func TestPromoteConfirm_YEmitsMsgWithFields(t *testing.T) {
	m := PromoteConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should emit PromoteConfirmMsg")
	}
	msg, ok := cmd().(PromoteConfirmMsg)
	if !ok {
		t.Fatalf("expected PromoteConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_x" || msg.TargetID != "usr_bob" {
		t.Errorf("emitted msg wrong: %+v", msg)
	}
}

func TestPromoteConfirm_ViewContainsImplicationText(t *testing.T) {
	m := PromoteConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	view := m.View(80)
	for _, frag := range []string{"Promote to admin", "Bob", "remove", "demote", "[y] Promote", "[n] Cancel"} {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing fragment %q", frag)
		}
	}
}

// --- DemoteConfirmModel ---

func TestDemoteConfirm_YEmitsMsgWithFields(t *testing.T) {
	m := DemoteConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should emit DemoteConfirmMsg")
	}
	msg, ok := cmd().(DemoteConfirmMsg)
	if !ok {
		t.Fatalf("expected DemoteConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_x" || msg.TargetID != "usr_bob" {
		t.Errorf("emitted msg wrong: %+v", msg)
	}
}

// --- TransferConfirmModel ---

func TestTransferConfirm_YEmitsMsgWithFields(t *testing.T) {
	m := TransferConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob", false)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should emit TransferConfirmMsg")
	}
	msg, ok := cmd().(TransferConfirmMsg)
	if !ok {
		t.Fatalf("expected TransferConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_x" || msg.TargetID != "usr_bob" || msg.TargetAlreadyAdmin {
		t.Errorf("emitted msg wrong: %+v", msg)
	}
}

func TestTransferConfirm_AlreadyAdminFlagSurfacesOnEmit(t *testing.T) {
	m := TransferConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob", true)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should emit TransferConfirmMsg")
	}
	msg := cmd().(TransferConfirmMsg)
	if !msg.TargetAlreadyAdmin {
		t.Error("TargetAlreadyAdmin flag lost on emit")
	}
}

func TestTransferConfirm_ViewWhenAlreadyAdminSkipsPromoteWording(t *testing.T) {
	m := TransferConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob", true)
	view := m.View(80)
	if !strings.Contains(view, "already an admin") {
		t.Error("already-admin view should say 'already an admin'")
	}
	if strings.Contains(view, "Promote Bob to admin and leave") {
		t.Error("already-admin view should NOT promise to promote")
	}
}

func TestTransferConfirm_ViewWhenNotAdminShowsPromoteAndLeave(t *testing.T) {
	m := TransferConfirmModel{}
	m.Show("group_x", "Project X", "usr_bob", "Bob", false)
	view := m.View(80)
	if !strings.Contains(view, "Promote") {
		t.Error("not-yet-admin view should include Promote wording")
	}
}

// --- Input parser coverage for the new commands ---

func TestInputParser_NewAdminVerbsRouteToApp(t *testing.T) {
	// handleCommand is a method on *InputModel. It sets i.pendingCmd
	// when a command needs app-level handling. We verify that each
	// new Phase 14 admin verb routes to the app (doesn't silently
	// drop) and carries the group + arg through.
	cases := []struct {
		name string
		cmd  string
		arg  string
	}{
		{"add", "/add", "@bob"},
		{"kick", "/kick", "@bob"},
		{"promote", "/promote", "@bob"},
		{"demote", "/demote", "@bob"},
		{"transfer", "/transfer", "@bob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &InputModel{}
			i.handleCommand(tc.cmd+" "+tc.arg, nil, "", "group_x", "")
			sc := i.PendingCommand()
			if sc == nil {
				t.Fatalf("%s should route to app via pendingCmd", tc.cmd)
			}
			if sc.Command != tc.cmd {
				t.Errorf("Command = %q, want %q", sc.Command, tc.cmd)
			}
			if sc.Group != "group_x" {
				t.Errorf("Group = %q, want group_x", sc.Group)
			}
			if sc.Arg != tc.arg {
				t.Errorf("Arg = %q, want %q", sc.Arg, tc.arg)
			}
		})
	}
}

func TestInputParser_AdminVerbsWithoutGroupAreDropped(t *testing.T) {
	// /add /kick /promote /demote /transfer are group-context only.
	// Typed outside a group they silently drop (no pendingCmd set).
	// The app layer's handleGroupAdminCommand would also reject, but
	// this verifies the parser never emits a cmd without a group.
	for _, cmd := range []string{"/add", "/kick", "/promote", "/demote", "/transfer"} {
		i := &InputModel{}
		i.handleCommand(cmd+" @bob", nil, "room_x", "", "")
		if sc := i.PendingCommand(); sc != nil {
			t.Errorf("%s in room context should not pendingCmd, got %+v", cmd, sc)
		}
	}
}

func TestInputParser_WhoamiRoutesToAppInAnyContext(t *testing.T) {
	// /whoami works in room, group, and DM contexts. Verify it
	// routes to the app with the context preserved.
	contexts := []struct {
		name       string
		room       string
		group      string
		dm         string
	}{
		{"room", "room_x", "", ""},
		{"group", "", "group_x", ""},
		{"dm", "", "", "dm_x"},
	}
	for _, tc := range contexts {
		t.Run(tc.name, func(t *testing.T) {
			i := &InputModel{}
			i.handleCommand("/whoami", nil, tc.room, tc.group, tc.dm)
			sc := i.PendingCommand()
			if sc == nil {
				t.Fatal("/whoami should route to app")
			}
			if sc.Command != "/whoami" {
				t.Errorf("Command = %q", sc.Command)
			}
		})
	}
}

func TestInputParser_GroupinfoRequiresGroupContext(t *testing.T) {
	i := &InputModel{}
	// Group context — should route
	i.handleCommand("/groupinfo", nil, "", "group_x", "")
	if sc := i.PendingCommand(); sc == nil || sc.Command != "/groupinfo" {
		t.Errorf("groupinfo in group context should route, got %+v", sc)
	}
	// Room context — should drop
	i.handleCommand("/groupinfo", nil, "room_x", "", "")
	if sc := i.PendingCommand(); sc != nil {
		t.Error("groupinfo in room context should drop")
	}
}

func TestInputParser_RenameRoutesToAppForPreCheck(t *testing.T) {
	// Phase 14 changed /rename from direct-encode to app-routed so
	// the admin pre-check can run client-side. Verify routing.
	i := &InputModel{}
	i.handleCommand("/rename NewName", nil, "", "group_x", "")
	sc := i.PendingCommand()
	if sc == nil {
		t.Fatal("/rename should route to app (not direct-encode)")
	}
	if sc.Command != "/rename" || sc.Group != "group_x" || sc.Arg != "NewName" {
		t.Errorf("unexpected pendingCmd: %+v", sc)
	}
}
