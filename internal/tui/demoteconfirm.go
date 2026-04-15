package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DemoteConfirmModel is the y/n confirmation dialog for /demote
// (revoking admin status in a group). Phase 14.
//
// adminCount is the number of admins BEFORE the demote, so the
// dialog can compute the resulting count ("will have N admins")
// and warn when demoting drops the group to a single admin.
type DemoteConfirmModel struct {
	visible       bool
	group         string
	groupName     string
	targetID      string
	targetName    string
	adminCount    int // admins BEFORE the demote
	targetIsSelf  bool
}

// DemoteConfirmMsg is emitted on confirm. The app handles it by
// sending demote_group_admin via client.DemoteGroupAdmin.
type DemoteConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *DemoteConfirmModel) Show(groupID, groupName, targetID, targetName string, adminCount int, targetIsSelf bool) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
	m.adminCount = adminCount
	m.targetIsSelf = targetIsSelf
}

func (m *DemoteConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
	m.adminCount = 0
	m.targetIsSelf = false
}

func (m *DemoteConfirmModel) IsVisible() bool {
	return m.visible
}

func (m DemoteConfirmModel) Update(msg tea.KeyMsg) (DemoteConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := m.group
		targetID := m.targetID
		m.Hide()
		return m, func() tea.Msg {
			return DemoteConfirmMsg{Group: groupID, TargetID: targetID}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m DemoteConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Demote admin?"))
	b.WriteString("\n\n")
	b.WriteString("  Demote " + errorStyle.Render(m.targetName) + " from admin?\n")
	if m.adminCount > 0 {
		resulting := m.adminCount - 1
		adminsLabel := "admins"
		if resulting == 1 {
			adminsLabel = "admin"
		}
		groupLabel := m.groupName
		if groupLabel == "" {
			groupLabel = "the group"
		}
		if resulting == 1 && !m.targetIsSelf {
			b.WriteString(helpDescStyle.Render(fmt.Sprintf("  After: %s will have 1 %s (you).\n", groupLabel, adminsLabel)))
			b.WriteString(helpDescStyle.Render("  If you retire your account, the oldest remaining member\n"))
			b.WriteString(helpDescStyle.Render("  will be auto-promoted as successor.\n"))
		} else {
			b.WriteString(helpDescStyle.Render(fmt.Sprintf("  After: %s will have %d %s.\n", groupLabel, resulting, adminsLabel)))
		}
	}
	b.WriteString("\n")
	b.WriteString("  " + m.targetName + " will lose the ability to add, remove, promote,\n")
	b.WriteString("  or demote members. They remain a regular member of\n")
	b.WriteString("  the group.\n\n")
	b.WriteString("  The server will reject this if it would leave the\n")
	b.WriteString("  group with zero admins.\n\n")
	b.WriteString("  [y] Demote  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
