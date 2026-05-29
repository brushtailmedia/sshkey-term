package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/keygen"
)

// addServerMode distinguishes the form and generate sub-views.
type addServerMode int

const (
	addServerForm addServerMode = iota
	addServerGenerate
)

// Add Server form field indices. Key is LAST on purpose: the "Down enters the
// scanned-keys list / Shift+Tab from the list returns here" navigation and the
// layout (the list sits directly below the key field) all key off the final
// input. The display-name field is inserted at index 3 (before key) so the
// host/port indices stay stable.
const (
	fieldName        = 0 // local server label ("Home", "Work")
	fieldHost        = 1
	fieldPort        = 2
	fieldDisplayName = 3 // requested display name on this server (SSH username hint)
	fieldKey         = 4 // MUST stay last (adjacent to the scanned-keys list)
)

// AddServerModel manages the add server dialog.
type AddServerModel struct {
	visible bool
	mode    addServerMode
	inputs  []textinput.Model
	// focused tracks input focus across two zones:
	//   0..4                    — the five form fields
	//                             (name/host/port/display-name/key)
	//   len(inputs) (i.e. 5)    — sentinel meaning "selection is in the
	//                             scanned-keys list below the form"; the
	//                             active list row lives in keyCursor.
	// Tab/Shift+Tab cycle within 0..4 only. Down from the key field (last)
	// enters the list (focused=len(inputs), keyCursor=0); Up at the top of the
	// list returns to the key field. Mouse click on a list row jumps directly
	// to focused=fieldKey with the path filled — same end-state as keyboard
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
	// RequestedDisplayName is the user's chosen display name on this server,
	// persisted to ServerConfig and sent as the SSH username hint on connect.
	// Distinct from Name (the local label). Empty = no hint.
	RequestedDisplayName string
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
// copyKeyToManagedStoreAndRewriteName: when displayName is non-empty it
// rewrites the MANAGED destination .pub comment to that name (matching the
// wizard's generate/import behavior) so the operator sees a recognizable
// comment. It never mutates the user's original source key or comment — only
// the app-managed copy. An empty displayName preserves the source .pub
// verbatim.
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
func copyKeyForServer(srcKeyPath, host, displayName string) (string, error) {
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

	// Destination .pub bytes: comment rewritten to the requested display name
	// when one was given, else the source .pub verbatim. Computed before any
	// write so a malformed source .pub fails before we touch the filesystem.
	pubOut, err := pubLineWithComment(pubData, displayName)
	if err != nil {
		return "", err
	}

	configDir := config.DefaultConfigDir()
	keysDir := config.ServerKeysDir(configDir, host)
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", fmt.Errorf("create keys dir (%s): %w", keysDir, err)
	}

	dst := config.ServerKeyPath(configDir, host)

	// Idempotent: user pointed -key at the already-managed file. Still rewrite
	// the managed .pub comment when a display name is present (the source IS
	// the managed copy here, so this only touches app-managed data); otherwise
	// no-op.
	if filepath.Clean(src) == filepath.Clean(dst) {
		if strings.TrimSpace(displayName) != "" {
			if err := os.WriteFile(dst+".pub", pubOut, 0644); err != nil {
				return "", fmt.Errorf("rewrite managed .pub (%s.pub): %w", dst, err)
			}
		}
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
	if err := os.WriteFile(dst+".pub", pubOut, 0644); err != nil {
		// Roll back the partial private-key write so we don't
		// leave a private-only orphan on disk.
		_ = os.Remove(dst)
		return "", fmt.Errorf("write public key (%s.pub): %w", dst, err)
	}

	return dst, nil
}

// pubLineWithComment returns authorized-keys bytes for pubData with its comment
// replaced by displayName. An empty/whitespace displayName returns pubData
// unchanged (preserving any existing comment). Mirrors the wizard's managed
// .pub rewrite (copyKeyToManagedStoreAndRewriteName).
func pubLineWithComment(pubData []byte, displayName string) ([]byte, error) {
	if strings.TrimSpace(displayName) == "" {
		return pubData, nil
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey))) + " " + strings.TrimSpace(displayName) + "\n"
	return []byte(line), nil
}

// addServerZxcvbnContext collects form-field values to pass to zxcvbn
// as context strings, so a passphrase containing the requested display name
// or hostname the user just typed gets penalized. Mirrors the wizard's
// ValidateUserPassphraseWithContext call which passes the chosen display name.
// Uses the requested-display-name field (NOT the server label, which is an
// arbitrary "Home"/"Work" tag and poor passphrase context) plus the host. Port
// is omitted (low signal; usually just digits).
//
// Empty / whitespace-only values are skipped — zxcvbn does exact
// substring matching, so empty strings would be no-ops but noisy.
func addServerZxcvbnContext(a AddServerModel) []string {
	var ctx []string
	if name := strings.TrimSpace(a.inputs[fieldDisplayName].Value()); name != "" {
		ctx = append(ctx, name)
	}
	if host := strings.TrimSpace(a.inputs[fieldHost].Value()); host != "" {
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
	labels := []string{"Name", "Host", "Port", "Your display name", "SSH key path"}

	inputs := make([]textinput.Model, 5)
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = ""
		inputs[i].CharLimit = 256
	}

	inputs[fieldName].Placeholder = "My Server"
	inputs[fieldHost].Placeholder = "chat.example.com"
	inputs[fieldPort].Placeholder = "2222"
	inputs[fieldPort].SetValue("2222")
	inputs[fieldDisplayName].Placeholder = "e.g. Alice"
	inputs[fieldKey].Placeholder = "~/.ssh/id_ed25519"

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
		if i == fieldPort {
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
	a.inputs[fieldName].Focus()

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

// Form focus zones. `focused` in [0, len(inputs)) is a text field; the two
// sentinels below sit past the fields, in render/Tab order:
//
//	focusGenRow  — the [Generate new key] row (not a text input)
//	focusKeyList — the scanned-keys list (active row tracked by keyCursor)
func (a AddServerModel) focusGenRow() int  { return len(a.inputs) }
func (a AddServerModel) focusKeyList() int { return len(a.inputs) + 1 }

// enterGenerateMode validates the hostname (required — it doubles as the
// per-server key-folder name) and switches to the generate-key sub-view.
// Shared by the Alt+g shortcut, Enter on the [Generate new key] row, and a
// mouse click on that row. On a missing/invalid host it sets formErr and
// moves focus to the host field instead of switching modes, so the
// filesystem never sees a key written under an unusable folder name.
func (a AddServerModel) enterGenerateMode() (AddServerModel, tea.Cmd) {
	// If focus was in the gen-row / list zone, clamp back into the form
	// range — the error paths and Esc-back call inputs[focused].Focus()
	// and would panic on an out-of-bounds index.
	if a.focused >= len(a.inputs) {
		a.focused = fieldKey
	}
	host := strings.TrimSpace(a.inputs[fieldHost].Value())
	if host == "" {
		a.formErr = "Type a hostname first — used as the per-server folder name."
		a.inputs[a.focused].Blur()
		a.focused = fieldHost
		a.inputs[fieldHost].Focus()
		return a, nil
	}
	if err := config.ValidateHost(host); err != nil {
		a.formErr = "Invalid hostname: " + err.Error()
		a.inputs[a.focused].Blur()
		a.focused = fieldHost
		a.inputs[fieldHost].Focus()
		return a, nil
	}
	// Enter generate sub-view with a host-derived save path and a fresh
	// passphrase slate (no stale value/echo carried over from a prior open).
	a.mode = addServerGenerate
	for i := range a.inputs {
		a.inputs[i].Blur()
	}
	a.genInputs[0].SetValue(config.ServerKeyPath(config.DefaultConfigDir(), host))
	a.genInputs[1].SetValue("")
	a.genInputs[2].SetValue("")
	a.strengthHint = keygen.LiveHint{}
	a.genFocused = 0
	a.genInputs[0].Focus()
	a.genErr = ""
	return a, nil
}

func (a AddServerModel) updateForm(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.Hide()
		return a, nil

	case "alt+g":
		// Generate-key shortcut. Ctrl+g is reserved as the global server
		// navigation prefix (intercepted at the App level while Add Server
		// is open), so key generation lives on Alt/Option+g and on the
		// selectable [Generate new key] row. Host validation + sub-view
		// entry are shared via enterGenerateMode.
		return a.enterGenerateMode()

	case "tab":
		// Tab order: field0 → … → fieldKey → [Generate new key] row → field0.
		// The scanned-keys list is NOT in the Tab cycle (its size varies;
		// it's reached via Down). From the list or the gen row, Tab wraps
		// back to field 0.
		if a.focused < len(a.inputs) {
			a.inputs[a.focused].Blur()
		}
		switch {
		case a.focused < fieldKey:
			a.focused++
			a.inputs[a.focused].Focus()
		case a.focused == fieldKey:
			a.focused = a.focusGenRow()
		default: // gen row or list → wrap to the first field
			a.focused = 0
			a.inputs[0].Focus()
		}
		return a, nil

	case "shift+tab":
		// Reverse Tab order: field0 ← [Generate new key] row ← fieldKey ←
		// … ← field0. From the list, Shift+Tab goes up to the gen row (the
		// focus stop directly above it).
		if a.focused < len(a.inputs) {
			a.inputs[a.focused].Blur()
		}
		switch {
		case a.focused == a.focusKeyList():
			a.focused = a.focusGenRow()
		case a.focused == a.focusGenRow():
			a.focused = fieldKey
			a.inputs[fieldKey].Focus()
		case a.focused == 0:
			a.focused = a.focusGenRow()
		default:
			a.focused--
			a.inputs[a.focused].Focus()
		}
		return a, nil

	case "down":
		// Down descends, matching the visual layout: fields → [Generate
		// new key] row → scanned-keys list. Within the list, Down moves the
		// cursor (no wrap at the bottom — keeps the user oriented in a long
		// list). The gen row is always present; the list only if keys exist.
		switch {
		case a.focused == a.focusKeyList():
			if a.keyCursor < len(a.scannedKeys)-1 {
				a.keyCursor++
			}
		case a.focused == a.focusGenRow():
			if len(a.scannedKeys) > 0 {
				a.focused = a.focusKeyList()
				a.keyCursor = 0
			}
		case a.focused == fieldKey:
			a.inputs[fieldKey].Blur()
			a.focused = a.focusGenRow()
		default:
			a.inputs[a.focused].Blur()
			a.focused++
			a.inputs[a.focused].Focus()
		}
		return a, nil

	case "up":
		// Up ascends: list → [Generate new key] row → fieldKey → … (wraps
		// to fieldKey at the top of the form). From the top list row it
		// steps up to the gen row; from the gen row, back to the key field.
		switch {
		case a.focused == a.focusKeyList():
			if a.keyCursor > 0 {
				a.keyCursor--
			} else {
				a.focused = a.focusGenRow()
			}
		case a.focused == a.focusGenRow():
			a.focused = fieldKey
			a.inputs[fieldKey].Focus()
		default:
			a.inputs[a.focused].Blur()
			a.focused--
			if a.focused < 0 {
				a.focused = len(a.inputs) - 1
			}
			a.inputs[a.focused].Focus()
		}
		return a, nil

	case "enter", "ctrl+enter":
		// Enter on a list row means "select this key" (parallel to
		// mouse click on the same row), not "submit the form". Fill
		// the key field with the highlighted path and return focus to
		// it so the user can review or adjust before pressing Enter
		// again to submit.
		if a.focused == a.focusKeyList() {
			if a.keyCursor >= 0 && a.keyCursor < len(a.scannedKeys) {
				a.inputs[fieldKey].SetValue(a.scannedKeys[a.keyCursor].Path)
			}
			a.focused = fieldKey
			a.inputs[fieldKey].Focus()
			return a, nil
		}
		// Enter on the [Generate new key] row opens the generate sub-view
		// (same host-validation + entry path as Alt+g and a row click).
		if a.focused == a.focusGenRow() {
			return a.enterGenerateMode()
		}
		// Validate and submit
		a.formErr = "" // clear any prior submit error
		name := strings.TrimSpace(a.inputs[fieldName].Value())
		host := strings.TrimSpace(a.inputs[fieldHost].Value())
		portStr := strings.TrimSpace(a.inputs[fieldPort].Value())
		displayName := strings.TrimSpace(a.inputs[fieldDisplayName].Value())
		key := strings.TrimSpace(a.inputs[fieldKey].Value())

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

		// Validate the requested display name with the same policy as the
		// wizard (incl. the DP9 '+' ban) so a bad value is caught before we
		// copy the key or emit the add message. Empty is allowed — no hint.
		if displayName != "" {
			validated, err := ValidateDisplayName(displayName)
			if err != nil {
				a.formErr = "Display name: " + err.Error()
				return a, nil
			}
			displayName = validated
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
		if _, err := keyCopyFn(key, host, displayName); err != nil {
			a.formErr = err.Error()
			return a, nil
		}

		a.Hide()
		return a, func() tea.Msg {
			return AddServerMsg{
				Name:                 name,
				Host:                 host,
				Port:                 port,
				RequestedDisplayName: displayName,
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
			a.focused = fieldKey
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
		displayName := strings.TrimSpace(a.inputs[fieldDisplayName].Value())

		if path == "" {
			a.genErr = "Path is required"
			return a, nil
		}
		if pass != confirm {
			a.genErr = "Passphrases don't match"
			return a, nil
		}
		if displayName != "" {
			validated, err := ValidateDisplayName(displayName)
			if err != nil {
				a.genErr = "Display name: " + err.Error()
				return a, nil
			}
			displayName = validated
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

		// Embed the requested display name (if the field is filled) as the
		// .pub comment so a generated managed key matches the wizard's
		// behavior. The submit step re-affirms this via copyKeyForServer, but
		// writing it now keeps the generated .pub correct even before submit.
		fingerprint, err := generateEd25519KeyFile(path, pass, displayName)
		if err != nil {
			a.genErr = "Generation failed: " + err.Error()
			return a, nil
		}

		// Success: fill key path in main form, return to form view
		a.inputs[fieldKey].SetValue(expanded)
		a.genNotice = "✓ Key generated (" + fingerprint + ") — back it up"
		a.mode = addServerForm
		for i := range a.genInputs {
			a.genInputs[i].Blur()
		}
		a.focused = fieldKey
		a.inputs[fieldKey].Focus()
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
	//   Y=10..11: Your display name
	//   Y=12..13: SSH key path
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

	// [Generate new key] row: rendered directly below the form fields
	// (Y = first-field 4 + len(inputs)*2). A click opens generate mode —
	// same path as Alt+g and Enter on the row. enterGenerateMode handles
	// focus clamping + host validation.
	if msg.Y == 4+len(a.inputs)*2 {
		return a.enterGenerateMode()
	}

	// Check scanned key lines
	keyStartY := a.keyListStartY()
	for i, entry := range a.scannedKeys {
		if msg.Y == keyStartY+i {
			// Select this key — fill the key path input
			a.inputs[fieldKey].SetValue(entry.Path)
			if a.focused < len(a.inputs) {
				a.inputs[a.focused].Blur()
			}
			a.focused = fieldKey
			a.keyCursor = i
			a.inputs[fieldKey].Focus()
			return a, nil
		}
	}

	return a, nil
}

// keyListStartY computes the Y position of the first scanned-key entry in the
// rendered form view. Must match viewForm()'s layout exactly — change both
// together.
func (a AddServerModel) keyListStartY() int {
	// Border(1) + padding(1) + header(1) + blank(1) + len(inputs) fields * 2.
	// Derived from len(inputs) so it tracks the field count (5 fields → 14).
	y := 4 + len(a.inputs)*2
	y += 2 // [Generate new key] row + blank (always rendered)
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

	// [Generate new key] selectable row — focus stop after the key field,
	// before notices/errors and the scanned-keys list. Highlighted when
	// focused; Enter / click / Alt+g all open the generate sub-view. Keep
	// in sync with keyListStartY() and HandleMouse (row at Y = 4+len*2).
	genRow := "  [ Generate new key ]"
	if a.focused == a.focusGenRow() {
		genRow = completionSelectedStyle.Render(genRow)
	}
	b.WriteString(genRow + "\n\n")

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
			if a.focused == a.focusKeyList() && i == a.keyCursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=field  ↑/↓=keys  Enter=add/select  Alt+g=generate  Esc=cancel"))

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
