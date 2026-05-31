package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// NewDeviceAlertModel is a non-fatal modal shown when a brand-new device
// registers under this identity (shadow-device transparency, Tier 1 — see
// sshkey-chat/docs/planning/open/device-identity-transparency.md). Unlike the
// device-revoked modal it does NOT quit: it warns and points the user at the
// device manager to revoke if the device is not theirs. Multiple alerts queue
// and are shown one at a time.
type NewDeviceAlertModel struct {
	pending []newDeviceEntry
}

type newDeviceEntry struct {
	deviceID  string
	createdAt string
}

func NewNewDeviceAlert() NewDeviceAlertModel {
	return NewDeviceAlertModel{}
}

// Show enqueues an alert for deviceID. A device_id already queued is ignored
// so a live push plus a connect-reconcile for the same device don't stack.
func (d *NewDeviceAlertModel) Show(deviceID, createdAt string) {
	if deviceID == "" {
		return
	}
	for _, e := range d.pending {
		if e.deviceID == deviceID {
			return
		}
	}
	d.pending = append(d.pending, newDeviceEntry{deviceID: deviceID, createdAt: createdAt})
}

// Hide dismisses the current alert, advancing to the next queued one (if any).
func (d *NewDeviceAlertModel) Hide() {
	if len(d.pending) > 0 {
		d.pending = d.pending[1:]
	}
}

func (d *NewDeviceAlertModel) IsVisible() bool {
	return len(d.pending) > 0
}

func (d NewDeviceAlertModel) Update(msg tea.KeyMsg) (NewDeviceAlertModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "q", "esc":
		d.Hide()
	}
	return d, nil
}

// HandleMouse lets the user click anywhere on the modal to dismiss it.
func (d NewDeviceAlertModel) HandleMouse(msg tea.MouseMsg) (NewDeviceAlertModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return d, nil
	}
	d.Hide()
	return d, nil
}

func (d NewDeviceAlertModel) View(width int) string {
	if len(d.pending) == 0 {
		return ""
	}
	e := d.pending[0]

	var b strings.Builder
	b.WriteString(errorStyle.Render(" ⚠ New device on your account"))
	b.WriteString("\n\n")
	b.WriteString("  A new device just started using your identity key.\n\n")
	if e.deviceID != "" {
		b.WriteString("  Device ID: " + helpDescStyle.Render(e.deviceID) + "\n")
	}
	if e.createdAt != "" {
		b.WriteString("  First seen: " + helpDescStyle.Render(e.createdAt) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  If this was you, you can dismiss this.\n")
	b.WriteString("  If NOT, your key may be compromised — open the device\n")
	b.WriteString("  manager to revoke it, then rotate your key.\n\n")
	if len(d.pending) > 1 {
		b.WriteString(helpDescStyle.Render(fmt.Sprintf("  (+%d more)", len(d.pending)-1)))
		b.WriteString("\n\n")
	}
	b.WriteString(helpDescStyle.Render("  Enter / q / Esc = dismiss"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
