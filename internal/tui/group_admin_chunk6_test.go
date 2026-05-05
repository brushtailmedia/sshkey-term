package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// Phase 14 Chunk 6 tests for the TUI rendering additions:
//
//   - /audit overlay
//   - /members and /admins overlays
//   - Event coalescing in MessagesModel
//   - /undo 30s window state
//   - Info panel A/K/P/X keybindings
//   - /groupcreate argument parser
//
// Uses minimalAppForServerMsg from groupleave_admin_test.go as the
// shared fixture.

// --- Event coalescing ---

func TestCoalesceSystemMessage_TwoSameAdminJoinsCollapseToOne(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_x", "")

	// First join: "alice added bob to the group"
	m.AddCoalescingSystemMessage(
		"join", "usr_alice", "alice", "bob", "group_x",
		"alice added bob to the group",
		func(joined string) string { return "alice added " + joined + " to the group" },
	)
	if len(m.messages) != 1 {
		t.Fatalf("after first event want 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].SystemText, "bob") {
		t.Errorf("first system text missing bob: %q", m.messages[0].SystemText)
	}

	// Second join by same admin: should collapse
	m.AddCoalescingSystemMessage(
		"join", "usr_alice", "alice", "carol", "group_x",
		"alice added carol to the group",
		func(joined string) string { return "alice added " + joined + " to the group" },
	)
	if len(m.messages) != 1 {
		t.Fatalf("after coalesce want 1 message, got %d", len(m.messages))
	}
	got := m.messages[0].SystemText
	if !strings.Contains(got, "bob and carol") {
		t.Errorf("coalesced text should say 'bob and carol', got %q", got)
	}
}

func TestCoalesceSystemMessage_ThreeTargetsUseOxfordComma(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_x", "")
	for _, target := range []string{"bob", "carol", "dave"} {
		m.AddCoalescingSystemMessage(
			"join", "usr_alice", "alice", target, "group_x",
			"alice added "+target+" to the group",
			func(joined string) string { return "alice added " + joined + " to the group" },
		)
	}
	if len(m.messages) != 1 {
		t.Fatalf("want 1 message after 3 coalesced, got %d", len(m.messages))
	}
	got := m.messages[0].SystemText
	if !strings.Contains(got, "bob, carol, and dave") {
		t.Errorf("3-target form wrong: %q", got)
	}
}

func TestCoalesceSystemMessage_FourPlusTargetsOverflowToAndNMore(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_x", "")
	for _, target := range []string{"a", "b", "c", "d", "e"} {
		m.AddCoalescingSystemMessage(
			"join", "usr_alice", "alice", target, "group_x",
			"alice added "+target+" to the group",
			func(joined string) string { return "alice added " + joined + " to the group" },
		)
	}
	if len(m.messages) != 1 {
		t.Fatalf("want 1 message after 5 coalesced, got %d", len(m.messages))
	}
	got := m.messages[0].SystemText
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("overflow should say 'and 2 more', got %q", got)
	}
}

func TestCoalesceSystemMessage_DifferentVerbsDoNotCoalesce(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_x", "")
	m.AddCoalescingSystemMessage(
		"join", "usr_alice", "alice", "bob", "group_x",
		"alice added bob to the group",
		func(s string) string { return "alice added " + s + " to the group" },
	)
	m.AddCoalescingSystemMessage(
		"promote", "usr_alice", "alice", "bob", "group_x",
		"alice promoted bob to admin",
		func(s string) string { return "alice promoted " + s + " to admin" },
	)
	if len(m.messages) != 2 {
		t.Fatalf("different verbs should stay separate, got %d", len(m.messages))
	}
}

func TestCoalesceSystemMessage_DifferentAdminsDoNotCoalesce(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_x", "")
	m.AddCoalescingSystemMessage(
		"join", "usr_alice", "alice", "dave", "group_x",
		"alice added dave to the group",
		func(s string) string { return "alice added " + s + " to the group" },
	)
	m.AddCoalescingSystemMessage(
		"join", "usr_bob", "bob", "eve", "group_x",
		"bob added eve to the group",
		func(s string) string { return "bob added " + s + " to the group" },
	)
	if len(m.messages) != 2 {
		t.Fatalf("different admins should stay separate, got %d", len(m.messages))
	}
}

func TestCoalesceSystemMessage_EmptyByIDDoesNotCoalesce(t *testing.T) {
	// Self-leave and retirement events have empty By and must never
	// coalesce with subsequent events — they represent distinct
	// user/system-initiated actions.
	m := NewMessages()
	m.SetContext("", "group_x", "")
	m.AddCoalescingSystemMessage(
		"leave", "", "", "bob", "group_x",
		"bob left the group",
		func(s string) string { return "somebody left" },
	)
	m.AddCoalescingSystemMessage(
		"leave", "", "", "carol", "group_x",
		"carol left the group",
		func(s string) string { return "somebody left" },
	)
	if len(m.messages) != 2 {
		t.Fatalf("empty-by events should stay separate, got %d", len(m.messages))
	}
}

// --- AuditOverlayModel ---

func TestAuditOverlay_InitialHidden(t *testing.T) {
	o := AuditOverlayModel{}
	if o.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestAuditOverlay_ShowPopulatesFields(t *testing.T) {
	o := AuditOverlayModel{}
	events := []store.GroupEventRow{
		{Event: "join", User: "usr_bob", By: "usr_alice", TS: 100},
	}
	o.Show("group_x", "Project X", events, nil)
	if !o.IsVisible() {
		t.Error("after Show should be visible")
	}
	if o.groupID != "group_x" || len(o.events) != 1 {
		t.Errorf("fields wrong: %+v", o)
	}
}

func TestAuditOverlay_EscHides(t *testing.T) {
	o := AuditOverlayModel{}
	o.Show("group_x", "Project X", nil, nil)
	o, _ = o.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if o.IsVisible() {
		t.Error("Esc should hide")
	}
}

func TestAuditOverlay_RenderIncludesEventsAndHeader(t *testing.T) {
	o := AuditOverlayModel{}
	events := []store.GroupEventRow{
		{Event: "join", User: "usr_bob", By: "usr_alice", TS: 1000},
		{Event: "promote", User: "usr_bob", By: "usr_alice", TS: 2000},
		{Event: "leave", User: "usr_carol", Reason: "removed", By: "usr_alice", TS: 3000},
	}
	o.Show("group_x", "Project X", events, func(u string) string {
		return strings.TrimPrefix(u, "usr_")
	})
	view := o.View(80)
	// Header fragments
	if !strings.Contains(view, "Audit — Project X") {
		t.Errorf("view missing header: %q", view)
	}
	if !strings.Contains(view, "(3)") {
		t.Errorf("view missing count: %q", view)
	}
	// Event fragments
	for _, frag := range []string{"alice added bob", "alice promoted bob", "alice removed carol"} {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing event text %q", frag)
		}
	}
}

// --- MembersOverlayModel ---

func TestMembersOverlay_ShowPopulatesAllMembers(t *testing.T) {
	m := MembersOverlayModel{}
	m.Show(
		"group_x", "Project X",
		[]string{"usr_alice", "usr_bob", "usr_carol"},
		map[string]bool{"usr_alice": true},
		false,
		func(u string) string { return strings.TrimPrefix(u, "usr_") },
	)
	if len(m.rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(m.rows))
	}
	// Admin (alice) should be sorted first
	if m.rows[0].DisplayName != "alice" || !m.rows[0].IsAdmin {
		t.Errorf("admin should sort first, got %+v", m.rows[0])
	}
}

func TestMembersOverlay_AdminsOnlyFiltersToAdmins(t *testing.T) {
	m := MembersOverlayModel{}
	m.Show(
		"group_x", "Project X",
		[]string{"usr_alice", "usr_bob", "usr_carol"},
		map[string]bool{"usr_alice": true},
		true,
		func(u string) string { return strings.TrimPrefix(u, "usr_") },
	)
	if len(m.rows) != 1 {
		t.Fatalf("admins-only should return 1 row, got %d", len(m.rows))
	}
	if m.rows[0].DisplayName != "alice" {
		t.Errorf("expected alice, got %+v", m.rows[0])
	}
}

func TestMembersOverlay_ViewShowsAdminMarker(t *testing.T) {
	m := MembersOverlayModel{}
	m.Show(
		"group_x", "Project X",
		[]string{"usr_alice", "usr_bob"},
		map[string]bool{"usr_alice": true},
		false,
		func(u string) string { return strings.TrimPrefix(u, "usr_") },
	)
	view := m.View(80)
	if !strings.Contains(view, "Members — Project X") {
		t.Errorf("view missing header: %q", view)
	}
	if !strings.Contains(view, "★") {
		t.Errorf("view missing admin marker ★: %q", view)
	}
}

func TestMembersOverlay_AdminsOnlyHasAdminsHeader(t *testing.T) {
	m := MembersOverlayModel{}
	m.Show(
		"group_x", "Project X",
		[]string{"usr_alice"},
		map[string]bool{"usr_alice": true},
		true, nil,
	)
	view := m.View(80)
	if !strings.Contains(view, "Admins — Project X") {
		t.Errorf("admins-only view missing admin header: %q", view)
	}
}

// --- /undo state tracking via App.handleUndoCommand ---

func TestUndoCommand_NothingToUndoWhenStateEmpty(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.handleUndoCommand(&SlashCommandMsg{Command: "/undo", Group: "group_x"})
	if !strings.Contains(a.statusBar.errorMsg, "Nothing to undo") {
		t.Errorf("expected 'Nothing to undo', got %q", a.statusBar.errorMsg)
	}
}

func TestUndoCommand_DifferentGroupRejected(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.lastKickGroup = "group_a"
	a.lastKickUserID = "usr_bob"
	a.lastKickTS = time.Now().Unix() // just now — within window
	a.handleUndoCommand(&SlashCommandMsg{Command: "/undo", Group: "group_b"})
	if !strings.Contains(a.statusBar.errorMsg, "different group") {
		t.Errorf("expected 'different group' message, got %q", a.statusBar.errorMsg)
	}
}

// --- /groupcreate argument parser ---

func TestParseGroupcreateArgs_NoQuotedName(t *testing.T) {
	name, tokens := parseGroupcreateArgs("@alice @bob @carol")
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestParseGroupcreateArgs_QuotedName(t *testing.T) {
	name, tokens := parseGroupcreateArgs(`"Project X" @alice @bob`)
	if name != "Project X" {
		t.Errorf("expected 'Project X', got %q", name)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestParseGroupcreateArgs_QuotedNameOnly(t *testing.T) {
	name, tokens := parseGroupcreateArgs(`"Only Name"`)
	if name != "Only Name" {
		t.Errorf("expected 'Only Name', got %q", name)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %v", tokens)
	}
}

func TestParseGroupcreateArgs_Empty(t *testing.T) {
	name, tokens := parseGroupcreateArgs("")
	if name != "" || tokens != nil {
		t.Errorf("empty input should return empty values, got name=%q tokens=%v", name, tokens)
	}
}

// --- Input parser coverage for new commands ---

func TestInputParser_AuditMembersAdminsRouteToAppWithGroup(t *testing.T) {
	for _, cmd := range []string{"/audit", "/admins"} {
		i := &InputModel{}
		i.handleCommand(cmd, nil, "", "group_x", "")
		sc := i.PendingCommand()
		if sc == nil || sc.Command != cmd || sc.Group != "group_x" {
			t.Errorf("%s should route with group, got %+v", cmd, sc)
		}
	}
}

func TestInputParser_MembersRoutesToAppInAnyContext(t *testing.T) {
	contexts := []struct {
		name        string
		room, group string
		dm          string
	}{
		{name: "room", room: "room_x"},
		{name: "group", group: "group_x"},
		{name: "dm", dm: "dm_x"},
	}
	for _, tc := range contexts {
		t.Run(tc.name, func(t *testing.T) {
			i := &InputModel{}
			i.handleCommand("/members", nil, tc.room, tc.group, tc.dm)
			sc := i.PendingCommand()
			if sc == nil || sc.Command != "/members" {
				t.Fatalf("/members should route in %s context, got %+v", tc.name, sc)
			}
		})
	}
}

func TestInputParser_InfoRoutesToAppInAnyContext(t *testing.T) {
	contexts := []struct {
		name        string
		room, group string
		dm          string
	}{
		{name: "room", room: "room_x"},
		{name: "group", group: "group_x"},
		{name: "dm", dm: "dm_x"},
	}
	for _, tc := range contexts {
		t.Run(tc.name, func(t *testing.T) {
			i := &InputModel{}
			i.handleCommand("/info", nil, tc.room, tc.group, tc.dm)
			sc := i.PendingCommand()
			if sc == nil || sc.Command != "/info" {
				t.Fatalf("/info should route in %s context, got %+v", tc.name, sc)
			}
		})
	}
}

func TestInputParser_AuditCarriesNumericArg(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/audit 50", nil, "", "group_x", "")
	sc := i.PendingCommand()
	if sc == nil || sc.Arg != "50" {
		t.Errorf("expected arg=50, got %+v", sc)
	}
}

func TestInputParser_AuditOutsideGroupRoutesToApp(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/audit", nil, "room_x", "", "")
	if sc := i.PendingCommand(); sc == nil || sc.Command != "/audit" {
		t.Errorf("/audit in room context should route, got %+v", sc)
	}
}

func TestInputParser_AdminsOutsideGroupRoutesToApp(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/admins", nil, "room_x", "", "")
	if sc := i.PendingCommand(); sc == nil || sc.Command != "/admins" {
		t.Errorf("/admins in room context should route, got %+v", sc)
	}
}

func TestInputParser_UndoRequiresGroupContext(t *testing.T) {
	i := &InputModel{}
	// In a group — routes
	i.handleCommand("/undo", nil, "", "group_x", "")
	if sc := i.PendingCommand(); sc == nil || sc.Command != "/undo" {
		t.Errorf("/undo in group context should route, got %+v", sc)
	}
	// In a room — drops
	i.handleCommand("/undo", nil, "room_x", "", "")
	if sc := i.PendingCommand(); sc != nil {
		t.Error("/undo in room context should drop")
	}
}

func TestInputParser_GroupcreateDmcreateRouteWithArg(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/groupcreate @alice @bob", nil, "", "", "")
	sc := i.PendingCommand()
	if sc == nil || sc.Command != "/groupcreate" || sc.Arg != "@alice @bob" {
		t.Errorf("groupcreate wrong: %+v", sc)
	}
	i.handleCommand("/dmcreate @alice", nil, "", "", "")
	sc = i.PendingCommand()
	if sc == nil || sc.Command != "/dmcreate" || sc.Arg != "@alice" {
		t.Errorf("dmcreate wrong: %+v", sc)
	}
}

// --- Info panel A/K/P/X keybindings ---

func TestInfoPanel_AKPXEmitAdminMemberActionMsgs(t *testing.T) {
	cases := []struct {
		key    string
		action string
	}{
		{"K", "admin_kick"},
		{"p", "admin_promote"},
		{"x", "admin_demote"},
	}
	for _, tc := range cases {
		i := InfoPanelModel{
			visible: true,
			isGroup: true,
			members: []memberInfo{
				{User: "usr_alice", DisplayName: "alice"},
				{User: "usr_bob", DisplayName: "bob"},
			},
			cursor: 1, // bob
		}
		_, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if cmd == nil {
			t.Errorf("%s should emit MemberActionMsg", tc.key)
			continue
		}
		action, ok := cmd().(MemberActionMsg)
		if !ok {
			t.Errorf("%s expected MemberActionMsg, got %T", tc.key, cmd())
			continue
		}
		if action.Action != tc.action {
			t.Errorf("%s action = %q, want %q", tc.key, action.Action, tc.action)
		}
		if action.User != "usr_bob" {
			t.Errorf("%s user = %q, want usr_bob", tc.key, action.User)
		}
	}
}

func TestInfoPanel_AdminKeysInactiveOutsideGroup(t *testing.T) {
	// Room context — A/K/P/X should not emit anything.
	i := InfoPanelModel{
		visible: true,
		isGroup: false, // room
		members: []memberInfo{{User: "usr_alice"}},
		cursor:  0,
	}
	_, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	if cmd != nil {
		t.Error("K in room context should not emit")
	}
}

// --- group_event rendering: extended leave with by field ---

func TestGroupEventHandler_RemovedWithByFieldShowsSpecificAdmin(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("", "group_kick", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:   "group_event",
		Group:  "group_kick",
		Event:  "leave",
		User:   "bob",
		By:     "alice",
		Reason: "removed",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	if len(a.messages.messages) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(a.messages.messages))
	}
	text := a.messages.messages[0].SystemText
	if !strings.Contains(text, "alice removed bob") {
		t.Errorf("system text should say 'alice removed bob', got %q", text)
	}
}

func TestGroupEventHandler_QuietSuppressesSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("", "group_x", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:  "group_event",
		Group: "group_x",
		Event: "promote",
		User:  "bob",
		By:    "alice",
		Quiet: true,
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	if len(a.messages.messages) != 0 {
		t.Errorf("quiet event should not render a system message, got %d", len(a.messages.messages))
	}
}
