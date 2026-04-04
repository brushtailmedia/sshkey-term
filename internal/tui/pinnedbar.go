package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PinnedBarModel manages the collapsible pinned messages bar.
type PinnedBarModel struct {
	expanded bool
	pins     []string // pinned message IDs
	room     string
	cursor   int
}

// PinnedJumpMsg is sent when the user clicks a pinned message to jump to it.
type PinnedJumpMsg struct {
	MessageID string
}

func (p *PinnedBarModel) SetPins(room string, pins []string) {
	p.room = room
	p.pins = pins
	p.cursor = 0
}

func (p *PinnedBarModel) Toggle() {
	p.expanded = !p.expanded
}

func (p *PinnedBarModel) HasPins() bool {
	return len(p.pins) > 0
}

func (p PinnedBarModel) Update(msg tea.KeyMsg) (PinnedBarModel, tea.Cmd) {
	if !p.expanded {
		return p, nil
	}
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.pins)-1 {
			p.cursor++
		}
	case "enter":
		if p.cursor < len(p.pins) {
			id := p.pins[p.cursor]
			return p, func() tea.Msg {
				return PinnedJumpMsg{MessageID: id}
			}
		}
	case "esc":
		p.expanded = false
	}
	return p, nil
}

// View returns the pinned bar (collapsed or expanded). Returns empty if no pins.
func (p PinnedBarModel) View(width int) string {
	if len(p.pins) == 0 {
		return ""
	}

	if !p.expanded {
		return systemMsgStyle.Render(fmt.Sprintf(" ▶ %d pinned message(s)  (Ctrl+P to expand)", len(p.pins)))
	}

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" Pinned (%d)", len(p.pins))))
	b.WriteString("\n")

	for i, id := range p.pins {
		line := " 📌 " + id
		if i == p.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(systemMsgStyle.Render(" Enter=jump  Esc=collapse"))

	return b.String()
}
