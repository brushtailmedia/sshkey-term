package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// withPassthroughKeyCopy swaps keyCopyFn for a no-op passthrough
// (returns the source path unchanged) for the duration of a test.
// Used by submit-flow tests so they don't need real key files on
// disk to exercise the form-validation + AddServerMsg-emission path.
func withPassthroughKeyCopy(t *testing.T) {
	t.Helper()
	prev := keyCopyFn
	keyCopyFn = func(configDir, src, host, displayName string) (string, error) { return src, nil }
	t.Cleanup(func() { keyCopyFn = prev })
}

// withRecordingKeyCopy swaps keyCopyFn for a spy that captures the
// src argument (the user-typed key path entering the submit flow).
// Returns a pointer the caller can inspect after submit to assert
// that the right source path reached the copy step. Replaces the
// previous addMsg.Key-based assertions, which read the
// AddServerMsg.Key field that Phase 3e of the path-centralization
// refactor deletes.
func withRecordingKeyCopy(t *testing.T) *string {
	t.Helper()
	var captured string
	prev := keyCopyFn
	keyCopyFn = func(configDir, src, host, displayName string) (string, error) {
		captured = src
		return src, nil
	}
	t.Cleanup(func() { keyCopyFn = prev })
	return &captured
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
	case "alt+g":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}, Alt: true}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestAddServer_InitialState(t *testing.T) {
	a := NewAddServer(nil)
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
	a := NewAddServer(nil)
	a.Show()

	for want := 1; want < 5; want++ {
		a, _ = a.Update(keyMsg("tab"))
		if a.focused != want {
			t.Errorf("after tab: focused = %d, want %d", a.focused, want)
			break
		}
	}
	// After the last field (fieldKey) comes the [Generate new key] row.
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != a.focusGenRow() {
		t.Errorf("Tab from key field should focus the gen row (%d), got %d", a.focusGenRow(), a.focused)
	}
	// One more Tab wraps to field 0 (the scanned-keys list is Down-only).
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != 0 {
		t.Errorf("focus should wrap to 0, got %d", a.focused)
	}
}

func TestAddServer_EscHides(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a, _ = a.Update(keyMsg("esc"))
	if a.IsVisible() {
		t.Error("Esc should hide the dialog")
	}
}

func TestAddServer_AltGEntersGenerateMode(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
	if a.mode != addServerGenerate {
		t.Errorf("mode after Alt+g = %d, want addServerGenerate", a.mode)
	}
	if a.genFocused != 0 {
		t.Errorf("genFocused = %d, want 0 (path)", a.genFocused)
	}
}

func TestAddServer_AltGRequiresHost(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	// Host left empty — Alt+g should refuse to enter generate mode.
	a, _ = a.Update(keyMsg("alt+g"))
	if a.mode != addServerForm {
		t.Errorf("Alt+g with empty host should stay in form mode, got %d", a.mode)
	}
	if a.formErr == "" {
		t.Error("Alt+g with empty host should set formErr")
	}
	if !strings.Contains(a.formErr, "hostname") {
		t.Errorf("formErr should mention hostname, got: %q", a.formErr)
	}
	if a.focused != 1 {
		t.Errorf("focus should jump to host field (1), got %d", a.focused)
	}
}

func TestAddServer_AltGRejectsWhitespaceOnlyHost(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	// Whitespace-only host — same treatment as empty.
	a.inputs[1].SetValue("   ")
	a, _ = a.Update(keyMsg("alt+g"))
	if a.mode != addServerForm {
		t.Errorf("Alt+g with whitespace host should stay in form, got mode=%d", a.mode)
	}
	if a.formErr == "" {
		t.Error("whitespace host should set formErr")
	}
}

func TestAddServer_GenerateEscReturnsToForm(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))

	dir := t.TempDir()
	a.genInputs[0].SetValue(filepath.Join(dir, "newkey"))
	a.genInputs[1].SetValue("pass1")
	a.genInputs[2].SetValue("pass2-different")

	a, _ = a.Update(keyMsg("enter"))
	if a.genErr == "" || !strings.Contains(a.genErr, "don't match") {
		t.Errorf("passphrase mismatch should produce matching error, got: %q", a.genErr)
	}
}

func TestAddServer_GenerateRejectsInvalidDisplayName(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[fieldHost].SetValue("chat.example.com")
	a.inputs[fieldDisplayName].SetValue("bad+name") // DP9: '+' is banned.
	a, _ = a.Update(keyMsg("alt+g"))

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "generated_key")
	a.genInputs[0].SetValue(keyPath)
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")

	a, _ = a.Update(keyMsg("enter"))
	if a.mode != addServerGenerate {
		t.Errorf("invalid display name should stay in generate mode, got %d", a.mode)
	}
	if !strings.Contains(a.genErr, "Display name") {
		t.Errorf("genErr should mention Display name, got %q", a.genErr)
	}
	if _, err := os.Stat(keyPath); err == nil {
		t.Fatal("invalid display name must not write generated private key")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat generated key: %v", err)
	}
	if _, err := os.Stat(keyPath + ".pub"); err == nil {
		t.Fatal("invalid display name must not write generated public key")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat generated pubkey: %v", err)
	}
}

func TestAddServer_GenerateExistingFileRejected(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))

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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))

	dir := t.TempDir()
	newPath := filepath.Join(dir, "generated_key")
	a.genInputs[0].SetValue(newPath)
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")

	a, _ = a.Update(keyMsg("enter"))

	if a.mode != addServerForm {
		t.Errorf("after successful generation, mode = %d, want addServerForm", a.mode)
	}
	if a.focused != fieldKey {
		t.Errorf("focus should be on key path field (key field), got %d", a.focused)
	}
	if a.inputs[fieldKey].Value() != newPath {
		t.Errorf("key path input = %q, want %q", a.inputs[fieldKey].Value(), newPath)
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
	a := NewAddServer(nil)
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
	gotSrc := withRecordingKeyCopy(t)
	a := NewAddServer(nil)
	a.Show()
	a.inputs[fieldName].SetValue("Test Server")
	a.inputs[fieldHost].SetValue("chat.example.com")
	// port default "2222" already set
	a.inputs[fieldDisplayName].SetValue("Alice")
	a.inputs[fieldKey].SetValue("~/.ssh/id_ed25519")

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
	// The requested display name (distinct from the server label Name) flows
	// through to the message so it can be persisted + sent as the SSH username.
	if addMsg.RequestedDisplayName != "Alice" {
		t.Errorf("RequestedDisplayName = %q, want Alice", addMsg.RequestedDisplayName)
	}
	// Phase 3e dropped AddServerMsg.Key; assert via the recording
	// keyCopyFn spy that the user-typed source path reached the
	// copy step. Caller-side behavior (config.AddServer writes
	// without a persisted key reference) is covered by the
	// per-server canonical-location invariant.
	if *gotSrc != "~/.ssh/id_ed25519" {
		t.Errorf("keyCopyFn source = %q, want ~/.ssh/id_ed25519", *gotSrc)
	}
	if a.IsVisible() {
		t.Error("should hide after successful submit")
	}
}

func TestAddServer_SubmitCopiesIntoActiveConfigDir(t *testing.T) {
	configDir := t.TempDir()
	var gotConfigDir string
	prev := keyCopyFn
	keyCopyFn = func(configDir, src, host, displayName string) (string, error) {
		gotConfigDir = configDir
		return src, nil
	}
	t.Cleanup(func() { keyCopyFn = prev })

	a := NewAddServerWithConfigDir(configDir, nil)
	a.Show()
	a.inputs[fieldName].SetValue("Test Server")
	a.inputs[fieldHost].SetValue("chat.example.com")
	a.inputs[fieldDisplayName].SetValue("Alice")
	a.inputs[fieldKey].SetValue("~/.ssh/id_ed25519")

	_, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("valid form should submit")
	}
	if gotConfigDir != configDir {
		t.Fatalf("keyCopyFn configDir = %q, want active configDir %q", gotConfigDir, configDir)
	}
}

func TestAddServer_SubmitDefaultsName(t *testing.T) {
	withPassthroughKeyCopy(t)
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a.inputs[fieldKey].SetValue("~/.ssh/id_ed25519")

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
	gotSrc := withRecordingKeyCopy(t)
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	// Leave key blank

	_, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("should submit")
	}
	// Verify the empty-key-defaults-to-~/.ssh/id_ed25519 path
	// fires by checking the source that reached keyCopyFn (replaces
	// the pre-Phase-3e addMsg.Key assertion).
	if *gotSrc != "~/.ssh/id_ed25519" {
		t.Errorf("default key source = %q, want ~/.ssh/id_ed25519", *gotSrc)
	}
}

func TestPubLineWithComment(t *testing.T) {
	// Build a real authorized-keys line carrying an existing comment.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	base := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	withComment := []byte(base + " old-comment\n")

	// Empty display name → input returned unchanged (existing comment kept).
	out, err := pubLineWithComment(withComment, "")
	if err != nil {
		t.Fatalf("empty name: %v", err)
	}
	if string(out) != string(withComment) {
		t.Errorf("empty name should return input unchanged, got %q", string(out))
	}

	// Non-empty display name → comment replaced with the name.
	out, err = pubLineWithComment(withComment, "Alice Smith")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(out)), " Alice Smith") {
		t.Errorf("comment not rewritten to the display name: %q", string(out))
	}
	if strings.Contains(string(out), "old-comment") {
		t.Errorf("old comment should be gone: %q", string(out))
	}

	// Invalid pub + non-empty name → error (never write garbage to the .pub).
	if _, err := pubLineWithComment([]byte("not a valid key"), "Alice"); err == nil {
		t.Error("invalid pub data with a display name should error")
	}
	// Invalid pub + empty name → returned as-is (no parse attempted).
	if _, err := pubLineWithComment([]byte("not a valid key"), ""); err != nil {
		t.Errorf("empty name should not parse/err on bad input: %v", err)
	}
}

// TestCopyKeyForServer_RewritesManagedPubComment exercises the REAL copy
// (not the keyCopyFn passthrough the submit tests use): the managed
// destination .pub comment is rewritten to the requested display name, the
// user's original source .pub is left untouched, and the source-equals-
// destination case still rewrites the managed .pub. DefaultConfigDir keys off
// $HOME, so t.Setenv redirects the managed tree into a temp dir.
func TestCopyKeyForServer_RewritesManagedPubComment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source key pair outside the managed tree, carrying an existing comment.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "id_ed25519")
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	srcPubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " old-comment\n"
	if err := os.WriteFile(src, []byte("PRIVATE KEY BYTES\n"), 0o600); err != nil {
		t.Fatalf("write src priv: %v", err)
	}
	if err := os.WriteFile(src+".pub", []byte(srcPubLine), 0o644); err != nil {
		t.Fatalf("write src pub: %v", err)
	}

	const host = "copytest.example.com"
	dst, err := copyKeyForServer(src, host, "Alice Smith")
	if err != nil {
		t.Fatalf("copyKeyForServer: %v", err)
	}

	// Destination is the canonical per-server managed location.
	if want := config.ServerKeyPath(config.DefaultConfigDir(), host); dst != want {
		t.Errorf("dst = %q, want managed path %q", dst, want)
	}

	// Managed .pub comment rewritten to the display name; the source comment
	// is gone from the managed copy.
	managedPub, err := os.ReadFile(dst + ".pub")
	if err != nil {
		t.Fatalf("read managed .pub: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(managedPub)), " Alice Smith") {
		t.Errorf("managed .pub comment = %q, want suffix ' Alice Smith'", string(managedPub))
	}
	if strings.Contains(string(managedPub), "old-comment") {
		t.Errorf("managed .pub should not keep the source comment: %q", string(managedPub))
	}

	// The user's ORIGINAL source .pub must be untouched.
	srcPubAfter, err := os.ReadFile(src + ".pub")
	if err != nil {
		t.Fatalf("read src .pub after copy: %v", err)
	}
	if string(srcPubAfter) != srcPubLine {
		t.Errorf("source .pub was mutated: %q, want %q", string(srcPubAfter), srcPubLine)
	}

	// Source-equals-destination: re-copy from the managed path itself with a
	// new name → the managed .pub is rewritten in place.
	if _, err := copyKeyForServer(dst, host, "Bob"); err != nil {
		t.Fatalf("copyKeyForServer (src==dst): %v", err)
	}
	managedPub2, err := os.ReadFile(dst + ".pub")
	if err != nil {
		t.Fatalf("re-read managed .pub: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(managedPub2)), " Bob") {
		t.Errorf("src==dst rewrite: managed .pub = %q, want suffix ' Bob'", string(managedPub2))
	}
}

func TestAddServer_SubmitRejectsInvalidDisplayName(t *testing.T) {
	called := false
	prev := keyCopyFn
	keyCopyFn = func(configDir, src, host, displayName string) (string, error) { called = true; return src, nil }
	t.Cleanup(func() { keyCopyFn = prev })

	a := NewAddServer(nil)
	a.Show()
	a.inputs[fieldHost].SetValue("chat.example.com")
	a.inputs[fieldDisplayName].SetValue("bad+name") // DP9: '+' is banned
	a.inputs[fieldKey].SetValue("~/.ssh/id_ed25519")

	a, cmd := a.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("invalid display name should block submit (no AddServerMsg emitted)")
	}
	if !a.IsVisible() {
		t.Error("dialog should stay visible on invalid display name")
	}
	if !strings.Contains(a.formErr, "Display name") {
		t.Errorf("formErr should mention Display name, got %q", a.formErr)
	}
	// Validation must run BEFORE the key copy, so a bad name never touches
	// the filesystem.
	if called {
		t.Error("keyCopyFn must NOT run when the display name is rejected")
	}
}

func TestAddServer_KeyListStartY_NoNotice(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	// No notice, no keys scanned (depends on environment — use explicit 0)
	a.scannedKeys = nil
	a.genNotice = ""

	// With no keys: header rows 0..3 + 5 fields*2 (=14) + the [Generate new
	// key] row + blank (+2) = 16.
	y := a.keyListStartY()
	if y != 16 {
		t.Errorf("keyListStartY with no keys = %d, want 16", y)
	}
}

func TestAddServer_KeyListStartY_WithKeys(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.scannedKeys = []keyEntry{{Path: "/tmp/k1", Type: "ed25519"}}
	a.genNotice = ""

	// 14 (fields) + 2 (gen row) + 2 (keys header) → first key at 18.
	y := a.keyListStartY()
	if y != 18 {
		t.Errorf("keyListStartY with 1 key, no notice = %d, want 18", y)
	}
}

func TestAddServer_KeyListStartY_WithNoticeAndKeys(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.scannedKeys = []keyEntry{{Path: "/tmp/k1", Type: "ed25519"}}
	a.genNotice = "✓ Key generated — back it up"

	// 14 (fields) + 2 (gen row) + 2 (notice) + 2 (keys header) = 20.
	y := a.keyListStartY()
	if y != 20 {
		t.Errorf("keyListStartY with notice + keys = %d, want 20", y)
	}
}

func TestAddServer_HandleMouse_ClickOnField(t *testing.T) {
	a := NewAddServer(nil)
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
	a := NewAddServer(nil)
	a.Show()
	a.scannedKeys = []keyEntry{
		{Path: "/home/me/.ssh/id_ed25519", Type: "ed25519"},
		{Path: "/home/me/.ssh/work_key", Type: "ed25519"},
	}
	a.genNotice = ""

	// First key is at keyListStartY() (computed dynamically; 16 with the gen row).
	startY := a.keyListStartY()
	a, _ = a.HandleMouse(tea.MouseMsg{
		X:      10,
		Y:      startY + 1, // click second key
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	})
	if a.inputs[fieldKey].Value() != "/home/me/.ssh/work_key" {
		t.Errorf("clicking second key should fill path input, got: %q", a.inputs[fieldKey].Value())
	}
	if a.focused != fieldKey {
		t.Errorf("focus should be on key path input after click (key field), got %d", a.focused)
	}
}

func TestAddServer_HandleMouse_IgnoresNonLeftClick(t *testing.T) {
	a := NewAddServer(nil)
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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
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
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("alt+g"))
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
	a := NewAddServer(nil)
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

func TestAddServer_AltGClearsPassphraseFields(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[1].SetValue("chat.example.com")

	// Open generate, type a passphrase, Esc back to form
	a, _ = a.Update(keyMsg("alt+g"))
	a.genInputs[1].SetValue("typed-then-bailed")
	a.genInputs[2].SetValue("typed-then-bailed")
	a, _ = a.Update(keyMsg("esc"))
	if a.mode != addServerForm {
		t.Fatal("precondition: Esc should return to form")
	}

	// Re-enter generate — passphrase fields should be fresh
	a, _ = a.Update(keyMsg("alt+g"))
	if a.genInputs[1].Value() != "" {
		t.Errorf("re-entering generate should clear passphrase, got %q", a.genInputs[1].Value())
	}
	if a.genInputs[2].Value() != "" {
		t.Errorf("re-entering generate should clear confirm, got %q", a.genInputs[2].Value())
	}
}

// --- Pass B: keyboard nav for scanned-keys list (#3) ---

// setupAddServerWithScannedKeys returns a visible AddServerModel with two
// fake scanned keys plus focus advanced to the key-path field, ready for
// nav-key tests.
func setupAddServerWithScannedKeys(t *testing.T) AddServerModel {
	t.Helper()
	a := NewAddServer(nil)
	a.Show()
	a.scannedKeys = []keyEntry{
		{Path: "/home/me/.ssh/id_ed25519", Type: "ed25519"},
		{Path: "/home/me/.ssh/id_ed25519_alt", Type: "ed25519"},
	}
	// Position focus on the key field (the key-path input) — the natural
	// jumping-off point for entering the list.
	a.focused = fieldKey
	return a
}

func TestAddServer_DownFromKeyFieldEntersGenRow(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down"))

	if a.focused != a.focusGenRow() {
		t.Errorf("Down from the key field should focus the gen row (%d), got %d", a.focusGenRow(), a.focused)
	}
}

func TestAddServer_DownFromGenRowEntersKeyList(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // key field → gen row
	a, _ = a.Update(keyMsg("down")) // gen row → list

	if a.focused != a.focusKeyList() {
		t.Errorf("Down from the gen row should enter the list (%d), got %d", a.focusKeyList(), a.focused)
	}
	if a.keyCursor != 0 {
		t.Errorf("entering list should set keyCursor=0, got %d", a.keyCursor)
	}
}

func TestAddServer_DownFromGenRowNoListStays(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.scannedKeys = nil // explicit: no keys to descend into
	a.focused = fieldKey

	a, _ = a.Update(keyMsg("down")) // key field → gen row
	a, _ = a.Update(keyMsg("down")) // no list below → stays on the gen row

	if a.focused != a.focusGenRow() {
		t.Errorf("Down from the gen row with no keys should stay on it (%d), got %d", a.focusGenRow(), a.focused)
	}
}

func TestAddServer_DownInKeyListAdvancesCursor(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // advance to cursor=1

	if a.focused != a.focusKeyList() {
		t.Errorf("should still be in list, focused=%d", a.focused)
	}
	if a.keyCursor != 1 {
		t.Errorf("Down in list should advance cursor to 1, got %d", a.keyCursor)
	}
}

func TestAddServer_DownAtBottomOfKeyListStays(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // cursor=1 (last)
	a, _ = a.Update(keyMsg("down")) // should stay at 1

	if a.keyCursor != 1 {
		t.Errorf("Down at bottom of list should stay at last index, got %d", a.keyCursor)
	}
	if a.focused != a.focusKeyList() {
		t.Errorf("Down at bottom should not exit list, focused=%d", a.focused)
	}
}

func TestAddServer_UpInKeyListDecrementsCursor(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // cursor=1
	a, _ = a.Update(keyMsg("up"))   // back to 0

	if a.keyCursor != 0 {
		t.Errorf("Up in list should decrement cursor to 0, got %d", a.keyCursor)
	}
	if a.focused != a.focusKeyList() {
		t.Errorf("Up from cursor=1 should stay in list, focused=%d", a.focused)
	}
}

func TestAddServer_UpAtTopOfKeyListReturnsToGenRow(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list at cursor=0
	a, _ = a.Update(keyMsg("up"))   // top of list → gen row

	if a.focused != a.focusGenRow() {
		t.Errorf("Up at top of list should return to the gen row (%d), got %d", a.focusGenRow(), a.focused)
	}
}

func TestAddServer_UpFromGenRowReturnsToKeyField(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // key field → gen row
	a, _ = a.Update(keyMsg("up"))   // gen row → key field

	if a.focused != fieldKey {
		t.Errorf("Up from the gen row should return to the key field, got %d", a.focused)
	}
}

func TestAddServer_EnterInKeyListSelectsKey(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list (cursor=0)
	a, _ = a.Update(keyMsg("down")) // cursor=1

	a, _ = a.Update(keyMsg("enter"))

	if a.focused != fieldKey {
		t.Errorf("Enter in list should return focus to the key field, got %d", a.focused)
	}
	want := "/home/me/.ssh/id_ed25519_alt"
	if a.inputs[fieldKey].Value() != want {
		t.Errorf("Enter on cursor=1 should fill inputs[fieldKey] with %q, got %q", want, a.inputs[fieldKey].Value())
	}
}

func TestAddServer_EnterOnGenRowEntersGenerate(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a.inputs[fieldHost].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("down")) // key field → gen row
	a, _ = a.Update(keyMsg("enter"))
	if a.mode != addServerGenerate {
		t.Errorf("Enter on the gen row should open generate mode, got %d", a.mode)
	}
}

func TestAddServer_TabFromKeyListReturnsToFirstField(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list
	a, _ = a.Update(keyMsg("tab"))

	if a.focused != 0 {
		t.Errorf("Tab from list should jump to field 0, got %d", a.focused)
	}
}

func TestAddServer_ShiftTabFromKeyListReturnsToGenRow(t *testing.T) {
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down"))      // → gen row
	a, _ = a.Update(keyMsg("down"))      // → list
	a, _ = a.Update(keyMsg("shift+tab")) // exit upward

	if a.focused != a.focusGenRow() {
		t.Errorf("Shift+Tab from list should return to the gen row (%d), got %d", a.focusGenRow(), a.focused)
	}
}

func TestAddServer_TabCyclesFieldsThenGenRowSkipsKeyList(t *testing.T) {
	// Tab cycles 0..4 then the [Generate new key] row even when the list has
	// entries — the scanned-keys list is only reachable via Down.
	a := setupAddServerWithScannedKeys(t)
	a.focused = 0
	a.inputs[0].Focus()
	for want := 1; want <= 4; want++ {
		a, _ = a.Update(keyMsg("tab"))
		if a.focused != want {
			t.Errorf("Tab cycle: focused=%d, want %d", a.focused, want)
		}
	}
	// From the key field, Tab lands on the gen row (not the list).
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != a.focusGenRow() {
		t.Errorf("Tab from key field should focus the gen row (%d), got %d", a.focusGenRow(), a.focused)
	}
	// One more Tab wraps to field 0 (still skipping the list).
	a, _ = a.Update(keyMsg("tab"))
	if a.focused != 0 {
		t.Errorf("Tab from gen row should wrap to 0 (not enter list), got %d", a.focused)
	}
}

func TestAddServer_FormKeystrokeIgnoredInKeyList(t *testing.T) {
	// Typing a regular character while the cursor is in the list
	// should not insert into any input — the list zone has no
	// editable target and the fall-through guard skips Update().
	a := setupAddServerWithScannedKeys(t)
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list
	before := a.inputs[fieldKey].Value()

	a, _ = a.Update(keyMsg("x"))

	if a.inputs[fieldKey].Value() != before {
		t.Errorf("typing in list should not modify inputs[fieldKey]: before=%q after=%q", before, a.inputs[fieldKey].Value())
	}
	if a.focused != a.focusKeyList() {
		t.Errorf("typing in list should not change focus, got %d", a.focused)
	}
}

func TestAddServer_AltGFromKeyListClampsFocus(t *testing.T) {
	// Alt+g while in the list must clamp focused back into the form
	// range — Esc-back from generate calls inputs[focused].Focus()
	// which would index out of bounds otherwise.
	a := setupAddServerWithScannedKeys(t)
	a.inputs[1].SetValue("chat.example.com")
	a, _ = a.Update(keyMsg("down")) // → gen row
	a, _ = a.Update(keyMsg("down")) // → list
	if a.focused != a.focusKeyList() {
		t.Fatal("precondition: should be in the list")
	}

	a, _ = a.Update(keyMsg("alt+g"))
	if a.mode != addServerGenerate {
		t.Fatalf("Alt+g should enter generate mode, got mode=%d", a.mode)
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
	a := NewAddServer(nil)
	a.keyCursor = 7 // stale leftover

	a.Show()

	if a.keyCursor != 0 {
		t.Errorf("Show() should reset keyCursor to 0, got %d", a.keyCursor)
	}
}
