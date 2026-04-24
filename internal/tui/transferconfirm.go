package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// TransferConfirmModel is the y/n confirmation dialog for /transfer
// (atomic promote-successor-and-leave). Phase 14.
//
// The command is client-side syntactic sugar: on confirm, the client
// sends promote_group_admin for the target (unless they're already
// an admin) then immediately sends leave_group. The server serializes
// both writes so the leave lands after the promote, satisfying the
// "at least one admin" invariant.
//
// TargetAlreadyAdmin flips the dialog text to "bob is already an
// admin — just leave the group?" so the user isn't confused when
// the promote gets skipped.
type TransferConfirmModel struct {
	visible            bool
	group              string
	groupName          string
	targetID           string
	targetName         string
	targetAlreadyAdmin bool
}

// TransferConfirmMsg is emitted on confirm. The app handles it by
// sending promote_group_admin (if not already admin) followed by
// leave_group.
type TransferConfirmMsg struct {
	Group              string
	TargetID           string
	TargetAlreadyAdmin bool
}

func (m *TransferConfirmModel) Show(groupID, groupName, targetID, targetName string, targetAlreadyAdmin bool) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
	m.targetAlreadyAdmin = targetAlreadyAdmin
}

func (m *TransferConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
	m.targetAlreadyAdmin = false
}

func (m *TransferConfirmModel) IsVisible() bool {
	return m.visible
}

func (m TransferConfirmModel) Update(msg tea.KeyMsg) (TransferConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := m.group
		targetID := m.targetID
		already := m.targetAlreadyAdmin
		m.Hide()
		return m, func() tea.Msg {
			return TransferConfirmMsg{
				Group:              groupID,
				TargetID:           targetID,
				TargetAlreadyAdmin: already,
			}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m TransferConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Transfer and leave?"))
	b.WriteString("\n\n")
	groupName := m.groupName
	if groupName == "" {
		groupName = "the group"
	}
	if m.targetAlreadyAdmin {
		b.WriteString("  " + errorStyle.Render(m.targetName) + " is already an admin.\n\n")
		b.WriteString("  Leave " + errorStyle.Render(groupName) + "?\n\n")
	} else {
		b.WriteString("  Promote " + errorStyle.Render(m.targetName) + " to admin and leave " + errorStyle.Render(groupName) + "?\n\n")
		b.WriteString("  " + m.targetName + " will become an admin of the group.\n")
		b.WriteString("  You will then leave — you stop receiving new messages\n")
		b.WriteString("  and cannot post. Your local history remains visible.\n\n")
	}
	b.WriteString("  [y] Transfer and leave  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
