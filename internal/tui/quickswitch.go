package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// QuickSwitchMsg is emitted when the user selects a room or group DM.
type QuickSwitchMsg struct {
	Room  string
	Group string
}

// QuickSwitchModel is a fuzzy-search overlay for switching rooms/group DMs.
type QuickSwitchModel struct {
	visible         bool
	input           textinput.Model
	rooms           []string
	groups          []protocol.GroupInfo
	resolveName     func(string) string // user nanoid → display name
	resolveRoomName func(string) string // room nanoid → display name
	filtered        []switchItem
	cursor          int
}

type switchItem struct {
	label string // display text
	room  string // non-empty for rooms
	group string // non-empty for group DMs
}

func NewQuickSwitch() QuickSwitchModel {
	ti := textinput.New()
	ti.Placeholder = "Switch to..."
	ti.Prompt = ""
	return QuickSwitchModel{input: ti}
}

func (q *QuickSwitchModel) Show(rooms []string, groups []protocol.GroupInfo, resolve, resolveRoom func(string) string) {
	q.visible = true
	q.rooms = rooms
	q.groups = groups
	q.resolveName = resolve
	q.resolveRoomName = resolveRoom
	q.input.SetValue("")
	q.input.Focus()
	q.cursor = 0
	q.updateFiltered()
}

func (q *QuickSwitchModel) Hide() {
	q.visible = false
	q.input.Blur()
}

func (q *QuickSwitchModel) IsVisible() bool {
	return q.visible
}

func (q *QuickSwitchModel) updateFiltered() {
	query := strings.ToLower(q.input.Value())
	q.filtered = nil

	for _, r := range q.rooms {
		displayName := r
		if q.resolveRoomName != nil {
			displayName = q.resolveRoomName(r)
		}
		label := "#" + displayName
		if query == "" || strings.Contains(strings.ToLower(label), query) {
			q.filtered = append(q.filtered, switchItem{label: label, room: r})
		}
	}

	for _, g := range q.groups {
		label := g.Name
		if label == "" {
			// Unnamed group — show member names
			var names []string
			for _, m := range g.Members {
				name := m
				if q.resolveName != nil {
					name = q.resolveName(m)
				}
				names = append(names, name)
			}
			label = strings.Join(names, ", ")
		}
		if query == "" || strings.Contains(strings.ToLower(label), query) {
			q.filtered = append(q.filtered, switchItem{label: label, group: g.ID})
		}
	}

	if q.cursor >= len(q.filtered) {
		q.cursor = 0
	}
}

func (q QuickSwitchModel) Update(msg tea.KeyMsg) (QuickSwitchModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		q.Hide()
		return q, nil
	case "enter":
		if len(q.filtered) > 0 && q.cursor < len(q.filtered) {
			item := q.filtered[q.cursor]
			q.Hide()
			return q, func() tea.Msg {
				return QuickSwitchMsg{Room: item.room, Group: item.group}
			}
		}
		return q, nil
	case "up":
		if q.cursor > 0 {
			q.cursor--
		}
		return q, nil
	case "down":
		if q.cursor < len(q.filtered)-1 {
			q.cursor++
		}
		return q, nil
	default:
		var cmd tea.Cmd
		q.input, cmd = q.input.Update(msg)
		q.updateFiltered()
		return q, cmd
	}
}

func (q QuickSwitchModel) View(width int) string {
	if !q.visible {
		return ""
	}

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Quick Switch"))
	b.WriteString("\n\n")
	b.WriteString("  " + q.input.View())
	b.WriteString("\n\n")

	max := 10
	if len(q.filtered) < max {
		max = len(q.filtered)
	}
	for i := 0; i < max; i++ {
		line := "  " + q.filtered[i].label
		if i == q.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if len(q.filtered) == 0 && q.input.Value() != "" {
		b.WriteString(helpDescStyle.Render("  No matches"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  Enter=switch  Esc=cancel"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
