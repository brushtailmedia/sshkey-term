package tui

import (
	"strings"
	"testing"
)

// Fixture: a message with 👍 from alice and bob and ❤️ from carol; the local
// user is usr_self (has not reacted unless a test adds it).
func reactionFixture() DisplayMessage {
	return DisplayMessage{
		ID: "msg_1", From: "Alice", FromID: "usr_alice", Body: "hello", TS: 1000,
		ReactionsByUser: map[string]map[string][]string{
			"usr_alice": {"👍": {"r1"}},
			"usr_bob":   {"👍": {"r2", "r2b"}}, // two devices → one name
			"usr_carol": {"❤️": {"r3"}},
		},
	}
}

func fixtureResolver(id string) string {
	switch id {
	case "usr_alice":
		return "Alice"
	case "usr_bob":
		return "Bob"
	case "usr_carol":
		return "Carol"
	}
	return id
}

// Unselected rows keep chips only — no names leak into the compact view.
func TestRenderReactions_UnselectedChipsOnly(t *testing.T) {
	m := NewMessages()
	m.resolveName = fixtureResolver
	m.SetSelfUserID("usr_self")
	msg := reactionFixture()

	out := m.renderReactions(&msg, false)
	for _, want := range []string{"👍 2", "❤️ 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("unselected reactions %q missing %q", out, want)
		}
	}
	for _, leak := range []string{"Alice", "Bob", "Carol", "you", "t=all"} {
		if strings.Contains(out, leak) {
			t.Errorf("unselected reactions %q must not contain %q", out, leak)
		}
	}
}

// The selected row appends the reactor names after each chip, "you" first,
// then alphabetical; a user reacting from two devices is listed once.
func TestRenderReactions_SelectedShowsNames(t *testing.T) {
	m := NewMessages()
	m.resolveName = fixtureResolver
	m.SetSelfUserID("usr_self")
	msg := reactionFixture()
	msg.ReactionsByUser["usr_self"] = map[string][]string{"👍": {"r9"}}

	out := m.renderReactions(&msg, true)
	if !strings.Contains(out, "👍 3 you, Alice, Bob") {
		t.Errorf("selected reactions %q: want '👍 3 you, Alice, Bob'", out)
	}
	if !strings.Contains(out, "❤️ 1 Carol") {
		t.Errorf("selected reactions %q: want '❤️ 1 Carol'", out)
	}
	if strings.Contains(out, "t=all") {
		t.Errorf("no truncation happened, hint must be absent: %q", out)
	}
}

// Past maxInlineReactorNames the list ends in "+n". The thread-view hint is
// not this line's job (buildContent appends it once per selected block), so
// the reactions line itself never carries one.
func TestRenderReactions_Truncates(t *testing.T) {
	m := NewMessages()
	m.SetSelfUserID("usr_self")
	msg := DisplayMessage{ID: "msg_1", ReactionsByUser: map[string]map[string][]string{}}
	for _, u := range []string{"u1", "u2", "u3", "u4", "u5", "u6", "u7"} {
		msg.ReactionsByUser[u] = map[string][]string{"👍": {"r_" + u}}
	}

	out := m.renderReactions(&msg, true)
	if !strings.Contains(out, "👍 7 u1, u2, u3, u4, u5, +2") {
		t.Errorf("truncated list wrong: %q", out)
	}
	if strings.Contains(out, "t=") {
		t.Errorf("the reactions line must not carry a key hint itself: %q", out)
	}

	if un := m.renderReactions(&msg, false); strings.Contains(un, "+2") {
		t.Errorf("unselected row must not truncate: %q", un)
	}
}

// Own reactions are marked by bolding the chip, never by a new color.
func TestReactionChipStyle_OwnIsBoldSameColor(t *testing.T) {
	msg := reactionFixture()
	msg.ReactionsByUser["usr_self"] = map[string][]string{"❤️": {"r9"}}

	own := reactionChipStyle(&msg, "❤️", "usr_self")
	other := reactionChipStyle(&msg, "👍", "usr_self")
	if !own.GetBold() {
		t.Errorf("own reaction chip must be bold")
	}
	if other.GetBold() {
		t.Errorf("other users' chip must not be bold")
	}
	if own.GetForeground() != other.GetForeground() {
		t.Errorf("own and other chips must share the accent color: %v vs %v", own.GetForeground(), other.GetForeground())
	}
	if none := reactionChipStyle(&msg, "❤️", ""); none.GetBold() {
		t.Errorf("unknown self id must never bold a chip")
	}
}
