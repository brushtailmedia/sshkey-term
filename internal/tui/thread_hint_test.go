package tui

import (
	"strings"
	"testing"
)

// The selected message carries one gray "t=thread" hint whenever the thread
// view has something to show — it has reactions, it is a reply, or it has
// replies — and never otherwise. Unselected rows never carry it.
func TestBuildContent_ThreadHintOnSelectedMessage(t *testing.T) {
	m := NewMessages()
	m.SetSelfUserID("usr_self")
	m.messages = []DisplayMessage{
		{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root", TS: 1000},                  // 0: has a reply
		{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1100, ReplyTo: "root"}, // 1: is a reply
		{ID: "reacted", From: "Carol", FromID: "usr_carol", Body: "popular", TS: 1200, // 2: has reactions
			ReactionsByUser: map[string]map[string][]string{"usr_dave": {"👍": {"r1"}}}},
		{ID: "plain", From: "Dave", FromID: "usr_dave", Body: "nothing here", TS: 1300}, // 3: none
	}

	count := func(s string) int { return strings.Count(s, "t=thread") }

	m.cursor = -1
	if c := count(mustContent(t, m)); c != 0 {
		t.Errorf("no selection: want 0 hints, got %d", c)
	}

	for _, tc := range []struct {
		cursor int
		want   int
		why    string
	}{
		{0, 1, "root with replies"},
		{1, 1, "reply"},
		{2, 1, "reactions"},
		{3, 0, "plain message"},
	} {
		m.cursor = tc.cursor
		content := mustContent(t, m)
		if c := count(content); c != tc.want {
			t.Errorf("cursor on %s: want %d hint(s), got %d\n%s", tc.why, tc.want, c, content)
		}
	}
}

// The hint sits at the end of the selected block: after the reactions line
// when there is one, so it reads as the last thing under the message.
func TestBuildContent_ThreadHintFollowsReactionsLine(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "reacted", From: "Carol", FromID: "usr_carol", Body: "popular", TS: 1200,
			ReactionsByUser: map[string]map[string][]string{"usr_dave": {"👍": {"r1"}}}},
	}
	m.cursor = 0
	content := mustContent(t, m)
	var hintLine string
	for _, l := range strings.Split(content, "\n") {
		if strings.Contains(l, "t=thread") {
			hintLine = l
		}
	}
	if hintLine == "" || !strings.Contains(hintLine, "👍 1") {
		t.Errorf("hint must share the reactions line: %q\n%s", hintLine, content)
	}
}

func mustContent(t *testing.T, m MessagesModel) string {
	t.Helper()
	content, _ := m.buildContent(60)
	return content
}
