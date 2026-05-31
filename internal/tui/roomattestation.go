package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// RoomAttestationAlertModel is a non-fatal modal shown when a room epoch key
// failed F7 member-attestation verification and was NOT adopted (fail-closed —
// the room is un-decryptable for that epoch). It warns of possible server
// tampering (a key wrapped for a member set the user can't see — a shadow
// reader). Multiple alerts queue and show one at a time. See
// sshkey-chat/docs/planning/open/f7-room-member-attestation.md.
type RoomAttestationAlertModel struct {
	pending []roomAttEntry
}

type roomAttEntry struct {
	room      string
	generator string
	reason    string
}

func NewRoomAttestationAlert() RoomAttestationAlertModel {
	return RoomAttestationAlertModel{}
}

// Show enqueues an alert. Duplicate (room) entries already queued are ignored
// so a burst of failures for one room doesn't stack.
func (d *RoomAttestationAlertModel) Show(room, generator, reason string) {
	if room == "" {
		return
	}
	for _, e := range d.pending {
		if e.room == room {
			return
		}
	}
	d.pending = append(d.pending, roomAttEntry{room: room, generator: generator, reason: reason})
}

func (d *RoomAttestationAlertModel) Hide() {
	if len(d.pending) > 0 {
		d.pending = d.pending[1:]
	}
}

func (d *RoomAttestationAlertModel) IsVisible() bool {
	return len(d.pending) > 0
}

func (d RoomAttestationAlertModel) Update(msg tea.KeyMsg) (RoomAttestationAlertModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "q", "esc":
		d.Hide()
	}
	return d, nil
}

func (d RoomAttestationAlertModel) HandleMouse(msg tea.MouseMsg) (RoomAttestationAlertModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return d, nil
	}
	d.Hide()
	return d, nil
}

func (d RoomAttestationAlertModel) View(width int) string {
	if len(d.pending) == 0 {
		return ""
	}
	e := d.pending[0]

	var b strings.Builder
	b.WriteString(errorStyle.Render(" ⚠ Room membership could not be verified"))
	b.WriteString("\n\n")
	b.WriteString("  A new room key could NOT be confirmed to be shared with\n")
	b.WriteString("  exactly the members you can see. It was rejected — new\n")
	b.WriteString("  messages in this room may not appear until it's resolved.\n\n")
	if e.room != "" {
		b.WriteString("  Room: " + helpDescStyle.Render(e.room) + "\n")
	}
	if e.generator != "" {
		b.WriteString("  Key from: " + helpDescStyle.Render(e.generator) + "\n")
	}
	if e.reason != "" {
		b.WriteString("  Reason: " + helpDescStyle.Render(e.reason) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  This can indicate a compromised or malicious server trying\n")
	b.WriteString("  to add a hidden reader. If it persists, stop using this\n")
	b.WriteString("  server and report it.\n\n")
	if len(d.pending) > 1 {
		b.WriteString(helpDescStyle.Render(fmt.Sprintf("  (+%d more)", len(d.pending)-1)))
		b.WriteString("\n\n")
	}
	b.WriteString(helpDescStyle.Render("  Enter / q / Esc = dismiss"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
