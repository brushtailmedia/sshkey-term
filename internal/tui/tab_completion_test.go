package tui

// Tab completes when it can, else cycles focus. In the input, with the
// cursor at the end of a /command or @name word (or the popup open), Tab is
// the completer's key and reaches the input through the normal routing;
// anywhere else, and from every other panel, it cycles focus as before.
// Before the guard the cycle ran first and the popup was unreachable.

import "testing"

// tabApp is a live session in general with two members known to the
// completer and the given draft, cursor at its end, focus on the input.
func tabApp(t *testing.T, draft string) App {
	t.Helper()
	a := newTestApp(t, withContext("general", "", ""))
	a.input.SetMembers([]MemberEntry{{UserID: "usr_alice", DisplayName: "Alice"}, {UserID: "usr_bob", DisplayName: "Bob"}})
	a.input.textInput.SetValue(draft)
	a.input.textInput.SetCursor(len(draft))
	return a
}

func TestTab_OpensAndAcceptsMentionCompletion(t *testing.T) {
	got := pressFocusKey(t, tabApp(t, "hi @al"), "tab")
	if got.focus != FocusInput || !got.input.CompletionOpen() {
		t.Fatalf("Tab after @al: focus %v, popup %v; want the input with the popup open", got.focus, got.input.CompletionOpen())
	}
	got = pressFocusKey(t, got, "tab")
	if got.focus != FocusInput || got.input.CompletionOpen() || got.input.Value() != "hi @Alice " {
		t.Fatalf("second Tab: focus %v, popup %v, draft %q; want the suggestion accepted into the draft", got.focus, got.input.CompletionOpen(), got.input.Value())
	}
}

func TestTab_OpensCommandCompletion(t *testing.T) {
	got := pressFocusKey(t, tabApp(t, "/he"), "tab")
	if got.focus != FocusInput || !got.input.CompletionOpen() {
		t.Fatalf("Tab after /he: focus %v, popup %v; want the input with the popup open", got.focus, got.input.CompletionOpen())
	}
}

// Nothing to complete at the cursor: an empty draft, plain words, a mention
// or command earlier in the draft, a slash that is not the first word.
func TestTab_CyclesFocusWhenNothingToComplete(t *testing.T) {
	for _, draft := range []string{"", "hello", "hello @alice done", "say /help"} {
		got := pressFocusKey(t, tabApp(t, draft), "tab")
		if got.focus != FocusSidebar || got.input.Value() != draft {
			t.Fatalf("Tab with draft %q: focus %v, draft %q; want the sidebar with the draft kept", draft, got.focus, got.input.Value())
		}
	}
}

// A bare prefix lists everything on Tab; a word nothing matches opens
// nothing; neither jumps focus away from the word being typed.
func TestTab_BarePrefixListsEverythingNoMatchStays(t *testing.T) {
	for _, tc := range []struct {
		draft string
		open  bool
	}{{"/", true}, {"@", true}, {"@zz", false}} {
		got := pressFocusKey(t, tabApp(t, tc.draft), "tab")
		if got.focus != FocusInput || got.input.CompletionOpen() != tc.open || got.input.Value() != tc.draft {
			t.Fatalf("Tab with draft %q: focus %v, popup %v, draft %q; want the input kept and popup=%v", tc.draft, got.focus, got.input.CompletionOpen(), got.input.Value(), tc.open)
		}
	}
}

// Only the input completes; the same draft with focus elsewhere cycles.
func TestTab_FromOtherPanelsStillCycles(t *testing.T) {
	a := tabApp(t, "@al")
	a.setFocus(FocusMessages)
	got := pressFocusKey(t, a, "tab")
	if got.focus != FocusInput || got.input.CompletionOpen() {
		t.Fatalf("Tab from the pane: focus %v, popup %v; want the cycle to the input and no popup", got.focus, got.input.CompletionOpen())
	}
}
