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
	MemberPanel    string `toml:"member_panel"`
	PinnedMessages string `toml:"pinned_messages"`
	InfoPanel      string `toml:"info_panel"`
	Settings       string `toml:"settings"`
	Search         string `toml:"search"`
	CommandMode    string `toml:"command_mode"`
}

type NavigationKeys struct {
	PrevRoom   string `toml:"prev_room"`
	NextRoom   string `toml:"next_room"`
	Focus      string `toml:"sidebar_focus"`
	ScrollUp   string `toml:"scroll_up"`
	ScrollDown string `toml:"scroll_down"`
	JumpTop    string `toml:"jump_top"`
	JumpBottom string `toml:"jump_bottom"`
	Up         string `toml:"up"`
	Down       string `toml:"down"`
	VimUp      string `toml:"vim_up"`
	VimDown    string `toml:"vim_down"`
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
	Send      string `toml:"send"`
	Newline   string `toml:"newline"`
	Clear     string `toml:"clear"`
	SelectAll string `toml:"select_all"`
	Paste     string `toml:"paste"`
}

// DefaultKeybindings returns the default keybinding set.
func DefaultKeybindings() Keybindings {
	return Keybindings{
		Global: GlobalKeys{
			Quit:           "ctrl+q",
			QuickSwitch:    "ctrl+k",
			NewGroup:       "ctrl+n",
			MemberPanel:    "ctrl+m",
			PinnedMessages: "ctrl+p",
			InfoPanel:      "ctrl+i",
			Settings:       "ctrl+,",
			Search:         "ctrl+f",
			CommandMode:    "/",
		},
		Navigation: NavigationKeys{
			PrevRoom:   "alt+up",
			NextRoom:   "alt+down",
			Focus:      "tab",
			ScrollUp:   "pageup",
			ScrollDown: "pagedown",
			JumpTop:    "home",
			JumpBottom: "end",
			Up:         "up",
			Down:       "down",
			VimUp:      "k",
			VimDown:    "j",
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
			Send:      "enter",
			Newline:   "shift+enter",
			Clear:     "ctrl+u",
			SelectAll: "ctrl+a",
			Paste:     "ctrl+v",
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
# quick_switch = "ctrl+k"
# new_group = "ctrl+n"
# member_panel = "ctrl+m"
# pinned_messages = "ctrl+p"
# info_panel = "ctrl+i"
# settings = "ctrl+,"
# search = "ctrl+f"

# [navigation]
# prev_room = "alt+up"
# next_room = "alt+down"
# scroll_up = "pageup"
# scroll_down = "pagedown"

# [message]
# reply = "r"
# react = "e"
# pin = "p"
# delete = "d"
# copy = "c"

# [input]
# send = "enter"
# newline = "shift+enter"
`)
			f.Close()
		}
	}

	// Load user overrides
	var overrides Keybindings
	if _, err := toml.DecodeFile(userPath, &overrides); err == nil {
		mergeKeybindings(&kb, &overrides)
	}

	return kb
}

// mergeKeybindings applies non-empty overrides on top of defaults.
func mergeKeybindings(dst, src *Keybindings) {
	if src.Global.Quit != "" { dst.Global.Quit = src.Global.Quit }
	if src.Global.QuickSwitch != "" { dst.Global.QuickSwitch = src.Global.QuickSwitch }
	if src.Global.NewGroup != "" { dst.Global.NewGroup = src.Global.NewGroup }
	if src.Global.MemberPanel != "" { dst.Global.MemberPanel = src.Global.MemberPanel }
	if src.Global.PinnedMessages != "" { dst.Global.PinnedMessages = src.Global.PinnedMessages }
	if src.Global.InfoPanel != "" { dst.Global.InfoPanel = src.Global.InfoPanel }
	if src.Global.Settings != "" { dst.Global.Settings = src.Global.Settings }
	if src.Global.Search != "" { dst.Global.Search = src.Global.Search }
	if src.Navigation.PrevRoom != "" { dst.Navigation.PrevRoom = src.Navigation.PrevRoom }
	if src.Navigation.NextRoom != "" { dst.Navigation.NextRoom = src.Navigation.NextRoom }
	if src.Navigation.Focus != "" { dst.Navigation.Focus = src.Navigation.Focus }
	if src.Navigation.Up != "" { dst.Navigation.Up = src.Navigation.Up }
	if src.Navigation.Down != "" { dst.Navigation.Down = src.Navigation.Down }
	if src.Message.Reply != "" { dst.Message.Reply = src.Message.Reply }
	if src.Message.React != "" { dst.Message.React = src.Message.React }
	if src.Message.Pin != "" { dst.Message.Pin = src.Message.Pin }
	if src.Message.Delete != "" { dst.Message.Delete = src.Message.Delete }
	if src.Message.Copy != "" { dst.Message.Copy = src.Message.Copy }
	if src.Input.Send != "" { dst.Input.Send = src.Input.Send }
	if src.Input.Newline != "" { dst.Input.Newline = src.Input.Newline }
}
