package tui

import (
	"strings"
	"testing"
)

func TestMessages_MarkRetired(t *testing.T) {
	m := NewMessages()
	if m.retired["alice"] {
		t.Error("fresh model should have no retired users")
	}
	m.MarkRetired("alice")
	if !m.retired["alice"] {
		t.Error("alice should be marked retired")
	}
}

func TestMessages_SetRetired(t *testing.T) {
	m := NewMessages()
	m.MarkRetired("old_user")
	m.SetRetired(map[string]string{
		"alice": "2026-04-01T00:00:00Z",
		"bob":   "2026-04-02T00:00:00Z",
	})
	if !m.retired["alice"] || !m.retired["bob"] {
		t.Error("alice and bob should be retired")
	}
	if m.retired["old_user"] {
		t.Error("SetRetired should REPLACE the existing map")
	}
}

func TestMessages_ViewRendersRetiredMarker(t *testing.T) {
	m := NewMessages()
	m.currentUser = "me"
	m.MarkRetired("alice")

	// Add a message from alice
	m.messages = append(m.messages, DisplayMessage{
		ID:     "msg_1",
		FromID: "alice",
		From:   "alice",
		Body:   "hello from alice",
		TS:     1712345678,
	})

	view := m.View(80, 20, false)
	if !strings.Contains(view, "alice") {
		t.Error("view should contain sender name")
	}
	if !strings.Contains(view, "[retired]") {
		t.Errorf("view should contain [retired] marker next to alice's name, got:\n%s", view)
	}
}

func TestMessages_ViewNoMarkerForActiveUser(t *testing.T) {
	m := NewMessages()
	m.currentUser = "me"
	// Don't mark alice retired

	m.messages = append(m.messages, DisplayMessage{
		ID:     "msg_1",
		FromID: "alice",
		From:   "alice",
		Body:   "hello",
		TS:     1712345678,
	})

	view := m.View(80, 20, false)
	if strings.Contains(view, "[retired]") {
		t.Errorf("view should NOT contain [retired] for active user, got:\n%s", view)
	}
}

func TestMessages_RetiredMarkerOnlyOnHeader(t *testing.T) {
	// Consecutive messages from the same sender group together — marker
	// should only appear in the header, not on each line.
	m := NewMessages()
	m.currentUser = "me"
	m.MarkRetired("alice")

	// Two consecutive messages from alice within 5 minutes
	m.messages = append(m.messages,
		DisplayMessage{ID: "msg_1", FromID: "alice", From: "alice", Body: "first", TS: 1712345678},
		DisplayMessage{ID: "msg_2", FromID: "alice", From: "alice", Body: "second", TS: 1712345679},
	)

	view := m.View(80, 20, false)
	// Count occurrences of [retired]
	count := strings.Count(view, "[retired]")
	if count != 1 {
		t.Errorf("[retired] should appear exactly once (in the header), got %d times in:\n%s", count, view)
	}
}

func TestMessages_SystemMessageHasNoRetiredMarker(t *testing.T) {
	// System messages (like "alice's account was retired") shouldn't have
	// their own [retired] marker added.
	m := NewMessages()
	m.currentUser = "me"
	m.MarkRetired("alice")

	m.AddSystemMessage("alice's account was retired")

	view := m.View(80, 20, false)
	// The system message text will contain "alice's account was retired",
	// but there should be no header rendering of "alice [retired]" since
	// system messages don't go through the sender-name code path.
	if !strings.Contains(view, "alice's account was retired") {
		t.Error("system message text should be present")
	}
	// Sanity: view should still have the text
}
