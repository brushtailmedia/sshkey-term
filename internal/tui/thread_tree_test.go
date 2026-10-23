package tui

import (
	"fmt"
	"strings"
	"testing"
)

// chainMessages is root ← a ← b ← c ← d ← e, a chain five replies deep.
// With the cap three levels below the root, c is the last row shown and
// d, e are hidden under it.
func chainMessages() []DisplayMessage {
	ids := []string{"root", "a", "b", "c", "d", "e"}
	names := []string{"Alice", "Bob", "Carol", "Dave", "Erin", "Frank"}
	msgs := make([]DisplayMessage, 0, len(ids))
	for i, id := range ids {
		m := DisplayMessage{ID: id, From: names[i], FromID: "usr_" + id, Body: "body " + id, TS: int64(1000 + i)}
		if i > 0 {
			m.ReplyTo = ids[i-1]
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func idList(msgs []DisplayMessage) string {
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = m.ID
	}
	return strings.Join(parts, ",")
}

// Depth-first: each reply is followed by its own replies, siblings in time
// order. Flat time order would interleave the subtrees.
func TestThreadTree_DepthFirstOrder(t *testing.T) {
	msgs := []DisplayMessage{
		{ID: "root", TS: 1},
		{ID: "x", TS: 2, ReplyTo: "root"},
		{ID: "y", TS: 3, ReplyTo: "root"},
		{ID: "x1", TS: 4, ReplyTo: "x"},
		{ID: "y1", TS: 5, ReplyTo: "y"},
		{ID: "x2", TS: 6, ReplyTo: "x"},
	}
	var p ThreadPanelModel
	p.Show("root", msgs)
	if got, want := idList(p.messages), "root,x,x1,x2,y,y1"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(p.depths), fmt.Sprint([]int{0, 1, 2, 2, 1, 2}); got != want {
		t.Errorf("depths = %s, want %s", got, want)
	}
	if p.hiddenTotal != 0 {
		t.Errorf("nothing is deeper than the cap here; hiddenTotal = %d", p.hiddenTotal)
	}
}

// The cap hides rows below three levels, counts them on the row at the
// cap, and the header counts the whole subtree. The hint follows `t`: it
// shows on any highlighted reply with replies, capped or not.
func TestThreadTree_CapHidesAndCounts(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", chainMessages())
	if got, want := idList(p.messages), "root,a,b,c"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	if p.hidden[3] != 2 || p.hiddenTotal != 2 {
		t.Errorf("hidden under c = %d (total %d), want 2", p.hidden[3], p.hiddenTotal)
	}

	view := p.View(80, 24) // cursor on root
	if !strings.Contains(view, "Thread (5 replies)") {
		t.Errorf("header must count shown and hidden replies:\n%s", view)
	}
	if l := lineContaining(view, "Dave"); !strings.Contains(l, "· 2 more replies") {
		t.Errorf("row at the cap must count what is hidden beneath it: %q", l)
	}
	for _, name := range []string{"Bob", "Carol"} {
		if l := lineContaining(view, name); strings.Contains(l, "more") {
			t.Errorf("%s's children are visible; no count: %q", name, l)
		}
	}
	if strings.Contains(view, "body d") || strings.Contains(view, "body e") {
		t.Errorf("rows below the cap must be hidden, not flattened:\n%s", view)
	}
	if strings.Contains(view, "t=thread") {
		t.Errorf("no hint while the root is highlighted")
	}
	p.cursor = 2 // b: has replies, visible below it
	if c := strings.Count(p.View(80, 24), "t=thread"); c != 1 {
		t.Errorf("hint count on a reply with visible replies = %d, want 1", c)
	}
	p.cursor = 3 // c: at the cap, descendants hidden
	if c := strings.Count(p.View(80, 24), "t=thread"); c != 1 {
		t.Errorf("hint count with the capped row highlighted = %d, want 1", c)
	}
}

// t on the row at the cap re-roots there; the hidden rows become visible
// under "..", with three fresh levels.
func TestThreadTree_CapRowOpensAsThread(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", chainMessages())
	p.cursor = 3 // c
	_, cmd := p.Update(keyRune('t'))
	if act := threadActionOf(t, cmd); act.Data != "c" {
		t.Fatalf("t on the capped row: Data = %q, want c", act.Data)
	}

	p.Show("c", chainMessages())
	if got, want := idList(p.messages), "c,d,e"; got != want || !p.hasParent {
		t.Errorf("re-rooted rows = %s (hasParent=%v), want %s under ..", got, p.hasParent, want)
	}
	if p.hiddenTotal != 0 || !strings.Contains(p.View(80, 24), "Thread (2 replies)") {
		t.Errorf("nothing hidden after re-rooting; hiddenTotal = %d", p.hiddenTotal)
	}
}

// The cap counts from the view's root, not from "..": re-rooted on a reply,
// the view still gets three levels under it.
func TestThreadTree_CapCountsFromNestedRoot(t *testing.T) {
	var p ThreadPanelModel
	p.Show("a", chainMessages()) // a ← b ← c ← d shown; e hidden under d
	if got, want := idList(p.messages), "a,b,c,d"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	if p.hidden[3] != 1 {
		t.Errorf("hidden under d = %d, want 1", p.hidden[3])
	}
	view := p.View(80, 24)
	deep := strings.Repeat(replyIndent, 4) // .. is 0, a is 1, d is 4
	if l := lineContaining(view, "Erin"); !strings.Contains(l, "│"+deep+"↳ Erin") || !strings.Contains(l, "· 1 more reply") {
		t.Errorf("d must sit at absolute depth 4 with its count: %q", l)
	}
	if !strings.Contains(view, "Thread (4 replies)") {
		t.Errorf("header must count shown and hidden:\n%s", view)
	}
}

// A deleted reply mid-chain keeps its children: the tombstone sits at its
// depth and the conversation beneath it stays visible.
func TestThreadTree_TombstoneKeepsChildren(t *testing.T) {
	msgs := chainMessages()
	msgs[2].Deleted = true // b
	var p ThreadPanelModel
	p.Show("root", msgs)
	view := p.View(80, 24)
	if !strings.Contains(view, "│"+strings.Repeat(replyIndent, 2)+"↳ ── message deleted ──") {
		t.Errorf("tombstone must sit at its depth:\n%s", view)
	}
	if l := lineContaining(view, "Dave"); !strings.Contains(l, "│"+strings.Repeat(replyIndent, 3)+"↳ Dave") {
		t.Errorf("the deleted reply's child must stay beneath it: %q", l)
	}
}

// A deleted row at the cap still shows its hidden count, so the subtree
// beneath it is not lost from view.
func TestThreadTree_DeletedCapRowShowsCount(t *testing.T) {
	msgs := chainMessages()
	msgs[3].Deleted = true // c, at the cap
	var p ThreadPanelModel
	p.Show("root", msgs)
	if l := lineContaining(p.View(80, 24), "message deleted"); !strings.Contains(l, "· 2 more replies") {
		t.Errorf("deleted row at the cap must still count what is hidden: %q", l)
	}
}

// r on a deep row replies to that row, so nested replies can be written
// from the view.
func TestThreadTree_ReplyOnDeepRow(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", chainMessages())
	p.cursor = 3 // c, depth 3
	p, cmd := p.Update(keyRune('r'))
	if act := messageActionOf(t, cmd, "reply"); act.Msg.ID != "c" {
		t.Errorf("reply target = %q, want c", act.Msg.ID)
	}
	if p.IsVisible() {
		t.Errorf("r must close the view")
	}
}

// A malformed reply chain (a loop through the root) terminates and lists
// each message once; the hidden count claims each descendant once.
func TestThreadTree_LoopTerminates(t *testing.T) {
	msgs := chainMessages()
	msgs[0].ReplyTo = "a" // root ↔ a
	var p ThreadPanelModel
	p.Show("root", msgs)
	if got, want := idList(p.messages), "root,a,b,c"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
	if p.hidden[3] != 2 {
		t.Errorf("hidden under c = %d, want 2 (d, e; the root is not re-counted)", p.hidden[3])
	}
	_ = p.View(80, 24) // renders without recursion trouble
}
