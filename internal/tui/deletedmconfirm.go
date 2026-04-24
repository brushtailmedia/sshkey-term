package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DeleteDMConfirmModel is the yes/no confirmation dialog for /delete on a
// 1:1 DM. /delete is silent (the other party is never notified) but
// globally destructive on the leaver's side: every device purges local
// history when the dm_left echo arrives. We require an explicit
// confirmation because the action is unrecoverable on this account —
// only the OTHER party still has the messages, and the next time they
// message us it will appear as a fresh conversation.
type DeleteDMConfirmModel struct {
	visible   bool
	dm        string // DM ID — passed back via DeleteDMConfirmMsg on confirm
	otherName string // display name of the other party, shown in the prompt
}

// DeleteDMConfirmMsg is emitted when the user confirms /delete on a 1:1
// DM. The app handles this by sending the leave_dm protocol message; the
// local effects (purge messages, drop sidebar entry) happen later when
// the dm_left echo arrives.
type DeleteDMConfirmMsg struct {
	DM string
}

func (d *DeleteDMConfirmModel) Show(dmID, otherName string) {
	d.visible = true
	d.dm = dmID
	d.otherName = otherName
}

func (d *DeleteDMConfirmModel) Hide() {
	d.visible = false
	d.dm = ""
	d.otherName = ""
}

func (d *DeleteDMConfirmModel) IsVisible() bool {
	return d.visible
}

func (d DeleteDMConfirmModel) Update(msg tea.KeyMsg) (DeleteDMConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		dmID := d.dm
		d.Hide()
		return d, func() tea.Msg {
			return DeleteDMConfirmMsg{DM: dmID}
		}
	case "n", "esc":
		d.Hide()
		return d, nil
	}
	return d, nil
}

func (d DeleteDMConfirmModel) View(width int) string {
	if !d.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Delete conversation?"))
	b.WriteString("\n\n")

	name := d.otherName
	if name == "" {
		name = "this conversation"
	}
	b.WriteString("  Delete conversation with " + errorStyle.Render(name) + "?\n\n")
	b.WriteString("  This will remove the conversation from every device on\n")
	b.WriteString("  your account and delete all local messages and history.\n\n")
	b.WriteString("  If " + name + " messages you again, it will appear as a\n")
	b.WriteString("  new conversation with no history.\n\n")
	b.WriteString("  [y] Delete  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
