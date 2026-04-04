package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KeyWarningModel shows when a user's key has changed (potential MITM).
type KeyWarningModel struct {
	visible        bool
	user           string
	oldFingerprint string
	newFingerprint string
}

// KeyWarningAcceptMsg is sent when the user accepts the new key.
type KeyWarningAcceptMsg struct {
	User string
}

// KeyWarningDisconnectMsg is sent when the user chooses to disconnect.
type KeyWarningDisconnectMsg struct{}

func (k *KeyWarningModel) Show(user, oldFP, newFP string) {
	k.visible = true
	k.user = user
	k.oldFingerprint = oldFP
	k.newFingerprint = newFP
}

func (k *KeyWarningModel) Hide() {
	k.visible = false
}

func (k *KeyWarningModel) IsVisible() bool {
	return k.visible
}

func (k KeyWarningModel) Update(msg tea.KeyMsg) (KeyWarningModel, tea.Cmd) {
	switch msg.String() {
	case "a", "enter":
		user := k.user
		k.Hide()
		return k, func() tea.Msg {
			return KeyWarningAcceptMsg{User: user}
		}
	case "d", "esc":
		k.Hide()
		return k, func() tea.Msg {
			return KeyWarningDisconnectMsg{}
		}
	}
	return k, nil
}

func (k KeyWarningModel) View(width int) string {
	if !k.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(errorStyle.Render(" ⚠ Key Changed"))
	b.WriteString("\n\n")
	b.WriteString("  " + k.user + "'s key has changed since you\n")
	b.WriteString("  last communicated.\n\n")
	b.WriteString("  This could indicate the server has\n")
	b.WriteString("  been compromised or the user's key\n")
	b.WriteString("  was rotated.\n\n")
	b.WriteString("  Old: " + helpDescStyle.Render(k.oldFingerprint) + "\n")
	b.WriteString("  New: " + helpDescStyle.Render(k.newFingerprint) + "\n\n")
	b.WriteString("  [a] Accept new key  [d] Disconnect\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
