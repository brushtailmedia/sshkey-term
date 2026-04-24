package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LeaveConfirmModel is a yes/no confirmation dialog for /leave on a group DM.
// Leaving is a server-side membership change with consequences (other members
// notified, can't post anymore), so we require an explicit confirmation
// before sending the protocol message.
type LeaveConfirmModel struct {
	visible   bool
	group     string // group DM ID — passed back via LeaveConfirmMsg on confirm
	groupName string // display name shown in the prompt
}

// LeaveConfirmMsg is emitted when the user confirms /leave. The app handles
// this by sending the leave_group protocol message.
type LeaveConfirmMsg struct {
	Group string
}

func (l *LeaveConfirmModel) Show(groupID, groupName string) {
	l.visible = true
	l.group = groupID
	l.groupName = groupName
}

func (l *LeaveConfirmModel) Hide() {
	l.visible = false
	l.group = ""
	l.groupName = ""
}

func (l *LeaveConfirmModel) IsVisible() bool {
	return l.visible
}

func (l LeaveConfirmModel) Update(msg tea.KeyMsg) (LeaveConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := l.group
		l.Hide()
		return l, func() tea.Msg {
			return LeaveConfirmMsg{Group: groupID}
		}
	case "n", "esc":
		l.Hide()
		return l, nil
	}
	return l, nil
}

func (l LeaveConfirmModel) View(width int) string {
	if !l.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Leave group?"))
	b.WriteString("\n\n")

	name := l.groupName
	if name == "" {
		name = "this group"
	}
	b.WriteString("  Leave " + errorStyle.Render(name) + "?\n\n")
	b.WriteString("  You will stop receiving new messages and cannot post.\n")
	b.WriteString("  Other members will be notified that you left.\n")
	b.WriteString("  Your local message history will remain visible (read-only).\n\n")
	b.WriteString("  To remove the group entirely from your view,\n")
	b.WriteString("  use " + errorStyle.Render("/delete") + " instead.\n\n")
	b.WriteString("  [y] Leave  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
