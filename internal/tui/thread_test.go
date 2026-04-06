package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func makeMessages() []DisplayMessage {
	return []DisplayMessage{
		{ID: "msg_1", From: "Alice", FromID: "usr_a", Body: "original message", TS: 1000},
		{ID: "msg_2", From: "Bob", FromID: "usr_b", Body: "unrelated", TS: 1001},
		{ID: "msg_3", From: "Bob", FromID: "usr_b", Body: "first reply", TS: 1002, ReplyTo: "msg_1"},
		{ID: "msg_4", From: "Carol", FromID: "usr_c", Body: "second reply", TS: 1003, ReplyTo: "msg_1"},
		{ID: "msg_5", From: "Alice", FromID: "usr_a", Body: "reply to bob", TS: 1004, ReplyTo: "msg_2"},
	}
}

func TestThreadPanel_ShowCollectsThread(t *testing.T) {
	var tp ThreadPanelModel
	msgs := makeMessages()

	tp.Show("msg_1", msgs)

	if !tp.IsVisible() {
		t.Fatal("should be visible")
	}
	// Should have root + 2 replies to msg_1
	if len(tp.messages) != 3 {
		t.Fatalf("expected 3 messages (root + 2 replies), got %d", len(tp.messages))
	}
	if tp.messages[0].ID != "msg_1" {
		t.Errorf("first message should be root, got %s", tp.messages[0].ID)
	}
	if tp.messages[1].ID != "msg_3" {
		t.Errorf("second should be msg_3, got %s", tp.messages[1].ID)
	}
	if tp.messages[2].ID != "msg_4" {
		t.Errorf("third should be msg_4, got %s", tp.messages[2].ID)
	}
}

func TestThreadPanel_ShowNoReplies(t *testing.T) {
	var tp ThreadPanelModel
	msgs := makeMessages()

	// msg_2 has one reply (msg_5)
	tp.Show("msg_2", msgs)
	if len(tp.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tp.messages))
	}
}

func TestThreadPanel_ShowNonexistentRoot(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_nonexistent", makeMessages())
	if tp.IsVisible() {
		t.Error("should not be visible for nonexistent root")
	}
}

func TestThreadPanel_EscCloses(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_1", makeMessages())

	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tp.IsVisible() {
		t.Error("Esc should close")
	}
}

func TestThreadPanel_TKeyToggles(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_1", makeMessages())

	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if tp.IsVisible() {
		t.Error("t should close")
	}
}

func TestThreadPanel_Navigation(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_1", makeMessages())

	if tp.cursor != 0 {
		t.Fatalf("initial cursor = %d", tp.cursor)
	}

	// Down
	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tp.cursor != 1 {
		t.Errorf("cursor after j = %d, want 1", tp.cursor)
	}

	// Down again
	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tp.cursor != 2 {
		t.Errorf("cursor after 2x j = %d, want 2", tp.cursor)
	}

	// Down at end — clamp
	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tp.cursor != 2 {
		t.Errorf("cursor at end = %d, want 2", tp.cursor)
	}

	// Up
	tp, _ = tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if tp.cursor != 1 {
		t.Errorf("cursor after k = %d, want 1", tp.cursor)
	}
}

func TestThreadPanel_ReplyEmitsAction(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_1", makeMessages())

	tp, cmd := tp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r should emit a command")
	}
	msg := cmd()
	action, ok := msg.(MessageAction)
	if !ok {
		t.Fatalf("command returned %T, want MessageAction", msg)
	}
	if action.Action != "reply" {
		t.Errorf("action = %q, want reply", action.Action)
	}
	if action.Msg.ID != "msg_1" {
		t.Errorf("reply target = %q, want msg_1", action.Msg.ID)
	}
}

func TestThreadPanel_ViewRendersContent(t *testing.T) {
	var tp ThreadPanelModel
	tp.Show("msg_1", makeMessages())

	view := tp.View(60, 20)
	if !strings.Contains(view, "Thread") {
		t.Error("view should contain 'Thread'")
	}
	if !strings.Contains(view, "2 replies") {
		t.Error("view should show reply count")
	}
	if !strings.Contains(view, "original message") {
		t.Error("view should show root body")
	}
	if !strings.Contains(view, "first reply") {
		t.Error("view should show reply body")
	}
}
