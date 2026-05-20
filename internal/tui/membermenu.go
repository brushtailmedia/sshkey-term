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
	x, y    int // screen position — passed by Show, used by App overlay
}

func NewMemberMenu() MemberMenuModel {
	return MemberMenuModel{}
}

// Show opens the menu with caller-injected items. App owns the
// active client/sidebar/group state needed to decide which actions
// are available (e.g. "Add to group..." is only included when there's
// at least one eligible group — §9 step 7) so building the items
// list is the App's job; the menu stays dumb. Callers normally start
// from `defaultMemberMenuItems(displayName)` and append per context.
func (m *MemberMenuModel) Show(user string, items []ContextMenuItem, x, y int) {
	m.visible = true
	m.user = user
	m.cursor = 0
	m.x = x
	m.y = y
	m.items = items
}

func (m *MemberMenuModel) Hide() {
	m.visible = false
}

func (m *MemberMenuModel) IsVisible() bool {
	return m.visible
}

// AnchorXY returns the (col, row) anchor where the menu should be drawn.
func (m MemberMenuModel) AnchorXY() (int, int) {
	return m.x, m.y
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
