package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReplyPreview_FoundInView(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", From: "Alice", Body: "the original message here", TS: 1000},
		{ID: "msg_2", From: "Bob", Body: "this is a reply", TS: 1001, ReplyTo: "msg_1"},
	}

	preview := m.replyPreview("msg_1")
	if preview != "Alice: the original message here" {
		t.Errorf("preview = %q", preview)
	}
}

func TestReplyPreview_UsesResolverForFallbackFromID(t *testing.T) {
	m := NewMessages()
	m.resolveName = func(id string) string {
		if id == "usr_alice" {
			return "Alice"
		}
		return id
	}
	m.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", From: "usr_alice", Body: "hello", TS: 1000},
	}

	preview := m.replyPreview("msg_1")
	if preview != "Alice: hello" {
		t.Errorf("preview = %q, want %q", preview, "Alice: hello")
	}
}

func TestReplyPreview_Truncated(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "this is a very long message body that should be truncated at sixty characters to fit the display", TS: 1000},
	}

	preview := m.replyPreview("msg_1")
	if len(preview) > 60 {
		t.Errorf("preview should be <= 60 chars, got %d: %q", len(preview), preview)
	}
	if preview[len(preview)-3:] != "..." {
		t.Errorf("truncated preview should end with '...': %q", preview)
	}
}

func TestReplyPreview_NotFound(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000},
	}

	preview := m.replyPreview("msg_nonexistent_long_id")
	if preview != "msg_nonexist..." {
		t.Errorf("missing preview = %q", preview)
	}
}

func TestReplyPreview_ShortMissingID(t *testing.T) {
	m := NewMessages()
	preview := m.replyPreview("msg_1")
	if preview != "msg_1" {
		t.Errorf("short missing preview = %q", preview)
	}
}

func TestJumpToParent_MoveCursor(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "root", TS: 1000},
		{ID: "msg_2", From: "Bob", Body: "unrelated", TS: 1001},
		{ID: "msg_3", From: "Carol", Body: "reply", TS: 1002, ReplyTo: "msg_1"},
	}
	m.cursor = 2 // on the reply

	// Press g — should jump to msg_1 (index 0)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.cursor != 0 {
		t.Errorf("cursor after g = %d, want 0", m.cursor)
	}
}

func TestJumpToParent_NoReplyTo(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "not a reply", TS: 1000},
	}
	m.cursor = 0

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	// Should stay on same message (no ReplyTo)
	if m.cursor != 0 {
		t.Errorf("cursor should stay at 0, got %d", m.cursor)
	}
}

func TestThreadAction_FromRoot(t *testing.T) {
	m := NewMessages()
	m.messages = makeMessages()
	m.cursor = 0 // on msg_1 (the root)

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd == nil {
		t.Fatal("t should emit command")
	}
	msg := cmd()
	action, ok := msg.(MessageAction)
	if !ok {
		t.Fatalf("got %T, want MessageAction", msg)
	}
	if action.Action != "thread" {
		t.Errorf("action = %q, want thread", action.Action)
	}
	if action.Data != "msg_1" {
		t.Errorf("root ID = %q, want msg_1", action.Data)
	}
}

func TestThreadAction_FromReply(t *testing.T) {
	m := NewMessages()
	m.messages = makeMessages()
	m.cursor = 2 // on msg_3 which is a reply to msg_1

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd == nil {
		t.Fatal("t should emit command")
	}
	action := cmd().(MessageAction)
	// Should use the ReplyTo (msg_1) as root, not the reply itself
	if action.Data != "msg_1" {
		t.Errorf("root ID = %q, want msg_1 (parent of reply)", action.Data)
	}
}
