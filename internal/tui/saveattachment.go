package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// SaveAttachmentModel is the save-as dialog for attachment files. Replaces
// the pre-v0.3.0 behavior where `s` on a focused attachment would
// silently write to a hardcoded `~/Downloads/<name>` path, always
// overwriting anything already there.
//
// Two-phase flow:
//
//  1. phaseEdit   — user edits the destination path (pre-filled with the
//                   platform default + the sender's filename, sanitized).
//                   Enter submits; Esc cancels.
//
//  2. phaseExists — the submitted path already points at an existing
//                   file. User picks: overwrite / auto-rename / edit the
//                   path again / cancel.
//
// The model never touches the filesystem directly beyond a non-destructive
// os.Stat. The actual copy is delegated to the App via SaveAttachmentDoMsg
// so download + copy + error reporting all stay in one place.
type SaveAttachmentModel struct {
	visible bool
	phase   saveAttachmentPhase

	input textinput.Model

	// sourcePath is the cached-plaintext path on disk (<dataDir>/files/<fileID>).
	// Set by Show() once the download has completed. The modal does not
	// trigger the download itself — the App kicks that off before opening
	// the modal, so by the time the user sees the dialog the bytes are
	// already on disk ready to be copied to the destination.
	sourcePath string

	// attachmentName is the original sender-supplied filename, retained
	// so the help text and rename suggestions can reference it. Already
	// sanitized (filepath.Base) before being passed to Show.
	attachmentName string

	// suggested is the rename suggestion shown in the phaseExists prompt
	// — e.g. if the user's path is "~/Downloads/photo.png" and that file
	// exists, suggested becomes "~/Downloads/photo (1).png". Computed
	// once when transitioning into phaseExists.
	suggested string

	// errMsg surfaces a transient error to the bottom of the dialog
	// (e.g., "cannot write to that location: permission denied"). Cleared
	// on the next keypress that isn't the trigger that set it.
	errMsg string
}

type saveAttachmentPhase int

const (
	phaseEdit saveAttachmentPhase = iota
	phaseExists
)

// SaveAttachmentDoMsg asks the App to perform the actual file copy
// from SourcePath (the local cache path) to DestPath (the user's
// chosen destination). Modal is hidden by the time this fires.
type SaveAttachmentDoMsg struct {
	SourcePath string
	DestPath   string
}

// SaveAttachmentCancelledMsg signals that the user bailed out of the
// save flow without writing anything. App surfaces "Save cancelled"
// in the status bar.
type SaveAttachmentCancelledMsg struct{}

// NewSaveAttachment constructs the modal. No pre-population — the
// actual path is set in Show() when the App has both the download
// result and the default save directory available.
func NewSaveAttachment() SaveAttachmentModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 4096
	return SaveAttachmentModel{input: ti}
}

// Show opens the modal with a pre-filled destination path. sourcePath
// is the cached plaintext file (source of the copy) and defaultPath is
// what to show pre-filled in the input — typically
// filepath.Join(defaultSaveDir(), filepath.Base(attachmentName)).
// The attachment name is stored separately so the rename suggestion
// can reference it by its base form regardless of what the user types.
//
// input.Focus()'s returned tea.Cmd is intentionally discarded here,
// matching the convention in addserver.go / wizard.go: the global
// cursor-blink ticker is kicked off once at App startup via
// InputModel.Init() returning textinput.Blink, and all textinputs
// across the TUI share that one ticker. Re-running Focus().cmd per
// modal would just add redundant tickers.
func (m *SaveAttachmentModel) Show(sourcePath, attachmentName, defaultPath string) {
	m.visible = true
	m.phase = phaseEdit
	m.sourcePath = sourcePath
	m.attachmentName = attachmentName
	m.suggested = ""
	m.errMsg = ""
	m.input.SetValue(defaultPath)
	m.input.CursorEnd()
	m.input.Focus()
}

// Hide closes the modal and clears in-flight state so a subsequent
// Show() starts fresh.
func (m *SaveAttachmentModel) Hide() {
	m.visible = false
	m.phase = phaseEdit
	m.sourcePath = ""
	m.attachmentName = ""
	m.suggested = ""
	m.errMsg = ""
	m.input.SetValue("")
	m.input.Blur()
}

// IsVisible reports whether the modal is currently showing; used by
// the App to gate keyboard + mouse delegation and to cover the
// background in the View stack.
func (m *SaveAttachmentModel) IsVisible() bool {
	return m.visible
}

// Update handles keystrokes while the modal is visible. Returns a
// SaveAttachmentDoMsg when the save is confirmed, or a
// SaveAttachmentCancelledMsg on Esc / explicit cancel.
func (m SaveAttachmentModel) Update(msg tea.KeyMsg) (SaveAttachmentModel, tea.Cmd) {
	// Clear any previous transient error on the next keypress; a real
	// new error will set it again below.
	m.errMsg = ""

	switch m.phase {
	case phaseEdit:
		return m.updateEdit(msg)
	case phaseExists:
		return m.updateExists(msg)
	}
	return m, nil
}

// updateEdit handles keystrokes in the path-editing phase. Enter
// performs the exists-check and either fires the save or transitions
// to phaseExists. Esc cancels.
func (m SaveAttachmentModel) updateEdit(msg tea.KeyMsg) (SaveAttachmentModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.Hide()
		return m, func() tea.Msg { return SaveAttachmentCancelledMsg{} }

	case "enter":
		dest := expandTilde(strings.TrimSpace(m.input.Value()))
		if dest == "" {
			m.errMsg = "destination path cannot be empty"
			return m, nil
		}
		// If the user typed a directory (either existing, or trailing
		// slash) we join the sanitized attachment name onto it so they
		// don't accidentally overwrite a directory with a file.
		if info, err := os.Stat(dest); err == nil && info.IsDir() {
			dest = filepath.Join(dest, m.attachmentName)
		} else if strings.HasSuffix(m.input.Value(), string(filepath.Separator)) {
			dest = filepath.Join(dest, m.attachmentName)
		}
		// Non-destructive existence check. If the destination file
		// already exists as a regular file, move to the overwrite
		// prompt; otherwise fire the save directly.
		if info, err := os.Stat(dest); err == nil && !info.IsDir() {
			m.phase = phaseExists
			m.suggested = uniqueDest(dest)
			// Reflect the normalized dest in the input so the user can
			// see exactly where we're about to write.
			m.input.SetValue(dest)
			m.input.CursorEnd()
			return m, nil
		}
		src := m.sourcePath
		m.Hide()
		return m, func() tea.Msg {
			return SaveAttachmentDoMsg{SourcePath: src, DestPath: dest}
		}
	}

	// Forward everything else to the text input so cursor movement,
	// backspace, normal typing all behave normally.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateExists handles keystrokes in the overwrite-prompt phase.
//
//	o — overwrite: save at the user's typed destination, replacing the
//	    existing file.
//	r — rename:    save at the auto-suggested "(N)" variant so nothing
//	    pre-existing gets touched.
//	e — edit:      go back to the path-editing phase to pick a
//	    different destination.
//	Esc — cancel:  abort the save entirely.
func (m SaveAttachmentModel) updateExists(msg tea.KeyMsg) (SaveAttachmentModel, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "o":
		dest := expandTilde(strings.TrimSpace(m.input.Value()))
		src := m.sourcePath
		m.Hide()
		return m, func() tea.Msg {
			return SaveAttachmentDoMsg{SourcePath: src, DestPath: dest}
		}

	case "r":
		dest := m.suggested
		src := m.sourcePath
		m.Hide()
		return m, func() tea.Msg {
			return SaveAttachmentDoMsg{SourcePath: src, DestPath: dest}
		}

	case "e":
		m.phase = phaseEdit
		// Focus cmd discarded — global blink ticker (started by
		// InputModel.Init()'s textinput.Blink) keeps driving the
		// animation across all textinputs in the program.
		m.input.Focus()
		return m, nil

	case "esc":
		m.Hide()
		return m, func() tea.Msg { return SaveAttachmentCancelledMsg{} }
	}
	return m, nil
}

// HandleMouse is the mouse-event sink. The modal is keyboard-only;
// clicks inside or outside its footprint are absorbed without effect so
// nothing leaks through to the sidebar / messages pane / compose input
// underneath. The pattern mirrors the keyboard-only overlay list in
// App.handleMouse.
func (m SaveAttachmentModel) HandleMouse(_ tea.MouseMsg) (SaveAttachmentModel, tea.Cmd) {
	return m, nil
}

// View renders the modal. Covers the full screen via the Width(width-4)
// dialogStyle pattern used by the other confirmation modals in this
// package — so the background is fully visually replaced even when the
// modal is visually smaller, and clicks in the "gap" are absorbed by
// the App's handleMouse early-return.
func (m SaveAttachmentModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Save attachment"))
	b.WriteString("\n\n")

	switch m.phase {
	case phaseEdit:
		b.WriteString("  File: " + m.attachmentName + "\n\n")
		b.WriteString("  Save to:\n")
		b.WriteString("  " + m.input.View() + "\n\n")
		if m.errMsg != "" {
			b.WriteString("  " + errorStyle.Render(m.errMsg) + "\n\n")
		}
		b.WriteString("  [Enter] save    [Esc] cancel\n")

	case phaseExists:
		b.WriteString("  " + errorStyle.Render("File already exists") + " at:\n")
		b.WriteString("  " + m.input.View() + "\n\n")
		b.WriteString("  Rename suggestion: " + m.suggested + "\n\n")
		b.WriteString("  [o] overwrite    [r] rename    [e] edit path    [Esc] cancel\n")
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}

// uniqueDest returns a variant of path that does not currently exist on
// disk, by appending " (N)" before the extension: "photo.png" becomes
// "photo (1).png", then "(2)", etc. Fails safe by returning the input
// unchanged if 1000 candidates all exist (pathological).
func uniqueDest(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return path
}

// expandTilde handles `~/...` and `~` expansion in user-typed paths.
// Non-expandable inputs (a path that doesn't start with `~` or a home
// directory that can't be resolved) pass through unchanged — errors
// surface later when the save itself tries to write.
func expandTilde(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	// Only expand "~/..." (tilde followed by separator), not "~user".
	if len(path) >= 2 && (path[1] == '/' || path[1] == filepath.Separator) {
		return filepath.Join(home, path[2:])
	}
	return path
}

// defaultSaveDir returns the platform-appropriate default destination
// for an attachment save. Linux honors $XDG_DOWNLOAD_DIR when set (some
// distros redirect Downloads elsewhere); macOS and Windows follow the
// canonical ~/Downloads convention. Falls through to the current
// working directory if no home is resolvable — rare but avoids a
// panic on headless/containerized setups.
func defaultSaveDir() string {
	if runtime.GOOS == "linux" {
		if xdg := strings.TrimSpace(os.Getenv("XDG_DOWNLOAD_DIR")); xdg != "" {
			return xdg
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if cwd, werr := os.Getwd(); werr == nil {
			return cwd
		}
		return "."
	}
	return filepath.Join(home, "Downloads")
}

// sanitizeAttachmentName strips any path components from a
// sender-supplied filename, preventing `att.Name = "../../etc/passwd"`
// from causing the save path to escape the chosen destination
// directory. Empty / dot / dot-dot results collapse to a generic
// "attachment" fallback so the user still sees a valid target and can
// edit it.
func sanitizeAttachmentName(name string) string {
	base := filepath.Base(name)
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "\x00/\\") {
		return "attachment"
	}
	return base
}
