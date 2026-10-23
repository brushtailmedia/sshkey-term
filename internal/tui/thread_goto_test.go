package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

// Enter on a message row closes the view and asks to go to that message;
// it works on a reply and on the view's root alike.
func TestThreadPanel_EnterGoesToMessage(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	p, _ = p.Update(keyRune('j')) // mid
	p, cmd := p.Update(enterKey())
	if act := messageActionOf(t, cmd, "goto"); act.Msg.ID != "mid" {
		t.Errorf("goto target = %q, want mid", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Error("Enter must close the view")
	}

	p.Show("top", nestedMessages()) // cursor on the root
	p, cmd = p.Update(enterKey())
	if act := messageActionOf(t, cmd, "goto"); act.Msg.ID != "top" {
		t.Errorf("goto target = %q, want top", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Error("Enter on the root must close the view too")
	}
}

// Enter on ".." still goes up — the view stays for the App to re-show it
// rooted at the parent — and t keeps its meaning on every row.
func TestThreadPanel_EnterOnParentRowStillGoesUp(t *testing.T) {
	var p ThreadPanelModel
	p.Show("mid", nestedMessages())
	p, _ = p.Update(keyRune('k')) // ".."
	q, cmd := p.Update(enterKey())
	if act := threadActionOf(t, cmd); act.Data != "top" || act.Msg.ID != "top" {
		t.Errorf("Enter on ..: root=%q focus=%q, want top/top (land on the parent)", act.Data, act.Msg.ID)
	}
	if !q.IsVisible() {
		t.Error("going up must not hide the panel")
	}

	q, cmd = p.Update(keyRune('t'))
	if act := threadActionOf(t, cmd); act.Data != "top" {
		t.Errorf("t on ..: root=%q, want top", act.Data)
	}
	if !q.IsVisible() {
		t.Error("t on .. must not hide the panel")
	}
}

// End to end: Enter from the thread view lands on the message in the pane
// with focus on the pane — a navigation jump, like the pinned-bar jump.
func TestApp_ThreadEnter_LandsOnMessageInPane(t *testing.T) {
	a := newThreadActionApp(t) // view open on top, mid highlighted; pane cursor on top
	model, cmd := a.Update(enterKey())
	a = deliver(t, model.(App), cmd)
	if a.threadPanel.IsVisible() {
		t.Fatal("the view must be closed")
	}
	if sel := a.messages.SelectedMessage(); sel == nil || sel.ID != "mid" {
		t.Errorf("pane selection is not on the message Enter was pressed on")
	}
	if a.focus != FocusMessages {
		t.Errorf("focus = %v, want the pane (a navigation jump)", a.focus)
	}
}
