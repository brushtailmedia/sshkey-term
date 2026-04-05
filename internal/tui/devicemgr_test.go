package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func sampleDevices() []protocol.DeviceInfo {
	return []protocol.DeviceInfo{
		{DeviceID: "dev_laptop", CreatedAt: "2026-01-01T00:00:00Z", LastSyncedAt: "2026-04-05T12:00:00Z", Current: true},
		{DeviceID: "dev_phone", CreatedAt: "2026-02-01T00:00:00Z", LastSyncedAt: "2026-04-01T08:00:00Z"},
		{DeviceID: "dev_old", CreatedAt: "2025-06-01T00:00:00Z", LastSyncedAt: "2025-12-01T00:00:00Z", Revoked: true},
	}
}

func TestDeviceMgr_InitialStateHidden(t *testing.T) {
	d := NewDeviceMgr()
	if d.IsVisible() {
		t.Error("should start hidden")
	}
}

func TestDeviceMgr_ShowStartsLoading(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	if !d.IsVisible() {
		t.Error("should be visible")
	}
	if !d.loading {
		t.Error("should be in loading state until SetDevices called")
	}
}

func TestDeviceMgr_SetDevicesClearsLoading(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	if d.loading {
		t.Error("loading should be false after SetDevices")
	}
	if len(d.devices) != 3 {
		t.Errorf("devices = %d, want 3", len(d.devices))
	}
}

func TestDeviceMgr_Navigation(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())

	if d.cursor != 0 {
		t.Errorf("cursor = %d, want 0", d.cursor)
	}

	d, _ = d.Update(keyMsg("down"))
	if d.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", d.cursor)
	}
	d, _ = d.Update(keyMsg("down"))
	if d.cursor != 2 {
		t.Errorf("after down x2: cursor = %d, want 2", d.cursor)
	}
	d, _ = d.Update(keyMsg("down"))
	if d.cursor != 2 {
		t.Errorf("cursor should clamp at max: got %d", d.cursor)
	}

	d, _ = d.Update(keyMsg("up"))
	if d.cursor != 1 {
		t.Errorf("after up: cursor = %d, want 1", d.cursor)
	}
}

func TestDeviceMgr_EscHides(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d, _ = d.Update(keyMsg("esc"))
	if d.IsVisible() {
		t.Error("esc should hide")
	}
}

func TestDeviceMgr_QHides(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if d.IsVisible() {
		t.Error("q should hide")
	}
}

func TestDeviceMgr_RefreshEmitsMsg(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("r should emit refresh command")
	}
	if _, ok := cmd().(DeviceMgrRefreshMsg); !ok {
		t.Error("expected DeviceMgrRefreshMsg")
	}
	if !d.loading {
		t.Error("should be in loading state after refresh")
	}
}

func TestDeviceMgr_EnterOnCurrentDevice(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	// cursor is 0 = current device (dev_laptop)
	d, cmd := d.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter on current device should not emit")
	}
	if d.confirm {
		t.Error("should not enter confirm mode for current device")
	}
	if !strings.Contains(d.status, "current device") {
		t.Errorf("should set status about current device, got: %q", d.status)
	}
}

func TestDeviceMgr_EnterOnRevokedDevice(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 2 // dev_old (revoked)
	d, cmd := d.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter on revoked device should not emit")
	}
	if d.confirm {
		t.Error("should not enter confirm mode for already-revoked device")
	}
}

func TestDeviceMgr_EnterOnValidDeviceShowsConfirm(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1 // dev_phone
	d, cmd := d.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter should not emit yet (confirm required)")
	}
	if !d.confirm {
		t.Error("should enter confirm mode")
	}
}

func TestDeviceMgr_ConfirmYEmitsRevoke(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1 // dev_phone
	d, _ = d.Update(keyMsg("enter"))
	if !d.confirm {
		t.Fatal("precondition: should be confirming")
	}
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should emit DeviceMgrRevokeMsg")
	}
	msg, ok := cmd().(DeviceMgrRevokeMsg)
	if !ok {
		t.Fatalf("expected DeviceMgrRevokeMsg, got %T", cmd())
	}
	if msg.DeviceID != "dev_phone" {
		t.Errorf("revoke target = %q, want dev_phone", msg.DeviceID)
	}
	if d.confirm {
		t.Error("should exit confirm mode after submission")
	}
}

func TestDeviceMgr_ConfirmNCancels(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1
	d, _ = d.Update(keyMsg("enter"))
	if !d.confirm {
		t.Fatal("should be confirming")
	}
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Error("n should NOT emit revoke")
	}
	if d.confirm {
		t.Error("should exit confirm mode on n")
	}
}

func TestDeviceMgr_SetStatus(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetStatus("✓ revoked")
	if d.status != "✓ revoked" {
		t.Errorf("status = %q", d.status)
	}
}

func TestDeviceMgr_ViewLoadingState(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	view := d.View(80)
	if !strings.Contains(view, "Loading") {
		t.Error("loading view should say 'Loading'")
	}
}

func TestDeviceMgr_ViewEmpty(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(nil)
	view := d.View(80)
	if !strings.Contains(view, "No devices") {
		t.Error("empty view should say 'No devices'")
	}
}

func TestDeviceMgr_ViewShowsDevices(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	view := d.View(80)

	for _, name := range []string{"dev_laptop", "dev_phone", "dev_old", "current", "revoked"} {
		if !strings.Contains(view, name) {
			t.Errorf("view missing %q", name)
		}
	}
	// Safety warning
	if !strings.Contains(view, "does NOT protect against key extraction") {
		t.Error("view should warn about key extraction")
	}
}

func TestDeviceMgr_ViewEmptyWhenHidden(t *testing.T) {
	d := NewDeviceMgr()
	if view := d.View(80); view != "" {
		t.Errorf("hidden view should be empty, got: %q", view)
	}
}

func TestDeviceMgr_ConfirmationShowsTargetInView(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1
	d, _ = d.Update(keyMsg("enter"))

	view := d.View(80)
	if !strings.Contains(view, "Revoke dev_phone") {
		t.Errorf("confirmation view should mention target device, got:\n%s", view)
	}
}
