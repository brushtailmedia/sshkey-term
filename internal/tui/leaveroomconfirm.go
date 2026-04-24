package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LeaveRoomConfirmModel is a yes/no confirmation dialog for /leave on a
// room. Mirrors LeaveConfirmModel for groups but kept as a separate type
// so the room flow can grow features (e.g., admin policy hints, retired
// room handling in Phase 12) independently of the group flow.
//
// Leaving a room is a server-side membership change with consequences
// (other members notified, can't post anymore, epoch rotates) AND it is
// gated by the [server] allow_self_leave_rooms config flag — the server
// may reject the request entirely. We require an explicit confirmation
// before sending the protocol message even when the flag might be off,
// because the user shouldn't have to know the policy state up front.
type LeaveRoomConfirmModel struct {
	visible  bool
	room     string // room ID — passed back via LeaveRoomConfirmMsg on confirm
	roomName string // display name shown in the prompt
}

// LeaveRoomConfirmMsg is emitted when the user confirms /leave for a
// room. The app handles this by sending the leave_room protocol message.
type LeaveRoomConfirmMsg struct {
	Room string
}

func (l *LeaveRoomConfirmModel) Show(roomID, roomName string) {
	l.visible = true
	l.room = roomID
	l.roomName = roomName
}

func (l *LeaveRoomConfirmModel) Hide() {
	l.visible = false
	l.room = ""
	l.roomName = ""
}

func (l *LeaveRoomConfirmModel) IsVisible() bool {
	return l.visible
}

func (l LeaveRoomConfirmModel) Update(msg tea.KeyMsg) (LeaveRoomConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		roomID := l.room
		l.Hide()
		return l, func() tea.Msg {
			return LeaveRoomConfirmMsg{Room: roomID}
		}
	case "n", "esc":
		l.Hide()
		return l, nil
	}
	return l, nil
}

func (l LeaveRoomConfirmModel) View(width int) string {
	if !l.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Leave room?"))
	b.WriteString("\n\n")

	name := l.roomName
	if name == "" {
		name = "this room"
	}
	b.WriteString("  Leave " + errorStyle.Render("#"+name) + "?\n\n")
	b.WriteString("  You will stop receiving new messages and cannot post.\n")
	b.WriteString("  Other members will be notified that you left.\n")
	b.WriteString("  Your local message history will remain visible (read-only).\n\n")
	b.WriteString("  To remove the room entirely from your view,\n")
	b.WriteString("  use " + errorStyle.Render("/delete") + " instead.\n\n")
	b.WriteString("  Note: leaving rooms may be disabled by the server admin.\n\n")
	b.WriteString("  [y] Leave  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
