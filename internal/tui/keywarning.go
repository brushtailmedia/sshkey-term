package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KeyWarningModel shows when an account's immutable key has changed.
type KeyWarningModel struct {
	visible        bool
	user           string
	oldFingerprint string
	newFingerprint string
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
	b.WriteString(errorStyle.Render(" ⚠ Account Key Changed"))
	b.WriteString("\n\n")
	b.WriteString("  Account key changed for " + k.user + ".\n")
	b.WriteString("  Account keys are immutable; this may indicate\n")
	b.WriteString("  server/state corruption or compromise.\n\n")
	b.WriteString("  The changed key was not accepted. If this user\n")
	b.WriteString("  needs a new key, retire the old account and\n")
	b.WriteString("  approve a new account.\n\n")
	b.WriteString("  Old: " + helpDescStyle.Render(k.oldFingerprint) + "\n")
	b.WriteString("  New: " + helpDescStyle.Render(k.newFingerprint) + "\n\n")
	b.WriteString("  [d] Disconnect  [Esc] Disconnect\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
