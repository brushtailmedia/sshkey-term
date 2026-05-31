package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/keygen"
)

// WizardStep tracks which screen the wizard is on.
type WizardStep int

const (
	WizardWelcome WizardStep = iota
	WizardChooseName
	WizardKeySelect
	WizardKeyImport
	WizardKeyGenerate
	WizardBackup
	WizardExport
	WizardShare
	WizardServer
	WizardDone
)

// WizardResult is returned when the wizard completes.
type WizardResult struct {
	KeyPath       string
	PreferredName string // chosen display name, embedded in key comment
	ServerName    string
	ServerHost    string
	ServerPort    int
}

// WizardModel manages the first-launch setup wizard.
type WizardModel struct {
	step       WizardStep
	result     WizardResult
	err        string
	width      int
	height     int
	chosenName string // preferred display name, embedded in key comment

	// Name input
	nameInput textinput.Model

	// Key selection
	keys      []keyEntry
	keyCursor int

	// Key generation
	genPathInput textinput.Model
	genPassInput textinput.Model
	genConfirm   textinput.Model
	genFocused   int // 0=path, 1=pass, 2=confirm

	// Live strength hint — recomputed on every keystroke in the keygen
	// step. Shown as a compact one-line indicator under the passphrase
	// field. Empty passphrases show the unencrypted-key warning immediately;
	// non-empty passphrases run zxcvbn from the first character.
	strengthHint keygen.LiveHint

	// Import
	importInput textinput.Model

	// Export
	exportInput textinput.Model

	// Server
	serverInputs  []textinput.Model
	serverFocused int
	serverLabels  []string

	// Key fingerprint (set after selection/generation)
	keyFingerprint string
}

// NewWizard creates the setup wizard.
func NewWizard() WizardModel {
	// Name input
	nameIn := textinput.New()
	nameIn.Placeholder = "your display name"
	nameIn.Prompt = ""
	nameIn.CharLimit = 32

	// Key generation inputs
	genPath := textinput.New()
	// Default destination is the wizard's transient staging dir; the
	// final per-server location at <configDir>/<host>/keys/id_ed25519
	// isn't derivable yet (no host until WizardServer), so we stage
	// here and the server step moves the key into place. User can
	// still override this path if they want the bytes elsewhere too
	// (finalizeStagedKey treats off-staging paths as "copy, don't
	// move", so the user's chosen location is preserved alongside
	// the canonical per-server copy).
	genPath.SetValue(filepath.Join(wizardStagingDir(config.DefaultConfigDir()), "id_ed25519"))
	genPath.Prompt = ""

	genPass := textinput.New()
	genPass.Placeholder = "passphrase"
	genPass.EchoMode = textinput.EchoPassword
	genPass.Prompt = ""

	genConfirm := textinput.New()
	genConfirm.Placeholder = "confirm passphrase"
	genConfirm.EchoMode = textinput.EchoPassword
	genConfirm.Prompt = ""

	// Import input
	importIn := textinput.New()
	importIn.Placeholder = "~/path/to/private_key"
	importIn.Prompt = ""

	// Export input. Default destination is ~/Documents/sshkey-backup —
	// a save-destination concern (out of scope for path centralization
	// per §"Scope — Out"), so home resolution stays inline here.
	home, _ := os.UserHomeDir()
	exportIn := textinput.New()
	exportIn.SetValue(filepath.Join(home, "Documents", "sshkey-backup"))
	exportIn.Prompt = ""

	// Server inputs
	serverLabels := []string{"Server name", "Host", "Port"}
	serverInputs := make([]textinput.Model, 3)
	for i := range serverInputs {
		serverInputs[i] = textinput.New()
		serverInputs[i].Prompt = ""
	}
	serverInputs[0].Placeholder = "Personal"
	serverInputs[1].Placeholder = "chat.example.com"
	serverInputs[2].SetValue("2222")

	return WizardModel{
		step:         WizardWelcome,
		nameInput:    nameIn,
		genPathInput: genPath,
		genPassInput: genPass,
		genConfirm:   genConfirm,
		importInput:  importIn,
		exportInput:  exportIn,
		serverInputs: serverInputs,
		serverLabels: serverLabels,
	}
}

func (w WizardModel) Init() tea.Cmd {
	return nil
}

func (w WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.width = msg.Width
		w.height = msg.Height
		return w, nil

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease {
			return w.handleMouse(msg)
		}
		return w, nil

	case tea.KeyMsg:
		// Global
		if msg.String() == "ctrl+c" {
			return w, tea.Quit
		}

		switch w.step {
		case WizardWelcome:
			return w.updateWelcome(msg)
		case WizardChooseName:
			return w.updateChooseName(msg)
		case WizardKeySelect:
			return w.updateKeySelect(msg)
		case WizardKeyImport:
			return w.updateKeyImport(msg)
		case WizardKeyGenerate:
			return w.updateKeyGenerate(msg)
		case WizardBackup:
			return w.updateBackup(msg)
		case WizardExport:
			return w.updateExport(msg)
		case WizardShare:
			return w.updateShare(msg)
		case WizardServer:
			return w.updateServer(msg)
		}
	}
	return w, nil
}

// -- Step updates --

func (w WizardModel) updateWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		w.step = WizardChooseName
		w.nameInput.Focus()
	case "q":
		return w, tea.Quit
	}
	return w, nil
}

func (w WizardModel) updateChooseName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name, err := ValidateDisplayName(w.nameInput.Value())
		if err != nil {
			w.err = err.Error()
			return w, nil
		}
		w.chosenName = name
		w.err = ""
		w.step = WizardKeySelect
		// Wizard runs only on first launch (empty cfg.Servers — see
		// main.go ~line 99-101), so there are no per-server keys
		// folders to walk. Pass nil for extraDirs; the scanner falls
		// back to ~/.ssh/ only.
		w.keys = scanSSHKeys(nil)
	case "esc":
		w.step = WizardWelcome
		w.nameInput.Blur()
		w.err = ""
	case "q":
		return w, tea.Quit
	default:
		var cmd tea.Cmd
		w.nameInput, cmd = w.nameInput.Update(msg)
		return w, cmd
	}
	return w, nil
}

func (w WizardModel) updateKeySelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalItems := len(w.keys) + 2 // keys + import + generate

	switch msg.String() {
	case "up", "k":
		if w.keyCursor > 0 {
			w.keyCursor--
		}
	case "down", "j":
		if w.keyCursor < totalItems-1 {
			w.keyCursor++
		}
	case "enter":
		if w.keyCursor < len(w.keys) {
			// Selected an existing key
			key := w.keys[w.keyCursor]
			if key.Type != "ed25519" {
				w.err = "Only Ed25519 keys are supported. Select another or generate a new one."
				return w, nil
			}
			managedPath, fingerprint, err := w.copyKeyToManagedStoreAndRewriteName(key.Path)
			if err != nil {
				w.err = err.Error()
				return w, nil
			}
			w.result.KeyPath = managedPath
			w.keyFingerprint = fingerprint
			w.err = ""
			w.step = WizardBackup
		} else if w.keyCursor == len(w.keys) {
			// Import from file
			w.step = WizardKeyImport
			w.importInput.Focus()
		} else {
			// Generate new key. Clear any state the user might have
			// left from a previous visit to this step (typed
			// passphrase, stale strength hint) so re-entry is a
			// fresh slate.
			w.resetKeyGenState()
			w.step = WizardKeyGenerate
			w.genPathInput.Focus()
			w.genFocused = 0
		}
	case "esc":
		w.step = WizardChooseName
		w.nameInput.Focus()
		w.err = ""
	case "q":
		return w, tea.Quit
	}
	return w, nil
}

func (w WizardModel) updateKeyImport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		path := strings.TrimSpace(w.importInput.Value())
		if path == "" {
			return w, nil
		}
		// Expand ~
		path = config.ExpandUserPath(path)
		// Validate
		if _, err := os.Stat(path); err != nil {
			w.err = "File not found: " + path
			return w, nil
		}
		managedPath, fingerprint, err := w.copyKeyToManagedStoreAndRewriteName(path)
		if err != nil {
			w.err = err.Error()
			return w, nil
		}
		w.result.KeyPath = managedPath
		w.keyFingerprint = fingerprint
		w.err = ""
		w.step = WizardBackup
	case "esc":
		w.step = WizardKeySelect
		w.err = ""
	default:
		var cmd tea.Cmd
		w.importInput, cmd = w.importInput.Update(msg)
		return w, cmd
	}
	return w, nil
}

func (w WizardModel) updateKeyGenerate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		w.genFocused = (w.genFocused + 1) % 3
		w.genPathInput.Blur()
		w.genPassInput.Blur()
		w.genConfirm.Blur()
		switch w.genFocused {
		case 0:
			w.genPathInput.Focus()
		case 1:
			w.genPassInput.Focus()
		case 2:
			w.genConfirm.Focus()
		}
		return w, nil
	case "enter":
		return w.doGenerateKey()
	case "esc":
		// Bail back to KeySelect — user changed their mind. Don't
		// leave a typed passphrase or stale strength hint behind;
		// the next visit (whether back here or to a
		// different keysource) shouldn't see prior in-progress work.
		w.resetKeyGenState()
		w.step = WizardKeySelect
		w.err = ""
		return w, nil
	}

	var cmd tea.Cmd
	switch w.genFocused {
	case 0:
		w.genPathInput, cmd = w.genPathInput.Update(msg)
	case 1:
		w.genPassInput, cmd = w.genPassInput.Update(msg)
	case 2:
		w.genConfirm, cmd = w.genConfirm.Update(msg)
	}
	// Recompute the live strength hint after any input update — the
	// user might have edited the passphrase field (case 1) or the
	// display name context via a previous wizard step. Cheap enough
	// to run on every keystroke (zxcvbn is sub-millisecond for short
	// strings) and keeps the indicator in sync with what the user
	// sees.
	w.strengthHint = keygen.LivePassphraseHint(w.genPassInput.Value(), []string{w.chosenName})
	return w, cmd
}

func (w WizardModel) doGenerateKey() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(w.genPathInput.Value())
	pass := w.genPassInput.Value()
	confirm := w.genConfirm.Value()

	if path == "" {
		w.err = "Path is required"
		return w, nil
	}
	if pass != "" && pass != confirm {
		w.err = "Passphrases don't match"
		return w, nil
	}

	// Passphrase strength is advisory-only. Blank passphrases are a
	// deliberate user choice (unencrypted key), so non-blank weak
	// passphrases must not be harder to use than blank ones. The live
	// hint above carries the warning; submit only enforces mechanical
	// validity such as path presence and matching confirmation.

	// Expand ~ for storing in result (generateEd25519KeyFile does its own expansion)
	expandedPath := config.ExpandUserPath(path)

	fingerprint, err := generateEd25519KeyFile(path, pass, w.chosenName)
	if err != nil {
		w.err = "Key generation failed: " + err.Error()
		return w, nil
	}

	w.result.KeyPath = expandedPath
	w.keyFingerprint = fingerprint
	w.err = ""
	w.step = WizardBackup
	// Passphrase is consumed by generateEd25519KeyFile above and has
	// no further use — wipe it (along with the live hint) so it doesn't
	// sit in cleartext memory through the remaining wizard steps. Most
	// important of the three call sites:
	// the wizard's lifetime past this point is whatever it takes the
	// user to read Backup → Export → Share → Server, which can be
	// minutes. Treating the passphrase as ephemeral matches what an
	// agent or keyring would do.
	w.resetKeyGenState()
	return w, nil
}

func (w WizardModel) updateBackup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e", "enter":
		w.step = WizardExport
		w.exportInput.Focus()
	case "a":
		// Explicit acknowledgement: "I'll back it up myself — no recovery exists"
		w.step = WizardShare
	case "esc":
		// Go back to key selection rather than silently skipping
		w.step = WizardKeySelect
		w.err = ""
	}
	return w, nil
}

func (w WizardModel) updateExport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		dst := strings.TrimSpace(w.exportInput.Value())
		if dst == "" {
			return w, nil
		}
		dst = config.ExpandUserPath(dst)
		os.MkdirAll(filepath.Dir(dst), 0700)
		// Copy key file
		data, err := os.ReadFile(w.result.KeyPath)
		if err != nil {
			w.err = "Read failed: " + err.Error()
			return w, nil
		}
		if err := os.WriteFile(dst, data, 0600); err != nil {
			w.err = "Export failed: " + err.Error()
			return w, nil
		}
		// Also copy .pub
		pubData, _ := os.ReadFile(w.result.KeyPath + ".pub")
		if len(pubData) > 0 {
			os.WriteFile(dst+".pub", pubData, 0644)
		}
		w.err = ""
		w.step = WizardShare
	case "esc":
		w.step = WizardBackup
		w.err = ""
	default:
		var cmd tea.Cmd
		w.exportInput, cmd = w.exportInput.Update(msg)
		return w, cmd
	}
	return w, nil
}

func (w WizardModel) updateShare(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "c":
		// Copy full public key to clipboard
		pubKey := w.readPublicKey()
		if pubKey != "" {
			CopyToClipboard(pubKey)
			w.err = "Public key copied to clipboard"
		}
	case "enter":
		w.step = WizardServer
		w.serverInputs[0].Focus()
		w.serverFocused = 0
		w.err = ""
	case "esc":
		w.step = WizardBackup
		w.err = ""
	}
	return w, nil
}

func (w WizardModel) readPublicKey() string {
	data, err := os.ReadFile(w.result.KeyPath + ".pub")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (w WizardModel) updateServer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		w.serverInputs[w.serverFocused].Blur()
		w.serverFocused = (w.serverFocused + 1) % len(w.serverInputs)
		w.serverInputs[w.serverFocused].Focus()
		// Auto-fill name from host
		if w.serverFocused == 0 && w.serverInputs[0].Value() == "" {
			host := w.serverInputs[1].Value()
			if host != "" {
				w.serverInputs[0].SetValue(host)
			}
		}
		return w, nil
	case "enter":
		host := strings.TrimSpace(w.serverInputs[1].Value())
		// Host validation gates everything downstream: the
		// destination path <configDir>/<host>/keys/id_ed25519
		// is derived from this string, so any unsafe segment
		// (`/`, `..`, control bytes) would corrupt the per-server
		// folder layout. Catches the empty case too.
		if err := config.ValidateHost(host); err != nil {
			w.err = err.Error()
			return w, nil
		}
		name := strings.TrimSpace(w.serverInputs[0].Value())
		if name == "" {
			name = host
		}
		port := 2222
		if p := strings.TrimSpace(w.serverInputs[2].Value()); p != "" {
			fmt.Sscanf(p, "%d", &port)
		}
		// Always-copy finalize: move (or copy, depending on source
		// location) the wizard-staged key into the per-server
		// canonical location. This is the last step that can fail
		// — keep the user on this screen so they can retry without
		// losing the form values.
		finalPath, err := w.finalizeStagedKey(config.DefaultConfigDir(), host, w.result.KeyPath)
		if err != nil {
			w.err = "Finalize key failed: " + err.Error()
			return w, nil
		}
		w.result.KeyPath = finalPath
		w.result.ServerName = name
		w.result.ServerHost = host
		w.result.ServerPort = port
		w.result.PreferredName = w.chosenName
		w.step = WizardDone
		return w, tea.Quit
	case "esc":
		w.serverInputs[w.serverFocused].Blur()
		w.step = WizardShare
		w.err = ""
	case "q":
		return w, tea.Quit
	default:
		var cmd tea.Cmd
		w.serverInputs[w.serverFocused], cmd = w.serverInputs[w.serverFocused].Update(msg)
		return w, cmd
	}
	return w, nil
}

// handleMouse processes mouse clicks for wizard screens.
// Uses rendered view line counting to find clickable targets rather than
// hardcoded Y offsets, making it resilient to layout changes.
func (w WizardModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch w.step {
	case WizardWelcome:
		// Click anywhere → continue
		return w.updateWelcome(tea.KeyMsg{Type: tea.KeyEnter})

	case WizardChooseName:
		// Click anywhere focuses the text input (it's the only element)
		w.nameInput.Focus()

	case WizardKeySelect:
		line := w.clickLineInView(msg.Y)
		if line < 0 {
			break
		}
		// Find clickable items in rendered view
		view := w.viewKeySelect()
		lines := strings.Split(view, "\n")
		for i, l := range lines {
			if i != line {
				continue
			}
			// Check if this line matches a key entry or action button
			for ki, key := range w.keys {
				if strings.Contains(l, key.Path) {
					w.keyCursor = ki
					return w, nil
				}
			}
			if strings.Contains(l, "Import from file") {
				w.keyCursor = len(w.keys)
				return w, nil
			}
			if strings.Contains(l, "Generate new key") {
				w.keyCursor = len(w.keys) + 1
				return w, nil
			}
		}

	case WizardKeyImport:
		// Click focuses the text input
		w.importInput.Focus()

	case WizardKeyGenerate:
		// Click on a field label or input to focus that field
		line := w.clickLineInView(msg.Y)
		if line >= 0 {
			view := w.viewKeyGenerate()
			lines := strings.Split(view, "\n")
			if line < len(lines) {
				l := lines[line]
				if strings.Contains(l, "Save to") || line > 0 && line < len(lines) && strings.Contains(lines[max(0, line-1)], "Save to") {
					w.setGenFocus(0)
				} else if strings.Contains(l, "Passphrase") || line > 0 && strings.Contains(lines[max(0, line-1)], "Passphrase") {
					w.setGenFocus(1)
				} else if strings.Contains(l, "Confirm") || line > 0 && strings.Contains(lines[max(0, line-1)], "Confirm") {
					w.setGenFocus(2)
				}
			}
		}

	case WizardBackup:
		line := w.clickLineInView(msg.Y)
		if line >= 0 {
			view := w.viewBackup()
			lines := strings.Split(view, "\n")
			if line < len(lines) {
				l := lines[line]
				if strings.Contains(l, "[e]") || strings.Contains(l, "Export copy") {
					return w.updateBackup(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
				}
				if strings.Contains(l, "[a]") || strings.Contains(l, "back it up myself") {
					return w.updateBackup(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
				}
			}
		}

	case WizardExport:
		// Click focuses the text input
		w.exportInput.Focus()

	case WizardShare:
		line := w.clickLineInView(msg.Y)
		if line >= 0 {
			view := w.viewShare()
			lines := strings.Split(view, "\n")
			if line < len(lines) {
				if strings.Contains(lines[line], "[c]") || strings.Contains(lines[line], "Copy public key") {
					return w.updateShare(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
				}
			}
		}

	case WizardServer:
		// Click on a field label or input to focus that field
		line := w.clickLineInView(msg.Y)
		if line >= 0 {
			view := w.viewServer()
			lines := strings.Split(view, "\n")
			if line < len(lines) {
				l := lines[line]
				for i, label := range w.serverLabels {
					if strings.Contains(l, label) || (line > 0 && line < len(lines) && strings.Contains(lines[max(0, line-1)], label)) {
						w.serverInputs[w.serverFocused].Blur()
						w.serverFocused = i
						w.serverInputs[i].Focus()
						return w, nil
					}
				}
			}
		}
	}

	return w, nil
}

// clickLineInView converts a screen Y coordinate to a line index within
// the rendered view string. Returns -1 if out of bounds.
// dialogStyle has border(1) + padding(1) = content starts at Y=2.
func (w WizardModel) clickLineInView(screenY int) int {
	const contentY = 2
	line := screenY - contentY
	if line < 0 {
		return -1
	}
	return line
}

// setGenFocus switches focus between the key generation input fields.
func (w *WizardModel) setGenFocus(idx int) {
	w.genPathInput.Blur()
	w.genPassInput.Blur()
	w.genConfirm.Blur()
	w.genFocused = idx
	switch idx {
	case 0:
		w.genPathInput.Focus()
	case 1:
		w.genPassInput.Focus()
	case 2:
		w.genConfirm.Focus()
	}
}

// resetKeyGenState zeros transient state owned by the keygen step:
// the two passphrase fields and the live strength hint. Called on every
// transition into / out of /
// past WizardKeyGenerate so that:
//
//   - Entering the step (from KeySelect "Generate new key") is a
//     fresh slate, even if the user previously visited and Esc'd back.
//   - Leaving via Esc doesn't strand a typed passphrase in textinput
//     state, where it'd survive in cleartext for the rest of the
//     wizard's lifetime if the user later progresses via "Import" or
//     "Existing key" instead of finishing through generate.
//   - Completing the step doesn't either — the passphrase has been
//     consumed by generateEd25519KeyFile and has zero remaining
//     purpose for the Backup → Export → Share → Server steps.
//
// Mirrors the AddServer dialog's Hide()/Show()/Ctrl+G clearing.
// Same hygiene reasoning, scoped to step transitions instead of
// dialog open/close cycles.
func (w *WizardModel) resetKeyGenState() {
	w.genPassInput.SetValue("")
	w.genConfirm.SetValue("")
	w.strengthHint = keygen.LivePassphraseHint("", []string{w.chosenName})
}

// copyKeyToManagedStoreAndRewriteName copies an existing private/public key
// pair into the wizard staging directory at <configDir>/.staging/id_ed25519
// and rewrites the .pub comment to the chosen display name. This path is
// strict: any copy or rewrite failure aborts progression so setup cannot
// silently continue with mismatched identity data.
//
// The destination is staging — not the final per-server location — because
// the wizard reaches this step before the user has typed a server host
// (Welcome → ChooseName → KeySelect → here ... Backup → Export → Share →
// Server). Once the server step settles, finalizeStagedKey moves the
// staged pair into <configDir>/<host>/keys/id_ed25519. See that helper
// for the move-vs-copy logic.
func (w WizardModel) copyKeyToManagedStoreAndRewriteName(srcKeyPath string) (string, string, error) {
	src := config.ExpandUserPath(srcKeyPath)

	pubPath := src + ".pub"
	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		return "", "", fmt.Errorf("read public key failed (%s): %w", pubPath, err)
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		return "", "", fmt.Errorf("parse public key failed (%s): %w", pubPath, err)
	}
	if pubKey.Type() != "ssh-ed25519" {
		return "", "", fmt.Errorf("only Ed25519 keys are supported, got %s", pubKey.Type())
	}

	privData, err := os.ReadFile(src)
	if err != nil {
		return "", "", fmt.Errorf("read private key failed (%s): %w", src, err)
	}

	stagingDir := wizardStagingDir(config.DefaultConfigDir())
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		return "", "", fmt.Errorf("create staging directory failed (%s): %w", stagingDir, err)
	}

	dstBase := filepath.Join(stagingDir, "id_ed25519")

	if err := os.WriteFile(dstBase, privData, 0600); err != nil {
		return "", "", fmt.Errorf("write private key failed (%s): %w", dstBase, err)
	}

	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey)))
	if w.chosenName != "" {
		pubLine += " " + w.chosenName
	}
	pubLine += "\n"
	if err := os.WriteFile(dstBase+".pub", []byte(pubLine), 0644); err != nil {
		// Roll back the private-key write so we don't leave a
		// private-only orphan in the staging dir. Matches the
		// addserver.go copyKeyForServer pattern.
		_ = os.Remove(dstBase)
		return "", "", fmt.Errorf("write public key failed (%s.pub): %w", dstBase, err)
	}

	fingerprint := ssh.FingerprintSHA256(pubKey)
	return dstBase, fingerprint, nil
}

// wizardStagingDir returns the transient staging directory used during
// wizard setup. Keys land here (either via copyKeyToManagedStoreAndRewriteName
// for the import/select flow, or via the default genPathInput value for
// the generate flow) before the user has chosen a server host. Once the
// server step completes, finalizeStagedKey moves them into
// <configDir>/<host>/keys/.
//
// Leading dot keeps the dir hidden so `ls <configDir>` doesn't show
// transient setup state alongside the user's real server folders.
// Removed by finalizeStagedKey after a successful wizard run; if the
// user cancels mid-wizard, the next run overwrites in place.
func wizardStagingDir(configDir string) string {
	return filepath.Join(configDir, ".staging")
}

// finalizeStagedKey moves or copies the wizard-staged key into its
// final per-server location at <configDir>/<host>/keys/id_ed25519.
// Called once from the wizard's server step, after host validation.
//
// Two source-aware behaviors:
//
//   - Source is inside wizardStagingDir(configDir): rename in-place
//     (no second copy) and remove the staging dir afterward. Cleans
//     up the transient state once the user commits to a server entry.
//   - Source is anywhere else (user-customized generate path that
//     wrote outside staging): copy bytes into the per-server folder,
//     leave the source alone. The user's chosen location is theirs
//     to keep; the canonical copy under <configDir>/<host>/ is the
//     one the app reads from runtime onward.
//
// Either way, the per-server destination is populated — always-copy
// semantics hold regardless of source location. Caller updates
// result.KeyPath with the returned canonical path.
func (w WizardModel) finalizeStagedKey(configDir, host, srcKeyPath string) (string, error) {
	if err := config.ValidateHost(host); err != nil {
		return "", err
	}
	keysDir := config.ServerKeysDir(configDir, host)
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", fmt.Errorf("create per-server keys dir (%s): %w", keysDir, err)
	}
	dst := config.ServerKeyPath(configDir, host)

	stagingDir := wizardStagingDir(configDir)
	cleanSrc := filepath.Clean(srcKeyPath)
	insideStaging := strings.HasPrefix(cleanSrc, filepath.Clean(stagingDir)+string(filepath.Separator))

	if insideStaging {
		if err := os.Rename(srcKeyPath, dst); err != nil {
			return "", fmt.Errorf("move staged private key (%s → %s): %w", srcKeyPath, dst, err)
		}
		if err := os.Rename(srcKeyPath+".pub", dst+".pub"); err != nil {
			// Best-effort rollback of the private-key move so we
			// don't leave a private-only orphan at the destination.
			_ = os.Rename(dst, srcKeyPath)
			return "", fmt.Errorf("move staged public key (%s.pub → %s.pub): %w", srcKeyPath, dst, err)
		}
		// Best-effort staging cleanup. Failure is fine — the dir is
		// transient and will be overwritten on the next wizard run.
		_ = os.RemoveAll(stagingDir)
		return dst, nil
	}

	// Custom user-chosen generate path. Copy bytes, leave source alone.
	privData, err := os.ReadFile(srcKeyPath)
	if err != nil {
		return "", fmt.Errorf("read source private key (%s): %w", srcKeyPath, err)
	}
	pubData, err := os.ReadFile(srcKeyPath + ".pub")
	if err != nil {
		return "", fmt.Errorf("read source public key (%s.pub): %w", srcKeyPath, err)
	}
	if err := os.WriteFile(dst, privData, 0600); err != nil {
		return "", fmt.Errorf("write per-server private key (%s): %w", dst, err)
	}
	if err := os.WriteFile(dst+".pub", pubData, 0644); err != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("write per-server public key (%s.pub): %w", dst, err)
	}
	return dst, nil
}

// -- View --

func (w WizardModel) View() string {
	switch w.step {
	case WizardWelcome:
		return w.viewWelcome()
	case WizardChooseName:
		return w.viewChooseName()
	case WizardKeySelect:
		return w.viewKeySelect()
	case WizardKeyImport:
		return w.viewKeyImport()
	case WizardKeyGenerate:
		return w.viewKeyGenerate()
	case WizardBackup:
		return w.viewBackup()
	case WizardExport:
		return w.viewExport()
	case WizardShare:
		return w.viewShare()
	case WizardServer:
		return w.viewServer()
	default:
		return ""
	}
}

func (w WizardModel) viewWelcome() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" sshkey-chat"))
	b.WriteString("\n\n")
	b.WriteString("  Welcome to sshkey-chat\n")
	b.WriteString("  Private messaging over SSH with\n")
	b.WriteString("  end-to-end encryption.\n\n")
	b.WriteString("  Let's get you set up.\n\n")
	b.WriteString("  " + searchHeaderStyle.Render("[Continue]"))
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewChooseName() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Choose Your Name"))
	b.WriteString("\n\n")
	b.WriteString("  This will be your display name on the server.\n")
	b.WriteString("  Your admin can change it if needed.\n\n")
	b.WriteString("  Display name:\n")
	b.WriteString("  " + w.nameInput.View() + "\n\n")
	b.WriteString(helpDescStyle.Render("  Enter=continue  Esc=back  q=quit"))
	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewKeySelect() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" SSH Key"))
	b.WriteString("\n\n")

	if len(w.keys) == 0 {
		b.WriteString("  No Ed25519 keys found.\n\n")
	} else {
		b.WriteString("  Select your SSH key:\n\n")
		for i, key := range w.keys {
			line := "  " + key.Path
			if key.Type != "ed25519" {
				line += helpDescStyle.Render(fmt.Sprintf(" (%s)", key.Type))
			}
			if i == w.keyCursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n  ─────────────────────────────────\n\n")
	}

	// Import and generate options
	importLine := "  [Import from file]"
	genLine := "  [Generate new key]"

	if w.keyCursor == len(w.keys) {
		importLine = completionSelectedStyle.Render(importLine)
	}
	if w.keyCursor == len(w.keys)+1 {
		genLine = completionSelectedStyle.Render(genLine)
	}

	b.WriteString(importLine + "\n")
	b.WriteString(genLine + "\n\n")
	b.WriteString(helpDescStyle.Render("  Only Ed25519 keys are supported."))

	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}

	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewKeyImport() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Import Key"))
	b.WriteString("\n\n")
	b.WriteString("  Path to SSH private key:\n")
	b.WriteString("  " + w.importInput.View() + "\n\n")
	b.WriteString(helpDescStyle.Render("  Enter=import  Esc=back"))
	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewKeyGenerate() string {
	inputWidth := keygenInputWidth(w.width)
	genPathInput := w.genPathInput
	genPassInput := w.genPassInput
	genConfirm := w.genConfirm
	setKeygenInputWidths(inputWidth, &genPathInput, &genPassInput, &genConfirm)

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Generate Key"))
	b.WriteString("\n\n")
	b.WriteString("  Save to:\n")
	b.WriteString("  " + genPathInput.View() + "\n\n")
	b.WriteString("  Passphrase (recommended):\n")
	b.WriteString("  " + genPassInput.View() + "\n")
	// Phase 16 Gap 4: live strength indicator, one line under the
	// passphrase field. Empty shows the unencrypted-key advisory
	// immediately; typing any character switches to zxcvbn feedback.
	if hint := renderStrengthHint(w.strengthHint); hint != "" {
		b.WriteString("  " + hint + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  Confirm passphrase:\n")
	b.WriteString("  " + genConfirm.View() + "\n\n")
	b.WriteString(helpDescStyle.Render("  ⚠ A passphrase protects your key if your\n  device is stolen. Strongly recommended."))
	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render("  Tab=next field  Enter=generate  Esc=back"))
	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewBackup() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Back Up Your Key"))
	b.WriteString("\n\n")
	b.WriteString("  This key is your identity. If you lose it,\n")
	b.WriteString("  you lose access to your account and all\n")
	b.WriteString("  encrypted message history. The server\n")
	b.WriteString("  cannot recover your account.\n\n")
	b.WriteString("  Your key:\n")
	b.WriteString("  " + w.result.KeyPath + "\n\n")
	b.WriteString("  " + searchHeaderStyle.Render("[e] Export copy to file") + "\n")
	b.WriteString("  " + helpDescStyle.Render("[a] I'll back it up myself — I understand there is no recovery") + "\n\n")
	b.WriteString(helpDescStyle.Render("  Esc=go back"))
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewExport() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Export Key"))
	b.WriteString("\n\n")
	b.WriteString("  Save a backup copy to:\n")
	b.WriteString("  " + w.exportInput.View() + "\n\n")
	b.WriteString(helpDescStyle.Render("  Enter=save  Esc=back"))
	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}
	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewShare() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Share With Your Admin"))
	b.WriteString("\n\n")
	b.WriteString("  Your server admin needs your public key\n")
	b.WriteString("  to add you to the server.\n\n")

	if w.chosenName != "" {
		b.WriteString("  Name: " + searchHeaderStyle.Render(w.chosenName) + "\n")
	}
	b.WriteString("  Fingerprint: " + searchHeaderStyle.Render(w.keyFingerprint) + "\n\n")

	pubKey := w.readPublicKey()
	if pubKey != "" {
		display := pubKey
		if len(display) > 50 {
			display = display[:50] + "..."
		}
		b.WriteString("  Public key (includes your name):\n")
		b.WriteString("  " + helpDescStyle.Render(display) + "\n\n")
	}

	b.WriteString("  " + searchHeaderStyle.Render("[c] Copy public key to clipboard") + "\n\n")
	b.WriteString(helpDescStyle.Render("  Send this to your admin via a trusted channel."))
	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render("  Enter=continue  Esc=back"))

	if w.err != "" {
		b.WriteString("\n\n  " + checkStyle.Render(w.err))
	}

	return dialogStyle.Render(b.String())
}

func (w WizardModel) viewServer() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Connect to Server"))
	b.WriteString("\n\n")

	for i, label := range w.serverLabels {
		b.WriteString("  " + label + ":\n")
		b.WriteString("  " + w.serverInputs[i].View() + "\n\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=next field  Enter=connect  Esc=back  q=quit"))
	if w.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(w.err))
	}
	return dialogStyle.Render(b.String())
}

// IsComplete returns true if the wizard finished successfully.
func (w WizardModel) IsComplete() bool {
	return w.step == WizardDone
}

// Result returns the wizard output.
func (w WizardModel) Result() WizardResult {
	return w.result
}
