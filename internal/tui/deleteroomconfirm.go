package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DeleteRoomConfirmModel is the yes/no confirmation dialog for /delete
// on a room. Parallel to DeleteGroupConfirmModel but with different
// copy based on whether the room is active or retired (Phase 12).
//
// Active room: "Delete room X?" — mentions that an admin will need to
// re-add the user if they change their mind.
//
// Retired room: "Delete archived room X?" — mentions the room is
// archived and cannot be undone. Uses the suffixed display name that
// the server set during retirement.
//
// Distinct from LeaveConfirmModel (which handles /leave — keep local
// history) — /delete wipes local messages on every device of the user
// via the room_deleted echo.
type DeleteRoomConfirmModel struct {
	visible  bool
	room     string // room ID — passed back via DeleteRoomConfirmMsg on confirm
	roomName string // display name shown in the prompt
	retired  bool   // if true, render the retired-room wording
}

// DeleteRoomConfirmMsg is emitted when the user confirms /delete on a
// room. The app handles this by sending the delete_room protocol
// message; the local effects (purge messages, drop sidebar entry)
// happen when the room_deleted echo arrives back from the server.
type DeleteRoomConfirmMsg struct {
	Room string
}

// Show displays the dialog. The retired flag picks the appropriate
// wording — callers should resolve it via client.IsRoomRetired on
// the local store before calling Show.
func (d *DeleteRoomConfirmModel) Show(roomID, roomName string, retired bool) {
	d.visible = true
	d.room = roomID
	d.roomName = roomName
	d.retired = retired
}

func (d *DeleteRoomConfirmModel) Hide() {
	d.visible = false
	d.room = ""
	d.roomName = ""
	d.retired = false
}

func (d *DeleteRoomConfirmModel) IsVisible() bool {
	return d.visible
}

func (d DeleteRoomConfirmModel) Update(msg tea.KeyMsg) (DeleteRoomConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		roomID := d.room
		d.Hide()
		return d, func() tea.Msg {
			return DeleteRoomConfirmMsg{Room: roomID}
		}
	case "n", "esc":
		d.Hide()
		return d, nil
	}
	return d, nil
}

func (d DeleteRoomConfirmModel) View(width int) string {
	if !d.visible {
		return ""
	}

	var b strings.Builder

	name := d.roomName
	if name == "" {
		name = "this room"
	}

	if d.retired {
		b.WriteString(searchHeaderStyle.Render(" Delete archived room?"))
		b.WriteString("\n\n")
		b.WriteString("  Delete archived room " + errorStyle.Render(name) + "?\n\n")
		b.WriteString("  This will remove you from the room membership and\n")
		b.WriteString("  delete all local messages and history from your device.\n")
		b.WriteString("  The room is archived — this action cannot be undone.\n\n")
		b.WriteString("  [y] Delete  [n] Cancel\n")
	} else {
		b.WriteString(searchHeaderStyle.Render(" Delete room?"))
		b.WriteString("\n\n")
		b.WriteString("  Delete room " + errorStyle.Render(name) + "?\n\n")
		b.WriteString("  This will remove you from the room and delete all\n")
		b.WriteString("  local messages and history from your device.\n\n")
		b.WriteString("  An admin will need to add you back if you change\n")
		b.WriteString("  your mind.\n\n")
		b.WriteString("  [y] Delete  [n] Cancel\n")
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
