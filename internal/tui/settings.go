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
	visible   bool
	cfg       *config.Config
	configDir string
	cursor    int
	items     []settingsItem
	confirm   *confirmDialog
	editing   bool           // true when inline editing a field
	editInput textinput.Model
	editAction string        // which field is being edited
}

type settingsItem struct {
	label    string
	value    string
	action   string // "edit_name", "edit_status", "clear_history", "add_server", "remove_server_N", "quit"
	isServer bool
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

// StatusUpdateMsg is sent when the user changes their status.
type StatusUpdateMsg struct {
	Text string
}

func NewSettings() SettingsModel {
	return SettingsModel{}
}

func (s *SettingsModel) Show(cfg *config.Config, configDir string, username string, currentServer int) {
	s.visible = true
	s.cfg = cfg
	s.configDir = configDir
	s.cursor = 0
	s.confirm = nil
	s.buildItems(username, currentServer)
}

func (s *SettingsModel) buildItems(username string, currentServer int) {
	s.items = nil

	// Active server section (profile is per-server)
	if s.cfg != nil && currentServer < len(s.cfg.Servers) {
		srv := s.cfg.Servers[currentServer]
		s.items = append(s.items, settingsItem{label: fmt.Sprintf("── %s ──", srv.Name), value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Host", value: fmt.Sprintf("%s:%d", srv.Host, srv.Port), action: ""})
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Profile", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "    Display name", value: username, action: "edit_name"})
		s.items = append(s.items, settingsItem{label: "    Status", value: "(not set)", action: "edit_status"})
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Storage", value: "", action: ""})
		size, _ := config.ServerDataSize(s.configDir, srv)
		s.items = append(s.items, settingsItem{label: "    Local DB", value: formatBytes(size), action: ""})
		s.items = append(s.items, settingsItem{label: "    [Clear local history]", value: "", action: "clear_history"})
		s.items = append(s.items, settingsItem{label: "", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "  Keys", value: "", action: ""})
		s.items = append(s.items, settingsItem{label: "    SSH key", value: srv.Key, action: ""})
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
			s.editing = false
			s.editInput.Blur()

			switch action {
			case "edit_name":
				return s, func() tea.Msg {
					return ProfileUpdateMsg{DisplayName: value}
				}
			case "edit_status":
				return s, func() tea.Msg {
					return StatusUpdateMsg{Text: value}
				}
			}
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
			case item.action == "edit_name" || item.action == "edit_status":
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
// Y=4. Each items[i] takes exactly one line (including blank spacers which
// render as "\n").
func (s SettingsModel) HandleMouse(msg tea.MouseMsg) (SettingsModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return s, nil
	}
	// Don't handle clicks while editing or confirming — keyboard drives those
	if s.editing || s.confirm != nil {
		return s, nil
	}

	const firstItemY = 4
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
			line = completionSelectedStyle.Render(line)
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
	b.WriteString(helpDescStyle.Render(" ↑/↓=navigate  Enter=select  d=remove server  Esc=close"))

	return dialogStyle.Width(width - 4).Render(b.String())
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
