package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInputView_LongTextStaysSingleRow pins the regression for the
// "more than one line of input text breaks layout" report. Input
// pane must always render exactly 3 rows (top border + 1 content
// row + bottom border), regardless of how long the typed text is.
//
// Pre-2026-05-04: setting `i.textInput.Width = N` inside View (a
// value-receiver method) mutated only a throwaway copy, so the
// textinput's offset/offsetRight viewport indices stayed at
// [0, len(value)]. View emitted the FULL value and lipgloss wrapped
// the over-wide line onto multiple inner rows, blowing the box up
// to 4+ rows. Fixed by setting Width on the persistent App.input
// state via SetTextInputWidth (called on tea.WindowSizeMsg) — see
// layoutInputContentWidth for the cell math.
func TestInputView_LongTextStaysSingleRow(t *testing.T) {
	const panelWidth = 80

	i := NewInput()
	i.SetTextInputWidth(panelWidth - 5) // see layoutInputContentWidth math
	// Type a string longer than the panel width so the textinput
	// must pan rather than render the whole value.
	long := strings.Repeat("e", 300)
	for _, r := range long {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, nil, "general", "", "")
	}

	view := i.View(panelWidth, true)
	rows := strings.Split(view, "\n")

	if len(rows) != 3 {
		t.Errorf("input.View with %d-char input rendered %d rows, want 3", len(long), len(rows))
		for idx, r := range rows {
			t.Logf("  [%d] %q", idx, stripANSIForOverlay(r))
		}
	}
}

// TestInputView_NarrowTerminalDoesntPanic ensures the tiWidth clamp
// (tiWidth >= 1) keeps the textinput from being asked to render at
// 0 or negative width on extremely narrow terminals.
func TestInputView_NarrowTerminalDoesntPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("input.View panicked at narrow width: %v", r)
		}
	}()

	i := NewInput()
	for _, w := range []int{1, 2, 3, 4, 5, 6, 10} {
		_ = i.View(w, true)
	}
}

// TestInputView_LeftArrowPansBack types a long string (cursor is
// pushed to the right end), then presses Left repeatedly. The
// textinput's offset should slide back so the cursor stays visible
// — and the box stays at exactly 3 rows the whole time. This is
// the contract the user wanted: "users can use the direction keys
// to move the cursor left and right; text scrollable on the
// x-axis."
func TestInputView_LeftArrowPansBack(t *testing.T) {
	const panelWidth = 80

	i := NewInput()
	i.SetTextInputWidth(panelWidth - 5) // see layoutInputContentWidth math
	long := strings.Repeat("a", 200)
	for _, r := range long {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, nil, "general", "", "")
	}

	endPos := i.textInput.Position()
	if endPos != 200 {
		t.Errorf("after typing 200 chars, cursor pos = %d, want 200", endPos)
	}

	// Press Left 10 times.
	for n := 0; n < 10; n++ {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyLeft}, nil, "general", "", "")
	}
	if got := i.textInput.Position(); got != 190 {
		t.Errorf("after 10 left arrows, cursor pos = %d, want 190", got)
	}

	view := i.View(panelWidth, true)
	rows := strings.Split(view, "\n")
	if len(rows) != 3 {
		t.Errorf("after Left navigation rendered %d rows, want 3", len(rows))
		for idx, r := range rows {
			t.Logf("  [%d] %q", idx, stripANSIForOverlay(r))
		}
	}
}

// TestInputView_RightArrowPansForward mirrors the left-arrow test.
// Position cursor at start with Home, then press Right and verify
// cursor advances + box stays 3 rows.
func TestInputView_RightArrowPansForward(t *testing.T) {
	const panelWidth = 80

	i := NewInput()
	i.SetTextInputWidth(panelWidth - 5) // see layoutInputContentWidth math
	long := strings.Repeat("b", 200)
	for _, r := range long {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, nil, "general", "", "")
	}

	// Home → cursor to start.
	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyHome}, nil, "general", "", "")
	if got := i.textInput.Position(); got != 0 {
		t.Fatalf("after Home, cursor pos = %d, want 0", got)
	}

	// Press Right 50 times.
	for n := 0; n < 50; n++ {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyRight}, nil, "general", "", "")
	}
	if got := i.textInput.Position(); got != 50 {
		t.Errorf("after 50 right arrows, cursor pos = %d, want 50", got)
	}

	view := i.View(panelWidth, true)
	rows := strings.Split(view, "\n")
	if len(rows) != 3 {
		t.Errorf("after Right navigation rendered %d rows, want 3", len(rows))
	}
}
