package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMarkDeleted_FlagsMessage(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000},
		{ID: "msg_2", From: "Bob", Body: "world", TS: 1001},
	}

	m.MarkDeleted("msg_1", "Alice")

	if !m.messages[0].Deleted {
		t.Error("msg_1 should be deleted")
	}
	if m.messages[0].DeletedBy != "Alice" {
		t.Errorf("deleted_by = %q", m.messages[0].DeletedBy)
	}
	if m.messages[0].Body != "" {
		t.Errorf("body should be cleared, got %q", m.messages[0].Body)
	}
	// msg_2 untouched
	if m.messages[1].Deleted {
		t.Error("msg_2 should not be deleted")
	}
	if m.messages[1].Body != "world" {
		t.Errorf("msg_2 body = %q", m.messages[1].Body)
	}
}

func TestMarkDeleted_ClearsReactions(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{
			ID: "msg_1", From: "Alice", Body: "hello", TS: 1000,
			ReactionsByUser: map[string]map[string][]string{
				"bob": {"👍": {"react_1"}},
			},
		},
	}
	// Populate tracker
	reactionTracker["react_1"] = reactionMeta{msgID: "msg_1", user: "bob", emoji: "👍"}

	m.MarkDeleted("msg_1", "Alice")

	if m.messages[0].ReactionsByUser != nil {
		t.Error("reactions should be nil after delete")
	}
	if _, ok := reactionTracker["react_1"]; ok {
		t.Error("reaction tracker should be cleaned up")
	}
}

func TestMarkDeleted_ClearsAttachments(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{
			ID: "msg_1", From: "Alice", Body: "see attached", TS: 1000,
			Attachments: []DisplayAttachment{{FileID: "f1", Name: "doc.pdf"}},
		},
	}

	m.MarkDeleted("msg_1", "Alice")

	if m.messages[0].Attachments != nil {
		t.Error("attachments should be nil after delete")
	}
}

func TestMarkDeleted_MessageStaysInSlice(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000},
		{ID: "msg_2", From: "Bob", Body: "world", TS: 1001},
	}

	m.MarkDeleted("msg_1", "Alice")

	if len(m.messages) != 2 {
		t.Errorf("message count should stay at 2, got %d", len(m.messages))
	}
}

func TestMarkDeleted_NonexistentNoOp(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000},
	}

	m.MarkDeleted("msg_nonexistent", "Alice")

	if m.messages[0].Deleted {
		t.Error("should not have deleted any message")
	}
}

func TestView_RendersDeletedAsTombstone(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "", "")
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000, Room: "general"},
		{ID: "msg_2", FromID: "usr_bob", From: "Bob", Body: "", TS: 1001, Room: "general", Deleted: true, DeletedBy: "usr_bob"},
		{ID: "msg_3", From: "Carol", Body: "still here", TS: 1002, Room: "general"},
	}

	view := m.View(60, 20, false)
	if !strings.Contains(view, "message deleted") {
		t.Error("view should show 'message deleted' tombstone")
	}
	if !strings.Contains(view, "hello") {
		t.Error("non-deleted message should render")
	}
	if !strings.Contains(view, "still here") {
		t.Error("non-deleted message should render")
	}
}

func TestView_SelfDeleteHasNoSenderName(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "", "")
	m.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", From: "Alice", Body: "", TS: 1000, Room: "general", Deleted: true, DeletedBy: "usr_alice"},
	}

	view := m.View(60, 20, false)
	if !strings.Contains(view, "message deleted") {
		t.Error("self-delete should show 'message deleted'")
	}
	if strings.Contains(view, "removed by") {
		t.Error("self-delete should NOT show 'removed by'")
	}
}

func TestView_AdminDeleteShowsWho(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "", "")
	m.resolveName = func(s string) string {
		if s == "usr_admin" {
			return "AdminUser"
		}
		return s
	}
	m.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_bob", From: "Bob", Body: "", TS: 1000, Room: "general", Deleted: true, DeletedBy: "usr_admin"},
	}

	view := m.View(60, 20, false)
	if !strings.Contains(view, "removed by AdminUser") {
		t.Errorf("admin delete should show 'removed by AdminUser', got: %s", view)
	}
}

func TestReplyPreview_DeletedParent(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
		{ID: "msg_2", From: "Bob", Body: "reply", TS: 1001, ReplyTo: "msg_1"},
	}

	preview := m.replyPreview("msg_1")
	if preview != "Deleted message" {
		t.Errorf("reply preview for deleted parent = %q, want 'Deleted message'", preview)
	}
}

func TestReplyPreview_LiveParent(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "the original", TS: 1000},
	}

	preview := m.replyPreview("msg_1")
	if preview != "Alice: the original" {
		t.Errorf("reply preview = %q", preview)
	}
}

// --- Keyboard no-ops on deleted messages ---

func TestKeyboard_ReplyNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Error("reply should be no-op on deleted message")
	}
}

func TestKeyboard_ReactNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Error("react should be no-op on deleted message")
	}
}

func TestKeyboard_DeleteNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Error("delete should be no-op on already deleted message")
	}
}

func TestKeyboard_PinNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.SetContext("general", "", "")
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Room: "general", Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd != nil {
		t.Error("pin should be no-op on deleted message")
	}
}

func TestKeyboard_CopyNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Error("copy should be no-op on deleted message")
	}
}

func TestKeyboard_ContextMenuNoOpOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
	}
	m.cursor = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("context menu should be no-op on deleted message")
	}
}

func TestKeyboard_GoToParentStillWorksOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "root", TS: 1000},
		{ID: "msg_2", From: "Bob", Body: "", TS: 1001, Deleted: true, ReplyTo: "msg_1"},
	}
	m.cursor = 1

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.cursor != 0 {
		t.Errorf("g should still jump to parent from deleted reply, cursor = %d", m.cursor)
	}
}

func TestKeyboard_ThreadStillWorksOnDeleted(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "", TS: 1000, Deleted: true},
		{ID: "msg_2", From: "Bob", Body: "reply", TS: 1001, ReplyTo: "msg_1"},
	}
	m.cursor = 1 // on the reply

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd == nil {
		t.Error("thread view should still work — replies exist even if root is deleted")
	}
}
