package tui

// The completion popup as an overlay: the App draws it just above the input
// box through the same compositor as the context menu, so the frame never
// grows; Esc closes it and keeps the draft; typing narrows it, Backspace
// widens it, and a word with no match (or a space) closes it without
// touching the text. The context menu, the member menu and the popup render
// through one list box.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// narrowApp is a live session with three members known to the completer
// (two of them share the prefix "al") and the given draft, cursor at its
// end, focus on the input.
func narrowApp(t *testing.T, draft string) App {
	t.Helper()
	a := newTestApp(t, withContext("general", "", ""))
	a.input.SetMembers([]MemberEntry{
		{UserID: "usr_alice", DisplayName: "Alice"},
		{UserID: "usr_albert", DisplayName: "Albert"},
		{UserID: "usr_bob", DisplayName: "Bob"},
	})
	a.input.textInput.SetValue(draft)
	a.input.textInput.SetCursor(len(draft))
	return a
}

func lineIndex(lines []string, needle string) int {
	for i, l := range lines {
		if strings.Contains(l, needle) {
			return i
		}
	}
	return -1
}

func suggestions(a App) []string {
	if !a.input.CompletionOpen() {
		return nil
	}
	var out []string
	for _, it := range a.input.completion.Items() {
		out = append(out, it.Display)
	}
	return out
}

func TestCompletion_PopupIsAnOverlayAboveTheInput(t *testing.T) {
	a := narrowApp(t, "hi @al")
	a.width, a.height = 90, 24
	got := pressFocusKey(t, a, "tab")
	if !got.input.CompletionOpen() {
		t.Fatal("precondition: Tab after @al opens the popup")
	}
	lines := strings.Split(stripANSI(got.View()), "\n")
	if len(lines) != 24 {
		t.Fatalf("frame is %d lines on a 24-row terminal; the popup must not grow it", len(lines))
	}
	sugg, prompt := lineIndex(lines, "@Alice"), lineIndex(lines, "> hi @al")
	if sugg < 0 || prompt < 0 || sugg >= prompt {
		t.Fatalf("suggestion row %d, prompt row %d; want the popup drawn above the prompt:\n%s", sugg, prompt, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "╭") {
		t.Fatalf("the top border must still be on the first row, got %q", lines[0])
	}
	// The popup floats inside the pane: its border never lands on the
	// pane's bottom border row, where the corners would double up.
	frame := strings.Join(lines, "\n")
	if strings.Contains(frame, "╰╰") || strings.Contains(frame, "╯╯") {
		t.Fatalf("the popup's border meets the pane's:\n%s", frame)
	}
}

// The popup never reaches the sidebar column, where the image preview
// lives: its width is capped to the pane (a row too long is cut with an
// ellipsis, never wrapped), its rows start inside the pane, and the
// sidebar's rows — kitty placement included — are byte-for-byte what they
// were before it opened. Unlike the modals, it does not clear the preview.
func TestCompletion_PopupStaysInThePaneAndKeepsTheImagePreview(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	a := narrowApp(t, "")
	a.width, a.height = 90, 24

	// A cached image on the selected message, with its render already in
	// the cache so the preview embeds a kitty placement synchronously.
	dir := t.TempDir()
	a.messages.SetFilesDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "file_pic"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.messages.messages = []DisplayMessage{{ID: "m1", From: "Alice", FromID: "usr_alice", Body: "look", TS: 1,
		Attachments: []DisplayAttachment{{FileID: "file_pic", Name: "pic.png", Mime: "image/png", IsImage: true}}}}
	a.messages.cursor = 0
	path := a.messages.SelectedImagePath()
	if path == "" {
		t.Fatal("precondition: the selected message has a cached image")
	}
	w, r := a.sidebarPreviewDims()
	placement := "\x1b_Ga=T,f=100,i=7;QUJD\x1b\\"
	putCachedInlineImage(imageRenderCacheKey{path: path, maxCols: w, maxRows: r, truecolor: useTrueColor(), rasterm: true}, 0, 0, placement)

	// One update so the preview is set, then the frame without the popup.
	model, _ := a.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	a = model.(App)
	before := a.View()
	if !strings.Contains(before, "\x1b_Ga=T,") {
		t.Fatal("precondition: the frame carries the preview's kitty placement")
	}

	// A member name far wider than the pane forces the width cap.
	a.input.SetMembers([]MemberEntry{{UserID: "usr_long", DisplayName: strings.Repeat("Longname", 30)}})
	got := typeInto(t, a, "@")
	if !got.input.CompletionOpen() {
		t.Fatal("precondition: the popup is open")
	}
	after := got.View()

	if !strings.Contains(after, "\x1b_Ga=T,") || strings.HasPrefix(after, "\x1b_Ga=d,") {
		t.Fatal("the popup must leave the preview's placement in place and must not clear the image layer")
	}
	layout := computeLayout(90, 24, false)
	popup := got.input.CompletionView(layout.MessagesWidth - 2)
	if lipgloss.Width(popup) > layout.MessagesWidth-2 {
		t.Fatalf("popup width %d exceeds the pane cap %d", lipgloss.Width(popup), layout.MessagesWidth-2)
	}
	if lipgloss.Height(popup) != 3 {
		t.Fatalf("popup height %d; want one row plus the border — a long row is cut, not wrapped:\n%s", lipgloss.Height(popup), stripANSI(popup))
	}
	beforeLines, afterLines := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("frame grew from %d to %d lines", len(beforeLines), len(afterLines))
	}
	for i := range afterLines {
		if ansi.Cut(beforeLines[i], 0, layout.MessagesX0) != ansi.Cut(afterLines[i], 0, layout.MessagesX0) {
			t.Fatalf("row %d: the sidebar column changed under the popup:\n%q\n%q", i, beforeLines[i], afterLines[i])
		}
	}
	row := lineIndex(afterLines, "LongnameLongname")
	if row < 0 {
		t.Fatal("the popup row with the long name is not in the frame")
	}
	plainRow := stripANSI(afterLines[row])
	if col := ansi.StringWidth(plainRow[:strings.Index(plainRow, "LongnameLongname")]); col <= layout.MessagesX0 {
		t.Fatalf("the popup starts at column %d, inside the sidebar (pane starts at %d)", col, layout.MessagesX0)
	}
}

func TestCompletion_EscClosesPopupAndKeepsDraft(t *testing.T) {
	got := pressFocusKey(t, narrowApp(t, "hi @al"), "tab")
	got = pressFocusKey(t, got, "esc")
	if got.input.CompletionOpen() || got.input.Value() != "hi @al" || got.focus != FocusInput {
		t.Fatalf("after Esc: popup %v, draft %q, focus %v; want the popup closed, the draft kept, focus on the input",
			got.input.CompletionOpen(), got.input.Value(), got.focus)
	}
}

func TestCompletion_TypingNarrowsBackspaceWidens(t *testing.T) {
	got := pressFocusKey(t, narrowApp(t, "@a"), "tab")
	if s := suggestions(got); len(s) != 2 {
		t.Fatalf("@a: suggestions %v, want Alice and Albert", s)
	}
	got = pressFocusKey(t, got, "l")
	if s := suggestions(got); len(s) != 2 || got.input.Value() != "@al" {
		t.Fatalf("@al: suggestions %v, draft %q; want both still listed", s, got.input.Value())
	}
	got = pressFocusKey(t, got, "b")
	if s := suggestions(got); len(s) != 1 || s[0] != "@Albert" {
		t.Fatalf("@alb: suggestions %v, want Albert alone", s)
	}
	got = pressFocusKey(t, got, "backspace")
	if s := suggestions(got); len(s) != 2 || got.input.Value() != "@al" {
		t.Fatalf("Backspace: suggestions %v, draft %q; want both again", s, got.input.Value())
	}
	got = pressFocusKey(t, got, "tab")
	if got.input.CompletionOpen() || got.input.Value() != "@Alice " {
		t.Fatalf("Tab: popup %v, draft %q; want the first suggestion accepted", got.input.CompletionOpen(), got.input.Value())
	}
}

func TestCompletion_NoMatchOrSpaceClosesWithoutLosingText(t *testing.T) {
	got := pressFocusKey(t, narrowApp(t, "@al"), "tab")
	got = pressFocusKey(t, got, "x")
	if got.input.CompletionOpen() || got.input.Value() != "@alx" {
		t.Fatalf("no match: popup %v, draft %q; want it closed with the text kept", got.input.CompletionOpen(), got.input.Value())
	}

	got = pressFocusKey(t, narrowApp(t, "@al"), "tab")
	got = pressFocusKey(t, got, "space")
	if got.input.CompletionOpen() || got.input.Value() != "@al " {
		t.Fatalf("space: popup %v, draft %q; want it closed with the text kept", got.input.CompletionOpen(), got.input.Value())
	}
}

func TestMenus_RenderThroughTheOneListBox(t *testing.T) {
	var cm ContextMenuModel
	cm.Show(DisplayMessage{ID: "m1", Body: "hi"}, 0, 0, ContextMenuOptions{})
	rows := make([]listRow, len(cm.items))
	for i, it := range cm.items {
		rows[i] = listRow{label: it.Label}
	}
	if cm.View() != renderListBox(rows, cm.cursor, 0, 0) {
		t.Fatal("the context menu must render through renderListBox")
	}

	var mm MemberMenuModel
	mm.Show("usr_a", []ContextMenuItem{{Label: "Message", Action: "dm"}, {Label: "Verify", Action: "verify"}}, 0, 0)
	if mm.View() != renderListBox([]listRow{{label: "Message"}, {label: "Verify"}}, 0, 0, 0) {
		t.Fatal("the member menu must render through renderListBox")
	}

	// Rows are padded to one width, so the band is uniform: the selected
	// row's visible width equals the longest row's.
	box := stripANSI(renderListBox([]listRow{{label: "a"}, {label: "a much longer label", desc: "and a note"}}, 0, 0, 0))
	lines := strings.Split(box, "\n")
	if len(lines) < 4 || len([]rune(lines[1])) != len([]rune(lines[2])) {
		t.Fatalf("rows must be padded to one width:\n%s", box)
	}

	// The window: twenty rows, the cursor on row 10, eight visible — three
	// hidden above, nine below, and the cursor's row shown.
	many := make([]listRow, 20)
	for i := range many {
		many[i] = listRow{label: fmt.Sprintf("row %d", i)}
	}
	win := stripANSI(renderListBox(many, 10, 0, 8))
	if !strings.Contains(win, "↑ 3 more") || !strings.Contains(win, "↓ 9 more") || !strings.Contains(win, "row 10") || strings.Contains(win, "row 2\n") {
		t.Fatalf("window markers or rows wrong:\n%s", win)
	}
}
