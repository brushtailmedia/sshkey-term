package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PinnedMessage holds a pin with preview info.
type PinnedMessage struct {
	ID   string
	From string
	Body string // truncated body for the preview line (≤ 60 chars)
}

// PinnedJumpMsg is emitted when the user presses Enter (or clicks) on
// a pin in the expanded bar — it asks the App to scroll the messages
// pane to that ID, select the message, and shift focus there. The
// pinned bar itself collapses (set p.expanded = false in Update)
// before the Cmd fires so the menu/cursor isn't hidden behind it.
type PinnedJumpMsg struct {
	MessageID string
}

// PinnedBarModel manages the collapsible pinned messages bar.
type PinnedBarModel struct {
	expanded bool
	pins     []PinnedMessage
	room     string
	cursor   int
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

// Update handles key events for the expanded pinned bar. The bar acts
// as a focused selector over its pins: up/down moves the cursor,
// Enter jumps to the selected pin, u unpins it, and Ctrl+P or Esc
// collapses the bar.
//
//	up, k       move cursor up one pin
//	down, j     move cursor down one pin
//	enter       jump → app scrolls + selects the message, focus moves
//	            to messages, bar collapses
//	u           unpin selected pin (UnpinRequestMsg)
//	ctrl+p, esc collapse the bar
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
		// Jump to the selected pin in the messages pane. Collapse
		// the bar first so the message the user just jumped to isn't
		// covered by the expanded bar; the App handler shifts focus
		// to messages so the cursor highlight is visible.
		if p.cursor < len(p.pins) {
			id := p.pins[p.cursor].ID
			p.expanded = false
			return p, func() tea.Msg {
				return PinnedJumpMsg{MessageID: id}
			}
		}

	case "u":
		// Unpin the selected pin. UnpinRequestMsg has an existing
		// handler in app.go that emits the wire envelope.
		if p.cursor < len(p.pins) {
			id := p.pins[p.cursor].ID
			return p, func() tea.Msg {
				return UnpinRequestMsg{MessageID: id}
			}
		}

	case "ctrl+p", "esc":
		// Ctrl+P is the canonical collapse — same key that opened it.
		// Esc kept as a secondary dismiss path matching other modals.
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
		return systemMsgStyle.Render(fmt.Sprintf(" ▶ %d pinned message(s)  (Ctrl+p to expand)", len(p.pins)))
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
	b.WriteString(systemMsgStyle.Render(" Enter=jump  u=unpin  Ctrl+p=collapse"))

	return b.String()
}
