package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"
)

// WizardStep tracks which screen the wizard is on.
type WizardStep int

const (
	WizardWelcome WizardStep = iota
	WizardKeySelect
	WizardKeyImport
	WizardKeyGenerate
	WizardBackup
	WizardExport
	WizardServer
	WizardDone
)

// WizardResult is returned when the wizard completes.
type WizardResult struct {
	KeyPath    string
	ServerName string
	ServerHost string
	ServerPort int
}

// WizardModel manages the first-launch setup wizard.
type WizardModel struct {
	step     WizardStep
	result   WizardResult
	err      string
	width    int
	height   int

	// Key selection
	keys     []keyEntry
	keyCursor int

	// Key generation
	genPathInput   textinput.Model
	genPassInput   textinput.Model
	genConfirm     textinput.Model
	genFocused     int // 0=path, 1=pass, 2=confirm

	// Import
	importInput textinput.Model

	// Export
	exportInput textinput.Model

	// Server
	serverInputs []textinput.Model
	serverFocused int
	serverLabels []string

	// Key fingerprint (set after selection/generation)
	keyFingerprint string
}

// NewWizard creates the setup wizard.
func NewWizard() WizardModel {
	// Key generation inputs
	genPath := textinput.New()
	home, _ := os.UserHomeDir()
	genPath.SetValue(filepath.Join(home, ".sshkey-chat", "keys", "id_ed25519"))
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

	// Export input
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

	case tea.KeyMsg:
		// Global
		if msg.String() == "ctrl+c" {
			return w, tea.Quit
		}

		switch w.step {
		case WizardWelcome:
			return w.updateWelcome(msg)
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
		case WizardServer:
			return w.updateServer(msg)
		}
	}
	return w, nil
}

// -- Step updates --

func (w WizardModel) updateWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		w.step = WizardKeySelect
		w.keys = scanSSHKeys()
		return w, nil
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
			w.result.KeyPath = key.Path
			w.keyFingerprint = w.computeFingerprint(key.Path)
			w.err = ""
			w.step = WizardBackup
		} else if w.keyCursor == len(w.keys) {
			// Import from file
			w.step = WizardKeyImport
			w.importInput.Focus()
		} else {
			// Generate new key
			w.step = WizardKeyGenerate
			w.genPathInput.Focus()
			w.genFocused = 0
		}
	case "esc":
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
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		}
		// Validate
		if _, err := os.Stat(path); err != nil {
			w.err = "File not found: " + path
			return w, nil
		}
		// Check it's Ed25519
		data, _ := os.ReadFile(path + ".pub")
		if len(data) > 0 && !strings.HasPrefix(string(data), "ssh-ed25519") {
			w.err = "Not an Ed25519 key"
			return w, nil
		}
		w.result.KeyPath = path
		w.keyFingerprint = w.computeFingerprint(path)
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

	// Expand ~
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}

	// Generate Ed25519 key
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		w.err = "Key generation failed: " + err.Error()
		return w, nil
	}

	// Marshal to OpenSSH format
	var pemBlock *pem.Block
	if pass != "" {
		pemBlock, err = ssh.MarshalPrivateKeyWithPassphrase(privKey, "", []byte(pass))
	} else {
		pemBlock, err = ssh.MarshalPrivateKey(privKey, "")
	}
	if err != nil {
		w.err = "Marshal failed: " + err.Error()
		return w, nil
	}

	privPEM := pem.EncodeToMemory(pemBlock)

	// Write private key
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, privPEM, 0600); err != nil {
		w.err = "Write failed: " + err.Error()
		return w, nil
	}

	// Write public key
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err == nil {
		pubLine := string(ssh.MarshalAuthorizedKey(sshPub))
		os.WriteFile(path+".pub", []byte(pubLine), 0644)
	}

	w.result.KeyPath = path
	w.keyFingerprint = ssh.FingerprintSHA256(sshPub)
	w.err = ""
	w.step = WizardBackup
	return w, nil
}

func (w WizardModel) updateBackup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e", "enter":
		w.step = WizardExport
		w.exportInput.Focus()
	case "l", "s", "esc":
		// "I'll do it later" / skip
		w.step = WizardServer
		w.serverInputs[0].Focus()
		w.serverFocused = 0
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
		if strings.HasPrefix(dst, "~/") {
			home, _ := os.UserHomeDir()
			dst = filepath.Join(home, dst[2:])
		}
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
		w.step = WizardServer
		w.serverInputs[0].Focus()
		w.serverFocused = 0
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
		if host == "" {
			w.err = "Host is required"
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
		w.result.ServerName = name
		w.result.ServerHost = host
		w.result.ServerPort = port
		w.step = WizardDone
		return w, tea.Quit
	case "esc":
		return w, tea.Quit
	default:
		var cmd tea.Cmd
		w.serverInputs[w.serverFocused], cmd = w.serverInputs[w.serverFocused].Update(msg)
		return w, cmd
	}
}

func (w WizardModel) computeFingerprint(keyPath string) string {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return ""
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Try without passphrase parsing
		return ""
	}
	return ssh.FingerprintSHA256(signer.PublicKey())
}

// -- View --

func (w WizardModel) View() string {
	switch w.step {
	case WizardWelcome:
		return w.viewWelcome()
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
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Generate Key"))
	b.WriteString("\n\n")
	b.WriteString("  Save to:\n")
	b.WriteString("  " + w.genPathInput.View() + "\n\n")
	b.WriteString("  Passphrase (recommended):\n")
	b.WriteString("  " + w.genPassInput.View() + "\n\n")
	b.WriteString("  Confirm passphrase:\n")
	b.WriteString("  " + w.genConfirm.View() + "\n\n")
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
	b.WriteString("  encrypted message history.\n\n")
	b.WriteString("  Your key:\n")
	b.WriteString("  " + w.result.KeyPath + "\n\n")
	b.WriteString("  " + searchHeaderStyle.Render("[e] Export copy to file") + "  " + helpDescStyle.Render("[l] I'll do it later") + "\n\n")
	b.WriteString(helpDescStyle.Render("  You can always export later from Settings."))
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

func (w WizardModel) viewServer() string {
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Connect to Server"))
	b.WriteString("\n\n")

	for i, label := range w.serverLabels {
		b.WriteString("  " + label + ":\n")
		b.WriteString("  " + w.serverInputs[i].View() + "\n\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=next field  Enter=connect  Esc=quit"))
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
