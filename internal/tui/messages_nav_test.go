package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func makeMultilineMessages(n int) []DisplayMessage {
	msgs := make([]DisplayMessage, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, DisplayMessage{
			ID:   fmt.Sprintf("msg_%d", i),
			From: fmt.Sprintf("user_%d", i), // avoid sender grouping so each row span is stable
			Body: "line 1\nline 2\nline 3\nline 4",
			TS:   int64(1000 + i),
		})
	}
	return msgs
}

func TestMessagesDownArrowScrollsViewportToSelectedMessage(t *testing.T) {
	m := NewMessages()
	m.hasMore = false
	m.messages = makeMultilineMessages(3)
	m.viewport.Height = 4
	m.RefreshContent(80)
	m.viewport.GotoTop()
	m.cursor = 0

	before := m.viewport.YOffset
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})

	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	if m.viewport.YOffset <= before {
		t.Fatalf("down should scroll viewport (before=%d after=%d)", before, m.viewport.YOffset)
	}

	start := m.rowMap[m.cursor]
	top := m.viewport.YOffset
	bottom := top + m.viewport.Height - 1
	if start < top || start > bottom {
		t.Fatalf("selected row %d not visible in [%d,%d]", start, top, bottom)
	}
}

func TestMessagesArrowRoundTripFromLatestScrollsBackDown(t *testing.T) {
	m := NewMessages()
	m.hasMore = false
	m.messages = makeMultilineMessages(12)
	m.viewport.Height = 6
	m.RefreshContent(80)

	m.cursor = len(m.messages) - 1 // simulate selecting latest message
	initialBottom := m.viewport.YOffset

	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	upOffset := m.viewport.YOffset
	if upOffset >= initialBottom {
		t.Fatalf("expected up-arrow path to move viewport up (start=%d up=%d)", initialBottom, upOffset)
	}

	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != len(m.messages)-1 {
		t.Fatalf("cursor after round-trip = %d, want %d", m.cursor, len(m.messages)-1)
	}
	if m.viewport.YOffset <= upOffset {
		t.Fatalf("down-arrow path should move viewport down (up=%d down=%d)", upOffset, m.viewport.YOffset)
	}

	start := m.rowMap[m.cursor]
	top := m.viewport.YOffset
	bottom := top + m.viewport.Height - 1
	if start < top || start > bottom {
		t.Fatalf("latest selected row %d not visible in [%d,%d]", start, top, bottom)
	}
}
