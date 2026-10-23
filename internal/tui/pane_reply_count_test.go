package tui

import (
	"strings"
	"testing"
)

// Each message counts the whole loaded subtree under it — what the thread
// view opens on — not just its direct replies.
func TestThreadReplyTotals_CountsSubtree(t *testing.T) {
	totals := threadReplyTotals(nestedMessages())
	for id, want := range map[string]int{"top": 4, "mid": 2, "leaf1": 0, "leaf2": 0, "other": 0} {
		if totals[id] != want {
			t.Errorf("totals[%s] = %d, want %d", id, totals[id], want)
		}
	}
}

// A malformed loop terminates and the counts stay finite.
func TestThreadReplyTotals_LoopTerminates(t *testing.T) {
	msgs := nestedMessages()
	msgs[0].ReplyTo = "mid" // top ↔ mid
	totals := threadReplyTotals(msgs)
	for _, id := range []string{"top", "mid"} {
		if totals[id] > len(msgs) {
			t.Errorf("loop inflated totals[%s] = %d", id, totals[id])
		}
	}
}

// The pane shows "· N replies" on the header of a message with a thread
// under it, and nothing on a childless one.
func TestBuildContent_ReplyCountOnHeader(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()
	m.cursor = -1
	content := mustContent(t, m)
	if l := lineContaining(content, "Alice"); !strings.Contains(l, "· 4 replies") {
		t.Errorf("root header lacks its thread size: %q", l)
	}
	if l := lineContaining(content, "Bob"); !strings.Contains(l, "· 2 replies") {
		t.Errorf("nested reply's header lacks its own subtree: %q", l)
	}
	for _, name := range []string{"Carol", "Dave", "Erin"} {
		if l := lineContaining(content, name); strings.Contains(l, "·") {
			t.Errorf("childless message must show no count: %q", l)
		}
	}
}

// Consecutive messages from one sender are grouped and the header is
// hidden; the count then trails the body, like the edited marker.
func TestBuildContent_ReplyCountTrailsGroupedBody(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "a1", From: "Alice", FromID: "usr_alice", Body: "first thought", TS: 1000},
		{ID: "a2", From: "Alice", FromID: "usr_alice", Body: "second thought", TS: 1010},
		{ID: "r", From: "Bob", FromID: "usr_bob", Body: "agreed", TS: 1020, ReplyTo: "a2"},
	}
	m.cursor = -1
	content := mustContent(t, m)
	if l := lineContaining(content, "second thought"); !strings.Contains(l, "· 1 reply") {
		t.Errorf("grouped message must carry the count after its body: %q", l)
	}
	if l := lineContaining(content, "Alice"); strings.Contains(l, "·") {
		t.Errorf("the first message has no replies; its header must show no count: %q", l)
	}
}

// A deleted message keeps its thread size, so the conversation under it
// stays discoverable.
func TestBuildContent_ReplyCountOnTombstone(t *testing.T) {
	msgs := nestedMessages()
	msgs[0].Deleted = true
	m := NewMessages()
	m.messages = msgs
	m.cursor = -1
	if l := lineContaining(mustContent(t, m), "message deleted"); !strings.Contains(l, "· 4 replies") {
		t.Errorf("tombstone lacks its thread size: %q", l)
	}
}

// The count follows the data: a reply that arrives shows on the next
// render.
func TestBuildContent_ReplyCountUpdatesLive(t *testing.T) {
	m := NewMessages()
	m.messages = nestedMessages()
	m.cursor = -1
	if l := lineContaining(mustContent(t, m), "Alice"); !strings.Contains(l, "· 4 replies") {
		t.Fatalf("initial count missing: %q", l)
	}
	m.messages = append(m.messages, DisplayMessage{ID: "late", From: "Frank", FromID: "usr_f", Body: "late", TS: 2000, ReplyTo: "top"})
	if l := lineContaining(mustContent(t, m), "Alice"); !strings.Contains(l, "· 5 replies") {
		t.Errorf("count did not follow the new reply: %q", l)
	}
}
