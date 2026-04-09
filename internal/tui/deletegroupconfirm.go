package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DeleteGroupConfirmModel is the yes/no confirmation dialog for /delete
// on a group DM. Distinct from LeaveConfirmModel:
//
//   - LeaveConfirmModel handles /leave: server-side membership change,
//     local history kept (sidebar greyed, read-only).
//
//   - DeleteGroupConfirmModel handles /delete: server-side leave (if still
//     a member) PLUS local history wiped on every device of the user via
//     the group_deleted echo. Sidebar entry gone entirely.
//
// We require an explicit confirmation because /delete is unrecoverable
// on the user's account — only the OTHER members still have copies of
// the messages.
type DeleteGroupConfirmModel struct {
	visible   bool
	group     string // group DM ID — passed back via DeleteGroupConfirmMsg on confirm
	groupName string // display name shown in the prompt
}

// DeleteGroupConfirmMsg is emitted when the user confirms /delete on a
// group DM. The app handles this by sending the delete_group protocol
// message; the local effects (purge messages, drop sidebar entry) happen
// when the group_deleted echo arrives back from the server.
type DeleteGroupConfirmMsg struct {
	Group string
}

func (d *DeleteGroupConfirmModel) Show(groupID, groupName string) {
	d.visible = true
	d.group = groupID
	d.groupName = groupName
}

func (d *DeleteGroupConfirmModel) Hide() {
	d.visible = false
	d.group = ""
	d.groupName = ""
}

func (d *DeleteGroupConfirmModel) IsVisible() bool {
	return d.visible
}

func (d DeleteGroupConfirmModel) Update(msg tea.KeyMsg) (DeleteGroupConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := d.group
		d.Hide()
		return d, func() tea.Msg {
			return DeleteGroupConfirmMsg{Group: groupID}
		}
	case "n", "esc":
		d.Hide()
		return d, nil
	}
	return d, nil
}

func (d DeleteGroupConfirmModel) View(width int) string {
	if !d.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Delete group conversation?"))
	b.WriteString("\n\n")

	name := d.groupName
	if name == "" {
		name = "this group"
	}
	b.WriteString("  Delete " + errorStyle.Render(name) + "?\n\n")
	b.WriteString("  This will leave the group (other members will be notified)\n")
	b.WriteString("  and remove the conversation from every device on your\n")
	b.WriteString("  account, deleting all local messages and history.\n\n")
	b.WriteString("  You can be re-invited by another member, but it will\n")
	b.WriteString("  appear as a new conversation with no history.\n\n")
	b.WriteString("  [y] Delete  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
