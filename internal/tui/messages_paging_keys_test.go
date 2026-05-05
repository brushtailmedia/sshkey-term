package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMessages_PgUpPgDownAliasesScrollViewport(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")

	for i := 0; i < 120; i++ {
		m.messages = append(m.messages, DisplayMessage{
			ID:   fmt.Sprintf("msg_%03d", i),
			From: "alice",
			Body: "hello",
			TS:   int64(i + 1),
			Room: "room_support",
		})
	}

	// Prime viewport sizing/content. NewMessages starts at bottom
	// after content refresh, which gives us room to scroll up.
	_ = m.View(80, 16, true)
	startOffset := m.viewport.YOffset
	if startOffset <= 0 {
		t.Fatalf("precondition failed: expected positive start YOffset, got %d", startOffset)
	}

	afterUp, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if afterUp.viewport.YOffset >= startOffset {
		t.Fatalf("pgup should scroll up: start=%d after=%d", startOffset, afterUp.viewport.YOffset)
	}

	afterDown, _ := afterUp.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if afterDown.viewport.YOffset <= afterUp.viewport.YOffset {
		t.Fatalf("pgdown should scroll down: afterUp=%d afterDown=%d", afterUp.viewport.YOffset, afterDown.viewport.YOffset)
	}
}
