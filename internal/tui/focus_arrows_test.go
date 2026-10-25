package tui

// Spatial focus (focus-consistency.md clause 5): ← and → move focus to the
// panel in that direction across sidebar | messages (input below) |
// members; h and l are the j/k-style aliases outside the input; Shift+Tab
// is Tab's cycle backwards. From the input only an empty draft moves: with
// text, ← and → stay cursor keys and h / l are letters.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func focusKey(name string) tea.KeyMsg {
	switch name {
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return keyRune([]rune(name)[0])
}

// focusApp is a live session in general with the nested fixture loaded,
// the member panel open or closed, and focus where the case starts.
func focusApp(t *testing.T, from Focus, members bool) App {
	t.Helper()
	a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()))
	if members {
		a.toggleMemberPanel()
		if !a.memberPanel.IsVisible() {
			t.Fatal("precondition: the member panel must be open")
		}
	}
	a.setFocus(from)
	return a
}

func pressFocusKey(t *testing.T, a App, key string) App {
	t.Helper()
	model, _ := a.Update(focusKey(key))
	return model.(App)
}

func TestFocus_ArrowsMoveBetweenPanels(t *testing.T) {
	cases := []struct {
		name    string
		from    Focus
		members bool
		key     string
		want    Focus
	}{
		{"sidebar → = pane", FocusSidebar, false, "right", FocusMessages},
		{"sidebar ← is an edge", FocusSidebar, false, "left", FocusSidebar},
		{"pane ← = sidebar", FocusMessages, false, "left", FocusSidebar},
		{"pane → with the member panel closed stays", FocusMessages, false, "right", FocusMessages},
		{"pane → = members", FocusMessages, true, "right", FocusMembers},
		{"members ← = pane", FocusMembers, true, "left", FocusMessages},
		{"members → is an edge", FocusMembers, true, "right", FocusMembers},
		{"empty input ← = sidebar", FocusInput, false, "left", FocusSidebar},
		{"empty input → with the member panel closed stays", FocusInput, false, "right", FocusInput},
		{"empty input → = members", FocusInput, true, "right", FocusMembers},
		{"h in the pane = sidebar", FocusMessages, false, "h", FocusSidebar},
		{"l in the sidebar = pane", FocusSidebar, false, "l", FocusMessages},
		{"l in the pane = members", FocusMessages, true, "l", FocusMembers},
		{"h in the member panel = pane", FocusMembers, true, "h", FocusMessages},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := focusApp(t, tc.from, tc.members)
			sidebarCursor, paneCursor := a.sidebar.cursor, a.messages.cursor
			got := pressFocusKey(t, a, tc.key)
			if got.focus != tc.want {
				t.Fatalf("focus = %v, want %v", got.focus, tc.want)
			}
			if got.memberPanel.focused != (got.focus == FocusMembers) {
				t.Fatalf("member panel focused flag %v is out of step with focus %v", got.memberPanel.focused, got.focus)
			}
			if got.sidebar.cursor != sidebarCursor || got.messages.cursor != paneCursor {
				t.Fatal("a focus move must not move the sidebar or pane cursor")
			}
		})
	}
}

// With text in the draft, ← and → are cursor keys and focus stays.
func TestFocus_ArrowsInInputWithTextMoveTheCursor(t *testing.T) {
	a := focusApp(t, FocusInput, true)
	a.input.textInput.SetValue("hello")
	if a.input.textInput.Position() != 5 {
		t.Fatalf("precondition: cursor at the end, got %d", a.input.textInput.Position())
	}
	got := pressFocusKey(t, a, "left")
	if got.focus != FocusInput || got.input.textInput.Position() != 4 {
		t.Fatalf("after ←: focus %v, cursor %d; want the input with the cursor at 4", got.focus, got.input.textInput.Position())
	}
	got = pressFocusKey(t, got, "right")
	if got.focus != FocusInput || got.input.textInput.Position() != 5 {
		t.Fatalf("after →: focus %v, cursor %d; want the input with the cursor at 5", got.focus, got.input.textInput.Position())
	}
}

// h and l are letters in the input, draft or no draft.
func TestFocus_HAndLTypeInTheInput(t *testing.T) {
	a := focusApp(t, FocusInput, true)
	got := pressFocusKey(t, pressFocusKey(t, a, "h"), "l")
	if got.focus != FocusInput || got.input.Value() != "hl" {
		t.Fatalf("focus %v, draft %q; want the input holding \"hl\"", got.focus, got.input.Value())
	}
}

// Shift+Tab walks Tab's cycle backwards, with and without the member
// panel, and Tab then Shift+Tab is a round trip from every panel.
func TestFocus_ShiftTabCyclesBackwards(t *testing.T) {
	for _, members := range []bool{false, true} {
		want := []Focus{FocusMessages, FocusSidebar, FocusInput}
		if members {
			want = []Focus{FocusMembers, FocusMessages, FocusSidebar, FocusInput}
		}
		got := focusApp(t, FocusInput, members)
		for i, f := range want {
			got = pressFocusKey(t, got, "shift+tab")
			if got.focus != f {
				t.Fatalf("members=%v, step %d: focus = %v, want %v", members, i, got.focus, f)
			}
		}
		starts := []Focus{FocusInput, FocusSidebar, FocusMessages}
		if members {
			starts = append(starts, FocusMembers)
		}
		for _, start := range starts {
			b := focusApp(t, start, members)
			b = pressFocusKey(t, pressFocusKey(t, b, "tab"), "shift+tab")
			if b.focus != start {
				t.Fatalf("members=%v: Tab then Shift+Tab from %v landed on %v", members, start, b.focus)
			}
		}
	}
}

// The pane's bottom edge: Down (or j) on the last message drops into the
// input with the cursor kept; above it Down moves the cursor as before;
// from "nothing selected" the first Down snaps to the last message; an
// empty pane has nothing below but the input. Other panels are untouched.
func TestFocus_DownPastTheLastMessageFocusesTheInput(t *testing.T) {
	last := len(nestedMessages()) - 1
	for _, key := range []string{"down", "j"} {
		a := focusApp(t, FocusMessages, false)
		a.messages.cursor = last
		got := pressFocusKey(t, a, key)
		if got.focus != FocusInput || got.messages.cursor != last {
			t.Fatalf("%s on the last message: focus %v, cursor %d; want the input with the cursor kept on %d", key, got.focus, got.messages.cursor, last)
		}
	}

	a := focusApp(t, FocusMessages, false) // cursor on the first message
	got := pressFocusKey(t, a, "down")
	if got.focus != FocusMessages || got.messages.cursor != 1 {
		t.Fatalf("Down above the last message: focus %v, cursor %d; want the pane with the cursor on 1", got.focus, got.messages.cursor)
	}

	a = focusApp(t, FocusMessages, false)
	a.messages.cursor = -1
	got = pressFocusKey(t, a, "down")
	if got.focus != FocusMessages || got.messages.cursor != last {
		t.Fatalf("Down with nothing selected: focus %v, cursor %d; want the snap to the last message", got.focus, got.messages.cursor)
	}

	empty := newTestApp(t, withContext("general", "", ""), withFocus(FocusMessages))
	got = pressFocusKey(t, empty, "down")
	if got.focus != FocusInput {
		t.Fatalf("Down in an empty pane: focus %v; want the input", got.focus)
	}

	got = pressFocusKey(t, focusApp(t, FocusSidebar, false), "down")
	if got.focus != FocusSidebar {
		t.Fatalf("Down in the sidebar: focus %v; want the sidebar", got.focus)
	}
	got = pressFocusKey(t, focusApp(t, FocusInput, false), "j")
	if got.focus != FocusInput || got.input.Value() != "j" {
		t.Fatalf("j in the input: focus %v, draft %q; want the input holding \"j\"", got.focus, got.input.Value())
	}
}

// The pane's bottom edge from below: Up in the input focuses the pane,
// draft or no draft (the composer is a single line, so Up has no editing
// meaning in it); the draft stays. Nothing selected snaps the cursor to the
// last message; a selected message is kept, so Down then Up is a round
// trip. The one exception is an open completion popup, whose
// previous-suggestion key Up is.
func TestFocus_UpInInputFocusesThePane(t *testing.T) {
	last := len(nestedMessages()) - 1

	a := focusApp(t, FocusInput, false)
	a.messages.cursor = -1
	got := pressFocusKey(t, a, "up")
	if got.focus != FocusMessages || got.messages.cursor != last {
		t.Fatalf("Up with nothing selected: focus %v, cursor %d; want the pane with the cursor on the last message", got.focus, got.messages.cursor)
	}

	a = focusApp(t, FocusInput, false)
	a.messages.cursor = 1
	got = pressFocusKey(t, a, "up")
	if got.focus != FocusMessages || got.messages.cursor != 1 {
		t.Fatalf("Up with a selection: focus %v, cursor %d; want the pane with the cursor kept on 1", got.focus, got.messages.cursor)
	}

	a = focusApp(t, FocusMessages, false)
	a.messages.cursor = last
	got = pressFocusKey(t, pressFocusKey(t, a, "down"), "up")
	if got.focus != FocusMessages || got.messages.cursor != last {
		t.Fatalf("Down then Up: focus %v, cursor %d; want the round trip back to the last message", got.focus, got.messages.cursor)
	}

	a = focusApp(t, FocusInput, false)
	a.input.textInput.SetValue("draft")
	a.input.textInput.SetCursor(2)
	got = pressFocusKey(t, a, "up")
	if got.focus != FocusMessages || got.input.Value() != "draft" || got.input.textInput.Position() != 2 {
		t.Fatalf("Up with a draft: focus %v, draft %q at %d; want the pane with the draft and cursor kept", got.focus, got.input.Value(), got.input.textInput.Position())
	}

	// The completion popup owns Up while it is open (opened the real way:
	// Tab at the end of an @name word).
	a = focusApp(t, FocusInput, false)
	a.input.SetMembers([]MemberEntry{{UserID: "usr_alice", DisplayName: "Alice"}, {UserID: "usr_albert", DisplayName: "Albert"}})
	a.input.textInput.SetValue("@al")
	a.input.textInput.SetCursor(3)
	got = pressFocusKey(t, a, "tab")
	if got.focus != FocusInput || !got.input.CompletionOpen() {
		t.Fatalf("precondition: Tab after @al opens the popup; focus %v, popup %v", got.focus, got.input.CompletionOpen())
	}
	got = pressFocusKey(t, got, "up")
	if got.focus != FocusInput || !got.input.CompletionOpen() {
		t.Fatalf("Up with the popup open: focus %v, popup %v; want the input with the popup still up", got.focus, got.input.CompletionOpen())
	}

	empty := newTestApp(t, withContext("general", "", ""))
	got = pressFocusKey(t, empty, "up")
	if got.focus != FocusMessages {
		t.Fatalf("Up into an empty pane: focus %v; want the pane", got.focus)
	}
}
