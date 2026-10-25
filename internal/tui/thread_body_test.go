package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// bodyFixture: a root with a long body, and replies with a multi-line
// body, an emoji-heavy body, attachments, a mention, a long URL and an
// edit — the real-content cases the earlier fixtures never had.
func bodyFixture() []DisplayMessage {
	return []DisplayMessage{
		{ID: "root", From: "Alice", FromID: "usr_alice", TS: 1000,
			Body: strings.Repeat("word ", 40)},
		{ID: "multi", From: "Bob", FromID: "usr_bob", TS: 1001, ReplyTo: "root",
			Body: "line one\nline two\nline three"},
		{ID: "emoji", From: "Carol", FromID: "usr_carol", TS: 1002, ReplyTo: "root",
			Body: strings.Repeat("🎉x", 30)},
		{ID: "files", From: "Dave", FromID: "usr_dave", TS: 1003, ReplyTo: "root", Body: "photo.png",
			Attachments: []DisplayAttachment{{FileID: "f1", Name: "photo.png", Size: 102700, IsImage: true}, {FileID: "f2", Name: "notes.pdf", Size: 2048}}},
		{ID: "mention", From: "Erin", FromID: "usr_erin", TS: 1004, ReplyTo: "root",
			Body: "thanks @alice"},
		{ID: "url", From: "Frank", FromID: "usr_frank", TS: 1005, ReplyTo: "root",
			Body: "see https://example.com/" + strings.Repeat("segment/", 8) + "end"},
		{ID: "edited", From: "Gail", FromID: "usr_gail", TS: 1006, ReplyTo: "root", EditedAt: 1100,
			Body: "fixed the typo"},
	}
}

func linesContaining(view, needle string) []string {
	var out []string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}

// A long body wraps to several lines, each at the row's indent, and the
// frame still fits.
func TestThreadBody_LongBodyWrapsAtIndent(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", bodyFixture())
	view := p.View(60, 30)
	assertFramed(t, view, 60, 30)
	lines := linesContaining(view, "word")
	if len(lines) < 4 {
		t.Fatalf("a 200-character root body must wrap to several lines, got %d:\n%s", len(lines), view)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "│ word") {
			t.Errorf("root body lines start at the one-column gutter: %q", l)
		}
	}
	if strings.Contains(view, "...") {
		t.Errorf("bodies wrap; they are never cut with an ellipsis:\n%s", view)
	}
}

// A body with newlines keeps the depth indent on every line.
func TestThreadBody_MultiLineKeepsIndent(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", bodyFixture())
	view := p.View(60, 30)
	indent := "│" + replyIndent + "  " // depth 1: arrow indent + two columns
	for _, want := range []string{"line one", "line two", "line three"} {
		if l := lineContaining(view, want); !strings.HasPrefix(l, indent+want) {
			t.Errorf("%q must sit at the body indent: %q", want, l)
		}
	}
}

// Wrapping counts cells, never bytes: an emoji-heavy body never splits a
// rune and the frame fits at a narrow width.
func TestThreadBody_RunesNeverSplit(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", bodyFixture())
	p.Select("emoji")
	view := p.View(40, 24)
	assertFramed(t, view, 40, 24)
	for _, l := range strings.Split(view, "\n") {
		if !utf8.ValidString(l) || strings.ContainsRune(l, utf8.RuneError) {
			t.Fatalf("a line is not valid UTF-8 (a rune was split): %q", l)
		}
	}
	if len(linesContaining(view, "🎉")) < 2 {
		t.Errorf("the emoji body must wrap across lines at 40 columns:\n%s", view)
	}
}

// Attachments render as the pane's placeholders, with sizes, under the
// body at its indent.
func TestThreadBody_AttachmentPlaceholders(t *testing.T) {
	var p ThreadPanelModel
	p.Show("root", bodyFixture())
	view := p.View(60, 30)
	indent := "│" + replyIndent + "  "
	if l := lineContaining(view, imagePlaceholderGlyph); !strings.HasPrefix(l, indent+imagePlaceholderGlyph+" ("+formatSize(102700)+")") {
		t.Errorf("image placeholder: %q", l)
	}
	if l := lineContaining(view, filePlaceholderGlyph); !strings.HasPrefix(l, indent+filePlaceholderGlyph+" ("+formatSize(2048)+")") {
		t.Errorf("file placeholder: %q", l)
	}
	// The glyph must be counted as the two cells terminals draw, or the
	// line is one cell too wide and the frame's right border shifts.
	if lipgloss.Width(imagePlaceholderGlyph) != 2 || lipgloss.Width(filePlaceholderGlyph) != 2 {
		t.Errorf("placeholder glyphs must measure two cells: image=%d file=%d", lipgloss.Width(imagePlaceholderGlyph), lipgloss.Width(filePlaceholderGlyph))
	}
}

// Mentions of the viewer are styled as in the pane, a long URL breaks
// rather than overflowing, and an edited reply carries the marker.
func TestThreadBody_MentionURLEdited(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	plain := wrapBody("thanks @alice", " ", 50, "")
	styled := wrapBody("thanks @alice", " ", 50, "alice")
	if plain == styled || !strings.Contains(styled, "\x1b[") {
		t.Errorf("a mention of the viewer must be styled (pane parity)")
	}

	lipgloss.SetColorProfile(termenv.Ascii)
	var p ThreadPanelModel
	p.Show("root", bodyFixture())
	view := p.View(40, 40)
	assertFramed(t, view, 40, 40)
	if len(linesContaining(view, "segment/")) < 2 {
		t.Errorf("a long URL must break across lines at 40 columns, not overflow:\n%s", view)
	}
	if l := lineContaining(view, "Gail"); !strings.Contains(l, "(edited)") {
		t.Errorf("edited reply must carry the marker on its header: %q", l)
	}
}

// Refresh keeps the cursor on the same message when a reply arrives above
// it and shifts every row below by one.
func TestThreadPanel_RefreshKeepsCursorByID(t *testing.T) {
	msgs := nestedMessages()
	var p ThreadPanelModel
	p.Show("top", msgs)
	p.Select("other") // last row
	if got := highlightedID(p); got != "other" {
		t.Fatalf("precondition: highlighted %q", got)
	}
	// A reply to leaf1 lands above "other" in depth-first order.
	msgs = append(msgs, DisplayMessage{ID: "late", From: "Frank", FromID: "usr_f", Body: "late", TS: 2000, ReplyTo: "leaf1"})
	p.Refresh(msgs)
	if got := highlightedID(p); got != "other" {
		t.Errorf("after a reply above it, highlighted = %q, want other (by ID, not row)", got)
	}

	p.Show("mid", msgs)
	p, _ = p.Update(keyUp) // ".."
	p.Refresh(msgs)
	if p.cursor != 0 {
		t.Errorf("a cursor on .. stays on .. across a refresh; cursor=%d", p.cursor)
	}
}
