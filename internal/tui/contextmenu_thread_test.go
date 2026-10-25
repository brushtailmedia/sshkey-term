package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func menuLabels(c ContextMenuModel) []string {
	out := make([]string, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it.Label)
	}
	return out
}

func menuIndexOf(c ContextMenuModel, action string) int {
	for i, it := range c.items {
		if it.Action == action {
			return i
		}
	}
	return -1
}

// The Thread item appears only when the thread view has something to
// show, carries the reply count when there is one, and sits with the read
// actions — after Reply, React and the remove-reaction rows, before Pin.
func TestContextMenu_ThreadItem(t *testing.T) {
	msg := DisplayMessage{ID: "m1", FromID: "u1", From: "alice", Body: "hi"}

	var c ContextMenuModel
	c.Show(msg, 5, 5, ContextMenuOptions{})
	if menuIndexOf(c, "thread") >= 0 {
		t.Errorf("no Thread item when nothing is there to show: %v", menuLabels(c))
	}

	c.Show(msg, 5, 5, ContextMenuOptions{ThreadAvailable: true})
	if i := menuIndexOf(c, "thread"); i < 0 || c.items[i].Label != "Thread" {
		t.Errorf("want a plain Thread item for a reply/reacted message: %v", menuLabels(c))
	}

	c.Show(msg, 5, 5, ContextMenuOptions{
		IsRoom:           true,
		MyReactionEmojis: []string{"👍"},
		ThreadAvailable:  true,
		ThreadReplies:    3,
	})
	want := []string{"Reply", "React", "Remove reaction: 👍", "Thread (3 replies)", "Pin to room", "Copy text"}
	got := menuLabels(c)
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("items = %v, want %v", got, want)
		}
	}

	c.Show(msg, 5, 5, ContextMenuOptions{ThreadAvailable: true, ThreadReplies: 1})
	if i := menuIndexOf(c, "thread"); c.items[i].Label != "Thread (1 reply)" {
		t.Errorf("singular label: %q", c.items[i].Label)
	}
}

// Choosing Thread emits the thread action for the message, with no root:
// the App resolves the top-level message.
func TestContextMenu_ThreadItemEmitsAction(t *testing.T) {
	var c ContextMenuModel
	c.Show(DisplayMessage{ID: "m1"}, 5, 5, ContextMenuOptions{ThreadAvailable: true})
	for c.cursor != menuIndexOf(c, "thread") {
		c, _ = c.Update(keyRune('j'))
	}
	c, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	act := messageActionOf(t, cmd, "thread")
	if act.Msg.ID != "m1" || act.Data != "" {
		t.Errorf("thread action = %+v, want Msg m1 and an empty root", act)
	}
	if c.IsVisible() {
		t.Error("choosing an item hides the menu")
	}
}

// q closes the menu on both key paths: the menu's own Update and the
// App-level guard that also handles Esc.
func TestContextMenu_QCloses(t *testing.T) {
	var c ContextMenuModel
	c.Show(DisplayMessage{ID: "m1"}, 5, 5, ContextMenuOptions{})
	c, _ = c.Update(keyRune('q'))
	if c.IsVisible() {
		t.Error("q must close the menu (model path)")
	}

	a := App{}
	a.contextMenu = NewContextMenu()
	a.contextMenu.Show(DisplayMessage{ID: "m1"}, 5, 5, ContextMenuOptions{})
	model, _ := a.Update(keyRune('q'))
	got := model.(App)
	if got.contextMenu.IsVisible() {
		t.Error("q must close the menu (App guard path)")
	}
}

// The pane's answer for the menu: available for a root with replies, a
// reply, and a reacted message; the count is the whole subtree.
func TestMessages_ThreadInfo(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()
	m.messages = append(m.messages,
		DisplayMessage{ID: "plain", From: "Zed", FromID: "usr_z", Body: "nothing", TS: 3000},
		DisplayMessage{ID: "reacted", From: "Yan", FromID: "usr_y", Body: "popular", TS: 3001,
			ReactionsByUser: map[string]map[string][]string{"usr_a": {"👍": {"r1"}}}},
	)
	for _, tc := range []struct {
		id        string
		available bool
		replies   int
	}{
		{"top", true, 4},
		{"mid", true, 2},
		{"leaf1", true, 0}, // a reply: the view shows its thread
		{"reacted", true, 0},
		{"plain", false, 0},
		{"missing", false, 0},
	} {
		available, replies := m.ThreadInfo(tc.id)
		if available != tc.available || replies != tc.replies {
			t.Errorf("ThreadInfo(%s) = (%v, %d), want (%v, %d)", tc.id, available, replies, tc.available, tc.replies)
		}
	}
}

// End to end: Enter on a nested reply in the pane opens the menu with a
// Thread item; choosing it opens the whole thread with the cursor on that
// reply, exactly as `t` does.
func TestApp_ContextMenuThread_OpensWholeThread(t *testing.T) {
	a := newPaneThreadApp(t)
	a.messages.cursor = 2 // leaf1

	model, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open_menu
	a = deliver(t, model.(App), cmd)
	if !a.contextMenu.IsVisible() {
		t.Fatal("Enter on a message must open the context menu")
	}
	idx := menuIndexOf(a.contextMenu, "thread")
	if idx < 0 {
		t.Fatalf("menu lacks a Thread item for a reply: %v", menuLabels(a.contextMenu))
	}
	for a.contextMenu.cursor != idx {
		model, _ = a.Update(keyRune('j'))
		a = model.(App)
	}
	model, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = deliver(t, model.(App), cmd)
	if a.contextMenu.IsVisible() {
		t.Error("choosing Thread must close the menu")
	}
	if !a.threadPanel.IsVisible() || a.threadPanel.rootID != "top" {
		t.Fatalf("thread view: visible=%v root=%q; want the whole thread rooted at top", a.threadPanel.IsVisible(), a.threadPanel.rootID)
	}
	if got := highlightedID(a.threadPanel); got != "leaf1" {
		t.Errorf("highlighted = %q, want the message the menu was opened on (leaf1)", got)
	}
}
