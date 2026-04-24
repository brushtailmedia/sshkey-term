package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KickConfirmModel is the y/n confirmation dialog for /kick (admin
// removal from a group). Phase 14.
//
// Phase 14 deferred-items pass: dialog now shows the current member
// count so the admin sees the impact before confirming. Join date per
// member would require extending the protocol (server doesn't return
// joined_at to clients today), so that's skipped — member count is
// the information-dense summary we can surface without new wire
// fields.
type KickConfirmModel struct {
	visible     bool
	group       string
	groupName   string
	targetID    string
	targetName  string
	memberCount int // total members in the group at Show() time
}

// KickConfirmMsg is emitted when the user confirms /kick. The app
// handles it by sending remove_from_group via client.RemoveFromGroup.
type KickConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *KickConfirmModel) Show(groupID, groupName, targetID, targetName string, memberCount int) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
	m.memberCount = memberCount
}

func (m *KickConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
	m.memberCount = 0
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
	b.WriteString("  Remove " + errorStyle.Render(m.targetName) + " from " + errorStyle.Render(groupName) + "?\n")
	if m.memberCount > 0 {
		remaining := m.memberCount - 1
		membersLabel := "members"
		if remaining == 1 {
			membersLabel = "member"
		}
		b.WriteString(helpDescStyle.Render(fmt.Sprintf("  After: %d %s will remain.\n", remaining, membersLabel)))
	}
	b.WriteString("\n")
	b.WriteString("  " + m.targetName + " will receive a notification that they were removed.\n")
	b.WriteString("  They will lose access to new messages in this group.\n")
	b.WriteString("  Remaining members see a system message.\n\n")
	b.WriteString("  [y] Remove  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
