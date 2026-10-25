package tui

// The popup opens as you type: a "/" as the first word or an "@" at a word
// start lists everything from the first character, typing narrows, Esc
// dismisses it for that word only, and Enter sends when the suggestion is
// already what was typed (otherwise it accepts).

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// typeInto presses each character of s as its own key.
func typeInto(t *testing.T, a App, s string) App {
	t.Helper()
	for _, r := range s {
		if r == ' ' {
			a = pressFocusKey(t, a, "space")
		} else {
			a = pressFocusKey(t, a, string(r))
		}
	}
	return a
}

func TestCompletion_BareSlashListsEveryCommandInAWindow(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/")
	if !got.input.CompletionOpen() {
		t.Fatal("a bare / must open the popup")
	}
	n := len(got.input.completion.Items())
	if n < 30 {
		t.Fatalf("a bare / must list every command, got %d", n)
	}
	box := stripANSI(got.input.CompletionView(0))
	if !strings.Contains(box, "↓ "+strconv.Itoa(n-completionMaxRows)+" more") {
		t.Fatalf("the list must be windowed with a ↓ marker:\n%s", box)
	}
	for i := 0; i < completionMaxRows; i++ {
		got = pressFocusKey(t, got, "down")
	}
	box = stripANSI(got.input.CompletionView(0))
	if !strings.Contains(box, "↑ 1 more") {
		t.Fatalf("moving past the window must scroll it and show a ↑ marker:\n%s", box)
	}
}

func TestCompletion_TypingACommandNarrowsFromTheFirstCharacter(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/he")
	if s := suggestions(got); len(s) != 1 || s[0] != "/help" {
		t.Fatalf("/he: suggestions %v, want /help alone", s)
	}
}

func TestCompletion_EnterSendsWhenTheTextIsAlreadyComplete(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/help")
	if s := suggestions(got); len(s) != 1 || s[0] != "/help" {
		t.Fatalf("precondition: /help typed in full keeps its single suggestion, got %v", s)
	}
	model, cmd := got.Update(focusKey("enter"))
	got = deliver(t, model.(App), cmd)
	if got.input.CompletionOpen() || got.input.Value() != "" {
		t.Fatalf("Enter on a complete command must send, not accept: popup %v, draft %q", got.input.CompletionOpen(), got.input.Value())
	}
	if !got.help.IsVisible() {
		t.Fatal("/help must have run")
	}
}

func TestCompletion_EnterAcceptsWhenItChangesTheText(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/he")
	got = pressFocusKey(t, got, "enter")
	if got.input.CompletionOpen() || got.input.Value() != "/help" || got.help.IsVisible() {
		t.Fatalf("Enter on a partial command must accept it: popup %v, draft %q, help %v", got.input.CompletionOpen(), got.input.Value(), got.help.IsVisible())
	}
}

func TestCompletion_EscDismissesForTheWordOnly(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/he")
	got = pressFocusKey(t, got, "esc")
	got = typeInto(t, got, "l")
	if got.input.CompletionOpen() || got.input.Value() != "/hel" {
		t.Fatalf("typing on after Esc must not reopen: popup %v, draft %q", got.input.CompletionOpen(), got.input.Value())
	}
	got = pressFocusKey(t, got, "tab")
	if !got.input.CompletionOpen() {
		t.Fatal("Tab must reopen a dismissed word")
	}
	got = pressFocusKey(t, got, "esc")
	got = typeInto(t, got, "p @a")
	if s := suggestions(got); len(s) != 2 {
		t.Fatalf("a new word opens again: suggestions %v, want Alice and Albert", s)
	}
}

func TestCompletion_AtOpensOnlyAtAWordStart(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "me@host")
	if got.input.CompletionOpen() {
		t.Fatal("an e-mail address must not open the popup")
	}
	got = typeInto(t, got, " @")
	if s := suggestions(got); len(s) != 3 {
		t.Fatalf("a bare @ at a word start lists every member: %v", s)
	}
}

// Input level: an exact mention, with or without the trailing space the
// suggestion carries, sends on Enter rather than being re-accepted.
func TestInput_EnterSendsAnExactMention(t *testing.T) {
	in := NewInput()
	in.SetMembers([]MemberEntry{{UserID: "usr_bob", DisplayName: "Bob"}})
	for _, r := range "@Bob" {
		in, _ = in.Update(keyRune(r), nil, "general", "", "")
	}
	if !in.CompletionOpen() {
		t.Fatal("precondition: @Bob has its popup")
	}
	in, _ = in.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, "general", "", "")
	if in.CompletionOpen() || in.Value() != "" {
		t.Fatalf("Enter must send (the input resets), not accept: popup %v, draft %q", in.CompletionOpen(), in.Value())
	}
}

// You cannot @ yourself: the local user is never offered, and your own name
// typed by hand is plain text, not a mention.
func TestCompletion_NeverOffersYourself(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""), withSelfUser("usr_self"))
	a.input.SetMembers([]MemberEntry{
		{UserID: "usr_self", DisplayName: "Me"},
		{UserID: "usr_alice", DisplayName: "Alice"},
		{UserID: "usr_bob", DisplayName: "Bob"},
	})
	got := typeInto(t, a, "@")
	if s := suggestions(got); len(s) != 2 {
		t.Fatalf("a bare @ must list the others only, got %v", s)
	}
	got = typeInto(t, got, "m")
	if got.input.CompletionOpen() {
		t.Fatal("@m must not offer Me")
	}
	if m := got.input.ExtractMentions("hi @Me and @Alice"); len(m) != 1 || m[0] != "usr_alice" {
		t.Fatalf("mentions = %v; want Alice only, never yourself", m)
	}
}

// The bare / menu reads by name: the command list is alphabetical.
func TestCompletion_CommandListIsAlphabetical(t *testing.T) {
	got := typeInto(t, narrowApp(t, ""), "/")
	s := suggestions(got)
	if len(s) < 30 || !sort.StringsAreSorted(s) {
		t.Fatalf("the command list must be alphabetical, got %v", s)
	}
}

// Right accepts like Tab: on a partial command it completes it, on a
// mention it inserts the name; focus stays on the input.
func TestCompletion_RightArrowAcceptsLikeTab(t *testing.T) {
	got := pressFocusKey(t, typeInto(t, narrowApp(t, ""), "/he"), "right")
	if got.input.CompletionOpen() || got.input.Value() != "/help" || got.focus != FocusInput {
		t.Fatalf("→ on /he: popup %v, draft %q, focus %v; want /help accepted in the input", got.input.CompletionOpen(), got.input.Value(), got.focus)
	}
	got = pressFocusKey(t, typeInto(t, narrowApp(t, ""), "hi @alb"), "right")
	if got.input.CompletionOpen() || got.input.Value() != "hi @Albert " || got.focus != FocusInput {
		t.Fatalf("→ on @alb: popup %v, draft %q, focus %v; want @Albert accepted", got.input.CompletionOpen(), got.input.Value(), got.focus)
	}
}
