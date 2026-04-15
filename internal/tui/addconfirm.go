package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// AddConfirmModel is the y/n confirmation dialog for /add. Phase 14.
// Asks the user to confirm adding a target to the current group.
// Target is resolved to a nanoid user ID before the dialog is shown,
// so the dialog just displays the target's display name for confirmation
// and emits AddConfirmMsg on y/enter.
//
// Rich contextual content (join date, resulting member count, etc.)
// is deferred to Chunk 6 where the rendering polish lives — this
// minimum-viable dialog just confirms the action.
type AddConfirmModel struct {
	visible    bool
	group      string
	groupName  string
	targetID   string
	targetName string
}

// AddConfirmMsg is emitted when the user confirms /add. The app handles
// it by sending add_to_group via client.AddToGroup.
type AddConfirmMsg struct {
	Group    string
	TargetID string
}

func (m *AddConfirmModel) Show(groupID, groupName, targetID, targetName string) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.targetID = targetID
	m.targetName = targetName
}

func (m *AddConfirmModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.targetID = ""
	m.targetName = ""
}

func (m *AddConfirmModel) IsVisible() bool {
	return m.visible
}

func (m AddConfirmModel) Update(msg tea.KeyMsg) (AddConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		groupID := m.group
		targetID := m.targetID
		m.Hide()
		return m, func() tea.Msg {
			return AddConfirmMsg{Group: groupID, TargetID: targetID}
		}
	case "n", "esc":
		m.Hide()
		return m, nil
	}
	return m, nil
}

func (m AddConfirmModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Add member?"))
	b.WriteString("\n\n")
	groupName := m.groupName
	if groupName == "" {
		groupName = "this group"
	}
	b.WriteString("  Add " + errorStyle.Render(m.targetName) + " to " + errorStyle.Render(groupName) + "?\n\n")
	b.WriteString("  " + m.targetName + " will see new messages from this point forward.\n")
	b.WriteString("  They cannot decrypt messages sent before they were added.\n\n")
	b.WriteString("  [y] Add  [n] Cancel\n")
	return dialogStyle.Width(width - 4).Render(b.String())
}
