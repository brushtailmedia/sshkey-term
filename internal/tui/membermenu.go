package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// MemberMenuModel shows a context menu when clicking a member name.
type MemberMenuModel struct {
	visible bool
	user    string
	cursor  int
	items   []ContextMenuItem
}

func NewMemberMenu() MemberMenuModel {
	return MemberMenuModel{}
}

func (m *MemberMenuModel) Show(user, displayName string, x, y int) {
	m.visible = true
	m.user = user
	m.cursor = 0
	m.items = []ContextMenuItem{
		{Label: "Message " + displayName, Action: "message"},
		{Label: "Create group with...", Action: "create_group"},
		{Label: "Verify " + displayName, Action: "verify"},
		{Label: "View profile", Action: "profile"},
	}
}

func (m *MemberMenuModel) Hide() {
	m.visible = false
}

func (m *MemberMenuModel) IsVisible() bool {
	return m.visible
}

func (m MemberMenuModel) Update(msg tea.KeyMsg) (MemberMenuModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.Hide()
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < len(m.items) {
			action := m.items[m.cursor].Action
			user := m.user
			m.Hide()
			return m, func() tea.Msg {
				return MemberActionMsg{Action: action, User: user}
			}
		}
	}
	return m, nil
}

func (m MemberMenuModel) View() string {
	if !m.visible {
		return ""
	}

	var b strings.Builder
	for i, item := range m.items {
		line := "  " + item.Label
		if i == m.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return dialogStyle.Render(b.String())
}
