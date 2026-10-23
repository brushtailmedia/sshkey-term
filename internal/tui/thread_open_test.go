package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// deliver runs cmd and feeds what it yields back into the App, unwrapping
// a batch the way Bubble Tea would.
func deliver(t *testing.T, a App, cmd tea.Cmd) App {
	t.Helper()
	if cmd == nil {
		return a
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range m {
			a = deliver(t, a, c)
		}
	default:
		model, _ := a.Update(m)
		a = model.(App)
	}
	return a
}

// highlightedID is the message under the thread view's cursor, or "" on
// the ".." row.
func highlightedID(p ThreadPanelModel) string {
	if idx, ok := p.msgAt(p.cursor); ok {
		return p.messages[idx].ID
	}
	return ""
}

// The pane resolves a message's thread to its top-level root.
func TestThreadRootOf_WalksToTopLevel(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()
	for _, id := range []string{"top", "mid", "leaf1", "other"} {
		if got := m.threadRootOf(id); got != "top" {
			t.Errorf("threadRootOf(%s) = %q, want top", id, got)
		}
	}
}

// When an ancestor is not loaded (older history not fetched), the walk
// stops at the last loaded message.
func TestThreadRootOf_StopsAtMissingAncestor(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()[1:] // "top" not loaded
	if got := m.threadRootOf("leaf1"); got != "mid" {
		t.Errorf("threadRootOf(leaf1) = %q, want mid (top is not loaded)", got)
	}
}

// A malformed reply loop terminates.
func TestThreadRootOf_LoopTerminates(t *testing.T) {
	msgs := nestedMessages()
	msgs[0].ReplyTo = "mid" // top ↔ mid
	m := NewMessages()
	m.messages = msgs
	if got := m.threadRootOf("leaf1"); got != "top" && got != "mid" {
		t.Errorf("threadRootOf(leaf1) = %q, want a member of the loop", got)
	}
}

// t in the pane on a nested reply opens the WHOLE thread and names the
// pressed message, so the view can land on it.
func TestThreadAction_FromNestedReply_OpensTopLevelRoot(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()
	m.cursor = 2 // leaf1, a reply to mid, which replies to top
	_, cmd := m.Update(keyRune('t'))
	act := threadActionOf(t, cmd)
	if act.Data != "top" {
		t.Errorf("root = %q, want top", act.Data)
	}
	if act.Msg.ID != "leaf1" {
		t.Errorf("pressed message = %q, want leaf1", act.Msg.ID)
	}
}

// Select lands on the row showing the message, accounting for "..".
func TestThreadPanel_SelectLandsOnMessage(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	p.Select("leaf2")
	if got := highlightedID(p); got != "leaf2" {
		t.Errorf("highlighted = %q, want leaf2", got)
	}

	p.Show("mid", nestedMessages())
	p.Select("leaf1")
	if p.cursor != 2 || highlightedID(p) != "leaf1" { // .. 0, mid 1, leaf1 2
		t.Errorf("cursor = %d (%q), want 2 (leaf1)", p.cursor, highlightedID(p))
	}
}

// A message hidden below the cap selects its nearest visible ancestor —
// the capped row whose count covers it.
func TestThreadPanel_SelectFallsBackToCappedAncestor(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", chainMessages())
	p.Select("e") // hidden under c
	if got := highlightedID(p); got != "c" {
		t.Errorf("highlighted = %q, want c (the capped row hiding e)", got)
	}
}

// An id outside the tree leaves the cursor alone.
func TestThreadPanel_SelectUnknownKeepsCursor(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	p.cursor = 2
	p.Select("nope")
	if p.cursor != 2 {
		t.Errorf("cursor = %d, want 2", p.cursor)
	}
}

// Both directions name the new root as the row to land on: ".." the
// parent itself, drill-down the reply opened.
func TestThreadPanel_OpenActionsCarryFocus(t *testing.T) {
	var p ThreadPanelModel
	p.Show("mid", nestedMessages())
	p, _ = p.Update(keyRune('k')) // ".."
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if act := threadActionOf(t, cmd); act.Data != "top" || act.Msg.ID != "top" {
		t.Errorf("..: root=%q focus=%q, want top/top (land on the parent itself)", act.Data, act.Msg.ID)
	}

	p.Show("top", nestedMessages())
	p, _ = p.Update(keyRune('j')) // mid
	_, cmd = p.Update(keyRune('t'))
	if act := threadActionOf(t, cmd); act.Data != "mid" || act.Msg.ID != "mid" {
		t.Errorf("drill-down: root=%q focus=%q, want mid/mid", act.Data, act.Msg.ID)
	}
}

// End to end from the pane: t on a nested reply opens the whole thread
// with the cursor on that reply.
func TestApp_PaneT_OpensWholeThreadOnPressedMessage(t *testing.T) {
	a := newPaneThreadApp(t)
	a.messages.cursor = 2 // leaf1
	model, cmd := a.Update(keyRune('t'))
	a = deliver(t, model.(App), cmd)
	if !a.threadPanel.IsVisible() || a.threadPanel.rootID != "top" {
		t.Fatalf("visible=%v root=%q; want the whole thread rooted at top", a.threadPanel.IsVisible(), a.threadPanel.rootID)
	}
	if got := highlightedID(a.threadPanel); got != "leaf1" {
		t.Errorf("highlighted = %q, want the pressed message (leaf1)", got)
	}
	if a.threadPanel.hasParent {
		t.Errorf("a view opened from the pane must not be a nested view")
	}
}

// End to end inside the view: Enter on ".." goes up and lands on the parent
// itself (the new root); t on a reply drills down and lands on the new root.
func TestApp_ThreadNavigationLandsCursor(t *testing.T) {
	a := newPaneThreadApp(t)
	a.threadPanel.Show("mid", a.messages.messages)
	a.threadPanel, _ = a.threadPanel.Update(keyRune('k')) // ".."
	model, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = deliver(t, model.(App), cmd)
	if a.threadPanel.rootID != "top" || highlightedID(a.threadPanel) != "top" || a.threadPanel.cursor != a.threadPanel.rowOffset() {
		t.Errorf("after ..: root=%q highlighted=%q cursor=%d; want top on its root row", a.threadPanel.rootID, highlightedID(a.threadPanel), a.threadPanel.cursor)
	}

	a.threadPanel, _ = a.threadPanel.Update(keyRune('j')) // mid
	model, cmd = a.Update(keyRune('t'))                   // drill down
	a = deliver(t, model.(App), cmd)
	if a.threadPanel.rootID != "mid" || highlightedID(a.threadPanel) != "mid" || a.threadPanel.cursor != a.threadPanel.rowOffset() {
		t.Errorf("after drill-down: root=%q highlighted=%q cursor=%d; want mid on its root row", a.threadPanel.rootID, highlightedID(a.threadPanel), a.threadPanel.cursor)
	}
}
