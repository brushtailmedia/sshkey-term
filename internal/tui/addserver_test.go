package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestAddServer_InitialState(t *testing.T) {
	a := NewAddServer()
	if a.IsVisible() {
		t.Error("fresh model should not be visible")
	}
	a.Show()
	if !a.IsVisible() {
		t.Error("after Show(), should be visible")
	}
	if a.mode != addServerForm {
		t.Errorf("initial mode = %d, want addServerForm(0)", a.mode)
	}
	if a.focused != 0 {
		t.Errorf("initial focus = %d, want 0 (name)", a.focused)
	}
}

func TestAddServer_TabCyclesFields(t *testing.T) {
	a := NewAddServer()
	a.Show()

	for want := 1; want < 4; want++ {
		a, _ = a.Update(keyMsg("tab"))
		if a.focused != want {
			t.Errorf("after tab: focused = %d, want %d", a.focused, want)
			break
		}
	}
	// Wrap around
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != 0 {
		t.Errorf("focus should wrap to 0, got %d", a.focused)
	}
}

func TestAddServer_EscHides(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("esc"))
	if a.IsVisible() {
		t.Error("Esc should hide the dialog")
	}
}

func TestAddServer_CtrlGEntersGenerateMode(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerGenerate {
		t.Errorf("mode after Ctrl+G = %d, want addServerGenerate", a.mode)
	}
	if a.genFocused != 0 {
		t.Errorf("genFocused = %d, want 0 (path)", a.genFocused)
	}
}

func TestAddServer_GenerateEscReturnsToForm(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerGenerate {
		t.Fatal("precondition: should be in generate mode")
	}
	a, _ = a.Update(keyMsg("esc"))
	if a.mode != addServerForm {
		t.Errorf("Esc in generate mode should return to form, got mode=%d", a.mode)
	}
	if !a.IsVisible() {
		t.Error("Esc in generate mode should NOT hide the whole dialog")
	}
}

func TestAddServer_GenerateTabCyclesFields(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))
	// 3 fields: path, pass, confirm
	for want := 1; want < 3; want++ {
		a, _ = a.Update(keyMsg("tab"))
		if a.genFocused != want {
			t.Errorf("genFocused = %d, want %d", a.genFocused, want)
		}
	}
	a, _ = a.Update(keyMsg("tab"))
	if a.genFocused != 0 {
		t.Errorf("genFocused should wrap to 0, got %d", a.genFocused)
	}
}

func TestAddServer_GenerateEmptyPathRejected(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))
	// Clear default path
	a.genInputs[0].SetValue("")
	a, _ = a.Update(keyMsg("enter"))
	if a.mode != addServerGenerate {
		t.Error("should stay in generate mode with error")
	}
	if a.genErr == "" {
		t.Error("empty path should set error")
	}
	if !strings.Contains(a.genErr, "required") {
		t.Errorf("error should mention required: %q", a.genErr)
	}
}

func TestAddServer_GeneratePassphraseMismatch(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))

	dir := t.TempDir()
	a.genInputs[0].SetValue(filepath.Join(dir, "newkey"))
	a.genInputs[1].SetValue("pass1")
	a.genInputs[2].SetValue("pass2-different")

	a, _ = a.Update(keyMsg("enter"))
	if a.genErr == "" || !strings.Contains(a.genErr, "don't match") {
		t.Errorf("passphrase mismatch should produce matching error, got: %q", a.genErr)
	}
}

func TestAddServer_GenerateExistingFileRejected(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))

	dir := t.TempDir()
	existingPath := filepath.Join(dir, "existing")
	os.WriteFile(existingPath, []byte("already here"), 0600)

	a.genInputs[0].SetValue(existingPath)
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")

	a, _ = a.Update(keyMsg("enter"))
	if a.genErr == "" || !strings.Contains(a.genErr, "already exists") {
		t.Errorf("existing file should be rejected, got: %q", a.genErr)
	}
}

func TestAddServer_GenerateSuccessReturnsToForm(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))

	dir := t.TempDir()
	newPath := filepath.Join(dir, "generated_key")
	a.genInputs[0].SetValue(newPath)
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")

	a, _ = a.Update(keyMsg("enter"))

	if a.mode != addServerForm {
		t.Errorf("after successful generation, mode = %d, want addServerForm", a.mode)
	}
	if a.focused != 3 {
		t.Errorf("focus should be on key path field (3), got %d", a.focused)
	}
	if a.inputs[3].Value() != newPath {
		t.Errorf("key path input = %q, want %q", a.inputs[3].Value(), newPath)
	}
	if a.genNotice == "" {
		t.Error("success notice should be set")
	}
	if !strings.Contains(a.genNotice, "back it up") {
		t.Errorf("notice should mention backup, got: %q", a.genNotice)
	}

	// Key file should exist
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("key file should exist: %v", err)
	}
	if _, err := os.Stat(newPath + ".pub"); err != nil {
		t.Errorf("public key file should exist: %v", err)
	}
}

func TestAddServer_SubmitRequiresHost(t *testing.T) {
	a := NewAddServer()
	a.Show()
	// Leave host empty
	a, cmd := a.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter with empty host should not submit")
	}
	if !a.IsVisible() {
		t.Error("dialog should remain visible")
	}
}

func TestAddServer_SubmitValidReturnsMsg(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[0].SetValue("Test Server")
	a.inputs[1].SetValue("chat.example.com")
	// port default "2222" already set
	a.inputs[3].SetValue("~/.ssh/id_ed25519")

	a, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("valid form should submit")
	}
	msg := cmd()
	addMsg, ok := msg.(AddServerMsg)
	if !ok {
		t.Fatalf("expected AddServerMsg, got %T", msg)
	}
	if addMsg.Name != "Test Server" {
		t.Errorf("Name = %q", addMsg.Name)
	}
	if addMsg.Host != "chat.example.com" {
		t.Errorf("Host = %q", addMsg.Host)
	}
	if addMsg.Port != 2222 {
		t.Errorf("Port = %d, want 2222", addMsg.Port)
	}
	if addMsg.Key != "~/.ssh/id_ed25519" {
		t.Errorf("Key = %q", addMsg.Key)
	}
	if a.IsVisible() {
		t.Error("should hide after successful submit")
	}
}

func TestAddServer_SubmitDefaultsName(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a.inputs[3].SetValue("~/.ssh/id_ed25519")

	_, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("should submit")
	}
	addMsg := cmd().(AddServerMsg)
	if addMsg.Name != "chat.example.com" {
		t.Errorf("Name should default to Host, got %q", addMsg.Name)
	}
}

func TestAddServer_SubmitDefaultsKey(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	// Leave key blank

	_, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("should submit")
	}
	addMsg := cmd().(AddServerMsg)
	if addMsg.Key != "~/.ssh/id_ed25519" {
		t.Errorf("Key should default to ~/.ssh/id_ed25519, got %q", addMsg.Key)
	}
}

func TestAddServer_KeyListStartY_NoNotice(t *testing.T) {
	a := NewAddServer()
	a.Show()
	// No notice, no keys scanned (depends on environment — use explicit 0)
	a.scannedKeys = nil
	a.genNotice = ""

	// With no keys, keyListStartY returns the base (form takes rows 0..11)
	y := a.keyListStartY()
	if y != 12 {
		t.Errorf("keyListStartY with no keys = %d, want 12", y)
	}
}

func TestAddServer_KeyListStartY_WithKeys(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.scannedKeys = []keyEntry{{Path: "/tmp/k1", Type: "ed25519"}}
	a.genNotice = ""

	// With keys, header adds 2 lines (label + blank) → first key at 14
	y := a.keyListStartY()
	if y != 14 {
		t.Errorf("keyListStartY with 1 key, no notice = %d, want 14", y)
	}
}

func TestAddServer_KeyListStartY_WithNoticeAndKeys(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.scannedKeys = []keyEntry{{Path: "/tmp/k1", Type: "ed25519"}}
	a.genNotice = "✓ Key generated — back it up"

	// Notice adds 2 lines (line + blank) + keys header 2 lines → 12 + 2 + 2 = 16
	y := a.keyListStartY()
	if y != 16 {
		t.Errorf("keyListStartY with notice + keys = %d, want 16", y)
	}
}

func TestAddServer_HandleMouse_ClickOnField(t *testing.T) {
	a := NewAddServer()
	a.Show()
	if a.focused != 0 {
		t.Fatalf("precondition: focused=%d", a.focused)
	}

	// Click on the Host field (Y=6 per layout comment)
	a, _ = a.HandleMouse(tea.MouseMsg{
		X:      10,
		Y:      6,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	})
	if a.focused != 1 {
		t.Errorf("click on Host row should focus Host (1), got %d", a.focused)
	}
}

func TestAddServer_HandleMouse_ClickOnKeyEntry(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.scannedKeys = []keyEntry{
		{Path: "/home/me/.ssh/id_ed25519", Type: "ed25519"},
		{Path: "/home/me/.ssh/work_key", Type: "ed25519"},
	}
	a.genNotice = ""

	// First key is at keyListStartY() = 14
	startY := a.keyListStartY()
	a, _ = a.HandleMouse(tea.MouseMsg{
		X:      10,
		Y:      startY + 1, // click second key
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	})
	if a.inputs[3].Value() != "/home/me/.ssh/work_key" {
		t.Errorf("clicking second key should fill path input, got: %q", a.inputs[3].Value())
	}
	if a.focused != 3 {
		t.Errorf("focus should be on key path input after click (3), got %d", a.focused)
	}
}

func TestAddServer_HandleMouse_IgnoresNonLeftClick(t *testing.T) {
	a := NewAddServer()
	a.Show()
	origFocus := a.focused

	// Right-click should be ignored
	a, _ = a.HandleMouse(tea.MouseMsg{
		X:      10,
		Y:      6,
		Button: tea.MouseButtonRight,
		Action: tea.MouseActionRelease,
	})
	if a.focused != origFocus {
		t.Errorf("right-click should not change focus (was %d, now %d)", origFocus, a.focused)
	}
}

func TestAddServer_HandleMouse_IgnoresInGenerateMode(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerGenerate {
		t.Fatal("should be in generate mode")
	}

	// Click should be ignored in generate mode
	a, _ = a.HandleMouse(tea.MouseMsg{
		X:      10,
		Y:      6,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	})
	if a.mode != addServerGenerate {
		t.Error("mouse click should not change mode in generate sub-view")
	}
}
