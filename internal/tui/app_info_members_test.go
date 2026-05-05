package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestApp_IKeyOpensInfoPanelWhenMessagesFocused(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.focus = FocusMessages
	a.messages.SetContext("", "", "dm_test")

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	updated := model.(App)

	if !updated.infoPanel.IsVisible() {
		t.Fatal("i key in messages focus should open info panel")
	}
}

func TestApp_IKeyDoesNotOpenInfoPanelWhenInputFocused(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.focus = FocusInput
	a.messages.SetContext("", "", "dm_test")

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	updated := model.(App)

	if updated.infoPanel.IsVisible() {
		t.Fatal("i key in input focus should not open info panel")
	}
	if updated.input.Value() != "i" {
		t.Fatalf("i key should still type into input, got %q", updated.input.Value())
	}
}

func TestApp_MembersCommandOpensMemberPanelInAnyContext(t *testing.T) {
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
			a, _ := newEditAppHarness(t)
			a.memberPanel = NewMemberPanel()
			a.messages.SetContext(tc.room, tc.group, tc.dm)

			a.handleSlashCommand(&SlashCommandMsg{
				Command: "/members",
				Room:    tc.room,
				Group:   tc.group,
				DM:      tc.dm,
			})

			if !a.memberPanel.IsVisible() {
				t.Fatalf("/members should open member panel in %s context", tc.name)
			}
		})
	}
}

func TestApp_MembersCommandTogglesClosedWhenAlreadyOpen(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.memberPanel = NewMemberPanel()
	a.messages.SetContext("room_x", "", "")

	a.handleSlashCommand(&SlashCommandMsg{
		Command: "/members",
		Room:    "room_x",
	})
	if !a.memberPanel.IsVisible() {
		t.Fatal("first /members should open member panel")
	}

	a.handleSlashCommand(&SlashCommandMsg{
		Command: "/members",
		Room:    "room_x",
	})
	if a.memberPanel.IsVisible() {
		t.Fatal("second /members should close member panel")
	}
}

func TestApp_MembersCommandShowsErrorWhenDisconnected(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.client = nil
	a.messages.SetContext("room_x", "", "")

	a.handleSlashCommand(&SlashCommandMsg{
		Command: "/members",
		Room:    "room_x",
	})

	if a.statusBar.errorMsg != "/members unavailable - not connected" {
		t.Fatalf("expected disconnected error, got %q", a.statusBar.errorMsg)
	}
	if a.memberPanel.IsVisible() {
		t.Fatal("member panel should remain hidden when disconnected")
	}
}

func TestApp_AdminsCommandShowsErrorOutsideGroupContext(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_x", "", "")

	a.handleSlashCommand(&SlashCommandMsg{
		Command: "/admins",
		Room:    "room_x",
	})

	if a.statusBar.errorMsg != "/admins only works inside a group DM" {
		t.Fatalf("expected context error, got %q", a.statusBar.errorMsg)
	}
	if a.membersOverlay.IsVisible() {
		t.Fatal("/admins overlay should remain hidden outside group context")
	}
}

func TestApp_MemberActionMessageSendsCreateDM(t *testing.T) {
	a, _ := newEditAppHarness(t)
	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MemberActionMsg{Action: "message", User: "usr_bob"})
	_ = model.(App)

	var frame map[string]any
	if err := json.NewDecoder(&out).Decode(&frame); err != nil {
		t.Fatalf("decode outbound frame: %v", err)
	}
	if got, _ := frame["type"].(string); got != "create_dm" {
		t.Fatalf("outbound type = %q, want create_dm", got)
	}
	if got, _ := frame["other"].(string); got != "usr_bob" {
		t.Fatalf("outbound other = %q, want usr_bob", got)
	}
}

func TestApp_MemberActionMessageRejectsSelf(t *testing.T) {
	a, _ := newEditAppHarness(t)
	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MemberActionMsg{Action: "message", User: "usr_alice"})
	updated := model.(App)

	if updated.statusBar.errorMsg != "Cannot create a DM with yourself" {
		t.Fatalf("expected self-DM error, got %q", updated.statusBar.errorMsg)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no outbound frame for self-DM, wrote %d bytes", out.Len())
	}
}

func TestApp_MemberPanelMessageActionFocusesCreatedDM(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"room_prev"})
	a.sidebar.updateSelection()
	a.messages.SetContext("room_prev", "", "")
	a.memberPanel = NewMemberPanel()
	a.memberPanel.visible = true
	a.focus = FocusMembers

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MemberActionMsg{Action: "message", User: "usr_bob"})
	updated := model.(App)

	raw, _ := json.Marshal(protocol.DMCreated{
		Type:    "dm_created",
		DM:      "dm_new",
		Members: []string{"usr_alice", "usr_bob"},
	})
	updated.handleServerMessage(ServerMsg{Type: "dm_created", Raw: raw})

	if updated.messages.dm != "dm_new" {
		t.Fatalf("active dm = %q, want dm_new", updated.messages.dm)
	}
	if updated.messages.room != "" || updated.messages.group != "" {
		t.Fatalf("expected room/group cleared after DM focus, got room=%q group=%q", updated.messages.room, updated.messages.group)
	}
	if updated.sidebar.SelectedDM() != "dm_new" {
		t.Fatalf("sidebar selected dm = %q, want dm_new", updated.sidebar.SelectedDM())
	}
}

func TestApp_MemberPanelCreateGroupFocusesCreatedGroup(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.newConv = NewNewConv()
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"room_prev"})
	a.sidebar.updateSelection()
	a.messages.SetContext("room_prev", "", "")
	a.memberPanel = NewMemberPanel()
	a.memberPanel.visible = true
	a.focus = FocusMembers

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(MemberActionMsg{Action: "create_group", User: "usr_bob"})
	updated := model.(App)
	if !updated.newConv.IsVisible() {
		t.Fatal("expected new group dialog to open")
	}

	model, _ = updated.Update(CreateConvMsg{
		Members: []string{"usr_bob", "usr_carol"},
		Name:    "Project",
	})
	updated = model.(App)

	raw, _ := json.Marshal(protocol.GroupCreated{
		Type:    "group_created",
		Group:   "group_new",
		Members: []string{"usr_alice", "usr_bob", "usr_carol"},
		Admins:  []string{"usr_alice"},
		Name:    "Project",
	})
	updated.handleServerMessage(ServerMsg{Type: "group_created", Raw: raw})

	if updated.messages.group != "group_new" {
		t.Fatalf("active group = %q, want group_new", updated.messages.group)
	}
	if updated.messages.room != "" || updated.messages.dm != "" {
		t.Fatalf("expected room/dm cleared after group focus, got room=%q dm=%q", updated.messages.room, updated.messages.dm)
	}
	if updated.sidebar.SelectedGroup() != "group_new" {
		t.Fatalf("sidebar selected group = %q, want group_new", updated.sidebar.SelectedGroup())
	}
}

func TestApp_DmCreatedDoesNotStealFocusWithoutPendingIntent(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"room_prev"})
	a.sidebar.updateSelection()
	a.messages.SetContext("room_prev", "", "")

	raw, _ := json.Marshal(protocol.DMCreated{
		Type:    "dm_created",
		DM:      "dm_incoming",
		Members: []string{"usr_alice", "usr_bob"},
	})
	a.handleServerMessage(ServerMsg{Type: "dm_created", Raw: raw})

	if a.messages.room != "room_prev" {
		t.Fatalf("room context changed unexpectedly to %q", a.messages.room)
	}
	if a.messages.dm != "" {
		t.Fatalf("dm context should remain empty, got %q", a.messages.dm)
	}
}

func TestApp_MemberPanelMouseClickSelectsOnly_NoMenuOpen(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.width = 120
	a.height = 40
	a.memberPanel = NewMemberPanel()
	a.memberPanel.visible = true
	a.memberPanel.members = []memberPanelEntry{
		{User: "usr_a", DisplayName: "Alice"},
		{User: "usr_b", DisplayName: "Bob"},
	}
	a.memberMenu = NewMemberMenu()

	layout := computeLayout(a.width, a.height, true)
	x := layout.MemberX0 + 1
	y := layout.MemberY0 + 3 // first member row is +2, second is +3

	model, _ := a.handleMouseClick(x, y)
	updated := model.(App)

	if updated.focus != FocusMembers {
		t.Fatalf("focus = %v, want FocusMembers", updated.focus)
	}
	if !updated.memberPanel.focused {
		t.Fatal("member panel should be focused after click in members pane")
	}
	if updated.memberPanel.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (second member)", updated.memberPanel.cursor)
	}
	if updated.memberMenu.IsVisible() {
		t.Fatal("member menu should stay closed on mouse click; Enter should open it")
	}
}

func TestApp_MemberPanelMouseClick_SelectsVisualRowWithLongNames(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.width = 120
	a.height = 40
	a.connected = true
	a.memberPanel = NewMemberPanel()
	a.memberPanel.visible = true
	a.memberPanel.focused = true
	a.memberPanel.members = []memberPanelEntry{
		{User: "usr_long", DisplayName: "usr_X39baHmKonsL4SyQVUmbU"},
		{User: "usr_bob", DisplayName: "bob_target"},
	}

	// Find the rendered row where bob_target is visible, then click that row.
	rendered := stripANSI(a.View())
	lines := strings.Split(rendered, "\n")
	bobY := -1
	for i, ln := range lines {
		if strings.Contains(ln, "bob_target") {
			bobY = i
			break
		}
	}
	if bobY < 0 {
		t.Fatalf("could not find bob_target in rendered view:\n%s", rendered)
	}

	layout := computeLayout(a.width, a.height, true)
	x := layout.MemberX0 + 2 // inside member panel content area

	model, _ := a.handleMouseClick(x, bobY)
	updated := model.(App)
	if updated.memberPanel.cursor != 1 {
		t.Fatalf("clicking bob row should select index 1, got %d", updated.memberPanel.cursor)
	}
}
