package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDeviceRevoked_InitialHidden(t *testing.T) {
	d := NewDeviceRevoked()
	if d.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestDeviceRevoked_Show(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_test123", "admin_action")
	if !d.IsVisible() {
		t.Error("should be visible after Show")
	}
	if d.deviceID != "dev_test123" || d.reason != "admin_action" {
		t.Errorf("fields not set: deviceID=%q reason=%q", d.deviceID, d.reason)
	}
}

func TestDeviceRevoked_EnterDismisses(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	d2, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d2.IsVisible() {
		t.Error("should hide after Enter")
	}
	if cmd == nil {
		t.Fatal("Enter should emit DeviceRevokedQuitMsg")
	}
	if _, ok := cmd().(DeviceRevokedQuitMsg); !ok {
		t.Error("expected DeviceRevokedQuitMsg from Enter")
	}
}

func TestDeviceRevoked_QDismisses(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	d2, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if d2.IsVisible() {
		t.Error("should hide after q")
	}
	if cmd == nil {
		t.Fatal("q should emit DeviceRevokedQuitMsg")
	}
}

func TestDeviceRevoked_EscDismisses(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	d2, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d2.IsVisible() {
		t.Error("should hide after Esc")
	}
	if cmd == nil {
		t.Fatal("Esc should emit DeviceRevokedQuitMsg")
	}
}

func TestDeviceRevoked_OtherKeysIgnored(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	// Random key should not dismiss
	d2, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !d2.IsVisible() {
		t.Error("should remain visible for non-dismiss keys")
	}
	if cmd != nil {
		t.Error("non-dismiss keys should not emit")
	}
}

func TestDeviceRevoked_View_ContainsKeyElements(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_abc123", "admin_action")
	view := d.View(80)

	expectedFragments := []string{
		"Device Revoked",
		"revoked by admin action",
		"dev_abc123",
		"admin_action",
		"retire your account instead",
	}
	for _, frag := range expectedFragments {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing expected fragment: %q", frag)
		}
	}
}

func TestDeviceRevoked_ViewHandlesEmptyFields(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("", "")
	// Should not crash
	view := d.View(80)
	if !strings.Contains(view, "Device Revoked") {
		t.Error("view should contain heading")
	}
}

func TestDeviceRevoked_ViewEmptyWhenHidden(t *testing.T) {
	d := NewDeviceRevoked()
	if got := d.View(80); got != "" {
		t.Errorf("hidden view should be empty, got: %q", got)
	}
}
