package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// top ← mid ← {leaf1, leaf2}; top ← other. "mid" is a reply that has
// replies; "other" is a reply without any.
func nestedMessages() []DisplayMessage {
	return []DisplayMessage{
		{ID: "top", From: "Alice", FromID: "usr_a", Body: "top-level message", TS: 1000},
		{ID: "mid", From: "Bob", FromID: "usr_b", Body: "a reply with its own replies", TS: 1001, ReplyTo: "top"},
		{ID: "leaf1", From: "Carol", FromID: "usr_c", Body: "nested one", TS: 1002, ReplyTo: "mid"},
		{ID: "leaf2", From: "Dave", FromID: "usr_d", Body: "nested two", TS: 1003, ReplyTo: "mid"},
		{ID: "other", From: "Erin", FromID: "usr_e", Body: "a plain reply", TS: 1004, ReplyTo: "top"},
	}
}

// keyRune (single-rune KeyMsg) comes from input_mouse_filter_test.go.

func threadActionOf(t *testing.T, cmd tea.Cmd) MessageAction {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a command")
	}
	act, ok := cmd().(MessageAction)
	if !ok {
		t.Fatalf("expected MessageAction, got %T", cmd())
	}
	if act.Action != "thread" {
		t.Fatalf("expected a thread action, got %q", act.Action)
	}
	return act
}

func lineContaining(view, needle string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// The ".." row exists only in a nested view (root is a reply whose parent
// is loaded); the cursor starts on the root, not on "..".
func TestThreadPanel_ParentRowOnlyInNestedView(t *testing.T) {
	var p ThreadPanelModel

	p.Show("top", nestedMessages())
	// top + its whole subtree: mid, leaf1, leaf2, other.
	if p.hasParent || p.rowCount() != 5 || p.cursor != 0 {
		t.Errorf("top-level: hasParent=%v rows=%d cursor=%d", p.hasParent, p.rowCount(), p.cursor)
	}
	view := p.View(60, 20)
	if strings.Contains(view, " ..") {
		t.Errorf("top-level view must not show a .. row")
	}
	// No label anywhere: the row without an arrow is the root.
	if strings.Contains(view, "root") {
		t.Errorf("the root carries no label:\n%s", view)
	}

	p.Show("mid", nestedMessages())
	if !p.hasParent || p.rowCount() != 4 || p.cursor != 1 {
		t.Errorf("nested: hasParent=%v rows=%d cursor=%d", p.hasParent, p.rowCount(), p.cursor)
	}
	view = p.View(60, 20)
	dots := lineContaining(view, "..")
	if dots == "" {
		t.Fatalf("nested view lacks the .. row:\n%s", view)
	}
	// Bare: no arrow, no parent name or preview competing with the content.
	if strings.Contains(dots, "↑") || strings.Contains(dots, "Alice") || strings.Contains(dots, "top-level") {
		t.Errorf(".. row must be bare: %q", dots)
	}
	// The first message of a nested view is a reply, not a root: no label.
	if strings.Contains(view, "root") {
		t.Errorf("nested view must not label a reply as root:\n%s", view)
	}
}

// Enter or t on ".." opens the parent's thread; the panel stays up for the
// App to re-show it rooted at the parent.
func TestThreadPanel_ParentRowOpensParent(t *testing.T) {
	var p ThreadPanelModel
	p.Show("mid", nestedMessages())
	p, _ = p.Update(keyRune('k')) // from the root up to ".."
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (the .. row)", p.cursor)
	}
	// The hint says where the row leads; the keys are in the footer.
	if v := p.View(60, 20); !strings.Contains(v, "go up one level") || strings.Contains(v, "t=parent") {
		t.Errorf("highlighted .. row must show the 'go up one level' hint:\n%s", v)
	}
	p.cursor = 1 // root highlighted: the hint is selection-only
	if strings.Contains(p.View(60, 20), "go up one level") {
		t.Errorf("the .. hint must show only while .. is highlighted")
	}
	p.cursor = 0

	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, keyRune('t')} {
		q, cmd := p.Update(key)
		if act := threadActionOf(t, cmd); act.Data != "top" {
			t.Errorf("%s on ..: Data = %q, want top", key, act.Data)
		}
		if !q.IsVisible() {
			t.Errorf("%s on .. must not hide the panel", key)
		}
	}
}

// t opens a reply's own thread only when it has replies; the root and
// childless replies open nothing and never close the view.
func TestThreadPanel_TOpensReplyWithReplies(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())

	p, _ = p.Update(keyRune('j')) // mid
	q, cmd := p.Update(keyRune('t'))
	if act := threadActionOf(t, cmd); act.Data != "mid" {
		t.Errorf("t on mid: Data = %q, want mid", act.Data)
	}
	if !q.IsVisible() {
		t.Errorf("t on mid must not hide the panel")
	}

	p, _ = p.Update(keyRune('j')) // leaf1 (no replies) — depth-first, so it follows mid
	q, cmd = p.Update(keyRune('t'))
	if cmd != nil || !q.IsVisible() {
		t.Errorf("t on a childless reply must do nothing (cmd=%v visible=%v)", cmd != nil, q.IsVisible())
	}

	p, _ = p.Update(keyRune('k'))
	p, _ = p.Update(keyRune('k')) // root
	q, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !q.IsVisible() {
		t.Errorf("Enter on the root must do nothing (cmd=%v visible=%v)", cmd != nil, q.IsVisible())
	}
}

// The count appears only where something is hidden: in a shallow thread
// every reply's children are visible right below it, so no row shows one.
// The t=thread hint follows what `t` does instead — it shows on any
// highlighted reply that has replies, visible or not.
func TestThreadPanel_NoCountWhereChildrenVisible_HintFollowsT(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())

	view := p.View(60, 20) // cursor on root
	if strings.Contains(view, "·") {
		t.Errorf("no count when every child is visible:\n%s", view)
	}
	if !strings.Contains(view, "Thread (4 replies)") {
		t.Errorf("header must count the whole subtree:\n%s", view)
	}
	if strings.Contains(view, "t=thread") {
		t.Errorf("no hint on the root: re-rooting there is a no-op")
	}
	p, _ = p.Update(keyRune('j')) // mid: has replies, all visible
	if c := strings.Count(p.View(60, 20), "t=thread"); c != 1 {
		t.Errorf("hint count on a reply with replies = %d, want 1", c)
	}
	if _, cmd := p.Update(keyRune('t')); cmd == nil {
		t.Errorf("t must re-root on a reply with replies")
	}
	p, _ = p.Update(keyRune('j')) // leaf1: childless
	if strings.Contains(p.View(60, 20), "t=thread") {
		t.Errorf("no hint on a childless reply")
	}
}

// Replies are indented under a gray arrow; the root stays flush.
func TestThreadPanel_RepliesIndentedWithArrow(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	view := p.View(60, 20)

	if l := lineContaining(view, "Bob"); !strings.Contains(l, "│"+replyIndent+"↳ Bob") {
		t.Errorf("reply header not indented under an arrow: %q", l)
	}
	if l := lineContaining(view, "a reply with its own replies"); !strings.Contains(l, "│"+replyIndent+"  a reply with") {
		t.Errorf("reply body not indented under the name: %q", l)
	}
	if l := lineContaining(view, "Alice"); strings.Contains(l, "↳") || !strings.Contains(l, "│ Alice") {
		t.Errorf("root header must be flush and arrow-free: %q", l)
	}
	if l := lineContaining(view, "top-level message"); !strings.Contains(l, "│ top-level message") {
		t.Errorf("root body must be flush: %q", l)
	}
}

// Without the parent loaded there is no way up, so no ".." row.
func TestThreadPanel_MissingParentHidesParentRow(t *testing.T) {
	var p ThreadPanelModel
	msgs := nestedMessages()[1:] // drop "top"
	p.Show("mid", msgs)
	if p.hasParent || p.cursor != 0 {
		t.Errorf("hasParent=%v cursor=%d; want no .. row", p.hasParent, p.cursor)
	}
	view := p.View(60, 20)
	if strings.Contains(view, " ..") {
		t.Errorf("view must not show .. when the parent is not loaded")
	}
	// With no ".." the view's root is at depth 0: flush, no arrow — and
	// its replies at depth 1, as in a top-level view.
	if l := lineContaining(view, "Bob"); !strings.Contains(l, "│ Bob") || strings.Contains(l, "↳") {
		t.Errorf("root with an unloaded parent must sit flush without an arrow: %q", l)
	}
	if l := lineContaining(view, "Carol"); !strings.Contains(l, "│"+replyIndent+"↳ Carol") {
		t.Errorf("replies must sit at depth 1 when there is no .. row: %q", l)
	}
}

// Nested view: ".." is the top (depth 0), the view's root sits under it
// with an arrow of its own (depth 1), and its replies step one further in
// (depth 2) — so arrows never line up across levels.
func TestThreadPanel_NestedViewIndentsByDepth(t *testing.T) {
	var p ThreadPanelModel
	p.Show("mid", nestedMessages())
	view := p.View(60, 20)

	if l := lineContaining(view, ".."); !strings.Contains(l, "│ ..") {
		t.Errorf(".. must sit flush at the top: %q", l)
	}
	if l := lineContaining(view, "Bob"); !strings.Contains(l, "│"+replyIndent+"↳ Bob") {
		t.Errorf("nested root must sit under .. with an arrow: %q", l)
	}
	if l := lineContaining(view, "a reply with its own replies"); !strings.Contains(l, "│"+replyIndent+"  a reply with") {
		t.Errorf("nested root body must align under its name: %q", l)
	}
	deep := replyIndent + replyIndent
	for _, name := range []string{"Carol", "Dave"} {
		if l := lineContaining(view, name); !strings.Contains(l, "│"+deep+"↳ "+name) {
			t.Errorf("reply %s must sit one level below the nested root: %q", name, l)
		}
	}
	if l := lineContaining(view, "nested one"); !strings.Contains(l, "│"+deep+"  nested one") {
		t.Errorf("depth-2 body must align under its name: %q", l)
	}
	if strings.Contains(view, "root") {
		t.Errorf("no label in a nested view either:\n%s", view)
	}
}

// A deleted root shows its tombstone in the root's position: flush at the
// top level, under the arrow in a nested view. No "root · deleted" label.
func TestThreadPanel_DeletedRootHasNoLabel(t *testing.T) {
	msgs := nestedMessages()
	for i := range msgs {
		if msgs[i].ID == "top" || msgs[i].ID == "mid" {
			msgs[i].Deleted = true
		}
	}
	var p ThreadPanelModel

	p.Show("top", msgs)
	view := p.View(60, 20)
	if !strings.Contains(view, "│ ── message deleted ──") {
		t.Errorf("top-level deleted root must show a flush tombstone:\n%s", view)
	}
	if strings.Contains(view, "root") {
		t.Errorf("no root label on a tombstone:\n%s", view)
	}

	p.Show("mid", msgs)
	view = p.View(60, 20)
	if !strings.Contains(view, "│"+replyIndent+"↳ ── message deleted ──") {
		t.Errorf("nested deleted root must show its tombstone under the arrow:\n%s", view)
	}
}

// A missing root is refused: the panel keeps what it was showing rather
// than going blank.
func TestThreadPanel_ShowMissingRootKeepsView(t *testing.T) {
	var p ThreadPanelModel
	p.Show("top", nestedMessages())
	p.Show("nope", nestedMessages())
	if !p.IsVisible() || p.rootID != "top" {
		t.Errorf("visible=%v root=%q; want the previous thread kept", p.IsVisible(), p.rootID)
	}
	if !strings.Contains(p.View(60, 20), "top-level message") {
		t.Errorf("previous thread must still render")
	}
}

// A reply arriving while the panel is open shows after Refresh.
func TestThreadPanel_RefreshPicksUpNewReply(t *testing.T) {
	var p ThreadPanelModel
	msgs := nestedMessages()
	p.Show("top", msgs)
	if !strings.Contains(p.View(60, 20), "Thread (4 replies)") {
		t.Fatalf("expected 4 replies initially (the whole subtree)")
	}
	msgs = append(msgs, DisplayMessage{ID: "late", From: "Frank", FromID: "usr_f", Body: "late reply", TS: 2000, ReplyTo: "top"})
	p.Refresh(msgs)
	view := p.View(60, 20)
	if !strings.Contains(view, "Thread (5 replies)") || !strings.Contains(view, "late reply") {
		t.Errorf("refresh did not pick up the new reply:\n%s", view)
	}
}
