package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sendKey(w *WizardModel, key string) {
	model, _ := w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	*w = model.(WizardModel)
}

func sendSpecial(w *WizardModel, k tea.KeyType) {
	model, _ := w.Update(tea.KeyMsg{Type: k})
	*w = model.(WizardModel)
}

// advanceToKeySelect takes a fresh wizard through Welcome → ChooseName → KeySelect
func advanceToKeySelect(w *WizardModel) {
	sendSpecial(w, tea.KeyEnter) // welcome → choose name
	w.nameInput.SetValue("testuser")
	sendSpecial(w, tea.KeyEnter) // choose name → key select
}

func TestWizard_StepTransitions(t *testing.T) {
	w := NewWizard()

	if w.step != WizardWelcome {
		t.Fatalf("initial step = %d, want WizardWelcome", w.step)
	}

	// Welcome → ChooseName
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardChooseName {
		t.Fatalf("step after welcome = %d, want WizardChooseName", w.step)
	}

	// ChooseName → KeySelect
	w.nameInput.SetValue("testuser")
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeySelect {
		t.Fatalf("step after name = %d, want WizardKeySelect", w.step)
	}
	if w.chosenName != "testuser" {
		t.Errorf("chosenName = %q, want testuser", w.chosenName)
	}

	// KeySelect → Generate (select last option: generate)
	totalItems := len(w.keys) + 2
	for i := 0; i < totalItems-1; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeyGenerate {
		t.Fatalf("step = %d, want WizardKeyGenerate", w.step)
	}

	t.Log("step transitions: Welcome → ChooseName → KeySelect → KeyGenerate OK")
}

func TestWizard_ChooseNameValidation(t *testing.T) {
	w := NewWizard()
	sendSpecial(&w, tea.KeyEnter) // → choose name

	// Empty name rejected
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardChooseName {
		t.Fatal("empty name should be rejected")
	}
	if w.err == "" {
		t.Error("should show error for empty name")
	}

	// Short name rejected
	w.nameInput.SetValue("a")
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardChooseName {
		t.Fatal("1-char name should be rejected")
	}

	// Valid name accepted
	w.nameInput.SetValue("alice")
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeySelect {
		t.Fatalf("step = %d, want WizardKeySelect", w.step)
	}

	t.Log("choose name validation: empty/short rejected, valid accepted")
}

func TestWizard_GenerateAndShare(t *testing.T) {
	// Override HOME so finalizeStagedKey (which fires at the WizardServer
	// enter step) writes the canonical per-server copy into a temp dir
	// rather than the developer's real ~/.sshkey-term/. Without this
	// the wizard would pollute the dev's home dir under
	// <home>/.sshkey-term/localhost/keys/.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	w := NewWizard()

	// Fast-forward to KeyGenerate
	advanceToKeySelect(&w)
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter) // → generate

	// Set key path to temp dir (outside staging on purpose — exercises
	// the copy-not-move branch of finalizeStagedKey).
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_ed25519")
	w.genPathInput.SetValue(keyPath)
	// No passphrase for test
	w.genPassInput.SetValue("")
	w.genConfirm.SetValue("")

	// Generate
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardBackup {
		t.Fatalf("step = %d, want WizardBackup (err: %s)", w.step, w.err)
	}
	if w.keyFingerprint == "" {
		t.Fatal("fingerprint not set after keygen")
	}
	t.Logf("generated key: %s fingerprint: %s", keyPath, w.keyFingerprint)

	// Verify key files exist
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("private key not found: %v", err)
	}
	if _, err := os.Stat(keyPath + ".pub"); err != nil {
		t.Fatalf("public key not found: %v", err)
	}

	// Backup → skip (acknowledge)
	sendKey(&w, "a")
	if w.step != WizardShare {
		t.Fatalf("step = %d, want WizardShare", w.step)
	}

	// Share screen should show fingerprint
	view := w.viewShare()
	if w.keyFingerprint == "" || !contains(view, "SHA256:") {
		t.Error("share screen doesn't show fingerprint")
	}
	if !contains(view, "Copy public key") {
		t.Error("share screen doesn't show copy option")
	}

	// Read public key
	pubKey := w.readPublicKey()
	if pubKey == "" {
		t.Fatal("readPublicKey returned empty")
	}
	if !contains(pubKey, "ssh-ed25519") {
		t.Errorf("public key doesn't start with ssh-ed25519: %s", pubKey[:30])
	}

	// Press c to copy (we can't verify clipboard but can verify no crash)
	sendKey(&w, "c")
	if w.err == "" {
		t.Error("expected 'copied' confirmation message")
	}

	// Share → Server
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardServer {
		t.Fatalf("step = %d, want WizardServer", w.step)
	}

	// Fill server details
	w.serverInputs[0].SetValue("Test Server")
	w.serverInputs[1].SetValue("localhost")
	w.serverInputs[2].SetValue("2222")

	// Server → Done
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardDone {
		t.Fatalf("step = %d, want WizardDone", w.step)
	}

	result := w.Result()
	// Under Phase 3c always-copy: finalizeStagedKey runs at the
	// WizardServer step and copies the user-typed generate path
	// into <configDir>/<host>/keys/id_ed25519. result.KeyPath now
	// points at the canonical per-server location, not the user's
	// chosen temp dir.
	wantPath := filepath.Join(homeDir, ".sshkey-term", "localhost", "keys", "id_ed25519")
	if result.KeyPath != wantPath {
		t.Errorf("result key = %q, want %q (canonical per-server)", result.KeyPath, wantPath)
	}
	// Copy-not-move: the user's typed path keeps its original bytes
	// because it lived outside the wizard staging dir.
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("user-typed generated key should remain at %q: %v", keyPath, err)
	}
	if result.ServerHost != "localhost" {
		t.Errorf("result host = %q", result.ServerHost)
	}
	if result.ServerPort != 2222 {
		t.Errorf("result port = %d", result.ServerPort)
	}

	t.Log("full wizard flow: generate → backup → share → server → done OK")
}

func TestWizard_ExportBackup(t *testing.T) {
	w := NewWizard()

	// Generate a key first
	advanceToKeySelect(&w)
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter) // generate

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "export_test_key")
	w.genPathInput.SetValue(keyPath)
	sendSpecial(&w, tea.KeyEnter) // → backup

	if w.step != WizardBackup {
		t.Fatalf("step = %d, want WizardBackup", w.step)
	}

	// Backup → Export
	sendKey(&w, "e")
	if w.step != WizardExport {
		t.Fatalf("step = %d, want WizardExport", w.step)
	}

	// Set export destination
	exportDir := t.TempDir()
	exportPath := filepath.Join(exportDir, "backup_key")
	w.exportInput.SetValue(exportPath)
	sendSpecial(&w, tea.KeyEnter)

	if w.step != WizardShare {
		t.Fatalf("step after export = %d, want WizardShare (err: %s)", w.step, w.err)
	}

	// Verify backup files exist
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("backup private key not found: %v", err)
	}
	if _, err := os.Stat(exportPath + ".pub"); err != nil {
		t.Fatalf("backup public key not found: %v", err)
	}

	// Verify backup matches original
	origData, _ := os.ReadFile(keyPath)
	backupData, _ := os.ReadFile(exportPath)
	if string(origData) != string(backupData) {
		t.Error("backup private key doesn't match original")
	}

	t.Log("export backup: key + pub copied correctly, lands on share screen")
}

func TestWizard_EscNavigation(t *testing.T) {
	w := NewWizard()

	// Welcome → ChooseName → KeySelect
	advanceToKeySelect(&w)
	if w.step != WizardKeySelect {
		t.Fatal("not at keyselect")
	}

	// KeySelect → Generate
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeyGenerate {
		t.Fatal("not at generate")
	}

	// Esc → back to KeySelect
	sendSpecial(&w, tea.KeyEsc)
	if w.step != WizardKeySelect {
		t.Fatalf("esc from generate: step = %d, want KeySelect", w.step)
	}

	// Esc → back to ChooseName
	sendSpecial(&w, tea.KeyEsc)
	if w.step != WizardChooseName {
		t.Fatalf("esc from keyselect: step = %d, want ChooseName", w.step)
	}

	// Esc → back to Welcome
	sendSpecial(&w, tea.KeyEsc)
	if w.step != WizardWelcome {
		t.Fatalf("esc from choosename: step = %d, want Welcome", w.step)
	}

	t.Log("esc navigation: generate → keyselect → choosename → welcome OK")
}

func TestWizard_EscFromServer(t *testing.T) {
	w := NewWizard()

	// Advance through full path to Server
	advanceToKeySelect(&w)
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter)
	tmpDir := t.TempDir()
	w.genPathInput.SetValue(filepath.Join(tmpDir, "esc_server_test"))
	sendSpecial(&w, tea.KeyEnter) // → backup
	sendKey(&w, "a")              // → share
	sendSpecial(&w, tea.KeyEnter) // → server

	if w.step != WizardServer {
		t.Fatalf("step = %d, want WizardServer", w.step)
	}

	// Esc → back to Share
	sendSpecial(&w, tea.KeyEsc)
	if w.step != WizardShare {
		t.Fatalf("esc from server: step = %d, want Share", w.step)
	}

	t.Log("esc from server → share OK")
}

func TestWizard_QuitFromAnyStep(t *testing.T) {
	// q should quit from any step
	steps := []struct {
		name  string
		setup func(w *WizardModel)
	}{
		{"welcome", func(w *WizardModel) {}},
		{"choosename", func(w *WizardModel) {
			sendSpecial(w, tea.KeyEnter) // welcome → choosename
		}},
		{"keyselect", func(w *WizardModel) {
			advanceToKeySelect(w)
		}},
	}

	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWizard()
			tc.setup(&w)
			model, cmd := w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
			_ = model
			if cmd == nil {
				t.Fatalf("q at %s should emit quit command", tc.name)
			}
		})
	}
}

func TestWizard_ShareScreenView(t *testing.T) {
	w := NewWizard()

	// Generate a key and advance to Share
	advanceToKeySelect(&w)
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter)
	tmpDir := t.TempDir()
	w.genPathInput.SetValue(filepath.Join(tmpDir, "share_test"))
	sendSpecial(&w, tea.KeyEnter) // → backup
	sendKey(&w, "a")              // → share

	if w.step != WizardShare {
		t.Fatalf("step = %d, want WizardShare", w.step)
	}

	view := w.viewShare()

	checks := []string{
		"Share With Your Admin",
		"public key",
		"Fingerprint",
		"SHA256:",
		"ssh-ed25519",
		"Copy public key to clipboard",
		"trusted channel",
		"Enter=continue",
	}

	for _, want := range checks {
		if !contains(view, want) {
			t.Errorf("share view missing %q", want)
		}
	}

	t.Log("share screen view: all expected elements present")
}

func TestConnectFailed_View(t *testing.T) {
	var cf ConnectFailedModel
	cf.Show(
		"key not authorized",
		"SHA256:xK9mQ2pR7vT3nW8jL5mZ",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOgdoNpun6JCDfucZGBYbIxiMkNOpREmLc4NwA3PUv29 user@test",
	)

	if !cf.IsVisible() {
		t.Fatal("not visible")
	}

	view := cf.View(60)

	checks := []string{
		"Pending Approval",
		"isn't authorized",
		"pending-keys queue",
		"server operator",
		"approve",
		"SHA256:xK9m",
		"ssh-ed25519",
		"[r] Retry",
		"[c] Copy public key",
		"[q] Quit",
	}

	for _, want := range checks {
		if !contains(view, want) {
			t.Errorf("connect failed view missing %q", want)
		}
	}

	// Should NOT show "copied" yet
	if contains(view, "copied to clipboard") {
		t.Error("should not show copied before pressing c")
	}

	// Press c
	cf, _ = cf.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	view = cf.View(60)
	if !contains(view, "copied to clipboard") {
		t.Error("should show copied after pressing c")
	}

	// Press r — should hide and emit retry
	cf, cmd := cf.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cf.IsVisible() {
		t.Error("should be hidden after retry")
	}
	if cmd == nil {
		t.Error("should emit retry command")
	} else {
		msg := cmd()
		if _, ok := msg.(ConnectFailedRetryMsg); !ok {
			t.Errorf("cmd returned %T, want ConnectFailedRetryMsg", msg)
		}
	}

	t.Log("connect failed: view + interactions OK")
}

func TestWizard_MouseKeySelect(t *testing.T) {
	w := NewWizard()

	// Advance to key select
	advanceToKeySelect(&w)
	if w.step != WizardKeySelect {
		t.Fatal("not at key select")
	}

	// Find the Y coordinate of "Generate new key" in the rendered view
	view := w.viewKeySelect()
	lines := strings.Split(view, "\n")
	generateLine := -1
	for i, l := range lines {
		if strings.Contains(l, "Generate new key") {
			generateLine = i
			break
		}
	}
	if generateLine < 0 {
		t.Fatal("'Generate new key' not found in view")
	}

	// dialogStyle: border(1) + padding(1) = content starts at Y=2
	clickY := 2 + generateLine
	totalItems := len(w.keys) + 2

	model, _ := w.Update(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
		X:      10,
		Y:      clickY,
	})
	w = model.(WizardModel)

	if w.keyCursor != totalItems-1 {
		t.Errorf("cursor = %d after click, want %d (generate)", w.keyCursor, totalItems-1)
	}

	t.Log("mouse key select: click moves cursor to generate")
}

func TestWizard_MouseWelcome(t *testing.T) {
	w := NewWizard()

	if w.step != WizardWelcome {
		t.Fatal("not at welcome")
	}

	// Click anywhere should advance to ChooseName
	model, _ := w.Update(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
		X:      10,
		Y:      5,
	})
	w = model.(WizardModel)

	if w.step != WizardChooseName {
		t.Errorf("step = %d after welcome click, want ChooseName", w.step)
	}

	t.Log("mouse welcome: click advances to choose name")
}

func TestWizard_ExistingKeyCopiedToManagedStoreAndRewritten(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	srcDir := filepath.Join(t.TempDir(), "source_keys")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatalf("mkdir source_keys: %v", err)
	}
	srcKey := filepath.Join(srcDir, "id_ed25519")
	if _, err := generateEd25519KeyFile(srcKey, "", "oldcomment"); err != nil {
		t.Fatalf("generate source key: %v", err)
	}

	w := NewWizard()
	advanceToKeySelect(&w) // chosenName = testuser
	w.keys = []keyEntry{{Path: srcKey, Type: "ed25519"}}
	w.keyCursor = 0

	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardBackup {
		t.Fatalf("step = %d, want WizardBackup (err: %s)", w.step, w.err)
	}
	if w.err != "" {
		t.Fatalf("unexpected wizard error: %s", w.err)
	}
	if w.result.KeyPath == srcKey {
		t.Fatalf("key path should be managed copy, got original path %q", w.result.KeyPath)
	}
	// Under Phase 3c: the wizard reaches the copy step before knowing
	// the server host, so the key lands in the transient staging dir.
	// finalizeStagedKey (invoked at the WizardServer step) moves it
	// into <configDir>/<host>/keys/id_ed25519 once the user types a
	// host. This test stops at WizardBackup, so result.KeyPath still
	// points to staging.
	wantDir := filepath.Join(homeDir, ".sshkey-term", ".staging")
	if filepath.Dir(w.result.KeyPath) != wantDir {
		t.Fatalf("staged key dir = %q, want %q", filepath.Dir(w.result.KeyPath), wantDir)
	}
	if _, err := os.Stat(w.result.KeyPath); err != nil {
		t.Fatalf("managed private key missing: %v", err)
	}
	gotPub, err := os.ReadFile(w.result.KeyPath + ".pub")
	if err != nil {
		t.Fatalf("read managed pub: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(gotPub)), "testuser") {
		t.Fatalf("managed pub comment not rewritten to chosen name: %q", strings.TrimSpace(string(gotPub)))
	}
	srcPub, err := os.ReadFile(srcKey + ".pub")
	if err != nil {
		t.Fatalf("read source pub: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(srcPub)), "oldcomment") {
		t.Fatalf("source pub should remain unchanged, got %q", strings.TrimSpace(string(srcPub)))
	}
	if w.keyFingerprint == "" {
		t.Fatal("key fingerprint should be set for managed copied key")
	}
}

func TestWizard_ImportMissingPublicKeyFailsExplicitly(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	srcKey := filepath.Join(t.TempDir(), "id_ed25519")
	if _, err := generateEd25519KeyFile(srcKey, "", "importme"); err != nil {
		t.Fatalf("generate source key: %v", err)
	}
	if err := os.Remove(srcKey + ".pub"); err != nil {
		t.Fatalf("remove source pub: %v", err)
	}

	w := NewWizard()
	advanceToKeySelect(&w)
	w.step = WizardKeyImport
	w.importInput.SetValue(srcKey)
	w.importInput.Focus()

	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeyImport {
		t.Fatalf("step = %d, want WizardKeyImport after import failure", w.step)
	}
	if !contains(w.err, "read public key failed") {
		t.Fatalf("expected explicit public key read error, got %q", w.err)
	}
}

// TestConnectFailed_Mouse used to live here; deleted along with the
// ConnectFailedModel.HandleMouse method. Mouse clicks are now
// absorbed (no-op) at the App.handleMouse level when the dialog is
// visible — see app.go's handleMouse for the rationale and
// connectfailed.go's package comment for the design intent.
// Keyboard input remains exercised by TestConnectFailed_View above.

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchContains(s, substr)
}

func searchContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Keygen state-leak fixes (parallel to AddServer Pass A) ---

// advanceToKeyGenerate advances the wizard from a fresh state through
// Welcome → ChooseName → KeySelect → KeyGenerate, leaving the cursor
// on the "Generate new key" entry and selecting it.
func advanceToKeyGenerate(t *testing.T, w *WizardModel) {
	t.Helper()
	advanceToKeySelect(w)
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(w, "j")
	}
	sendSpecial(w, tea.KeyEnter)
	if w.step != WizardKeyGenerate {
		t.Fatalf("precondition: should be at WizardKeyGenerate, got step=%d", w.step)
	}
}

func TestWizard_KeyGenerateEscClearsPassphraseState(t *testing.T) {
	w := NewWizard()
	advanceToKeyGenerate(t, &w)

	// Type passphrase + simulate having seen a weak-pass warning.
	w.genPassInput.SetValue("typed-then-bailed")
	w.genConfirm.SetValue("typed-then-bailed")
	w.weakPassConfirmed = "typed-then-bailed"

	sendSpecial(&w, tea.KeyEsc)

	if w.step != WizardKeySelect {
		t.Fatalf("Esc should return to KeySelect, got step=%d", w.step)
	}
	if w.genPassInput.Value() != "" {
		t.Errorf("Esc should clear passphrase, got %q", w.genPassInput.Value())
	}
	if w.genConfirm.Value() != "" {
		t.Errorf("Esc should clear confirm, got %q", w.genConfirm.Value())
	}
	if w.weakPassConfirmed != "" {
		t.Errorf("Esc should clear weakPassConfirmed, got %q", w.weakPassConfirmed)
	}
}

func TestWizard_KeyGenerateReentryClearsPassphraseState(t *testing.T) {
	w := NewWizard()
	advanceToKeyGenerate(t, &w)

	// Leave behind some state, Esc back to KeySelect.
	w.genPassInput.SetValue("from-first-visit")
	w.genConfirm.SetValue("from-first-visit")
	w.weakPassConfirmed = "from-first-visit"
	sendSpecial(&w, tea.KeyEsc)
	if w.step != WizardKeySelect {
		t.Fatal("precondition: should be at KeySelect after Esc")
	}

	// Re-enter via "Generate new key" — should be a fresh slate.
	for i := 0; i < len(w.keys)+2; i++ {
		sendKey(&w, "j")
	}
	sendSpecial(&w, tea.KeyEnter)
	if w.step != WizardKeyGenerate {
		t.Fatalf("re-entry: step=%d, want KeyGenerate", w.step)
	}

	if w.genPassInput.Value() != "" {
		t.Errorf("re-entry: passphrase should be cleared, got %q", w.genPassInput.Value())
	}
	if w.genConfirm.Value() != "" {
		t.Errorf("re-entry: confirm should be cleared, got %q", w.genConfirm.Value())
	}
	if w.weakPassConfirmed != "" {
		t.Errorf("re-entry: weakPassConfirmed should be cleared, got %q", w.weakPassConfirmed)
	}
}

func TestWizard_KeyGenerateSuccessClearsPassphraseState(t *testing.T) {
	w := NewWizard()
	advanceToKeyGenerate(t, &w)

	// Path to a temp file, no passphrase (lets keygen pass without
	// triggering the zxcvbn block tier — the wizard allows empty
	// passphrases for unencrypted keys). We still set values to
	// confirm clearing happens regardless.
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "wiz_clear_state")
	w.genPathInput.SetValue(keyPath)
	w.genPassInput.SetValue("")
	w.genConfirm.SetValue("")
	// Pretend a weakPassConfirmed mark was set during a prior submit
	// of a different (warned-tier) value — should be reset on success.
	w.weakPassConfirmed = "old-warned-value"

	sendSpecial(&w, tea.KeyEnter)

	if w.step != WizardBackup {
		t.Fatalf("success path should go to WizardBackup, got step=%d (err: %s)", w.step, w.err)
	}
	if w.genPassInput.Value() != "" {
		t.Errorf("success: passphrase should be cleared, got %q", w.genPassInput.Value())
	}
	if w.genConfirm.Value() != "" {
		t.Errorf("success: confirm should be cleared, got %q", w.genConfirm.Value())
	}
	if w.weakPassConfirmed != "" {
		t.Errorf("success: weakPassConfirmed should be cleared, got %q", w.weakPassConfirmed)
	}
}
