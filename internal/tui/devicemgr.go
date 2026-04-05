package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// DeviceMgrModel shows the user's registered devices and lets them revoke
// non-current devices. The list is populated asynchronously by the app
// via SetDevices() after a device_list response arrives.
type DeviceMgrModel struct {
	visible  bool
	devices  []protocol.DeviceInfo
	cursor   int
	loading  bool
	confirm  bool   // show confirmation prompt for revoke
	status   string // inline status/error message
}

// DeviceMgrRevokeMsg is emitted when the user confirms revoking a device.
type DeviceMgrRevokeMsg struct {
	DeviceID string
}

// DeviceMgrRefreshMsg is emitted when the user presses R to reload the list.
type DeviceMgrRefreshMsg struct{}

func NewDeviceMgr() DeviceMgrModel {
	return DeviceMgrModel{}
}

func (d *DeviceMgrModel) Show() {
	d.visible = true
	d.cursor = 0
	d.confirm = false
	d.status = ""
	d.loading = true
	d.devices = nil
}

func (d *DeviceMgrModel) Hide() {
	d.visible = false
	d.confirm = false
	d.status = ""
}

func (d *DeviceMgrModel) IsVisible() bool {
	return d.visible
}

// SetDevices populates the list from a device_list response and clears the
// loading indicator.
func (d *DeviceMgrModel) SetDevices(devices []protocol.DeviceInfo) {
	d.devices = devices
	d.loading = false
	if d.cursor >= len(devices) {
		d.cursor = 0
	}
}

// SetStatus updates the inline status/error message (e.g., after a
// device_revoke_result arrives).
func (d *DeviceMgrModel) SetStatus(msg string) {
	d.status = msg
}

func (d DeviceMgrModel) Update(msg tea.KeyMsg) (DeviceMgrModel, tea.Cmd) {
	// Confirmation prompt
	if d.confirm {
		switch msg.String() {
		case "y":
			target := d.devices[d.cursor].DeviceID
			d.confirm = false
			d.status = "revoking " + target + "..."
			return d, func() tea.Msg { return DeviceMgrRevokeMsg{DeviceID: target} }
		case "n", "esc":
			d.confirm = false
			return d, nil
		}
		return d, nil
	}

	switch msg.String() {
	case "esc", "q":
		d.Hide()
		return d, nil
	case "r":
		d.loading = true
		d.devices = nil
		d.status = ""
		return d, func() tea.Msg { return DeviceMgrRefreshMsg{} }
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.devices)-1 {
			d.cursor++
		}
	case "enter", "x":
		if d.cursor >= len(d.devices) {
			return d, nil
		}
		device := d.devices[d.cursor]
		if device.Current {
			d.status = "Cannot revoke your current device from here. Close the app or use Retire Account."
			return d, nil
		}
		if device.Revoked {
			d.status = "Device is already revoked."
			return d, nil
		}
		d.confirm = true
		return d, nil
	}
	return d, nil
}

// HandleMouse maps click coordinates onto rows in the device list and
// updates the cursor. A click doesn't trigger the revoke action (requires
// keyboard Enter/x + y/n confirm) — this matches the "mouse selects,
// keyboard acts" convention used elsewhere in the TUI.
//
// Layout (see View): border(1) + padding(1) + header(1) + blank(1)
// + 3 desc lines + blank = content starts at Y=8, each device uses 2 rows.
func (d DeviceMgrModel) HandleMouse(msg tea.MouseMsg) (DeviceMgrModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return d, nil
	}
	// Loading / empty / confirm states have no device rows to hit
	if d.loading || d.confirm || len(d.devices) == 0 {
		return d, nil
	}

	// First device starts at Y=8 (after border, padding, header, blank,
	// 3 desc lines, blank). Each device takes 2 rows (name + meta).
	const firstDeviceY = 8
	relY := msg.Y - firstDeviceY
	if relY < 0 {
		return d, nil
	}
	idx := relY / 2
	if idx >= 0 && idx < len(d.devices) {
		d.cursor = idx
	}
	return d, nil
}

func (d DeviceMgrModel) View(width int) string {
	if !d.visible {
		return ""
	}

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Your Devices"))
	b.WriteString("\n\n")

	if d.loading {
		b.WriteString("  " + helpDescStyle.Render("Loading...") + "\n\n")
		b.WriteString(helpDescStyle.Render("  Esc=close"))
		return dialogStyle.Width(width - 4).Render(b.String())
	}

	if len(d.devices) == 0 {
		b.WriteString("  " + helpDescStyle.Render("No devices registered.") + "\n\n")
		b.WriteString(helpDescStyle.Render("  r=refresh  Esc=close"))
		return dialogStyle.Width(width - 4).Render(b.String())
	}

	b.WriteString("  " + helpDescStyle.Render("Devices registered to your account on this server.") + "\n")
	b.WriteString("  " + helpDescStyle.Render("Revoking a device stops it from connecting. Your account") + "\n")
	b.WriteString("  " + helpDescStyle.Render("remains active on other devices.") + "\n\n")

	for i, dev := range d.devices {
		marker := " "
		if i == d.cursor {
			marker = ">"
		}

		tags := ""
		if dev.Current {
			tags += " " + checkStyle.Render("(current)")
		}
		if dev.Revoked {
			tags += " " + errorStyle.Render("(revoked)")
		}

		line := fmt.Sprintf(" %s %s%s", marker, dev.DeviceID, tags)
		if i == d.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")

		meta := fmt.Sprintf("      created: %s", dev.CreatedAt)
		if dev.LastSyncedAt != "" {
			meta += fmt.Sprintf("   last sync: %s", dev.LastSyncedAt)
		}
		b.WriteString(helpDescStyle.Render(meta) + "\n")
	}

	b.WriteString("\n")

	if d.confirm {
		target := d.devices[d.cursor].DeviceID
		b.WriteString(errorStyle.Render("  Revoke "+target+"? This cannot be undone.") + "\n")
		b.WriteString(helpDescStyle.Render("  y=confirm  n=cancel") + "\n")
	} else if d.status != "" {
		b.WriteString("  " + helpDescStyle.Render(d.status) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  ↑/↓=navigate  Enter/x=revoke  r=refresh  Esc=close"))
	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render("  Note: device revocation does NOT protect against key extraction."))
	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  If you suspect your key itself was stolen, retire your account instead."))

	return dialogStyle.Width(width - 4).Render(b.String())
}
