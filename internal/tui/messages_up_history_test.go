package tui

// Regression: the up arrow must load older history once the viewport is at the
// top of the loaded set — matching the mouse-wheel / pageup / home paths.
// Previously up only requested history at cursor==0 (after walking the cursor
// to the very first message), so up-arrow history loading felt broken next to
// the mouse.

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func seedMessages(m *MessagesModel, n int) {
	for i := 0; i < n; i++ {
		m.messages = append(m.messages, DisplayMessage{
			ID:   fmt.Sprintf("msg_%03d", i),
			From: "alice",
			Body: "hello",
			TS:   int64(i + 1),
			Room: "room_support",
		})
	}
}

func TestMessages_UpAtViewportTopLoadsHistory(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 120)
	m.hasMore = true

	_ = m.View(80, 16, true)
	m.viewport.GotoTop() // user scrolled to the top (mouse / pageup)
	if !m.viewport.AtTop() {
		t.Fatal("precondition: viewport should be at top")
	}

	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if cmd == nil {
		t.Fatal("up at viewport-top with hasMore must request history (was cursor==0-only)")
	}
	hist, ok := cmd().(HistoryRequestMsg)
	if !ok {
		t.Fatalf("expected HistoryRequestMsg, got %#v", cmd())
	}
	if hist.BeforeID != "msg_000" {
		t.Errorf("BeforeID = %q, want oldest loaded msg_000", hist.BeforeID)
	}
	if !after.loadingHistory {
		t.Error("requestHistory should set loadingHistory")
	}
}

func TestMessages_UpAtTopNoMoreHistoryEngagesCursor(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 5) // short — fits the viewport, so it's at top
	m.hasMore = false

	_ = m.View(80, 16, true)

	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		if _, ok := cmd().(HistoryRequestMsg); ok {
			t.Fatal("up with hasMore=false must not request history")
		}
	}
	// With no history to load, up engages cursor browsing instead.
	if after.cursor != len(m.messages)-1 {
		t.Errorf("up with no more history should engage cursor at last message, got cursor=%d", after.cursor)
	}
}

func TestMessages_UpAtTopWhileLoadingIsNoOp(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 120)
	m.hasMore = true
	m.loadingHistory = true // a fetch is already in flight

	_ = m.View(80, 16, true)
	m.viewport.GotoTop()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		t.Fatal("up at top while loadingHistory should be a no-op (no duplicate fetch)")
	}
}
