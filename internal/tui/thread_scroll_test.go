package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A root and thirty two-line replies, shown at 60×20: the content box is
// 58×18, the footer fits on one line, so the body budget is 14 lines —
// seven two-line entries per screen. Row k is reply k-1 ("reply number
// k-1"); row 0 is the root.
func longThreadPanel() ThreadPanelModel {
	msgs := []DisplayMessage{{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, DisplayMessage{ID: fmt.Sprintf("r%d", i), From: "Bob", FromID: "usr_bob",
			Body: fmt.Sprintf("reply number %d", i), TS: int64(1001 + i), ReplyTo: "root"})
	}
	var p ThreadPanelModel
	p.Show("root", msgs)
	p.SetSize(60, 20)
	return p
}

func pressKeys(p ThreadPanelModel, k tea.KeyMsg, n int) ThreadPanelModel {
	for i := 0; i < n; i++ {
		p, _ = p.Update(k)
	}
	return p
}

var (
	keyDown     = tea.KeyMsg{Type: tea.KeyDown}
	keyUp       = tea.KeyMsg{Type: tea.KeyUp}
	keyPageDown = tea.KeyMsg{Type: tea.KeyPgDown}
	keyPageUp   = tea.KeyMsg{Type: tea.KeyPgUp}
	keyHome     = tea.KeyMsg{Type: tea.KeyHome}
	keyEnd      = tea.KeyMsg{Type: tea.KeyEnd}
)

// The cursor moves inside the window without scrolling it; it pulls the
// window only at the edges, by exactly what is needed.
func TestThreadScroll_CursorPullsWindowOnlyAtEdges(t *testing.T) {
	p := longThreadPanel()

	p = pressKeys(p, keyDown, 3)
	if p.cursor != 3 || p.top != 0 {
		t.Fatalf("inside the window: cursor=%d top=%d, want 3/0", p.cursor, p.top)
	}

	p = pressKeys(p, keyDown, 4) // cursor 7: the eighth row, first one off the screen
	if p.cursor != 7 || p.top != 1 {
		t.Fatalf("past the bottom edge: cursor=%d top=%d, want 7/1 (window pulled one row)", p.cursor, p.top)
	}
	view := p.View(60, 20)
	if strings.Contains(view, "root body") || !strings.Contains(view, "reply number 6") {
		t.Errorf("window must start at row 1 and end at the cursor:\n%s", view)
	}

	p = pressKeys(p, keyUp, 3) // cursor 4, still inside rows 1..7
	if p.cursor != 4 || p.top != 1 {
		t.Fatalf("moving up inside the window: cursor=%d top=%d, want 4/1 (no scroll)", p.cursor, p.top)
	}

	p = pressKeys(p, keyUp, 4) // cursor 0: above the window
	if p.cursor != 0 || p.top != 0 {
		t.Fatalf("past the top edge: cursor=%d top=%d, want 0/0", p.cursor, p.top)
	}
}

// PageDown and PageUp move the window a screen at a time and leave the
// cursor alone, even off screen; the next cursor key pulls the window back.
func TestThreadScroll_PageKeysLeaveCursor(t *testing.T) {
	p := longThreadPanel()

	p, _ = p.Update(keyPageDown)
	if p.cursor != 0 || p.top != 7 {
		t.Fatalf("PageDown: cursor=%d top=%d, want 0/7 (next screen, cursor untouched)", p.cursor, p.top)
	}
	view := p.View(60, 20)
	if strings.Contains(view, "root body") || !strings.Contains(view, "reply number 6") || !strings.Contains(view, "reply number 12") {
		t.Errorf("after PageDown the window must show rows 7..13 with the cursor off screen:\n%s", view)
	}

	p, _ = p.Update(keyPageDown)
	if p.top != 14 {
		t.Fatalf("second PageDown: top=%d, want 14", p.top)
	}
	p, _ = p.Update(keyPageUp)
	if p.top != 7 || p.cursor != 0 {
		t.Fatalf("PageUp: cursor=%d top=%d, want 0/7", p.cursor, p.top)
	}

	p, _ = p.Update(keyDown) // cursor 1, above the window: the window comes back to it
	if p.cursor != 1 || p.top != 1 {
		t.Fatalf("cursor key after paging: cursor=%d top=%d, want 1/1", p.cursor, p.top)
	}
}

// PageDown stops with the last row at the bottom; Home scrolls to the top
// without moving the cursor; End puts the cursor on the last row at the
// bottom.
func TestThreadScroll_HomeEndAndBottom(t *testing.T) {
	p := longThreadPanel()

	p = pressKeys(p, keyPageDown, 10)
	if p.top != 24 { // rows 24..30 are the last seven
		t.Fatalf("PageDown past the end: top=%d, want 24 (last row at the bottom)", p.top)
	}
	if p.cursor != 0 {
		t.Fatalf("paging never moves the cursor; cursor=%d", p.cursor)
	}

	p, _ = p.Update(keyHome)
	if p.top != 0 || p.cursor != 0 {
		t.Fatalf("Home: cursor=%d top=%d, want 0/0", p.cursor, p.top)
	}

	p, _ = p.Update(keyEnd)
	if p.cursor != 30 || p.top != 24 {
		t.Fatalf("End: cursor=%d top=%d, want 30/24", p.cursor, p.top)
	}
	view := p.View(60, 20)
	assertFramed(t, view, 60, 20)
	if !strings.Contains(view, "reply number 29") {
		t.Errorf("End must show the last reply:\n%s", view)
	}
}

// Without a size yet (no key has reached the panel through the App) the
// cursor keys still move the cursor and the renderer keeps it on screen.
func TestThreadScroll_WithoutSizeRendererFollowsCursor(t *testing.T) {
	p := longThreadPanel()
	p.width, p.height = 0, 0
	p = pressKeys(p, keyDown, 20)
	if p.cursor != 20 {
		t.Fatalf("cursor=%d, want 20", p.cursor)
	}
	view := p.View(60, 20)
	assertFramed(t, view, 60, 20)
	if !strings.Contains(view, "reply number 19") {
		t.Errorf("the renderer must show the cursor's row without a persisted window:\n%s", view)
	}
}

// A row taller than the budget pins its first line at the top, as the
// pane does for a tall message.
func TestThreadScroll_TallRowPinsFirstLine(t *testing.T) {
	rows := []int{2, 2, 9, 2}
	if got := windowStart(0, 2, rows, 5); got != 2 {
		t.Errorf("windowStart on a tall row = %d, want 2 (pinned at top)", got)
	}
	if got := lastVisibleRow(2, rows, 5); got != 2 {
		t.Errorf("lastVisibleRow from a tall row = %d, want 2 (itself)", got)
	}
	if got := bottomStart(rows, 5); got != 3 {
		t.Errorf("bottomStart = %d, want 3", got)
	}
}
