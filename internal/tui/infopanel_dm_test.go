package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInfoPanel_ShowDMNilClient verifies the ShowDM fast-path when the
// client is nil: the panel becomes visible with the dm id set and empty
// members, and the view renders without panicking.
func TestInfoPanel_ShowDMNilClient(t *testing.T) {
	i := InfoPanelModel{}
	i.ShowDM("dm_ab", nil, nil)

	if !i.IsVisible() {
		t.Error("panel should be visible after ShowDM")
	}
	if i.dm != "dm_ab" {
		t.Errorf("dm = %q, want dm_ab", i.dm)
	}
	if !i.isDM {
		t.Error("isDM should be true")
	}
	if i.isGroup {
		t.Error("isGroup should be false for a 1:1 DM")
	}
	if i.room != "" || i.group != "" {
		t.Errorf("room/group should be empty, got room=%q group=%q", i.room, i.group)
	}
	if len(i.members) != 0 {
		t.Errorf("members should be empty when client is nil, got %d", len(i.members))
	}
	// View should render without crashing even when empty
	view := i.View(80)
	if view == "" {
		t.Error("view should not be empty")
	}
}

// TestInfoPanel_DMViewRendersHeader verifies that the info panel renders
// a "DM with <other>" header when the model is set up for a 1:1 DM.
func TestInfoPanel_DMViewRendersHeader(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me", Online: true},
			{User: "alice", DisplayName: "Alice", Online: true},
		},
	}

	view := i.View(80)
	if !strings.Contains(view, "DM with Alice") {
		t.Errorf("view missing 'DM with Alice' header:\n%s", view)
	}
}

// TestInfoPanel_DMViewShowsDeleteHint verifies the info panel surfaces the
// "/delete to remove this conversation" hint when the context is a 1:1 DM.
// The refactor doc (§ 398) explicitly specifies this text.
func TestInfoPanel_DMViewShowsDeleteHint(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me"},
			{User: "alice", DisplayName: "Alice"},
		},
	}

	view := i.View(80)
	if !strings.Contains(view, "/delete") {
		t.Errorf("DM info panel should mention /delete, got:\n%s", view)
	}
	if !strings.Contains(view, "remove this conversation") {
		t.Errorf("DM info panel should contain the removal hint, got:\n%s", view)
	}
}

// TestInfoPanel_DMViewNoAdminSections verifies that the info panel does
// not render "[Admins]" / "[Members]" sub-headers for a 1:1 DM. Those
// headers are only meaningful in rooms and group DMs; 1:1 has a flat
// two-member list.
func TestInfoPanel_DMViewNoAdminSections(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me"},
			{User: "alice", DisplayName: "Alice"},
		},
	}

	view := i.View(80)
	if strings.Contains(view, "[Admins]") {
		t.Error("DM info panel should not show [Admins] header")
	}
	if strings.Contains(view, "[Members]") {
		t.Error("DM info panel should not show [Members] header")
	}
}

// TestInfoPanel_DMViewRendersBothParties verifies both self and the other
// party appear in the rendered member list.
func TestInfoPanel_DMViewRendersBothParties(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me", Online: true},
			{User: "alice", DisplayName: "Alice", Online: false},
		},
	}

	view := i.View(80)
	if !strings.Contains(view, "Me") {
		t.Errorf("view missing self entry:\n%s", view)
	}
	if !strings.Contains(view, "Alice") {
		t.Errorf("view missing other party entry:\n%s", view)
	}
}

func TestInfoPanel_DMViewOmitsEnterMessageHint(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me"},
			{User: "alice", DisplayName: "Alice"},
		},
	}

	view := i.View(80)
	if strings.Contains(view, "Enter=message") {
		t.Errorf("DM info panel should not show Enter=message hint, got:\n%s", view)
	}
}

func TestInfoPanel_DMEnterNoop(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		dm:      "dm_ab",
		isDM:    true,
		members: []memberInfo{
			{User: "me", DisplayName: "Me"},
			{User: "alice", DisplayName: "Alice"},
		},
	}

	_, cmd := i.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("DM info panel Enter should be a no-op, got command")
	}
}

// TestInfoPanel_ShowRoomClearsDM verifies that calling ShowRoom after
// ShowDM clears the dm and isDM flags so the panel renders as a room.
func TestInfoPanel_ShowRoomClearsDM(t *testing.T) {
	i := InfoPanelModel{}
	i.ShowDM("dm_ab", nil, nil)
	i.ShowRoom("room_general", nil, nil)

	if i.isDM {
		t.Error("isDM should be false after ShowRoom")
	}
	if i.dm != "" {
		// ShowRoom doesn't currently clear the dm field — if this fires
		// we should add i.dm = "" to ShowRoom for consistency with how
		// ShowDM clears i.room and i.group.
		t.Errorf("dm should be cleared after ShowRoom, got %q", i.dm)
	}
}

// TestInfoPanel_ShowGroupClearsDM verifies that calling ShowGroup after
// ShowDM clears the dm and isDM flags.
func TestInfoPanel_ShowGroupClearsDM(t *testing.T) {
	i := InfoPanelModel{}
	i.ShowDM("dm_ab", nil, nil)
	i.ShowGroup("group_1", nil, nil)

	if i.isDM {
		t.Error("isDM should be false after ShowGroup")
	}
	if i.dm != "" {
		t.Errorf("dm should be cleared after ShowGroup, got %q", i.dm)
	}
}
