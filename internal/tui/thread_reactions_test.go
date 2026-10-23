package tui

import (
	"strings"
	"testing"
)

// The thread view is the detail view: it lists every reactor, one emoji per
// line, never truncated, for the root and for replies.
func TestThreadPanel_ShowsFullReactorLists(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000,
		ReactionsByUser: map[string]map[string][]string{}}
	for _, u := range []string{"u1", "u2", "u3", "u4", "u5", "u6", "u7"} {
		root.ReactionsByUser[u] = map[string][]string{"👍": {"r_" + u}}
	}
	root.ReactionsByUser["usr_self"] = map[string][]string{"❤️": {"r_self"}}
	reply := DisplayMessage{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1001, ReplyTo: "root",
		ReactionsByUser: map[string]map[string][]string{"u1": {"🎉": {"r_x"}}}}

	var p ThreadPanelModel
	p.SetSelfUserID("usr_self")
	p.SetResolveName(func(id string) string { return strings.ToUpper(id) })
	p.Show("root", []DisplayMessage{root, reply})

	view := p.View(80, 40)
	if !strings.Contains(view, "👍 7 U1, U2, U3, U4, U5, U6, U7") {
		t.Errorf("thread view must list all seven reactors untruncated:\n%s", view)
	}
	if !strings.Contains(view, "❤️ 1 you") {
		t.Errorf("own reaction must be listed as 'you':\n%s", view)
	}
	if !strings.Contains(view, "🎉 1 U1") {
		t.Errorf("reply reactions must be listed too:\n%s", view)
	}
	if strings.Contains(view, "+2") || strings.Contains(view, "t=all") {
		t.Errorf("thread view must never truncate or hint:\n%s", view)
	}
}

// A reaction arriving while the panel is open shows after Refresh, with the
// cursor kept where it was.
func TestThreadPanel_RefreshPicksUpNewReaction(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}
	reply := DisplayMessage{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1001, ReplyTo: "root"}
	all := []DisplayMessage{root, reply}

	var p ThreadPanelModel
	p.SetResolveName(func(id string) string { return id })
	p.Show("root", all)
	p.cursor = 1
	if strings.Contains(p.View(80, 30), "👍") {
		t.Fatalf("no reactions yet")
	}

	all[0].ReactionsByUser = map[string]map[string][]string{"usr_carol": {"👍": {"r1"}}}
	p.Refresh(all)

	if view := p.View(80, 30); !strings.Contains(view, "👍 1 usr_carol") {
		t.Errorf("refresh did not pick up the new reaction:\n%s", view)
	}
	if p.cursor != 1 {
		t.Errorf("refresh moved the cursor: got %d, want 1", p.cursor)
	}

	p.Hide()
	all[0].ReactionsByUser["usr_dave"] = map[string][]string{"👍": {"r2"}}
	p.Refresh(all) // hidden → no-op
	if p.IsVisible() {
		t.Errorf("Refresh on a hidden panel must not show it")
	}
}
