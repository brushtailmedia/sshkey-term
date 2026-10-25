package tui

// `a=edit`: the one message the user may edit right now carries an a=edit
// hint when selected, in the pane and in the thread view, and `a` on it
// emits the edit action; both read the ID the App sets from the same
// predicate the context menu's Edit item uses.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// editPane is a pane in room_edit with alice's msg_3 marked editable and
// the cursor on the given message.
func editPane(cursor int) MessagesModel {
	m := NewMessages()
	m.SetContext("room_edit", "", "")
	m.SetSelfUserID("usr_alice")
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "alice", FromID: "usr_alice", Body: "first", TS: 1},
		{ID: "msg_2", From: "bob", FromID: "usr_bob", Body: "second", TS: 2},
		{ID: "msg_3", From: "alice", FromID: "usr_alice", Body: "latest from me", TS: 3},
	}
	m.SetEditableMessageID("msg_3")
	_ = m.View(100, 16, true)
	m.cursor = cursor
	return m
}

func TestPane_EditHintOnlyOnTheEditableMessage(t *testing.T) {
	m := editPane(2)
	if out := stripANSI(m.View(100, 16, true)); !strings.Contains(out, "a=edit") {
		t.Fatalf("the selected editable message must show a=edit:\n%s", out)
	}
	for _, cursor := range []int{0, 1} {
		m := editPane(cursor)
		if out := stripANSI(m.View(100, 16, true)); strings.Contains(out, "a=edit") {
			t.Fatalf("a=edit must not show with the cursor on message %d:\n%s", cursor, out)
		}
	}
	m = editPane(2)
	m.SetEditableMessageID("")
	if out := stripANSI(m.View(100, 16, true)); strings.Contains(out, "a=edit") {
		t.Fatal("a=edit must not show when nothing is editable")
	}
}

func TestPane_AKeyEmitsEditOnlyOnTheEditableMessage(t *testing.T) {
	m := editPane(2)
	_, cmd := m.Update(keyRune('a'))
	if act := messageActionOf(t, cmd, "edit"); act.Msg.ID != "msg_3" || act.Msg.Body != "latest from me" {
		t.Fatalf("edit action = %+v, want msg_3 with its body", act.Msg)
	}
	for _, cursor := range []int{0, 1} {
		m := editPane(cursor)
		if _, cmd := m.Update(keyRune('a')); cmd != nil {
			t.Fatalf("a on message %d must do nothing", cursor)
		}
	}
}

// threadWithEditable opens the nested fixture's thread with "mid" marked
// editable and the highlight on it.
func threadWithEditable() ThreadPanelModel {
	var p ThreadPanelModel
	p.SetEditableMessageID("mid")
	p.Show("top", nestedMessages())
	p.SetSize(100, 30)
	p, _ = p.Update(keyRune('j')) // mid
	return p
}

func TestThreadPanel_EditHintAndKey(t *testing.T) {
	p := threadWithEditable()
	if out := stripANSI(p.View(100, 30)); !strings.Contains(out, "a=edit") {
		t.Fatalf("the highlighted editable row must show a=edit:\n%s", out)
	}
	p, _ = p.Update(keyRune('k')) // back to the root, not editable
	if out := stripANSI(p.View(100, 30)); strings.Contains(out, "a=edit") {
		t.Fatal("a=edit must follow the editable row, not the highlight")
	}
	if q, cmd := p.Update(keyRune('a')); cmd != nil || !q.IsVisible() {
		t.Fatal("a on a row that is not editable must do nothing and keep the view")
	}

	p = threadWithEditable()
	p, cmd := p.Update(keyRune('a'))
	if act := messageActionOf(t, cmd, "edit"); act.Msg.ID != "mid" || act.Msg.Body == "" {
		t.Fatalf("edit action = %+v, want mid with its body", act.Msg)
	}
	if p.IsVisible() {
		t.Fatal("a must close the thread view first, as r and e do")
	}
}

// The App feeds both views from the menu's predicate on every update.
func TestApp_EditableMessageSyncedToPaneAndThreadView(t *testing.T) {
	a, _ := newEditAppHarness(t)
	editFixture(&a)

	model, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	got := model.(App)
	if got.messages.editableID != "msg_3" || got.threadPanel.editableID != "msg_3" {
		t.Fatalf("pane %q, thread view %q; want msg_3 in both", got.messages.editableID, got.threadPanel.editableID)
	}
	if !got.canEditMessage(got.messages.messages[2]) || got.canEditMessage(got.messages.messages[0]) {
		t.Fatal("the menu's predicate must agree: msg_3 editable, msg_1 not")
	}

	// A context the user has left: the views learn there is nothing to edit.
	got.messages.SetLeft(true)
	model, _ = got.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	got = model.(App)
	if got.messages.editableID != "" || got.threadPanel.editableID != "" {
		t.Fatalf("pane %q, thread view %q; want none in a left room", got.messages.editableID, got.threadPanel.editableID)
	}
}
