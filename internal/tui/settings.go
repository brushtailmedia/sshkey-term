package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// SettingsModel manages the settings page.
type SettingsModel struct {
	visible    bool
	cfg        *config.Config
	configDir  string
	cursor     int
	items      []settingsItem
	confirm    *confirmDialog
	editing    bool // true when inline editing a field
	editInput  textinput.Model
	editAction string // which field is being edited

	// Cached state used by Refresh() to rebuild items in place after
	// an action mutates state. Without this, action handlers would
	// have to re-call Show() which resets the cursor and feels
	// jarring. Stored here at Show time and updated by the App via
	// SetDisplayName / Refresh.
	displayName   string
	currentServer int

	// notice is a transient inline confirmation rendered in the
	// footer area (just above navigation helpers) — used for copy actions, edit
	// completions, and similar "what just happened" feedback.
	// Settings overlays the entire screen so the status bar is
	// hidden behind it; without this, those actions feel silent.
	// Set via SetNotice; cleared by Hide and overwritten by the
	// next SetNotice call.
	notice string
}

type settingsItem struct {
	label     string
	value     string
	action    string // "edit_name", "clear_history", "add_server", "remove_server_N", "copy_pubkey", "copy_fingerprint", "manage_devices", "retire_account", "switch_server_N"
	isServer  bool
	serverIdx int
}

type confirmDialog struct {
	message string
	action  string
	param   int
}

// SettingsActionMsg is sent when the user performs an action in settings.
type SettingsActionMsg struct {
	Action    string
	ServerIdx int
}

// ProfileUpdateMsg is sent when the user changes their display name.
type ProfileUpdateMsg struct {
	DisplayName string
}

func NewSettings() SettingsModel {
	return SettingsModel{}
}

// Show opens the settings page. displayName is the local user's
// human-readable display name (NOT the nanoid user ID) — it's
// rendered as the "Display name" row's value. The caller resolves
// nanoid → display name via Client.DisplayName before passing in;
// the settings model deals only with the rendered string.
func (s *SettingsModel) Show(cfg *config.Config, configDir string, displayName string, currentServer int) {
	s.visible = true
	s.cfg = cfg
	s.configDir = configDir
	s.cursor = 0
	s.confirm = nil
	s.notice = ""
	s.displayName = displayName
	s.currentServer = currentServer
	s.buildItems(displayName, currentServer)
}

// Refresh rebuilds the items list with an updated display name,
// preserving cursor position. Called by the App after a successful
// display-name edit so the panel reflects the new value
// immediately rather than waiting for the next Show. Cursor stays
// on the same row index unless that row no longer exists, in
// which case it clamps to the last item.
func (s *SettingsModel) Refresh(displayName string) {
	if !s.visible {
		return
	}
	s.displayName = displayName
	prevCursor := s.cursor
	s.buildItems(displayName, s.currentServer)
	if prevCursor >= len(s.items) {
		prevCursor = len(s.items) - 1
	}
	if prevCursor < 0 {
		prevCursor = 0
	}
	s.cursor = prevCursor
}

// SetNotice puts a transient confirmation message in the footer area
// just above navigation helpers. Used for copy actions, edit confirmations, and similar
// feedback that the status bar would normally carry — but settings
// covers the status bar, so without this the actions look silent.
// Persists until the next SetNotice or Hide call. Empty string
// clears the current notice without setting a new one.
func (s *SettingsModel) SetNotice(message string) {
	s.notice = message
}

func (s *SettingsModel) buildItems(displayName string, currentServer int) {
	s.items = nil

	// Active server section (profile is per-server)
	if s.cfg != nil && currentServer < len(s.cfg.Servers) {
		srv := s.cfg.Servers[currentServer]
		s.items = append(s.items, settingsItem{label: fmt.Sprintf("── %s ──", srv.Name), value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Host", value: fmt.Sprintf("%s:%d", srv.Host, srv.Port), action: ""})
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Profile", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "    Display name", value: displayName, action: "edit_name"})
		// Status is set via the /setstatus slash command (locked
		// set: available / away / busy). Not a settings-panel row
		// because there's no point duplicating the input surface
		// once the slash command is canonical.
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Storage", value: "", action: ""})
		// Phase 2 of path-centralization: ServerDataSize now
		// validates srv.Host before walking the filesystem.
		// Surface the error in the settings panel instead of
		// silently displaying "0 B" for a malformed Host (which
		// would walk the wrong directory).
		size, sizeErr := config.ServerDataSize(s.configDir, srv)
		var dbValue string
		if sizeErr != nil {
			dbValue = "invalid host"
		} else {
			dbValue = formatBytes(size)
		}
		s.items = append(s.items, settingsItem{label: "    Local DB", value: dbValue, action: ""})
		s.items = append(s.items, settingsItem{label: "    [Clear local history]", value: "", action: "clear_history"})
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Keys", value: "", action: ""})
		// SSH key path derived from the per-server canonical layout
		// (Phase 3e: ServerConfig.Key was deleted; every server's key
		// lives at <configDir>/<host>/keys/id_ed25519).
		s.items = append(s.items, settingsItem{label: "    SSH key", value: config.ServerKeyPath(s.configDir, srv.Host), action: ""})
		s.items = append(s.items, settingsItem{label: "    [Copy public key]", value: "", action: "copy_pubkey"})
		s.items = append(s.items, settingsItem{label: "    [Copy fingerprint]", value: "", action: "copy_fingerprint"})
	}

	// All servers section
	s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
	s.items = append(s.items, settingsItem{label: "── Servers ──", value: "", action: ""})

	if s.cfg != nil {
		for i, srv := range s.cfg.Servers {
			active := ""
			if i == currentServer {
				active = " (active)"
			}
			label := fmt.Sprintf("  %s%s", srv.Name, active)
			value := fmt.Sprintf("%s:%d", srv.Host, srv.Port)
			s.items = append(s.items, settingsItem{
				label:     label,
				value:     value,
				action:    fmt.Sprintf("switch_server_%d", i),
				isServer:  true,
				serverIdx: i,
			})
		}
	}

	s.items = append(s.items, settingsItem{label: "  [Add server]", value: "", action: "add_server"})

	// Device section
	s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
	s.items = append(s.items, settingsItem{label: "── Device ──", value: "", action: ""})
	s.items = append(s.items, settingsItem{label: "  Device ID", value: s.cfg.Device.ID, action: ""})
	s.items = append(s.items, settingsItem{label: "  [Manage devices on this server]", value: "", action: "manage_devices"})

	// Account section (danger zone)
	s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
	s.items = append(s.items, settingsItem{label: "── Account ──", value: "", action: ""})
	s.items = append(s.items, settingsItem{label: "  [Retire account on this server]", value: "", action: "retire_account"})
}

func (s *SettingsModel) Hide() {
	s.visible = false
	s.confirm = nil
	s.notice = ""
}

func (s *SettingsModel) IsVisible() bool {
	return s.visible
}

func (s SettingsModel) Update(msg tea.KeyMsg) (SettingsModel, tea.Cmd) {
	// Handle inline editing
	if s.editing {
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(s.editInput.Value())
			action := s.editAction

			switch action {
			case "edit_name":
				validated, err := ValidateDisplayName(value)
				if err != nil {
					// Stay in edit mode so user can fix it
					return s, nil
				}
				s.editing = false
				s.editInput.Blur()
				return s, func() tea.Msg {
					return ProfileUpdateMsg{DisplayName: validated}
				}
			}
			s.editing = false
			s.editInput.Blur()
			return s, nil
		case "esc":
			s.editing = false
			s.editInput.Blur()
			return s, nil
		}
		var cmd tea.Cmd
		s.editInput, cmd = s.editInput.Update(msg)
		return s, cmd
	}

	// Handle confirmation dialog
	if s.confirm != nil {
		switch msg.String() {
		case "y", "enter":
			action := s.confirm.action
			param := s.confirm.param
			s.confirm = nil
			return s, func() tea.Msg {
				return SettingsActionMsg{Action: action, ServerIdx: param}
			}
		case "n", "esc":
			s.confirm = nil
			return s, nil
		}
		return s, nil
	}

	switch msg.String() {
	case "esc":
		s.Hide()
		return s, nil

	case "up", "k":
		s.cursor--
		if s.cursor < 0 {
			s.cursor = 0
		}
		// Skip non-actionable items
		for s.cursor > 0 && s.items[s.cursor].action == "" {
			s.cursor--
		}

	case "down", "j":
		s.cursor++
		if s.cursor >= len(s.items) {
			s.cursor = len(s.items) - 1
		}
		// Skip non-actionable items
		for s.cursor < len(s.items)-1 && s.items[s.cursor].action == "" {
			s.cursor++
		}

	case "enter":
		if s.cursor < len(s.items) {
			item := s.items[s.cursor]
			switch {
			case item.action == "edit_name":
				s.editing = true
				s.editAction = item.action
				s.editInput = textinput.New()
				s.editInput.SetValue(item.value)
				s.editInput.Focus()
				s.editInput.Prompt = ""
				s.editInput.CharLimit = 100
			case item.action == "clear_history":
				s.confirm = &confirmDialog{
					message: "Clear all local message history? This cannot be undone.",
					action:  "clear_history",
				}
			case item.isServer:
				s.confirm = &confirmDialog{
					message: fmt.Sprintf("Remove server %q and all its local data?", s.cfg.Servers[item.serverIdx].Name),
					action:  "remove_server",
					param:   item.serverIdx,
				}
			case item.action == "add_server":
				return s, func() tea.Msg {
					return SettingsActionMsg{Action: "add_server"}
				}
			case item.action == "copy_pubkey":
				return s, func() tea.Msg {
					return SettingsActionMsg{Action: "copy_pubkey"}
				}
			case item.action == "copy_fingerprint":
				return s, func() tea.Msg {
					return SettingsActionMsg{Action: "copy_fingerprint"}
				}
			case item.action == "retire_account":
				return s, func() tea.Msg {
					return SettingsActionMsg{Action: "retire_account"}
				}
			case item.action == "manage_devices":
				return s, func() tea.Msg {
					return SettingsActionMsg{Action: "manage_devices"}
				}
			}
		}

	case "d", "delete":
		// Delete server shortcut on a server item
		if s.cursor < len(s.items) && s.items[s.cursor].isServer {
			idx := s.items[s.cursor].serverIdx
			s.confirm = &confirmDialog{
				message: fmt.Sprintf("Remove server %q and all its local data?", s.cfg.Servers[idx].Name),
				action:  "remove_server",
				param:   idx,
			}
		}
	}

	return s, nil
}

// HandleMouse maps a click to the matching settings item and updates the
// cursor. Click-selects-only (same convention as DeviceMgr) — user presses
// Enter to actually activate the selected item.
//
// Layout: border(1) + padding(1) + header(1) + blank(1) = content starts at
// Y=4. The notice is rendered in the footer area, so it does not affect item
// row mapping. Each items[i] takes exactly one line (including blank spacers
// which render as "\n").
func (s SettingsModel) HandleMouse(msg tea.MouseMsg) (SettingsModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return s, nil
	}
	// Don't handle clicks while editing or confirming — keyboard drives those
	if s.editing || s.confirm != nil {
		return s, nil
	}

	firstItemY := 4
	idx := msg.Y - firstItemY
	if idx < 0 || idx >= len(s.items) {
		return s, nil
	}
	// Only move cursor to actionable items
	if s.items[idx].action != "" {
		s.cursor = idx
	}
	return s, nil
}

func (s SettingsModel) View(width, height int) string {
	if !s.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Settings"))
	b.WriteString("\n\n")

	for i, item := range s.items {
		if item.label == "" {
			b.WriteString("\n")
			continue
		}

		if s.editing && i == s.cursor {
			// Show inline edit input
			b.WriteString(item.label + ": " + s.editInput.View() + "\n")
			continue
		}

		line := item.label
		if item.value != "" {
			padding := width - 8 - len(item.label) - len(item.value)
			if padding < 2 {
				padding = 2
			}
			line += strings.Repeat(" ", padding) + helpDescStyle.Render(item.value)
		}

		if i == s.cursor && item.action != "" {
			// Match the cursor-highlight treatment used by the
			// sidebar, member panel, and messages pane —
			// selectedMsgStyle (dark grey bg, no fg change) is
			// less overpowering than the purple-bg + white-fg
			// completionSelectedStyle when the row content carries
			// its own color cues (helpDescStyle for values, etc.).
			// Width = inner content area: dialog width - 4 (border) -
			// 4 (padding) = width - 8, matching the label/value
			// padding logic above.
			rowWidth := width - 8
			if rowWidth < 1 {
				rowWidth = 1
			}
			line = selectedMsgStyle.Width(rowWidth).Render(line)
		}

		b.WriteString(line + "\n")
	}

	// Confirmation dialog
	if s.confirm != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render("  " + s.confirm.message))
		b.WriteString("\n")
		b.WriteString(helpDescStyle.Render("  y=confirm  n=cancel"))
	}

	b.WriteString("\n\n")
	if s.notice != "" {
		b.WriteString("  " + checkStyle.Render(s.notice) + "\n")
	}
	b.WriteString(helpDescStyle.Render(" ↑/↓=navigate  Enter=select  d=remove server  Esc=close"))

	// Slice to a cursor-following scroll window when total content
	// exceeds the available inner rows. Pre-2026-05-09 the full
	// content was passed to dialogStyle.Render unconditionally, and
	// when it overflowed the terminal height bubbletea's standard
	// renderer dropped lines from the TOP of the buffer
	// (standard_renderer.go:186-188) — the dialog's top border, the
	// "Settings" header, and the first few items were silently
	// truncated. Now we slice ourselves so the dialog chrome stays
	// intact and the cursor-highlighted row stays visible. Same
	// pattern the help panel uses for its scroll, just driven by
	// cursor position instead of independent scroll state since
	// settings already has cursor-based item selection.
	//
	// Inner rows budget = height - 4. The 4 = 1 row top border +
	// 1 row top vertical padding (from dialogStyle's Padding(1, 2))
	// + 1 row bottom padding + 1 row bottom border. If
	// dialogStyle's Padding ever changes, update this constant in
	// lockstep.
	full := b.String()
	lines := strings.Split(full, "\n")
	innerRows := height - 4
	if innerRows < 1 {
		innerRows = 1
	}
	if len(lines) > innerRows {
		// Cursor's rendered row = 2 (header + blank) + s.cursor.
		// Each items[i] produces exactly one line in the loop above
		// (blanks are rendered as "\n", real items as their content
		// + "\n"), so the index-to-line mapping is direct.
		cursorRow := 2 + s.cursor
		// Center the cursor in the window where possible. Clamps at
		// both ends so the first visible row is in [0, len-innerRows].
		scroll := cursorRow - innerRows/2
		if scroll < 0 {
			scroll = 0
		}
		maxScroll := len(lines) - innerRows
		if scroll > maxScroll {
			scroll = maxScroll
		}
		if scroll < 0 {
			scroll = 0
		}
		end := scroll + innerRows
		if end > len(lines) {
			end = len(lines)
		}
		lines = lines[scroll:end]
	}
	visible := strings.Join(lines, "\n")

	out := dialogStyle.Width(width - 4).Render(visible)

	// Append the kitty graphics-protocol delete escape so any
	// rasterm-rendered preview placement is removed when this
	// dialog is the rendered frame.
	//
	// Why this lives here, not in App.View: the settings dialog's
	// body is uniquely tall (active server + profile + storage +
	// keys + every configured server + device + account sections —
	// routinely 30+ lines on a typical 24-row terminal). When the
	// total render exceeds the terminal height, bubbletea's
	// standard renderer drops lines from the TOP of the buffer
	// (standard_renderer.go: `if r.height > 0 && len(newLines) > r.height`).
	// A kitty escape prepended at the App.View layer therefore
	// gets truncated away before reaching the terminal, and the
	// rasterm image visibly persists behind the dialog. Appending
	// to the dialog's own output puts the escape in the tail of
	// the rendered string — which bubbletea keeps when truncating
	// — so the terminal actually receives it.
	//
	// Kitty's `a=d,d=I,i=<id>` is idempotent (no-op if the image
	// is already gone), so re-emitting on every settings render
	// is harmless. Gated on rastermCapable to avoid emitting
	// stray bytes to non-rasterm terminals (where the escape
	// would be silently dropped as an unknown DCS sequence
	// anyway, but cleaner to skip).
	if rastermCapable() {
		out += rastermDeleteEscape()
	}
	return out
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	}
}
