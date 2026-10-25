package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The pane's title, as one value with its kind, is what the thread view's
// header reuses.
func TestMessages_ConversationTitle(t *testing.T) {
	m := NewMessages()
	m.resolveRoomName = func(id string) string { return "general" }
	m.resolveGroupName = func(id string) string { return "release crew" }
	m.resolveDMName = func(id string) string { return "Bob" }

	m.SetContext("room_1", "", "")
	if title, kind := m.ConversationTitle(); title != "general" || kind != "room" {
		t.Errorf("room: (%q, %q)", title, kind)
	}
	m.SetContext("", "grp_1", "")
	if title, kind := m.ConversationTitle(); title != "release crew" || kind != "group" {
		t.Errorf("group: (%q, %q)", title, kind)
	}
	m.SetContext("", "", "dm_1")
	if title, kind := m.ConversationTitle(); title != "Bob" || kind != "dm" {
		t.Errorf("dm: (%q, %q)", title, kind)
	}
	m.SetContext("", "", "")
	if title, kind := m.ConversationTitle(); title != "" || kind != "" {
		t.Errorf("no context: (%q, %q)", title, kind)
	}
}

// The header names the conversation when told, and stays plain otherwise.
func TestThreadPanel_HeaderNamesConversation(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	if v := p.View(60, 20); !strings.Contains(v, " Thread (4 replies)") {
		t.Errorf("plain header expected without a conversation:\n%s", v)
	}
	p.SetConversation("in general")
	if v := p.View(60, 20); !strings.Contains(v, " Thread in general (4 replies)") {
		t.Errorf("room header:\n%s", v)
	}
	p.SetConversation("with Bob")
	if v := p.View(60, 20); !strings.Contains(v, " Thread with Bob (4 replies)") {
		t.Errorf("dm header:\n%s", v)
	}
}

// End to end: opening the view from the pane carries the conversation
// name — a room by its resolved name, a DM by the partner's.
func TestApp_ThreadHeader_CarriesConversation(t *testing.T) {
	a := newPaneThreadApp(t)
	a.messages.resolveRoomName = func(id string) string { return "general" }
	a.messages.cursor = 1 // mid
	model, cmd := a.Update(keyRune('t'))
	a = deliver(t, model.(App), cmd)
	if v := a.threadPanel.View(80, 24); !strings.Contains(v, "Thread in general (") {
		t.Errorf("room thread header:\n%s", v)
	}

	a.threadPanel.Hide()
	a.messages.SetContext("", "", "dm_bob")
	a.messages.messages = nestedMessages()
	a.messages.resolveDMName = func(id string) string { return "Bob" }
	a.messages.cursor = 0
	a.focus = FocusMessages
	model, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	a = deliver(t, model.(App), cmd)
	if v := a.threadPanel.View(80, 24); !strings.Contains(v, "Thread with Bob (") {
		t.Errorf("dm thread header:\n%s", v)
	}
}
