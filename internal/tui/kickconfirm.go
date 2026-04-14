package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KickConfirmModel is the y/n confirmation dialog for /kick (admin
// removal from a group). Phase 14.
//
// The plan wants richer contextual content here ("Alice joined 2 weeks
// ago"), but that lives in Chunk 6 alongside the other render polish.
// This minimum-viable dialog just confirms the action with target name.
type KickConfirmModel struct {
	visible    bool
	group      string
	groupName  string
	targetID   string
	targetName string
}

// KickConfirmMsg is emitted when the user confirms /kick. The app
// handles it by sending remove_from_group via client.RemoveFromGroup.
type KickConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *KickConfirmModel) Show(groupID, groupName, targetID, targetName string) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
}

func (m *KickConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
}

func (m *KickConfirmModel) IsVisible() bool {
	return m.visible
}

func (m KickConfirmModel) Update(msg tea.KeyMsg) (KickConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := m.group
		targetID := m.targetID
		m.Hide()
		return m, func() tea.Msg {
			return KickConfirmMsg{Group: groupID, TargetID: targetID}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m KickConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Remove member?"))
	b.WriteString("\n\n")
	groupName := m.groupName
	if groupName == "" {
		groupName = "this group"
	}
	b.WriteString("  Remove " + errorStyle.Render(m.targetName) + " from " + errorStyle.Render(groupName) + "?\n\n")
	b.WriteString("  They will receive a notification that they were removed.\n")
	b.WriteString("  They lose access to new messages in this group.\n")
	b.WriteString("  Remaining members see a system message.\n\n")
	b.WriteString("  [y] Remove  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
