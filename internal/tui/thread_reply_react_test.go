package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newPaneThreadApp is a live-session App with the nested fixture loaded in
// the pane, focus on the pane, and no overlay open.
func newPaneThreadApp(t *testing.T) App {
	t.Helper()
	return newTestApp(t, withFocus(FocusMessages), withContext("general", "", ""), withMessages(nestedMessages()))
}

// newThreadActionApp is newPaneThreadApp with the thread view open on
// "top", highlighting "mid".
func newThreadActionApp(t *testing.T) App {
	t.Helper()
	a := newPaneThreadApp(t)
	a.threadPanel.Show("top", a.messages.messages)
	a.threadPanel, _ = a.threadPanel.Update(keyRune('j')) // mid
	return a
}

// pressInThread sends one key through the App and then delivers the
// command's message, the way Bubble Tea would.
func pressInThread(t *testing.T, a App, key tea.KeyMsg) App {
	t.Helper()
	model, cmd := a.Update(key)
	a = model.(App)
	if a.threadPanel.IsVisible() {
		t.Fatalf("%s must close the thread view", key)
	}
	if cmd == nil {
		t.Fatalf("%s must emit the action", key)
	}
	model, _ = a.Update(cmd())
	return model.(App)
}

// End to end: r from the thread view leaves the user in the input with the
// reply context on the highlighted reply, and the pane selecting it.
func TestApp_ThreadReply_FocusesInputOnHighlighted(t *testing.T) {
	a := pressInThread(t, newThreadActionApp(t), keyRune('r'))
	if a.focus != FocusInput {
		t.Errorf("focus = %v, want FocusInput", a.focus)
	}
	if a.input.replyTo != "mid" {
		t.Errorf("input reply target = %q, want mid", a.input.replyTo)
	}
	if sel := a.messages.SelectedMessage(); sel == nil || sel.ID != "mid" {
		t.Errorf("pane selection is not on the replied-to message")
	}
}

// End to end: e from the thread view opens the picker on the highlighted
// reply, with the pane selecting it so the reaction lands on the
// highlighted row when the picker closes.
func TestApp_ThreadReact_OpensPickerOnHighlighted(t *testing.T) {
	a := pressInThread(t, newThreadActionApp(t), keyRune('e'))
	if !a.emojiPicker.IsVisible() {
		t.Fatal("picker must be open")
	}
	if a.emojiPicker.targetMsg.ID != "mid" {
		t.Errorf("picker target = %q, want mid", a.emojiPicker.targetMsg.ID)
	}
	if sel := a.messages.SelectedMessage(); sel == nil || sel.ID != "mid" {
		t.Errorf("pane selection is not on the reacted-to message")
	}
}

// messageActionOf runs cmd and asserts it yields a MessageAction of the
// wanted kind.
func messageActionOf(t *testing.T, cmd tea.Cmd, want string) MessageAction {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a %q command", want)
	}
	act, ok := cmd().(MessageAction)
	if !ok {
		t.Fatalf("expected MessageAction, got %T", cmd())
	}
	if act.Action != want {
		t.Fatalf("action = %q, want %q", act.Action, want)
	}
	return act
}

// r replies to the HIGHLIGHTED message, not the root, and closes the view
// so the input's reply line is in front and the typing reaches it.
func TestThreadPanel_ReplyTargetsHighlightedAndCloses(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	p, _ = p.Update(keyRune('j')) // mid

	p, cmd := p.Update(keyRune('r'))
	if act := messageActionOf(t, cmd, "reply"); act.Msg.ID != "mid" {
		t.Errorf("reply target = %q, want the highlighted reply (mid)", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Error("r must close the thread view")
	}
}

// e reacts on the highlighted row — root or reply — and closes the view so
// the picker (an overlay the thread view would otherwise hide) is in front.
func TestThreadPanel_ReactTargetsHighlightedAndCloses(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())

	p, cmd := p.Update(keyRune('e')) // root highlighted
	if act := messageActionOf(t, cmd, "react"); act.Msg.ID != "top" {
		t.Errorf("react target = %q, want the root (top)", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Error("e must close the thread view")
	}

	p.Show("top", nestedMessages())
	p, _ = p.Update(keyRune('j'))
	p, _ = p.Update(keyRune('j')) // leaf1: depth-first, mid's first child follows mid
	p, cmd = p.Update(keyRune('e'))
	if act := messageActionOf(t, cmd, "react"); act.Msg.ID != "leaf1" {
		t.Errorf("react target = %q, want the highlighted reply (leaf1)", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Error("e must close the thread view")
	}
}

// The ".." row is navigation: r and e do nothing there and the view stays.
func TestThreadPanel_ParentRowTakesNeitherReplyNorReact(t *testing.T) {
	var p ThreadPanelModel
	p.Show("mid", nestedMessages())
	p, _ = p.Update(keyRune('k')) // up to ".."
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (the .. row)", p.cursor)
	}
	for _, r := range []rune{'r', 'e'} {
		q, cmd := p.Update(keyRune(r))
		if cmd != nil || !q.IsVisible() {
			t.Errorf("%c on ..: cmd=%v visible=%v; want no action and the view kept", r, cmd != nil, q.IsVisible())
		}
	}
}

// Tombstones take neither key, matching the pane's guard.
func TestThreadPanel_TombstoneTakesNeitherReplyNorReact(t *testing.T) {
	msgs := nestedMessages()
	for i := range msgs {
		if msgs[i].ID == "leaf1" {
			msgs[i].Deleted = true
		}
	}
	var p ThreadPanelModel
	p.Show("top", msgs)
	p, _ = p.Update(keyRune('j'))
	p, _ = p.Update(keyRune('j')) // leaf1 (deleted)
	if idx, ok := p.msgAt(p.cursor); !ok || p.messages[idx].ID != "leaf1" {
		t.Fatalf("cursor is not on the tombstone")
	}
	for _, r := range []rune{'r', 'e'} {
		q, cmd := p.Update(keyRune(r))
		if cmd != nil || !q.IsVisible() {
			t.Errorf("%c on a tombstone: cmd=%v visible=%v; want no action and the view kept", r, cmd != nil, q.IsVisible())
		}
	}
}
