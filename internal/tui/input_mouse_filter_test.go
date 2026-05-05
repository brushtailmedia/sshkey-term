package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyRune builds a single-rune KeyMsg, mimicking the way some tmux
// configs deliver each char of a leaked SGR mouse sequence.
func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// keyRunes builds a multi-rune KeyMsg.
func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestInputMouseFilter_StatefulSingleCharDropsSequence simulates the
// chunked-arrival case the user reported: `[`, `<`, digits, `;`,
// digits, `;`, digits, `M` arriving as 11 separate KeyMsgs. None of
// them should land in the textinput value.
func TestInputMouseFilter_StatefulSingleCharDropsSequence(t *testing.T) {
	i := NewInput()

	// `[<64;54;21M` arriving char by char.
	for _, r := range "[<64;54;21M" {
		i, _ = i.Update(keyRune(r), nil, "general", "", "")
	}

	if v := i.textInput.Value(); v != "" {
		t.Errorf("textinput value should be empty after dropped mouse seq, got %q", v)
	}
	if i.mouseSeqState != 0 {
		t.Errorf("mouseSeqState should be reset to 0 after terminator, got %d", i.mouseSeqState)
	}
}

// TestInputMouseFilter_StatefulMultipleSequences verifies the filter
// handles back-to-back leaks (the user's actual scenario — many
// rapid wheel events).
func TestInputMouseFilter_StatefulMultipleSequences(t *testing.T) {
	i := NewInput()

	stream := "[<64;54;21M[<65;42;26M[<64;40;25m"
	for _, r := range stream {
		i, _ = i.Update(keyRune(r), nil, "general", "", "")
	}

	if v := i.textInput.Value(); v != "" {
		t.Errorf("textinput value should be empty after dropping 3 sequences, got %q", v)
	}
}

// TestInputMouseFilter_MultiRuneChunkedSequence verifies the parser handles
// chunked multi-rune delivery (neither single-rune nor whole-sequence).
func TestInputMouseFilter_MultiRuneChunkedSequence(t *testing.T) {
	i := NewInput()

	parts := []string{"[<65;", "51;37", "M"}
	for _, p := range parts {
		i, _ = i.Update(keyRunes(p), nil, "general", "", "")
	}

	if v := i.textInput.Value(); v != "" {
		t.Errorf("textinput value should be empty after dropping chunked sequence, got %q", v)
	}
	if i.mouseSeqState != 0 {
		t.Errorf("mouseSeqState should reset to 0, got %d", i.mouseSeqState)
	}
}

// TestInputMouseFilter_MultipleSequencesInSingleKeyMsg verifies we drop
// packed back-to-back sequences delivered in one KeyRunes payload.
func TestInputMouseFilter_MultipleSequencesInSingleKeyMsg(t *testing.T) {
	i := NewInput()

	i, _ = i.Update(keyRunes("[<65;51;37M[<64;51;37M"), nil, "general", "", "")

	if v := i.textInput.Value(); v != "" {
		t.Errorf("textinput value should be empty after packed sequences, got %q", v)
	}
	if i.mouseSeqState != 0 {
		t.Errorf("mouseSeqState should reset to 0, got %d", i.mouseSeqState)
	}
}

// TestInputMouseFilter_StatefulHeldBracketFlushed simulates a user
// typing `[hello]` — the `[` is held one keystroke, then flushed
// when `h` arrives (which proves the state machine didn't eat it).
// Final value must contain the full `[hello]`.
func TestInputMouseFilter_StatefulHeldBracketFlushed(t *testing.T) {
	i := NewInput()

	for _, r := range "[hello]" {
		i, _ = i.Update(keyRune(r), nil, "general", "", "")
	}

	want := "[hello]"
	if v := i.textInput.Value(); v != want {
		t.Errorf("textinput value = %q, want %q (lone `[` should have been flushed when next char wasn't `<`)", v, want)
	}
}

// TestInputMouseFilter_NonRuneFlushesHeldBracket verifies that a
// non-rune event arriving while we hold a `[` flushes the bracket
// rather than stranding it. E.g., user types `[`, then presses left-
// arrow — the `[` should land in the textinput.
func TestInputMouseFilter_NonRuneFlushesHeldBracket(t *testing.T) {
	i := NewInput()

	i, _ = i.Update(keyRune('['), nil, "general", "", "")
	if i.mouseSeqState != 1 {
		t.Fatalf("after `[`, state should be 1, got %d", i.mouseSeqState)
	}

	// Non-rune event — left arrow.
	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyLeft}, nil, "general", "", "")
	if i.mouseSeqState != 0 {
		t.Errorf("non-rune event should reset state to 0, got %d", i.mouseSeqState)
	}
	if v := i.textInput.Value(); v != "[" {
		t.Errorf("held `[` should have been flushed, got %q", v)
	}
}

// TestLooksLikeLeakedMouseEvent pins the SGR-1006 detection used to
// drop mouse-escape bytes that arrive at the input as KeyRunes when
// the outer terminal/tmux config doesn't pass mouse events through
// cleanly. See looksLikeLeakedMouseEvent doc-comment for the
// pathology this guards against.
//
// Filter contract:
//   - matches: `[<` then digits/semicolons, ending in `M` or `m`
//   - rejects: anything that doesn't conform (regular text, partial
//     sequences, sequences with extra trailing bytes)
//
// We err on the side of false negatives over false positives: a real
// user pasting `[<64;1;1M` into a chat message is unaffected only
// because that exact-shape input is implausible. Any deviation
// (extra char, missing terminator, digits outside the col;row
// triple) falls through as legit text.
func TestLooksLikeLeakedMouseEvent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Real leaked mouse events from the user's screenshot.
		{"wheel up press", "[<64;54;21M", true},
		{"wheel down press", "[<65;42;26M", true},
		{"button release lowercase", "[<0;10;10m", true},

		// Plausible-but-clean text that must NOT match.
		{"empty", "", false},
		{"too short", "[<1M", false},
		{"missing terminator", "[<64;54;21", false},
		{"missing leading bracket", "<64;54;21M", false},
		{"trailing garbage", "[<64;54;21Mxyz", false},
		{"plain message", "hello world", false},
		{"square bracket alone", "[hello]", false},
		{"sgr style sequence", "[1;31m", false}, // valid ANSI but not mouse format
		{"non-digit body", "[<64;abc;21M", false},
		{"only digits no semis", "[<6421M", true}, // matches: digits then M
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := looksLikeLeakedMouseEvent([]rune(tc.in))
			if got != tc.want {
				t.Errorf("looksLikeLeakedMouseEvent(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
