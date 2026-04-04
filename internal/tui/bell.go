package tui

import (
	"fmt"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// BellConfig controls when the terminal bell fires.
type BellConfig struct {
	Mode          string            // "all", "mentions", "dms", "off"
	MuteRooms     map[string]bool   // room names where bell is silenced
	MuteDMs       bool              // silence bell for all DMs
	MuteMentions  bool              // silence bell for @mentions
}

// NewBellConfig creates a BellConfig from the notification config.
func NewBellConfig(cfg config.NotificationConfig) BellConfig {
	mode := cfg.Bell
	if mode == "" {
		mode = "mentions" // default
	}

	muteRooms := make(map[string]bool)
	for _, r := range cfg.BellMuteRooms {
		muteRooms[r] = true
	}

	return BellConfig{
		Mode:         mode,
		MuteRooms:    muteRooms,
		MuteDMs:      cfg.BellMuteDMs,
		MuteMentions: cfg.BellMuteMentions,
	}
}

// ShouldBell returns true if the bell should fire for this message.
// muteMap is the app-level mute state (room name or conv ID -> muted).
func (b BellConfig) ShouldBell(room, conversation, from, currentUser string, isMention bool, muteMap map[string]bool) bool {
	if b.Mode == "off" {
		return false
	}

	// Don't bell for own messages
	if from == currentUser {
		return false
	}

	// Check app-level mute (from info panel toggle)
	if room != "" && muteMap[room] {
		return false
	}
	if conversation != "" && muteMap[conversation] {
		return false
	}

	// Check config-level room mute
	if room != "" && b.MuteRooms[room] {
		return false
	}

	// Check DM mute
	if conversation != "" && b.MuteDMs {
		return false
	}

	// Check mention mute
	if isMention && b.MuteMentions {
		return false
	}

	switch b.Mode {
	case "all":
		return true
	case "mentions":
		return isMention
	case "dms":
		return conversation != ""
	default:
		return false
	}
}

// Ring sends the terminal bell character.
func Ring() {
	fmt.Print("\a")
}
