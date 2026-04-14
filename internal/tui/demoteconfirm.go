package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DemoteConfirmModel is the y/n confirmation dialog for /demote
// (revoking admin status in a group). Phase 14.
//
// The plan wants "After this, Project Alpha will have 1 admin (you)"
// contextual text — that's Chunk 6 rendering polish.
type DemoteConfirmModel struct {
	visible    bool
	group      string
	groupName  string
	targetID   string
	targetName string
}

// DemoteConfirmMsg is emitted on confirm. The app handles it by
// sending demote_group_admin via client.DemoteGroupAdmin.
type DemoteConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *DemoteConfirmModel) Show(groupID, groupName, targetID, targetName string) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
}

func (m *DemoteConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
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
	b.WriteString("  Demote " + errorStyle.Render(m.targetName) + " from admin?\n\n")
	b.WriteString("  They will lose the ability to add, remove, promote,\n")
	b.WriteString("  or demote members. They remain a regular member of\n")
	b.WriteString("  the group.\n\n")
	b.WriteString("  The server will reject this if it would leave the\n")
	b.WriteString("  group with zero admins.\n\n")
	b.WriteString("  [y] Demote  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
