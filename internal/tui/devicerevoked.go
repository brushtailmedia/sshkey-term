package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DeviceRevokedModel is the modal shown when the server reports the current
// device has been revoked (via sshkey-ctl revoke-device). The device can no
// longer connect to this server; the user's account may still be active on
// other devices.
type DeviceRevokedModel struct {
	visible  bool
	deviceID string
	reason   string
}

// DeviceRevokedQuitMsg is emitted when the user dismisses the dialog. The
// app should close the client (to stop the reconnect loop) and quit.
type DeviceRevokedQuitMsg struct{}

func NewDeviceRevoked() DeviceRevokedModel {
	return DeviceRevokedModel{}
}

func (d *DeviceRevokedModel) Show(deviceID, reason string) {
	d.visible = true
	d.deviceID = deviceID
	d.reason = reason
}

func (d *DeviceRevokedModel) Hide() {
	d.visible = false
}

func (d *DeviceRevokedModel) IsVisible() bool {
	return d.visible
}

func (d DeviceRevokedModel) Update(msg tea.KeyMsg) (DeviceRevokedModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "q", "esc":
		d.Hide()
		return d, func() tea.Msg { return DeviceRevokedQuitMsg{} }
	}
	return d, nil
}

func (d DeviceRevokedModel) View(width int) string {
	if !d.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(errorStyle.Render(" ⚠ Device Revoked"))
	b.WriteString("\n\n")
	b.WriteString("  This device has been revoked by admin action.\n\n")
	if d.deviceID != "" {
		b.WriteString("  Device ID: " + helpDescStyle.Render(d.deviceID) + "\n")
	}
	if d.reason != "" {
		b.WriteString("  Reason: " + helpDescStyle.Render(d.reason) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  Your account may still be active on other devices.\n")
	b.WriteString("  Contact your server admin if this was unexpected.\n\n")
	b.WriteString(errorStyle.Render("  Note: ") + helpDescStyle.Render("Revoking a device does NOT protect against key extraction.\n  If you suspect the key itself was compromised, the admin should\n  retire your account instead."))
	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render("  Enter / q / Esc = exit"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
