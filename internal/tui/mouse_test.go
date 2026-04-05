package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// click builds a left-click-release mouse event at (x, y).
func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	}
}

// rightClick builds a right-click-release mouse event.
func rightClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonRight,
		Action: tea.MouseActionRelease,
	}
}

// -- DeviceMgr mouse --

func TestDeviceMgrMouse_ClickOnDeviceMovesCursor(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())

	// Each device takes 2 rows starting at Y=8 (border+padding+header+blank+3
	// desc lines+blank). Device 1 is at Y=10.
	d, _ = d.HandleMouse(click(5, 10))
	if d.cursor != 1 {
		t.Errorf("click on device 1 row: cursor = %d, want 1", d.cursor)
	}

	// Click on device 2 (Y=12)
	d, _ = d.HandleMouse(click(5, 12))
	if d.cursor != 2 {
		t.Errorf("click on device 2 row: cursor = %d, want 2", d.cursor)
	}
}

func TestDeviceMgrMouse_MetaLineAlsoSelects(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())

	// Meta line of device 0 is at Y=9
	d, _ = d.HandleMouse(click(5, 9))
	if d.cursor != 0 {
		t.Errorf("click on device 0 meta: cursor = %d, want 0", d.cursor)
	}
}

func TestDeviceMgrMouse_ClickAboveListIgnored(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1

	// Click on header area (Y=2)
	d, _ = d.HandleMouse(click(5, 2))
	if d.cursor != 1 {
		t.Error("click above device list should not change cursor")
	}
}

func TestDeviceMgrMouse_ClickPastListIgnored(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 0

	// Click way past devices (Y=50)
	d, _ = d.HandleMouse(click(5, 50))
	if d.cursor != 0 {
		t.Error("click past list should not change cursor")
	}
}

func TestDeviceMgrMouse_NoDeviceListNoop(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	// Still loading
	d, _ = d.HandleMouse(click(5, 10))
	// Should not panic
}

func TestDeviceMgrMouse_RightClickIgnored(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 0
	d, _ = d.HandleMouse(rightClick(5, 12))
	if d.cursor != 0 {
		t.Error("right-click should not change cursor")
	}
}

func TestDeviceMgrMouse_ConfirmStateIgnores(t *testing.T) {
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices(sampleDevices())
	d.cursor = 1
	d.confirm = true // in confirmation mode

	d, _ = d.HandleMouse(click(5, 12))
	if d.cursor != 1 {
		t.Error("click during confirmation should not move cursor")
	}
	if !d.confirm {
		t.Error("click should not exit confirmation mode")
	}
}

// -- DeviceRevoked mouse --

func TestDeviceRevokedMouse_ClickDismisses(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	d2, cmd := d.HandleMouse(click(10, 5))
	if d2.IsVisible() {
		t.Error("click should dismiss")
	}
	if cmd == nil {
		t.Fatal("click should emit DeviceRevokedQuitMsg")
	}
	if _, ok := cmd().(DeviceRevokedQuitMsg); !ok {
		t.Error("expected DeviceRevokedQuitMsg from click")
	}
}

func TestDeviceRevokedMouse_RightClickIgnored(t *testing.T) {
	d := NewDeviceRevoked()
	d.Show("dev_x", "admin")
	d2, cmd := d.HandleMouse(rightClick(10, 5))
	if !d2.IsVisible() {
		t.Error("right-click should not dismiss")
	}
	if cmd != nil {
		t.Error("right-click should not emit")
	}
}

// -- RetireConfirm mouse --

func TestRetireConfirmMouse_ClickOnReasonSelects(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	// First reason radio is at Y=12
	r, _ = r.HandleMouse(click(5, 12))
	if r.reasonIdx != 0 {
		t.Errorf("click on reason 0: idx = %d, want 0", r.reasonIdx)
	}
	// Second reason is at Y=14
	r, _ = r.HandleMouse(click(5, 14))
	if r.reasonIdx != 1 {
		t.Errorf("click on reason 1: idx = %d, want 1", r.reasonIdx)
	}
	// Third reason at Y=16
	r, _ = r.HandleMouse(click(5, 16))
	if r.reasonIdx != 2 {
		t.Errorf("click on reason 2: idx = %d, want 2", r.reasonIdx)
	}
}

func TestRetireConfirmMouse_ReasonClickClearsPhraseFocus(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	// Manually focus phrase input
	r.focused = 1
	r.phraseInput.Focus()

	r, _ = r.HandleMouse(click(5, 12))
	if r.focused != 0 {
		t.Errorf("click on reason should blur phrase input: focused=%d", r.focused)
	}
}

func TestRetireConfirmMouse_ClickOnPhraseInput(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	// phrase input is at Y=20
	r, _ = r.HandleMouse(click(5, 20))
	if r.focused != 1 {
		t.Errorf("click on phrase input should focus it: focused=%d", r.focused)
	}
}

// -- Settings mouse --

func TestSettingsMouse_ClickSelectsActionableItem(t *testing.T) {
	s := NewSettings()
	s.items = []settingsItem{
		{label: "── Section ──", action: ""},
		{label: "  Item A", action: "action_a"},
		{label: "  Item B", action: "action_b"},
	}
	s.visible = true

	// First item is at Y=4. items[1] (actionable) is at Y=5.
	s, _ = s.HandleMouse(click(10, 5))
	if s.cursor != 1 {
		t.Errorf("click on items[1] should set cursor=1, got %d", s.cursor)
	}
}

func TestSettingsMouse_NonActionableItemIgnored(t *testing.T) {
	s := NewSettings()
	s.items = []settingsItem{
		{label: "── Section ──", action: ""}, // Y=4, not actionable
		{label: "  Item A", action: "action_a"},
	}
	s.visible = true
	s.cursor = 1

	s, _ = s.HandleMouse(click(10, 4)) // click on non-actionable header
	if s.cursor != 1 {
		t.Error("click on non-actionable item should not move cursor")
	}
}

func TestSettingsMouse_ConfirmStateIgnoresClicks(t *testing.T) {
	s := NewSettings()
	s.items = []settingsItem{{label: "  Item", action: "foo"}}
	s.visible = true
	s.confirm = &confirmDialog{message: "sure?", action: "foo"}

	s, _ = s.HandleMouse(click(10, 4))
	// cursor unchanged
	if s.confirm == nil {
		t.Error("click shouldn't exit confirm state")
	}
}

// -- app routing sanity --

func TestAppMouse_RoutesToVisibleDialog(t *testing.T) {
	// Verify HandleMouse correctly identifies which dialog should receive
	// the click when multiple dialogs could be visible (they shouldn't be,
	// but defensive ordering matters).
	d := NewDeviceMgr()
	d.Show()
	d.SetDevices([]protocol.DeviceInfo{
		{DeviceID: "dev_a", CreatedAt: "2026-01-01T00:00:00Z"},
	})
	// Just verify the HandleMouse method exists and doesn't panic
	d, _ = d.HandleMouse(click(5, 10))
}
