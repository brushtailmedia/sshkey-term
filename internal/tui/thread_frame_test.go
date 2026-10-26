package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// assertFramed checks that view is exactly width × height and that the
// rounded border is present on all four sides.
func assertFramed(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if got := len(lines); got != height {
		t.Fatalf("height = %d, want %d\n%s", got, height, view)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != width {
			t.Errorf("line %d width = %d, want %d: %q", i, w, width, l)
		}
	}
	first, last := lines[0], lines[len(lines)-1]
	if !strings.HasPrefix(first, "╭") || !strings.HasSuffix(first, "╮") {
		t.Errorf("top border missing or cut: %q", first)
	}
	if !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
		t.Errorf("bottom border missing or cut: %q", last)
	}
	for i, l := range lines[1 : len(lines)-1] {
		if !strings.HasPrefix(l, "│") || !strings.HasSuffix(l, "│") {
			t.Errorf("side border missing on line %d: %q", i+1, l)
		}
	}
}

// The panel is drawn full-screen; its frame must be exactly the terminal
// size with all four borders visible (the content box is width-2 × height-2).
func TestThreadPanel_FrameFitsTerminal(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}
	reply := DisplayMessage{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1001, ReplyTo: "root"}

	var p ThreadPanelModel
	p.Show("root", []DisplayMessage{root, reply})

	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		view := p.View(size[0], size[1])
		assertFramed(t, view, size[0], size[1])
		if !strings.Contains(view, "r=reply") {
			t.Errorf("%v: footer missing", size)
		}
	}
}

// The footer never pushes the bottom border off: at widths where it wraps,
// its measured height comes out of the body budget, and every key stays
// listed.
func TestThreadPanel_FooterNeverBreaksFrame(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}
	reply := DisplayMessage{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1001, ReplyTo: "root"}
	var p ThreadPanelModel
	p.Show("root", []DisplayMessage{root, reply})

	for _, size := range [][2]int{{60, 12}, {46, 12}, {40, 12}, {30, 12}, {20, 12}} {
		view := p.View(size[0], size[1])
		assertFramed(t, view, size[0], size[1])
		for _, key := range []string{"t=thread", "Enter=go to", "r=reply", "e=react", "q/Esc=close"} {
			if !strings.Contains(view, key) {
				t.Errorf("%v: footer lacks %q", size, key)
			}
		}
	}
}

// A long thread keeps the cursor's entry visible and still fits the frame.
func TestThreadPanel_LongThreadKeepsCursorVisibleAndFits(t *testing.T) {
	msgs := []DisplayMessage{{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, DisplayMessage{ID: fmt.Sprintf("r%d", i), From: "Bob", FromID: "usr_bob",
			Body: fmt.Sprintf("reply number %d", i), TS: int64(1001 + i), ReplyTo: "root"})
	}
	var p ThreadPanelModel
	p.Show("root", msgs)

	p.cursor = 30 // last reply
	view := p.View(40, 12)
	assertFramed(t, view, 40, 12)
	if !strings.Contains(view, "reply number 29") {
		t.Errorf("cursor entry not visible:\n%s", view)
	}
	if !strings.Contains(view, "r=reply") {
		t.Errorf("footer pushed off the frame:\n%s", view)
	}

	p.cursor = 0
	view = p.View(40, 12)
	assertFramed(t, view, 40, 12)
	if !strings.Contains(view, "root body") {
		t.Errorf("root not visible when selected:\n%s", view)
	}
}

// One entry taller than the whole body budget (many reaction rows) is cut
// to the budget rather than growing the frame past the terminal.
func TestThreadPanel_TallEntryIsCutToFrame(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000,
		ReactionsByUser: map[string]map[string][]string{}}
	emojis := []string{"👍", "❤️", "🎉", "😂", "😮", "😢", "🔥", "👀", "✅", "🚀", "💯", "🙏"}
	for i, e := range emojis {
		root.ReactionsByUser[fmt.Sprintf("u%d", i)] = map[string][]string{e: {"r"}}
	}
	var p ThreadPanelModel
	p.Show("root", []DisplayMessage{root})
	view := p.View(40, 10)
	assertFramed(t, view, 40, 10)
}
