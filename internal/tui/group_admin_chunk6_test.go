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

func TestInputParser_SetStatusRoutesToApp(t *testing.T) {
	i := &InputModel{}
	i.handleCommand("/setstatus away", nil, "room_x", "", "")
	sc := i.PendingCommand()
	if sc == nil {
		t.Fatal("/setstatus should route to app")
	}
	if sc.Command != "/setstatus" {
		t.Fatalf("command = %q, want /setstatus", sc.Command)
	}
	if sc.Arg != "away" {
		t.Fatalf("arg = %q, want away", sc.Arg)
	}
	if sc.Room != "room_x" {
		t.Fatalf("room = %q, want room_x", sc.Room)
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

func TestInputParser_GroupcreateDmcreateRouteBareAndWithArg(t *testing.T) {
	cases := []struct {
		text    string
		wantCmd string
		wantArg string
	}{
		{"/groupcreate", "/groupcreate", ""},
		{"/groupcreate @alice @bob", "/groupcreate", "@alice @bob"},
		{"/dmcreate", "/dmcreate", ""},
		{"/dmcreate @alice", "/dmcreate", "@alice"},
	}
	for _, tc := range cases {
		i := &InputModel{}
		i.handleCommand(tc.text, nil, "", "", "")
		sc := i.PendingCommand()
		if sc == nil || sc.Command != tc.wantCmd || sc.Arg != tc.wantArg {
			t.Errorf("%q routed wrong: got %+v, want command=%q arg=%q", tc.text, sc, tc.wantCmd, tc.wantArg)
		}
	}
}

// --- Info panel A/K/P/X keybindings ---

// A/K/P/X admin keys are DISABLED 2026-05-19 — they were mis-wired
// (K/x froze the app behind the modal info panel; a/p were no-ops).
// They are now inert no-ops in the group info panel pending the locked
// §9 step 6 re-enable (2026-05-20): the four admin-action footer keys
// (a/r/p/x) emit InfoPanelAdminKeyMsg with the right Verb and
// (for r/p/x) the highlighted member's ID. `a` carries no TargetID
// because its target is a NON-member (picked via the shared picker
// App opens in response). `K` (capital) is NOT re-enabled — `r` is
// the locked letter. Panel Hide()d before the message fires (#6).
// This test pair replaces the prior `…DisabledInGroup` locking test.
func TestInfoPanel_AKPXAdminKeysEmitMsgForAdmin(t *testing.T) {
	cases := []struct {
		key          string
		wantVerb     string
		wantTargetID string
	}{
		{"a", "/add", ""},          // target picked later via the /add picker
		{"r", "/kick", "usr_bob"},  // highlighted member
		{"p", "/promote", "usr_bob"},
		{"x", "/demote", "usr_bob"},
	}
	for _, tc := range cases {
		i := InfoPanelModel{
			visible:      true,
			group:        "group_1",
			isGroup:      true,
			isGroupAdmin: true,
			members: []memberInfo{
				{User: "usr_alice", DisplayName: "alice"},
				{User: "usr_bob", DisplayName: "bob"},
			},
			cursor: 1, // bob highlighted
		}
		updated, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if cmd == nil {
			t.Errorf("%q (admin) must emit InfoPanelAdminKeyMsg, got nil", tc.key)
			continue
		}
		// Modal lifecycle (#6 freeze-fix): panel Hide()d BEFORE the
		// confirm/picker opens — never two modals visible at once.
		if updated.IsVisible() {
			t.Errorf("%q must Hide the panel before emitting the message (#6 lifecycle)", tc.key)
		}
		msg, ok := cmd().(InfoPanelAdminKeyMsg)
		if !ok {
			t.Errorf("%q emitted %T, want InfoPanelAdminKeyMsg", tc.key, cmd())
			continue
		}
		if msg.Verb != tc.wantVerb || msg.Group != "group_1" || msg.TargetID != tc.wantTargetID {
			t.Errorf("%q emitted {Verb:%q Group:%q TargetID:%q}, want {%q group_1 %q}",
				tc.key, msg.Verb, msg.Group, msg.TargetID, tc.wantVerb, tc.wantTargetID)
		}
	}
	// Capital K stays out — `r` is the new letter (group-infopanel-picker-rework.md §1).
	i := InfoPanelModel{visible: true, group: "group_1", isGroup: true, isGroupAdmin: true,
		members: []memberInfo{{User: "usr_alice"}}, cursor: 0}
	if _, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")}); cmd != nil {
		t.Errorf("capital K must NOT be the remove key — `r` is locked; emitted %T", cmd())
	}
}

// Role-gating: a non-admin in the same group sees the keys as inert
// (no messages emitted), matching the footer hint suppression.
func TestInfoPanel_AKPXAdminKeysInertForNonAdmin(t *testing.T) {
	for _, key := range []string{"a", "r", "p", "x"} {
		i := InfoPanelModel{
			visible:      true,
			group:        "group_1",
			isGroup:      true,
			isGroupAdmin: false, // non-admin
			members:      []memberInfo{{User: "usr_alice"}, {User: "usr_bob"}},
			cursor:       1,
		}
		_, cmd := i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil {
			t.Errorf("%q for non-admin must be inert (role-gated), emitted %T", key, cmd())
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
