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
