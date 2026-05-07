package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// withPassthroughKeyCopy swaps keyCopyFn for a no-op passthrough
// (returns the source path unchanged) for the duration of a test.
// Used by submit-flow tests so they don't need real key files on
// disk to exercise the form-validation + AddServerMsg-emission path.
func withPassthroughKeyCopy(t *testing.T) {
	t.Helper()
	prev := keyCopyFn
	keyCopyFn = func(src, host string) (string, error) { return src, nil }
	t.Cleanup(func() { keyCopyFn = prev })
}

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
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerGenerate {
		t.Errorf("mode after Ctrl+G = %d, want addServerGenerate", a.mode)
	}
	if a.genFocused != 0 {
		t.Errorf("genFocused = %d, want 0 (path)", a.genFocused)
	}
}

func TestAddServer_CtrlGRequiresHost(t *testing.T) {
	a := NewAddServer()
	a.Show()
	// Host left empty — Ctrl+G should refuse to enter generate mode.
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerForm {
		t.Errorf("Ctrl+G with empty host should stay in form mode, got %d", a.mode)
	}
	if a.formErr == "" {
		t.Error("Ctrl+G with empty host should set formErr")
	}
	if !strings.Contains(a.formErr, "hostname") {
		t.Errorf("formErr should mention hostname, got: %q", a.formErr)
	}
	if a.focused != 1 {
		t.Errorf("focus should jump to host field (1), got %d", a.focused)
	}
}

func TestAddServer_CtrlGRejectsWhitespaceOnlyHost(t *testing.T) {
	a := NewAddServer()
	a.Show()
	// Whitespace-only host — same treatment as empty.
	a.inputs[1].SetValue("   ")
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerForm {
		t.Errorf("Ctrl+G with whitespace host should stay in form, got mode=%d", a.mode)
	}
	if a.formErr == "" {
		t.Error("whitespace host should set formErr")
	}
}

func TestAddServer_GenerateEscReturnsToForm(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
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
	a.inputs[1].SetValue("chat.example.com")
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
	a.inputs[1].SetValue("chat.example.com")
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
	a.inputs[1].SetValue("chat.example.com")
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
	a.inputs[1].SetValue("chat.example.com")
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
	a.inputs[1].SetValue("chat.example.com")
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
	withPassthroughKeyCopy(t)
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
	withPassthroughKeyCopy(t)
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
	withPassthroughKeyCopy(t)
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
	a.inputs[1].SetValue("chat.example.com")
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

// --- Pass A: state-leak fixes (#1, #2, #6, #7) ---

func TestAddServer_HideClearsPassphraseFields(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("ctrl+g"))
	a.genInputs[1].SetValue("secretpass")
	a.genInputs[2].SetValue("secretpass")

	a.Hide()

	if a.genInputs[1].Value() != "" {
		t.Errorf("passphrase field should be cleared on Hide(), got %q", a.genInputs[1].Value())
	}
	if a.genInputs[2].Value() != "" {
		t.Errorf("confirm field should be cleared on Hide(), got %q", a.genInputs[2].Value())
	}
}

func TestAddServer_ShowClearsPassphraseFields(t *testing.T) {
	a := NewAddServer()
	// Pre-populate genInputs to simulate residue from before Show()
	a.genInputs[1].SetValue("leftover")
	a.genInputs[2].SetValue("leftover")

	a.Show()

	if a.genInputs[1].Value() != "" {
		t.Errorf("passphrase field should be cleared on Show(), got %q", a.genInputs[1].Value())
	}
	if a.genInputs[2].Value() != "" {
		t.Errorf("confirm field should be cleared on Show(), got %q", a.genInputs[2].Value())
	}
}

func TestAddServer_CtrlGClearsPassphraseFields(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.inputs[1].SetValue("chat.example.com")

	// Open generate, type a passphrase, Esc back to form
	a, _ = a.Update(keyMsg("ctrl+g"))
	a.genInputs[1].SetValue("typed-then-bailed")
	a.genInputs[2].SetValue("typed-then-bailed")
	a, _ = a.Update(keyMsg("esc"))
	if a.mode != addServerForm {
		t.Fatal("precondition: Esc should return to form")
	}

	// Re-enter generate — passphrase fields should be fresh
	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.genInputs[1].Value() != "" {
		t.Errorf("re-entering generate should clear passphrase, got %q", a.genInputs[1].Value())
	}
	if a.genInputs[2].Value() != "" {
		t.Errorf("re-entering generate should clear confirm, got %q", a.genInputs[2].Value())
	}
}

func TestAddServer_HideClearsWeakPassConfirmed(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.weakPassConfirmed = "previously-warned"

	a.Hide()

	if a.weakPassConfirmed != "" {
		t.Errorf("weakPassConfirmed should be cleared on Hide(), got %q", a.weakPassConfirmed)
	}
}

func TestAddServer_ShowClearsWeakPassConfirmed(t *testing.T) {
	a := NewAddServer()
	a.weakPassConfirmed = "stale"

	a.Show()

	if a.weakPassConfirmed != "" {
		t.Errorf("weakPassConfirmed should be cleared on Show(), got %q", a.weakPassConfirmed)
	}
}

// --- Pass B: keyboard nav for scanned-keys list (#3) ---

// setupAddServerWithScannedKeys returns a visible AddServerModel with two
// fake scanned keys plus focus advanced to the key-path field, ready for
// nav-key tests.
func setupAddServerWithScannedKeys(t *testing.T) AddServerModel {
	t.Helper()
	a := NewAddServer()
	a.Show()
	a.scannedKeys = []keyEntry{
		{Path: "/home/me/.ssh/id_ed25519", Type: "ed25519"},
		{Path: "/home/me/.sshkey-term/keys/id_ed25519_alt", Type: "ed25519"},
	}
	// Position focus on field 3 (the key-path input) — the natural
	// jumping-off point for entering the list.
	a.focused = 3
	return a
}

func TestAddServer_DownFromKeyPathEntersKeyList(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down"))

	if a.focused != len(a.inputs) {
		t.Errorf("Down from field 3 should enter list (focused=%d), got %d", len(a.inputs), a.focused)
	}
	if a.keyCursor != 0 {
		t.Errorf("entering list should set keyCursor=0, got %d", a.keyCursor)
	}
}

func TestAddServer_DownFromKeyPathNoListWrapsToFirstField(t *testing.T) {
	a := NewAddServer()
	a.Show()
	a.scannedKeys = nil // explicit: no keys to enter
	a.focused = 3

	a, _ = a.Update(keyMsg("down"))

	if a.focused != 0 {
		t.Errorf("Down from field 3 with empty list should wrap to field 0, got %d", a.focused)
	}
}

func TestAddServer_DownInKeyListAdvancesCursor(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list
	a, _ = a.Update(keyMsg("down")) // advance

	if a.focused != len(a.inputs) {
		t.Errorf("should still be in list, focused=%d", a.focused)
	}
	if a.keyCursor != 1 {
		t.Errorf("Down in list should advance cursor to 1, got %d", a.keyCursor)
	}
}

func TestAddServer_DownAtBottomOfKeyListStays(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // cursor=1 (last)
	a, _ = a.Update(keyMsg("down")) // should stay at 1

	if a.keyCursor != 1 {
		t.Errorf("Down at bottom of list should stay at last index, got %d", a.keyCursor)
	}
	if a.focused != len(a.inputs) {
		t.Errorf("Down at bottom should not exit list, focused=%d", a.focused)
	}
}

func TestAddServer_UpInKeyListDecrementsCursor(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list
	a, _ = a.Update(keyMsg("down")) // cursor=1
	a, _ = a.Update(keyMsg("up"))   // back to 0

	if a.keyCursor != 0 {
		t.Errorf("Up in list should decrement cursor to 0, got %d", a.keyCursor)
	}
	if a.focused != len(a.inputs) {
		t.Errorf("Up from cursor=1 should stay in list, focused=%d", a.focused)
	}
}

func TestAddServer_UpAtTopOfKeyListReturnsToKeyPath(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list at cursor=0
	a, _ = a.Update(keyMsg("up"))   // exit back to field 3

	if a.focused != 3 {
		t.Errorf("Up at top of list should return to field 3, got %d", a.focused)
	}
}

func TestAddServer_EnterInKeyListSelectsKey(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // cursor=1

	a, _ = a.Update(keyMsg("enter"))

	if a.focused != 3 {
		t.Errorf("Enter in list should return focus to field 3, got %d", a.focused)
	}
	want := "/home/me/.sshkey-term/keys/id_ed25519_alt"
	if a.inputs[3].Value() != want {
		t.Errorf("Enter on cursor=1 should fill inputs[3] with %q, got %q", want, a.inputs[3].Value())
	}
}

func TestAddServer_TabFromKeyListReturnsToFirstField(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list
	a, _ = a.Update(keyMsg("tab"))

	if a.focused != 0 {
		t.Errorf("Tab from list should jump to field 0, got %d", a.focused)
	}
}

func TestAddServer_ShiftTabFromKeyListReturnsToKeyPath(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down"))      // enter list
	a, _ = a.Update(keyMsg("shift+tab")) // exit upward

	if a.focused != 3 {
		t.Errorf("Shift+Tab from list should return to field 3, got %d", a.focused)
	}
}

func TestAddServer_TabCyclesFieldsSkipsKeyList(t *testing.T) {
	// Tab cycles 0..3 even when the list has entries — the list is
	// only reachable via Down.
	a := setupAddServerWithScannedKeys(t)
	a.focused = 0
	a.inputs[0].Focus()
	for want := 1; want <= 3; want++ {
		a, _ = a.Update(keyMsg("tab"))
		if a.focused != want {
			t.Errorf("Tab cycle: focused=%d, want %d", a.focused, want)
		}
	}
	// One more Tab from field 3: should wrap to field 0, not enter list.
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != 0 {
		t.Errorf("Tab from field 3 should wrap to 0 (not enter list), got %d", a.focused)
	}
}

func TestAddServer_FormKeystrokeIgnoredInKeyList(t *testing.T) {
	// Typing a regular character while the cursor is in the list
	// should not insert into any input — the list zone has no
	// editable target and the fall-through guard skips Update().
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // enter list
	before := a.inputs[3].Value()

	a, _ = a.Update(keyMsg("x"))

	if a.inputs[3].Value() != before {
		t.Errorf("typing in list should not modify inputs[3]: before=%q after=%q", before, a.inputs[3].Value())
	}
	if a.focused != len(a.inputs) {
		t.Errorf("typing in list should not change focus, got %d", a.focused)
	}
}

func TestAddServer_CtrlGFromKeyListClampsFocus(t *testing.T) {
	// Ctrl+G while in the list must clamp focused back into the form
	// range — Esc-back from generate calls inputs[focused].Focus()
	// which would index out of bounds otherwise.
	a := setupAddServerWithScannedKeys(t)
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("down")) // enter list
	if a.focused != len(a.inputs) {
		t.Fatal("precondition: should be in list")
	}

	a, _ = a.Update(keyMsg("ctrl+g"))
	if a.mode != addServerGenerate {
		t.Fatalf("Ctrl+G should enter generate mode, got mode=%d", a.mode)
	}

	// Esc back — must not panic, must focus a real field.
	a, _ = a.Update(keyMsg("esc"))
	if a.mode != addServerForm {
		t.Errorf("Esc should return to form mode, got %d", a.mode)
	}
	if a.focused >= len(a.inputs) {
		t.Errorf("focused must be a real form field after Esc, got %d", a.focused)
	}
}

func TestAddServer_ShowResetsKeyCursor(t *testing.T) {
	a := NewAddServer()
	a.keyCursor = 7 // stale leftover

	a.Show()

	if a.keyCursor != 0 {
		t.Errorf("Show() should reset keyCursor to 0, got %d", a.keyCursor)
	}
}
