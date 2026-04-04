package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PinnedMessage holds a pin with preview info.
type PinnedMessage struct {
	ID     string
	From   string
	Body   string // truncated preview
}

// PinnedBarModel manages the collapsible pinned messages bar.
type PinnedBarModel struct {
	expanded bool
	pins     []PinnedMessage
	room     string
	cursor   int
}

// PinnedJumpMsg is sent when the user clicks a pinned message to jump to it.
type PinnedJumpMsg struct {
	MessageID string
}

func (p *PinnedBarModel) SetPins(room string, pinIDs []string, messages []DisplayMessage) {
	p.room = room
	p.cursor = 0
	p.pins = nil

	// Build previews by looking up each pinned ID in the message list
	msgMap := make(map[string]*DisplayMessage, len(messages))
	for i := range messages {
		msgMap[messages[i].ID] = &messages[i]
	}

	for _, id := range pinIDs {
		pin := PinnedMessage{ID: id, From: "unknown", Body: "(not loaded)"}
		if msg, ok := msgMap[id]; ok {
			pin.From = msg.From
			pin.Body = msg.Body
			if len(pin.Body) > 60 {
				pin.Body = pin.Body[:57] + "..."
			}
		}
		p.pins = append(p.pins, pin)
	}
}

// AddPin adds a single pin, looking up the message for preview.
func (p *PinnedBarModel) AddPin(id string, messages []DisplayMessage) {
	// Check if already pinned
	for _, pin := range p.pins {
		if pin.ID == id {
			return
		}
	}

	pin := PinnedMessage{ID: id, From: "unknown", Body: "(not loaded)"}
	for _, msg := range messages {
		if msg.ID == id {
			pin.From = msg.From
			pin.Body = msg.Body
			if len(pin.Body) > 60 {
				pin.Body = pin.Body[:57] + "..."
			}
			break
		}
	}
	p.pins = append(p.pins, pin)
}

// RemovePin removes a pin by message ID.
func (p *PinnedBarModel) RemovePin(id string) {
	for i, pin := range p.pins {
		if pin.ID == id {
			p.pins = append(p.pins[:i], p.pins[i+1:]...)
			if p.cursor >= len(p.pins) && p.cursor > 0 {
				p.cursor--
			}
			return
		}
	}
}

func (p *PinnedBarModel) Toggle() {
	p.expanded = !p.expanded
}

func (p *PinnedBarModel) HasPins() bool {
	return len(p.pins) > 0
}

// PinIDs returns the raw IDs (for compatibility).
func (p *PinnedBarModel) PinIDs() []string {
	ids := make([]string, len(p.pins))
	for i, pin := range p.pins {
		ids[i] = pin.ID
	}
	return ids
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
			id := p.pins[p.cursor].ID
			return p, func() tea.Msg {
				return PinnedJumpMsg{MessageID: id}
			}
		}
	case "u", "d", "delete":
		// Unpin the selected message
		if p.cursor < len(p.pins) {
			id := p.pins[p.cursor].ID
			return p, func() tea.Msg {
				return UnpinRequestMsg{MessageID: id}
			}
		}
	case "esc":
		p.expanded = false
	}
	return p, nil
}

// UnpinRequestMsg is sent when the user unpins from the pinned bar.
type UnpinRequestMsg struct {
	MessageID string
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

	for i, pin := range p.pins {
		line := fmt.Sprintf(" 📌 %s: %s", usernameStyle.Render(pin.From), pin.Body)
		if len(line) > width-4 {
			line = line[:width-7] + "..."
		}
		if i == p.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(systemMsgStyle.Render(" Enter=jump  u=unpin  Esc=collapse"))

	return b.String()
}
