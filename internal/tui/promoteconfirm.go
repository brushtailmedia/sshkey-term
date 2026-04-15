package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PromoteConfirmModel is the y/n confirmation dialog for /promote
// (granting admin status in a group). Phase 14.
//
// The plan wants contextual text like "After this, Bob can kick,
// demote, or remove you" — that lives in Chunk 6 rendering polish.
// Minimum viable dialog here.
type PromoteConfirmModel struct {
	visible    bool
	group      string
	groupName  string
	targetID   string
	targetName string
}

// PromoteConfirmMsg is emitted on confirm. The app handles it by
// sending promote_group_admin via client.PromoteGroupAdmin.
type PromoteConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *PromoteConfirmModel) Show(groupID, groupName, targetID, targetName string) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
}

func (m *PromoteConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
}

func (m *PromoteConfirmModel) IsVisible() bool {
	return m.visible
}

func (m PromoteConfirmModel) Update(msg tea.KeyMsg) (PromoteConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := m.group
		targetID := m.targetID
		m.Hide()
		return m, func() tea.Msg {
			return PromoteConfirmMsg{Group: groupID, TargetID: targetID}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m PromoteConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Promote to admin?"))
	b.WriteString("\n\n")
	b.WriteString("  Promote " + errorStyle.Render(m.targetName) + " to admin?\n\n")
	b.WriteString("  " + m.targetName + " will be able to add, remove, promote, and demote\n")
	b.WriteString("  any member (including you). All admins are peers — there\n")
	b.WriteString("  is no protected tier.\n\n")
	b.WriteString("  [y] Promote  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
