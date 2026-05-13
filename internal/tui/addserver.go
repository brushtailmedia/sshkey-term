package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/config"
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
	// focused tracks input focus across two zones:
	//   0..3                    — the four form fields (name/host/port/key)
	//   len(inputs) (i.e. 4)    — sentinel meaning "selection is in the
	//                             scanned-keys list below the form"; the
	//                             active list row lives in keyCursor.
	// Tab/Shift+Tab cycle within 0..3 only. Down from field 3 enters
	// the list (focused=4, keyCursor=0); Up at the top of the list
	// returns to field 3. Mouse click on a list row jumps directly to
	// focused=3 with the path filled — same end-state as keyboard
	// Enter on a row, just without the intermediate "in list" focus.
	focused int
	labels  []string

	// Scanned keys from ~/.ssh (Ed25519 only, for quick selection)
	scannedKeys []keyEntry
	// keyCursor is the highlighted row index within scannedKeys when
	// focused == len(inputs). Undefined otherwise (we don't read it
	// unless focused is in the list-zone). Reset to 0 on Show.
	keyCursor int

	// Generate sub-view
	genInputs  []textinput.Model // 0=path, 1=passphrase, 2=confirm
	genFocused int
	genErr     string
	genNotice  string // shown in form view after successful generation

	// Form-level error (e.g. failed key copy on submit). Rendered in
	// viewForm under the existing notice; cleared on each new submit.
	formErr string

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

	// scanDirsFn returns the list of per-server keys folders to walk
	// when populating the scanned-keys list. Called lazily from
	// rescanEd25519Keys so the list reflects the LIVE app config
	// (server added then removed then re-added all show up correctly).
	// Production wires it to a closure capturing `appCfg.Servers +
	// configDir` from main.go via tui.New; tests pass nil — when nil
	// the scanner falls back to scanning ~/.ssh/ only, which is the
	// natural test default.
	scanDirsFn func() []string
}

// AddServerMsg is sent when the user confirms adding a server. The
// key path isn't carried on the message — keyCopyFn has already
// copied the source key into <configDir>/<host>/keys/id_ed25519 by
// submit time, and downstream consumers derive the canonical path
// via config.ServerKeyPath when they need it.
type AddServerMsg struct {
	Name string
	Host string
	Port int
}

// keyCopyFn is the function the submit handler uses to copy keys
// into the managed folder. Production points it at copyKeyForServer;
// tests can swap it for a passthrough so they don't need real key
// files on disk. Package-level variable so the swap is local-effect
// without exposing a fake interface to callers.
var keyCopyFn = copyKeyForServer

// copyKeyForServer copies an SSH key (private + .pub) into the
// app-managed folder under a host-derived filename, returning the
// final destination path. Pattern parallels the wizard's
// copyKeyToManagedStoreAndRewriteName, but doesn't rewrite the
// .pub comment — the user's display name on this server isn't
// known at add-server time (the server assigns / user picks it
// during the first connect, separate from the .pub comment which
// is purely cosmetic at the protocol level).
//
// The point of always copying — even when the source is already
// in the managed folder — is per-server file separation: each
// server gets its own keys folder at `<configDir>/<host>/keys/`,
// with a fixed `id_ed25519` filename inside. This way:
//   - Deleting a server (config.RemoveServer) is a single
//     `os.RemoveAll(<configDir>/<host>/)` — keys go with the
//     server's data, no orphan files in a shared keys namespace.
//   - Each server's folder owns ONE key. The host (folder name)
//     makes ownership visible at filesystem level — no
//     fingerprint-derived names, no host-suffixed filenames.
//   - Future per-server edits (e.g. re-encrypting with a new
//     passphrase for one server but not another) don't disturb
//     siblings.
//
// Same physical bytes in two folders = same cryptographic identity.
// Whether you WANT one identity across servers vs separate keys
// per server is a security choice the user makes by selecting
// (reuse) vs generating (Ctrl+G).
//
// Source-equals-destination is idempotent (no-op, returns the
// path). Destination-already-exists is an error rather than a
// silent overwrite. Host validation runs first — defense in depth
// against malformed values reaching the filesystem layer.
func copyKeyForServer(srcKeyPath, host string) (string, error) {
	if err := config.ValidateHost(host); err != nil {
		return "", err
	}
	src := config.ExpandUserPath(srcKeyPath)

	privData, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read private key (%s): %w", src, err)
	}
	pubData, err := os.ReadFile(src + ".pub")
	if err != nil {
		return "", fmt.Errorf("read public key (%s.pub): %w", src, err)
	}

	configDir := config.DefaultConfigDir()
	keysDir := config.ServerKeysDir(configDir, host)
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", fmt.Errorf("create keys dir (%s): %w", keysDir, err)
	}

	dst := config.ServerKeyPath(configDir, host)

	// Idempotent: if user typed the exact target path, no-op.
	if filepath.Clean(src) == filepath.Clean(dst) {
		return dst, nil
	}

	// Don't silently overwrite an existing managed file. Same
	// safety stance as the generate-key path.
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("key file already exists: %s — pick a different host or delete the existing file", dst)
	}

	if err := os.WriteFile(dst, privData, 0600); err != nil {
		return "", fmt.Errorf("write private key (%s): %w", dst, err)
	}
	if err := os.WriteFile(dst+".pub", pubData, 0644); err != nil {
		// Roll back the partial private-key write so we don't
		// leave a private-only orphan on disk.
		_ = os.Remove(dst)
		return "", fmt.Errorf("write public key (%s.pub): %w", dst, err)
	}

	return dst, nil
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

// NewAddServer constructs the Add Server dialog. The scanDirsFn
// callback returns the list of per-server keys folders to include in
// the dialog's "Existing Ed25519 keys" list at scan time (called from
// rescanEd25519Keys). May be nil — the scanner falls back to ~/.ssh/
// only, which is the natural default for tests and for the first-run
// case where no servers exist yet.
func NewAddServer(scanDirsFn func() []string) AddServerModel {
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
	// genInputs[0] (the key save-path) intentionally starts empty.
	// The Ctrl+G handler populates it with `config.ServerKeyPath(
	// configDir, host)` once the user has typed a hostname — there
	// is no useful host-independent default under the per-server
	// keys-folder layout, and the only path into the generate view
	// is Ctrl+G (which always overwrites this value).
	genInputs[1].Placeholder = "passphrase"
	genInputs[1].EchoMode = textinput.EchoPassword
	genInputs[2].Placeholder = "confirm passphrase"
	genInputs[2].EchoMode = textinput.EchoPassword

	return AddServerModel{
		inputs:     inputs,
		labels:     labels,
		genInputs:  genInputs,
		scanDirsFn: scanDirsFn,
	}
}

func (a *AddServerModel) Show() {
	a.visible = true
	a.mode = addServerForm
	a.focused = 0
	a.keyCursor = 0
	a.genErr = ""
	a.genNotice = ""
	a.formErr = ""
	a.weakPassConfirmed = ""
	a.strengthHint = keygen.LiveHint{}
	for i := range a.inputs {
		if i == 2 {
			a.inputs[i].SetValue("2222")
		} else {
			a.inputs[i].SetValue("")
		}
	}
	// Clear any stale passphrase from a prior dialog session — the
	// textinput model retains values across Hide/Show, and we don't
	// want a passphrase typed previously to be lurking in the field
	// or in process memory longer than necessary. Path is reset on
	// Ctrl+G entry from the host-derived default; clearing here is
	// belt-and-suspenders.
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")
	a.inputs[0].Focus()

	a.rescanEd25519Keys()
}

func (a *AddServerModel) Hide() {
	a.visible = false
	a.mode = addServerForm
	a.genErr = ""
	a.genNotice = ""
	a.formErr = ""
	// Clear sensitive / per-session state. weakPassConfirmed is the
	// "you've been warned about this borderline passphrase" mark — if
	// it survived Hide(), the user could close+reopen and silently
	// get the warned passphrase accepted on first Enter. strengthHint
	// is the live zxcvbn indicator; keeping a stale one would briefly
	// flash a wrong reading on the next open before the first keystroke
	// recomputes it. Passphrase fields zeroed for the same reason as
	// in Show() — minimize cleartext-in-memory window.
	a.weakPassConfirmed = ""
	a.strengthHint = keygen.LiveHint{}
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")
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

// rescanEd25519Keys refreshes a.scannedKeys with only the Ed25519
// entries from disk. Called on Show (dialog open) and after a
// successful key generation (so a freshly-written key shows up in
// the list if it landed in a scanned directory). The protocol only
// accepts Ed25519, so unsupported types are filtered out here even
// though scanSSHKeys returns them — the scanner keeps non-Ed25519
// entries so future UIs could explain "why isn't my RSA key listed",
// but add-server only offers usable picks.
//
// extraDirs come from scanDirsFn (typically per-server keys folders
// for every configured server). When scanDirsFn is nil (tests, fresh
// installs with no servers) the scan covers ~/.ssh/ only.
func (a *AddServerModel) rescanEd25519Keys() {
	var extraDirs []string
	if a.scanDirsFn != nil {
		extraDirs = a.scanDirsFn()
	}
	all := scanSSHKeys(extraDirs)
	a.scannedKeys = a.scannedKeys[:0]
	for _, k := range all {
		if k.Type == "ed25519" {
			a.scannedKeys = append(a.scannedKeys, k)
		}
	}
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
		// Require a hostname before opening the generate sub-view.
		// The new key file lives at <configDir>/<host>/keys/id_ed25519,
		// so without a host we can't even derive the destination
		// directory — and the host doubles as the per-server folder
		// name, so any unsafe path segments (slashes, `..`, control
		// bytes) would corrupt the filesystem layout. Validate before
		// the user can type a passphrase, so we never write a key
		// into a directory we'd then refuse to read back.
		//
		// Refuse early: don't switch modes, set formErr to explain,
		// move focus to the host field so the next keystroke lands
		// where the user needs to type. One-step fix from the user's
		// side and the filesystem never sees a misnamed key.
		// If user was navigating the scanned-keys list, clamp focused
		// back into the form range — Esc-back from generate calls
		// inputs[focused].Focus() and would panic on out-of-bounds.
		if a.focused >= len(a.inputs) {
			a.focused = 3
		}
		host := strings.TrimSpace(a.inputs[1].Value())
		if host == "" {
			a.formErr = "Type a hostname first — used as the per-server folder name."
			a.inputs[a.focused].Blur()
			a.focused = 1
			a.inputs[1].Focus()
			return a, nil
		}
		if err := config.ValidateHost(host); err != nil {
			a.formErr = "Invalid hostname: " + err.Error()
			a.inputs[a.focused].Blur()
			a.focused = 1
			a.inputs[1].Focus()
			return a, nil
		}
		// Enter generate sub-view
		a.mode = addServerGenerate
		for i := range a.inputs {
			a.inputs[i].Blur()
		}
		a.genInputs[0].SetValue(config.ServerKeyPath(config.DefaultConfigDir(), host))
		// Always enter generate with empty passphrase fields and no
		// stale strength hint — even if the user previously typed a
		// passphrase in this dialog session and Esc'd back to the
		// form, the textinput retains the value. Re-show should be a
		// fresh slate so a passphrase isn't sitting visible (echo is
		// masked but the value is in memory) and the live hint isn't
		// reading old data until the first keystroke recomputes it.
		a.genInputs[1].SetValue("")
		a.genInputs[2].SetValue("")
		a.strengthHint = keygen.LiveHint{}
		a.genFocused = 0
		a.genInputs[0].Focus()
		a.genErr = ""
		return a, nil

	case "tab":
		// Tab cycles through the four form fields only — the scanned-
		// keys list isn't part of the tab order (its size varies and
		// users may want to skip past it quickly to submit). If the
		// user is currently in the list, Tab pops back out to field 0
		// rather than advancing through list rows.
		if a.focused >= len(a.inputs) {
			a.focused = 0
			a.inputs[0].Focus()
			return a, nil
		}
		a.inputs[a.focused].Blur()
		a.focused = (a.focused + 1) % len(a.inputs)
		a.inputs[a.focused].Focus()
		return a, nil

	case "shift+tab":
		if a.focused >= len(a.inputs) {
			// From the list, Shift+Tab returns to field 3 — the
			// natural "above the list" target.
			a.focused = 3
			a.inputs[3].Focus()
			return a, nil
		}
		a.inputs[a.focused].Blur()
		a.focused--
		if a.focused < 0 {
			a.focused = len(a.inputs) - 1
		}
		a.inputs[a.focused].Focus()
		return a, nil

	case "down":
		// Down acts like Tab within the form, but with one extra step:
		// from field 3 it descends into the scanned-keys list (if any),
		// matching the visual layout where the list sits below the
		// form. Within the list, Down moves the cursor; we deliberately
		// don't wrap back to field 0 at the bottom — keeps the user
		// oriented when scrolling through a long key list.
		if a.focused >= len(a.inputs) {
			if a.keyCursor < len(a.scannedKeys)-1 {
				a.keyCursor++
			}
			return a, nil
		}
		if a.focused == 3 && len(a.scannedKeys) > 0 {
			a.inputs[3].Blur()
			a.focused = len(a.inputs)
			a.keyCursor = 0
			return a, nil
		}
		a.inputs[a.focused].Blur()
		a.focused = (a.focused + 1) % len(a.inputs)
		a.inputs[a.focused].Focus()
		return a, nil

	case "up":
		// Up navigates within the list when the user is in it,
		// returning to field 3 from the top row. Outside the list,
		// behaves like Shift+Tab (cycle backward through fields).
		if a.focused >= len(a.inputs) {
			if a.keyCursor > 0 {
				a.keyCursor--
				return a, nil
			}
			a.focused = 3
			a.inputs[3].Focus()
			return a, nil
		}
		a.inputs[a.focused].Blur()
		a.focused--
		if a.focused < 0 {
			a.focused = len(a.inputs) - 1
		}
		a.inputs[a.focused].Focus()
		return a, nil

	case "enter", "ctrl+enter":
		// Enter on a list row means "select this key" (parallel to
		// mouse click on the same row), not "submit the form". Fill
		// inputs[3] with the highlighted path and return focus to
		// field 3 so the user can review or adjust before pressing
		// Enter again to submit.
		if a.focused >= len(a.inputs) {
			if a.keyCursor >= 0 && a.keyCursor < len(a.scannedKeys) {
				a.inputs[3].SetValue(a.scannedKeys[a.keyCursor].Path)
			}
			a.focused = 3
			a.inputs[3].Focus()
			return a, nil
		}
		// Validate and submit
		a.formErr = "" // clear any prior submit error
		name := strings.TrimSpace(a.inputs[0].Value())
		host := strings.TrimSpace(a.inputs[1].Value())
		portStr := strings.TrimSpace(a.inputs[2].Value())
		key := strings.TrimSpace(a.inputs[3].Value())

		// Submit-time host validation: rejects empty/whitespace,
		// path separators, traversal segments, control bytes. The
		// underlying copyKeyForServer revalidates as defense in
		// depth (it's reachable via the keyCopyFn indirection that
		// tests swap), so this gate is the user-facing one.
		if err := config.ValidateHost(host); err != nil {
			a.formErr = err.Error()
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

		// Always copy the source key into the managed folder under
		// a host-suffixed name. Gives every server its own physical
		// key file even when multiple servers reuse the same
		// underlying identity (same fingerprint, separate files).
		// Source-equals-destination is idempotent (no-op). See
		// copyKeyForServer for the rationale. Indirection via
		// keyCopyFn is so tests can swap in a passthrough.
		if _, err := keyCopyFn(key, host); err != nil {
			a.formErr = err.Error()
			return a, nil
		}

		a.Hide()
		return a, func() tea.Msg {
			return AddServerMsg{
				Name: name,
				Host: host,
				Port: port,
			}
		}
	}

	// Fall-through to the active textinput. When focused is in the
	// list zone (>= len(inputs)) there's no textinput to forward to —
	// keystrokes that aren't list-navigation are ignored, which is
	// the right thing: typing letters while highlighting a key
	// shouldn't insert into a hidden input.
	var cmd tea.Cmd
	if a.focused < len(a.inputs) {
		a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
	}
	return a, cmd
}

func (a AddServerModel) updateGenerate(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Return to form mode without generating. Defensive clamp:
		// Ctrl+G already pulls focused back into the form range, but
		// if anything ever leaves it as the list-sentinel value here
		// we'd index inputs out of bounds.
		a.mode = addServerForm
		for i := range a.genInputs {
			a.genInputs[i].Blur()
		}
		if a.focused >= len(a.inputs) {
			a.focused = 3
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
		expanded := config.ExpandUserPath(path)
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
		// (it was written to <configDir>/<host>/keys/ by default, not ~/.ssh/,
		// so it typically won't appear in the scan — but rescan covers
		// custom paths under ~/.ssh)
		a.rescanEd25519Keys()
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
			// Guard against blurring an out-of-range index when the
			// user was navigating the scanned-keys list with the
			// keyboard (focused == len(inputs)).
			if a.focused < len(a.inputs) {
				a.inputs[a.focused].Blur()
			}
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
			if a.focused < len(a.inputs) {
				a.inputs[a.focused].Blur()
			}
			a.focused = 3
			a.keyCursor = i
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
	if a.formErr != "" {
		y += 2 // error line + blank
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
	if a.formErr != "" {
		b.WriteString("  " + errorStyle.Render(a.formErr) + "\n\n")
	}

	if len(a.scannedKeys) > 0 {
		b.WriteString("  " + helpDescStyle.Render("Existing Ed25519 keys (↓ to navigate, Enter or click to use):") + "\n\n")
		for i, entry := range a.scannedKeys {
			// Shorten path by replacing $HOME with ~
			display := entry.Path
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(display, home) {
				display = "~" + display[len(home):]
			}
			line := "  " + display
			// Highlight the row only when keyboard focus is in the
			// list. A leftover keyCursor from a prior visit shouldn't
			// look "selected" while the user is typing in a form field.
			if a.focused == len(a.inputs) && i == a.keyCursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=field  ↑/↓=keys  Enter=add/select  Ctrl+g=generate  Esc=cancel"))

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
