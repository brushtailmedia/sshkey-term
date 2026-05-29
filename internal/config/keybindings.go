package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Keybindings holds all configurable keybindings.
type Keybindings struct {
	Global     GlobalKeys     `toml:"global"`
	Navigation NavigationKeys `toml:"navigation"`
	Message    MessageKeys    `toml:"message"`
	Input      InputKeys      `toml:"input"`
}

type GlobalKeys struct {
	Quit           string `toml:"quit"`
	QuickSwitch    string `toml:"quick_switch"`
	NewGroup       string `toml:"new_group"`
	PinnedMessages string `toml:"pinned_messages"`
	Settings       string `toml:"settings"`
	Search         string `toml:"search"`
	CommandMode    string `toml:"command_mode"`
}

type NavigationKeys struct {
	PrevRoom            string `toml:"prev_room"`
	NextRoom            string `toml:"next_room"`
	Focus               string `toml:"sidebar_focus"`
	ScrollUp            string `toml:"scroll_up"`
	ScrollDown          string `toml:"scroll_down"`
	JumpTop             string `toml:"jump_top"`
	JumpBottom          string `toml:"jump_bottom"`
	Up                  string `toml:"up"`
	Down                string `toml:"down"`
	VimUp               string `toml:"vim_up"`
	VimDown             string `toml:"vim_down"`
	NavModePopupDelayMs int    `toml:"nav_mode_popup_delay_ms"`
	NavModePopup        bool   `toml:"nav_mode_popup"`
}

type MessageKeys struct {
	Reply       string `toml:"reply"`
	React       string `toml:"react"`
	Pin         string `toml:"pin"`
	Delete      string `toml:"delete"`
	Copy        string `toml:"copy"`
	ContextMenu string `toml:"context_menu"`
}

type InputKeys struct {
	Send    string `toml:"send"`
	Newline string `toml:"newline"`
}

// DefaultKeybindings returns the default keybinding set.
func DefaultKeybindings() Keybindings {
	return Keybindings{
		Global: GlobalKeys{
			Quit:           "ctrl+q",
			QuickSwitch:    "ctrl+g k",
			NewGroup:       "ctrl+g n",
			PinnedMessages: "ctrl+p",
			Settings:       "ctrl+g s",
			Search:         "ctrl+g /",
			CommandMode:    "/",
		},
		Navigation: NavigationKeys{
			PrevRoom:            "alt+up",
			NextRoom:            "alt+down",
			Focus:               "tab",
			ScrollUp:            "pageup",
			ScrollDown:          "pagedown",
			JumpTop:             "home",
			JumpBottom:          "end",
			Up:                  "up",
			Down:                "down",
			VimUp:               "k",
			VimDown:             "j",
			NavModePopupDelayMs: 300,
			NavModePopup:        true,
		},
		Message: MessageKeys{
			Reply:       "r",
			React:       "e",
			Pin:         "p",
			Delete:      "d",
			Copy:        "c",
			ContextMenu: "enter",
		},
		Input: InputKeys{
			Send:    "enter",
			Newline: "alt+enter",
		},
	}
}

// LoadKeybindings loads keybindings from the config directory.
// Merges user overrides on top of defaults.
func LoadKeybindings(configDir string) Keybindings {
	kb := DefaultKeybindings()

	// Write defaults file for reference
	defaultPath := filepath.Join(configDir, "keybindings.default.toml")
	if _, err := os.Stat(defaultPath); os.IsNotExist(err) {
		os.MkdirAll(configDir, 0700)
		f, err := os.Create(defaultPath)
		if err == nil {
			f.WriteString("# sshkey-chat default keybindings (auto-generated, do not edit)\n")
			f.WriteString("# See keybindings.toml for user overrides\n\n")
			toml.NewEncoder(f).Encode(kb)
			f.Close()
		}
	}

	// Write user override template if it doesn't exist
	userPath := filepath.Join(configDir, "keybindings.toml")
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		f, err := os.Create(userPath)
		if err == nil {
			f.WriteString(`# sshkey-chat keybindings
#
# Uncomment and modify any binding below to override the default.
# See keybindings.default.toml for the full reference.
# Only uncommented lines take effect.

# [global]
# quit = "ctrl+q"
# quick_switch = "ctrl+g k"
# new_group = "ctrl+g n"
# pinned_messages = "ctrl+p"
# settings = "ctrl+g s"
# search = "ctrl+g /"

# [navigation]
# prev_room = "alt+up"
# next_room = "alt+down"
# scroll_up = "pageup"
# scroll_down = "pagedown"
# nav_mode_popup_delay_ms = 300
# nav_mode_popup = true

# [message]
# reply = "r"
# react = "e"
# pin = "p"
# delete = "d"
# copy = "c"

# [input]
# send = "enter"
# newline = "alt+enter"
`)
			f.Close()
		}
	}

	// Load user overrides
	var overrides Keybindings
	if md, err := toml.DecodeFile(userPath, &overrides); err == nil {
		mergeKeybindings(&kb, &overrides)
		// Int fields need metadata checks so an explicit zero is not
		// mistaken for "unset".
		if md.IsDefined("navigation", "nav_mode_popup_delay_ms") {
			kb.Navigation.NavModePopupDelayMs = overrides.Navigation.NavModePopupDelayMs
		}
		if md.IsDefined("navigation", "nav_mode_popup") {
			kb.Navigation.NavModePopup = overrides.Navigation.NavModePopup
		}
	}

	return kb
}

// mergeKeybindings applies non-empty overrides on top of defaults.
func mergeKeybindings(dst, src *Keybindings) {
	if src.Global.Quit != "" {
		dst.Global.Quit = src.Global.Quit
	}
	if src.Global.QuickSwitch != "" {
		dst.Global.QuickSwitch = src.Global.QuickSwitch
	}
	if src.Global.NewGroup != "" {
		dst.Global.NewGroup = src.Global.NewGroup
	}
	if src.Global.PinnedMessages != "" {
		dst.Global.PinnedMessages = src.Global.PinnedMessages
	}
	if src.Global.Settings != "" {
		dst.Global.Settings = src.Global.Settings
	}
	if src.Global.Search != "" {
		dst.Global.Search = src.Global.Search
	}
	if src.Global.CommandMode != "" {
		dst.Global.CommandMode = src.Global.CommandMode
	}
	if src.Navigation.PrevRoom != "" {
		dst.Navigation.PrevRoom = src.Navigation.PrevRoom
	}
	if src.Navigation.NextRoom != "" {
		dst.Navigation.NextRoom = src.Navigation.NextRoom
	}
	if src.Navigation.Focus != "" {
		dst.Navigation.Focus = src.Navigation.Focus
	}
	if src.Navigation.ScrollUp != "" {
		dst.Navigation.ScrollUp = src.Navigation.ScrollUp
	}
	if src.Navigation.ScrollDown != "" {
		dst.Navigation.ScrollDown = src.Navigation.ScrollDown
	}
	if src.Navigation.JumpTop != "" {
		dst.Navigation.JumpTop = src.Navigation.JumpTop
	}
	if src.Navigation.JumpBottom != "" {
		dst.Navigation.JumpBottom = src.Navigation.JumpBottom
	}
	if src.Navigation.Up != "" {
		dst.Navigation.Up = src.Navigation.Up
	}
	if src.Navigation.Down != "" {
		dst.Navigation.Down = src.Navigation.Down
	}
	if src.Navigation.VimUp != "" {
		dst.Navigation.VimUp = src.Navigation.VimUp
	}
	if src.Navigation.VimDown != "" {
		dst.Navigation.VimDown = src.Navigation.VimDown
	}
	if src.Message.Reply != "" {
		dst.Message.Reply = src.Message.Reply
	}
	if src.Message.React != "" {
		dst.Message.React = src.Message.React
	}
	if src.Message.Pin != "" {
		dst.Message.Pin = src.Message.Pin
	}
	if src.Message.Delete != "" {
		dst.Message.Delete = src.Message.Delete
	}
	if src.Message.Copy != "" {
		dst.Message.Copy = src.Message.Copy
	}
	if src.Message.ContextMenu != "" {
		dst.Message.ContextMenu = src.Message.ContextMenu
	}
	if src.Input.Send != "" {
		dst.Input.Send = src.Input.Send
	}
	if src.Input.Newline != "" {
		dst.Input.Newline = src.Input.Newline
	}
}
