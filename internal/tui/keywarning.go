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

	// Phase 21 F3.d closure 2026-04-19 — previous copy said "or the
	// user's key was rotated" which is false by protocol design.
	// Keys do not rotate in this app; a legitimate new-key flow
	// creates a NEW user account under a DIFFERENT user ID (which
	// does not trigger this modal). So a fingerprint change for an
	// existing user ID is always an anomaly. See PROTOCOL.md "Keys
	// as Identities" for the invariant.
	b.WriteString(errorStyle.Render(" ⚠ Key Changed"))
	b.WriteString("\n\n")
	b.WriteString("  " + k.user + "'s identity key changed.\n\n")
	b.WriteString("  Keys do not rotate in this app. A change\n")
	b.WriteString("  here indicates a compromised server, a\n")
	b.WriteString("  server bug, or local DB tampering.\n\n")
	b.WriteString("  If " + k.user + " is getting a new account (e.g.\n")
	b.WriteString("  after device loss), the admin retires the\n")
	b.WriteString("  old account and approves a new one with a\n")
	b.WriteString("  different user ID — that flow does NOT\n")
	b.WriteString("  trigger this warning.\n\n")
	b.WriteString("  Old: " + helpDescStyle.Render(k.oldFingerprint) + "\n")
	b.WriteString("  New: " + helpDescStyle.Render(k.newFingerprint) + "\n\n")
	b.WriteString("  [a] Accept new key  [d] Disconnect\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
