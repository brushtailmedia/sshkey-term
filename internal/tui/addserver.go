package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/keygen"
)

// addServerMode distinguishes the form and generate sub-views.
type addServerMode int

const (
	addServerForm addServerMode = iota
	addServerGenerate
)

// AddServerModel manages the add server dialog.
type AddServerModel struct {
	visible bool
	mode    addServerMode
	inputs  []textinput.Model
	focused int // 0=name, 1=host, 2=port, 3=key
	labels  []string

	// Scanned keys from ~/.ssh (Ed25519 only, for quick selection)
	scannedKeys []keyEntry

	// Generate sub-view
	genInputs  []textinput.Model // 0=path, 1=passphrase, 2=confirm
	genFocused int
	genErr     string
	genNotice  string // shown in form view after successful generation

	// Phase 16 Gap 4: zxcvbn warn-and-confirm state. When the user
	// submits a borderline passphrase (warn tier), we display the
	// warning and stash the passphrase here. If they re-submit with
	// the same value unchanged, treat it as confirmation and proceed.
	weakPassConfirmed string

	// Live strength hint — recomputed on every keystroke while the
	// generate-key dialog is open. Rendered as a compact one-line
	// indicator under the passphrase input, hidden below
	// MinPassphraseLength. Context includes hostname + display name
	// (see addServerZxcvbnContext).
	strengthHint keygen.LiveHint
}

// AddServerMsg is sent when the user confirms adding a server.
type AddServerMsg struct {
	Name string
	Host string
	Port int
	Key  string
}

// addServerZxcvbnContext collects form-field values to pass to zxcvbn
// as context strings, so a passphrase containing the display name or
// hostname the user just typed gets penalized. Mirrors the wizard's
// ValidateUserPassphraseWithContext call which passes the chosen
// display name. Port is omitted (low signal; usually just digits).
//
// Empty / whitespace-only values are skipped — zxcvbn does exact
// substring matching, so empty strings would be no-ops but noisy.
func addServerZxcvbnContext(a AddServerModel) []string {
	var ctx []string
	if name := strings.TrimSpace(a.inputs[0].Value()); name != "" {
		ctx = append(ctx, name)
	}
	if host := strings.TrimSpace(a.inputs[1].Value()); host != "" {
		ctx = append(ctx, host)
	}
	return ctx
}

func NewAddServer() AddServerModel {
	labels := []string{"Name", "Host", "Port", "SSH key path"}

	inputs := make([]textinput.Model, 4)
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = ""
		inputs[i].CharLimit = 256
	}

	inputs[0].Placeholder = "My Server"
	inputs[1].Placeholder = "chat.example.com"
	inputs[2].Placeholder = "2222"
	inputs[2].SetValue("2222")
	inputs[3].Placeholder = "~/.ssh/id_ed25519"

	// Generate inputs
	genInputs := make([]textinput.Model, 3)
	for i := range genInputs {
		genInputs[i] = textinput.New()
		genInputs[i].Prompt = ""
	}
	home, _ := os.UserHomeDir()
	genInputs[0].SetValue(filepath.Join(home, ".sshkey-term", "keys", "id_ed25519"))
	genInputs[1].Placeholder = "passphrase"
	genInputs[1].EchoMode = textinput.EchoPassword
	genInputs[2].Placeholder = "confirm passphrase"
	genInputs[2].EchoMode = textinput.EchoPassword

	return AddServerModel{
		inputs:    inputs,
		labels:    labels,
		genInputs: genInputs,
	}
}

func (a *AddServerModel) Show() {
	a.visible = true
	a.mode = addServerForm
	a.focused = 0
	a.genErr = ""
	a.genNotice = ""
	for i := range a.inputs {
		if i == 2 {
			a.inputs[i].SetValue("2222")
		} else {
			a.inputs[i].SetValue("")
		}
	}
	a.inputs[0].Focus()

	// Scan ~/.ssh for existing Ed25519 keys (filters non-ed25519 out)
	all := scanSSHKeys()
	a.scannedKeys = a.scannedKeys[:0]
	for _, k := range all {
		if k.Type == "ed25519" {
			a.scannedKeys = append(a.scannedKeys, k)
		}
	}
}

func (a *AddServerModel) Hide() {
	a.visible = false
	a.mode = addServerForm
	a.genErr = ""
	a.genNotice = ""
	for i := range a.inputs {
		a.inputs[i].Blur()
	}
	for i := range a.genInputs {
		a.genInputs[i].Blur()
	}
}

func (a *AddServerModel) IsVisible() bool {
	return a.visible
}

func (a AddServerModel) Update(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	if a.mode == addServerGenerate {
		return a.updateGenerate(msg)
	}
	return a.updateForm(msg)
}

func (a AddServerModel) updateForm(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.Hide()
		return a, nil

	case "ctrl+g":
		// Enter generate sub-view
		a.mode = addServerGenerate
		for i := range a.inputs {
			a.inputs[i].Blur()
		}
		a.genFocused = 0
		a.genInputs[0].Focus()
		a.genErr = ""
		return a, nil

	case "tab", "down":
		a.inputs[a.focused].Blur()
		a.focused = (a.focused + 1) % len(a.inputs)
		a.inputs[a.focused].Focus()
		return a, nil

	case "shift+tab", "up":
		a.inputs[a.focused].Blur()
		a.focused--
		if a.focused < 0 {
			a.focused = len(a.inputs) - 1
		}
		a.inputs[a.focused].Focus()
		return a, nil

	case "enter", "ctrl+enter":
		// Validate and submit
		name := strings.TrimSpace(a.inputs[0].Value())
		host := strings.TrimSpace(a.inputs[1].Value())
		portStr := strings.TrimSpace(a.inputs[2].Value())
		key := strings.TrimSpace(a.inputs[3].Value())

		if host == "" {
			return a, nil
		}
		if name == "" {
			name = host
		}

		port := 2222
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}

		if key == "" {
			key = "~/.ssh/id_ed25519"
		}

		a.Hide()
		return a, func() tea.Msg {
			return AddServerMsg{
				Name: name,
				Host: host,
				Port: port,
				Key:  key,
			}
		}
	}

	var cmd tea.Cmd
	a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
	return a, cmd
}

func (a AddServerModel) updateGenerate(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Return to form mode without generating
		a.mode = addServerForm
		for i := range a.genInputs {
			a.genInputs[i].Blur()
		}
		a.inputs[a.focused].Focus()
		a.genErr = ""
		return a, nil

	case "tab", "down":
		a.genInputs[a.genFocused].Blur()
		a.genFocused = (a.genFocused + 1) % len(a.genInputs)
		a.genInputs[a.genFocused].Focus()
		return a, nil

	case "shift+tab", "up":
		a.genInputs[a.genFocused].Blur()
		a.genFocused--
		if a.genFocused < 0 {
			a.genFocused = len(a.genInputs) - 1
		}
		a.genInputs[a.genFocused].Focus()
		return a, nil

	case "enter":
		path := strings.TrimSpace(a.genInputs[0].Value())
		pass := a.genInputs[1].Value()
		confirm := a.genInputs[2].Value()

		if path == "" {
			a.genErr = "Path is required"
			return a, nil
		}
		if pass != confirm {
			a.genErr = "Passphrases don't match"
			return a, nil
		}

		// Phase 16 Gap 4: zxcvbn passphrase strength check. Same
		// three-tier policy as the wizard:
		//   - block tier: hard error, user must change passphrase
		//   - warn tier: show warning, set weakPassConfirmed; if the
		//     user re-submits with the same value, proceed
		//   - silent pass: proceed immediately
		// Empty passphrase is allowed (matches existing behavior;
		// generates an unencrypted key).
		if pass != "" {
			// Pass hostname + username as zxcvbn context so passphrases
			// containing either (e.g. "sshkey.example.com" or the
			// chosen display name) get penalized. Mirrors the wizard's
			// context-awareness — main form inputs are available here
			// because the user fills the form before opening the
			// keygen dialog.
			context := addServerZxcvbnContext(a)
			result := keygen.ValidateUserPassphraseWithContext(pass, context)
			switch {
			case result.Blocked:
				a.genErr = result.Message
				a.weakPassConfirmed = ""
				return a, nil
			case result.Warning != "":
				if a.weakPassConfirmed != pass {
					a.weakPassConfirmed = pass
					a.genErr = result.Warning + " Press Enter again to use it anyway, or edit to try a stronger one."
					return a, nil
				}
				// User has already seen the warning and re-submitted
				// with the same passphrase — fall through to keygen.
			}
		}
		a.weakPassConfirmed = ""

		// Don't silently overwrite an existing file
		expanded := path
		if strings.HasPrefix(expanded, "~/") {
			home, _ := os.UserHomeDir()
			expanded = filepath.Join(home, expanded[2:])
		}
		if _, err := os.Stat(expanded); err == nil {
			a.genErr = "File already exists: " + expanded
			return a, nil
		}

		fingerprint, err := generateEd25519KeyFile(path, pass)
		if err != nil {
			a.genErr = "Generation failed: " + err.Error()
			return a, nil
		}

		// Success: fill key path in main form, return to form view
		a.inputs[3].SetValue(expanded)
		a.genNotice = "✓ Key generated (" + fingerprint + ") — back it up"
		a.mode = addServerForm
		for i := range a.genInputs {
			a.genInputs[i].Blur()
		}
		a.focused = 3
		a.inputs[3].Focus()
		a.genErr = ""

		// Rescan keys so the newly-generated one can appear in the list
		// (it was written to ~/.sshkey-term/keys/... by default, not ~/.ssh/,
		// so it typically won't — but rescan covers custom paths under ~/.ssh)
		all := scanSSHKeys()
		a.scannedKeys = a.scannedKeys[:0]
		for _, k := range all {
			if k.Type == "ed25519" {
				a.scannedKeys = append(a.scannedKeys, k)
			}
		}
		return a, nil
	}

	var cmd tea.Cmd
	a.genInputs[a.genFocused], cmd = a.genInputs[a.genFocused].Update(msg)
	// Recompute the live strength hint after any field update — the
	// user may have just typed in the passphrase field, or edited the
	// name / host fields on a previous dialog state change (context
	// for zxcvbn). Cheap enough to run on every keystroke.
	a.strengthHint = keygen.LivePassphraseHint(a.genInputs[1].Value(), addServerZxcvbnContext(a))
	return a, cmd
}

// HandleMouse processes a mouse event while the dialog is visible. Returns
// the updated model and a command. Routed to this method from the app's
// top-level mouse handler when IsVisible() is true.
func (a AddServerModel) HandleMouse(msg tea.MouseMsg) (AddServerModel, tea.Cmd) {
	// Only handle the form view — generate view is keyboard-only for now
	if a.mode != addServerForm {
		return a, nil
	}

	// Only react to left-click releases
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return a, nil
	}

	// Check form field lines: clicking on a field focuses it.
	// Layout (see viewForm):
	//   Y=0: top border
	//   Y=1: top padding
	//   Y=2: " Add Server" header
	//   Y=3: blank
	//   Y=4..5: Name label + input (input on Y=4)
	//   Y=6..7: Host
	//   Y=8..9: Port
	//   Y=10..11: SSH key path
	fieldStartY := 4
	for i := range a.inputs {
		fieldY := fieldStartY + i*2
		if msg.Y == fieldY {
			a.inputs[a.focused].Blur()
			a.focused = i
			a.inputs[i].Focus()
			return a, nil
		}
	}

	// Check scanned key lines
	keyStartY := a.keyListStartY()
	for i, entry := range a.scannedKeys {
		if msg.Y == keyStartY+i {
			// Select this key — fill the key path input
			a.inputs[3].SetValue(entry.Path)
			a.inputs[a.focused].Blur()
			a.focused = 3
			a.inputs[3].Focus()
			return a, nil
		}
	}

	return a, nil
}

// keyListStartY computes the Y position of the first scanned-key entry in the
// rendered form view. Must match viewForm()'s layout exactly — change both
// together.
func (a AddServerModel) keyListStartY() int {
	// Border(1) + padding(1) + header(1) + blank(1) + 4 fields * 2 = 12
	y := 12
	if a.genNotice != "" {
		y += 2 // notice line + blank
	}
	if len(a.scannedKeys) > 0 {
		y += 2 // "Existing Ed25519 keys:" header + blank
	}
	return y
}

func (a AddServerModel) View(width int) string {
	if !a.visible {
		return ""
	}

	if a.mode == addServerGenerate {
		return a.viewGenerate(width)
	}
	return a.viewForm(width)
}

func (a AddServerModel) viewForm(width int) string {
	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Add Server"))
	b.WriteString("\n\n")

	for i, label := range a.labels {
		b.WriteString("  " + label + ": ")
		b.WriteString(a.inputs[i].View())
		b.WriteString("\n\n")
	}

	if a.genNotice != "" {
		b.WriteString("  " + helpDescStyle.Render(a.genNotice) + "\n\n")
	}

	if len(a.scannedKeys) > 0 {
		b.WriteString("  " + helpDescStyle.Render("Existing Ed25519 keys (click to use):") + "\n\n")
		for _, entry := range a.scannedKeys {
			// Shorten path by replacing $HOME with ~
			display := entry.Path
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(display, home) {
				display = "~" + display[len(home):]
			}
			b.WriteString("  " + display + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=next field  Ctrl+g=generate new key  Enter=add  Esc=cancel"))

	return dialogStyle.Width(width - 4).Render(b.String())
}

func (a AddServerModel) viewGenerate(width int) string {
	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Generate New Key"))
	b.WriteString("\n\n")

	genLabels := []string{"Save to", "Passphrase (recommended)", "Confirm passphrase"}
	for i, label := range genLabels {
		b.WriteString("  " + label + ":\n")
		b.WriteString("  " + a.genInputs[i].View() + "\n")
		// Phase 16 Gap 4: live strength hint under the passphrase
		// input (index 1 only). Hidden under MinPassphraseLength —
		// renderStrengthHint returns an empty string for HintHidden.
		if i == 1 {
			if hint := renderStrengthHint(a.strengthHint); hint != "" {
				b.WriteString("  " + hint + "\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(helpDescStyle.Render("  ⚠ A passphrase protects your key if your device is stolen.\n  Back the key up after generating — the server cannot recover it."))
	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render("  Tab=next field  Enter=generate  Esc=back"))

	if a.genErr != "" {
		b.WriteString("\n\n  " + errorStyle.Render(a.genErr))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
