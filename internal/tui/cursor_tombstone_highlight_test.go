package tui

import (
	"strings"
	"testing"
)

// Rows the cursor can land on — deleted-message tombstones and system rows
// included — render the selection band when selected, so walking the cursor
// through consecutive deletions never looks like the keypress did nothing.
// Color profiles are stripped under test, but selectedMsgStyle pads the row
// to the content width, so a selected rendering is observably wider than the
// unselected one.
func TestBuildContent_SelectedTombstoneAndSystemRowsAreHighlighted(t *testing.T) {
	const width = 40
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", FromID: "usr_alice", Body: "hello", TS: 1000},
		{ID: "msg_2", From: "Bob", FromID: "usr_bob", Body: "gone", TS: 1001, Deleted: true},
		{ID: "sys_1", IsSystem: true, SystemText: "Carol joined", TS: 1002},
	}

	lineOf := func(content, needle string) string {
		t.Helper()
		for _, l := range strings.Split(content, "\n") {
			if strings.Contains(l, needle) {
				return l
			}
		}
		t.Fatalf("no line containing %q in:\n%s", needle, content)
		return ""
	}

	m.cursor = -1
	unselected, _ := m.buildContent(width)
	plainTomb := lineOf(unselected, "message deleted")
	plainSys := lineOf(unselected, "Carol joined")

	// Cursor on the tombstone: it gets the band, the system row does not.
	m.cursor = 1
	selected, _ := m.buildContent(width)
	if l := lineOf(selected, "message deleted"); len(l) <= len(plainTomb) {
		t.Errorf("tombstone under cursor lacks the selection band:\n  selected   %q\n  unselected %q", l, plainTomb)
	}
	if l := lineOf(selected, "Carol joined"); len(l) != len(plainSys) {
		t.Errorf("system row highlighted while the cursor is on the tombstone: %q", l)
	}

	// Cursor on the system row: the reverse.
	m.cursor = 2
	selected, _ = m.buildContent(width)
	if l := lineOf(selected, "Carol joined"); len(l) <= len(plainSys) {
		t.Errorf("system row under cursor lacks the selection band:\n  selected   %q\n  unselected %q", l, plainSys)
	}
	if l := lineOf(selected, "message deleted"); len(l) != len(plainTomb) {
		t.Errorf("tombstone highlighted while the cursor is on the system row: %q", l)
	}
}

// The row map must count the rows actually drawn, including a selected
// tombstone, or the viewport would follow the cursor to the wrong line.
func TestBuildContent_RowMapCountsSelectedTombstone(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", FromID: "usr_alice", Body: "hello", TS: 1000},
		{ID: "msg_2", From: "Bob", FromID: "usr_bob", Body: "gone", TS: 1001, Deleted: true},
		{ID: "msg_3", From: "Alice", FromID: "usr_alice", Body: "after", TS: 2000},
	}
	m.cursor = 1
	content, rowMap := m.buildContent(40)
	if len(rowMap) != len(m.messages) {
		t.Fatalf("rowMap has %d entries for %d messages", len(rowMap), len(m.messages))
	}
	lines := strings.Split(content, "\n")
	// The row the map says the third message starts on must actually hold it.
	start := rowMap[2]
	if start < 0 || start >= len(lines) {
		t.Fatalf("rowMap[2] = %d out of range for %d lines", start, len(lines))
	}
	found := false
	for _, l := range lines[start:] {
		if strings.Contains(l, "after") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("message after the selected tombstone not found at or after rowMap[2]=%d:\n%s", start, content)
	}
}
