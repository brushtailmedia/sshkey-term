package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// UnverifyConfirmModel is the y/n confirmation dialog for `/unverify`.
// It is the **one net-new dialog in the entire shared-picker effort**
// (shared-picker-widget.md #8): applied to BOTH the typed
// `/unverify @user` path AND the picker selection path so behavior
// stays consistent. Removing a verification is a (mildly) destructive
// trust action — surfacing a confirm closes the previous "silent
// trust removal in one keystroke" gap. Shape mirrors AddConfirmModel
// / PromoteConfirmModel — the established confirm pattern in this
// package.
type UnverifyConfirmModel struct {
	visible    bool
	targetID   string
	targetName string
}

// UnverifyConfirmMsg is emitted on y/enter. App handles it by calling
// Store.ClearVerified(TargetID) + a status confirmation.
type UnverifyConfirmMsg struct {
	TargetID string
}

func (m *UnverifyConfirmModel) Show(targetID, targetName string) {
	m.visible = true
	m.targetID = targetID
	m.targetName = targetName
}

func (m *UnverifyConfirmModel) Hide() {
	m.visible = false
	m.targetID = ""
	m.targetName = ""
}

func (m *UnverifyConfirmModel) IsVisible() bool {
	return m.visible
}

func (m UnverifyConfirmModel) Update(msg tea.KeyMsg) (UnverifyConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		targetID := m.targetID
		m.Hide()
		return m, func() tea.Msg {
			return UnverifyConfirmMsg{TargetID: targetID}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m UnverifyConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Remove verification?"))
	b.WriteString("\n\n")
	target := m.targetName
	if target == "" {
		target = "this user"
	}
	b.WriteString("  Remove verification for " + errorStyle.Render(target) + "?\n\n")
	b.WriteString("  Their key will no longer be marked as verified. You'll\n")
	b.WriteString("  need to re-verify them before trusting their identity\n")
	b.WriteString("  again. Their messages still arrive — only the verified\n")
	b.WriteString("  marker is cleared.\n\n")
	b.WriteString("  [y] Remove verification  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
