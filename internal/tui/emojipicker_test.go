package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func emojiPickerRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func emojiPickerSelect(t *testing.T, cmd tea.Cmd) EmojiSelectedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected selection command")
	}
	msg, ok := cmd().(EmojiSelectedMsg)
	if !ok {
		t.Fatalf("selection command produced %T, want EmojiSelectedMsg", cmd())
	}
	return msg
}

func TestEmojiPicker_QuickRowArrowSelectsHundred(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	var cmd tea.Cmd
	for i := 0; i < 4; i++ {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		if cmd != nil {
			t.Fatal("arrow navigation should not emit a command")
		}
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sel := emojiPickerSelect(t, cmd)
	if sel.Emoji != "💯" {
		t.Fatalf("quick slot 5 selected %q, want 💯", sel.Emoji)
	}
	if m.IsVisible() {
		t.Fatal("picker should hide after selecting an emoji")
	}
}

func TestEmojiPicker_DigitsTypeInsteadOfQuickSelect(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("5"))
	if !m.IsVisible() {
		t.Fatal("picker should remain visible after typing a digit")
	}
	if got := m.input.Value(); got != "5" {
		t.Fatalf("search input = %q, want 5", got)
	}
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("focus = %v, want search input after typing", m.focus)
	}
}

func TestEmojiPicker_SearchHundredSelectsHundred(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("100"))
	if got := m.input.Value(); got != "100" {
		t.Fatalf("search input = %q, want 100", got)
	}
	entry, ok := m.selectedEntry()
	if !ok {
		t.Fatal("expected selected filtered emoji")
	}
	if entry.Emoji != "💯" {
		t.Fatalf("selected filtered emoji = %q (%s), want 💯", entry.Emoji, entry.Name)
	}

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sel := emojiPickerSelect(t, cmd)
	if sel.Emoji != "💯" {
		t.Fatalf("selected emoji = %q, want 💯", sel.Emoji)
	}
}

func TestEmojiPicker_SearchInputEditsAtCursor(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("ab"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = m.Update(emojiPickerRunes("c"))
	if got := m.input.Value(); got != "acb" {
		t.Fatalf("cursor-aware edit produced %q, want acb", got)
	}
}

func TestEmojiPicker_NoMatchesEnterNoop(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("notamatch"))
	if len(m.body) != 0 {
		t.Fatalf("expected no matches, got %d", len(m.body))
	}
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter with no matches should not emit a selection")
	}
	if !m.IsVisible() {
		t.Fatal("picker should stay visible after no-op Enter")
	}
}

func TestEmojiPicker_EmptySearchRendersCatalog(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	view := stripANSI(m.View(80, 18))

	for _, want := range []string{"React", "Quick", "💯", "Search", "Smileys & Emotion"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty picker view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "1-8=quick") {
		t.Fatalf("picker should not advertise removed number shortcuts:\n%s", view)
	}
	assertBlankLineBetween(t, view, "Quick", "Search")
	assertBlankLineBetween(t, view, "Search", "Smileys & Emotion")
}

func TestEmojiPicker_RemoveRowForCurrentUserReactions(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{
		ID: "msg_1",
		ReactionsByUser: map[string]map[string][]string{
			"usr_me": {
				"❤️": {"react_heart"},
				"😂":  {"react_laugh"},
			},
			"usr_other": {
				"🔥": {"react_fire"},
			},
		},
	}, "usr_me")
	m.SetViewport(80, 18)

	view := stripANSI(m.View(80, 18))
	if !strings.Contains(view, "Remove") || !strings.Contains(view, "❤️") || !strings.Contains(view, "😂") {
		t.Fatalf("remove row missing current user's reactions:\n%s", view)
	}
	if strings.Contains(strings.Split(view, "Quick")[0], "🔥") {
		t.Fatalf("remove row should not include other users' reactions:\n%s", view)
	}
	if m.zone != emojiPickerZoneQuick {
		t.Fatalf("picker should still start on Quick to avoid accidental removal, zone=%v", m.zone)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.zone != emojiPickerZoneRemove {
		t.Fatalf("Up from Quick should select Remove row, zone=%v", m.zone)
	}
	entry, ok := m.selectedEntry()
	if !ok || entry.Emoji != "😂" {
		t.Fatalf("first remove entry = %q ok=%v, want 😂", entry.Emoji, ok)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sel := emojiPickerSelect(t, cmd)
	if !sel.Remove || sel.Emoji != "❤️" {
		t.Fatalf("remove selection = emoji %q remove %v, want ❤️/true", sel.Emoji, sel.Remove)
	}
	if m.IsVisible() {
		t.Fatal("picker should hide after selecting a remove reaction")
	}
}

func TestEmojiPicker_NavigationKeepsBodyCursorVisible(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.zone != emojiPickerZoneBody {
		t.Fatalf("PgDown should move into body, zone=%v", m.zone)
	}
	row := m.rowIndexForBodyCursor()
	if row < m.offset || row >= m.offset+m.bodyRows {
		t.Fatalf("selected row %d not visible in offset=%d bodyRows=%d", row, m.offset, m.bodyRows)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	row = m.rowIndexForBodyCursor()
	if row < m.offset || row >= m.offset+m.bodyRows {
		t.Fatalf("end-selected row %d not visible in offset=%d bodyRows=%d", row, m.offset, m.bodyRows)
	}
}

func TestEmojiPicker_DownFollowsRenderedRowsAcrossCategoryBoundary(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	m.zone = emojiPickerZoneBody
	m.bodyCur = emojiPickerBodyIndex(t, m, "☕")
	m.rebuild()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	entry, ok := m.selectedEntry()
	if !ok {
		t.Fatal("expected selected emoji after moving down")
	}
	if entry.Emoji != "🎉" {
		t.Fatalf("down from first Food & Drink cell should land on first Activities cell; got %q (%s)", entry.Emoji, entry.Name)
	}
}

func TestEmojiPicker_TabCyclesQuickSearchAndCategories(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	starts := m.categoryStarts()
	if len(starts) < 3 {
		t.Fatalf("expected multiple category starts, got %v", starts)
	}

	if m.zone != emojiPickerZoneQuick || m.focus != emojiPickerFocusGrid {
		t.Fatalf("initial picker focus = zone %v focus %v, want quick/grid", m.zone, m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != emojiPickerFocusInput || m.zone != emojiPickerZoneQuick {
		t.Fatalf("first Tab should move from quick to search; zone=%v focus=%v", m.zone, m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != emojiPickerFocusGrid || m.zone != emojiPickerZoneBody || m.bodyCur != starts[0] {
		t.Fatalf("second Tab should move to first category start; zone=%v focus=%v bodyCur=%d want %d", m.zone, m.focus, m.bodyCur, starts[0])
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.bodyCur != starts[1] {
		t.Fatalf("third Tab should move to second category start; bodyCur=%d want %d", m.bodyCur, starts[1])
	}

	for i := 2; i < len(starts); i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		if m.bodyCur != starts[i] {
			t.Fatalf("Tab to category %d bodyCur=%d want %d", i, m.bodyCur, starts[i])
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.zone != emojiPickerZoneQuick || m.focus != emojiPickerFocusGrid {
		t.Fatalf("Tab after last category should wrap to quick; zone=%v focus=%v", m.zone, m.focus)
	}
}

func TestEmojiPicker_ArrowsIncludeSearchAsVerticalSection(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	starts := m.categoryStarts()
	if len(starts) == 0 {
		t.Fatal("expected category starts")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != emojiPickerFocusInput || m.zone != emojiPickerZoneQuick {
		t.Fatalf("Down from quick should focus search; zone=%v focus=%v", m.zone, m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != emojiPickerFocusGrid || m.zone != emojiPickerZoneBody || m.bodyCur != starts[0] {
		t.Fatalf("Down from search should move to first category; zone=%v focus=%v bodyCur=%d want %d", m.zone, m.focus, m.bodyCur, starts[0])
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("Up from first category should focus search; focus=%v", m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.focus != emojiPickerFocusGrid || m.zone != emojiPickerZoneQuick {
		t.Fatalf("Up from search should return to quick; zone=%v focus=%v", m.zone, m.focus)
	}
}

func TestEmojiPicker_ArrowsPreserveColumnBetweenQuickSearchAndCatalog(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	starts := m.categoryStarts()
	if len(starts) == 0 {
		t.Fatal("expected category starts")
	}

	for i := 0; i < 4; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.quickCur != 4 {
		t.Fatalf("quick cursor = %d, want 4", m.quickCur)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("Down from quick should focus search; focus=%v", m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	wantBody := starts[0] + 4
	if m.bodyCur != wantBody {
		t.Fatalf("Down from search should preserve quick column into catalog; bodyCur=%d want %d", m.bodyCur, wantBody)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.focus != emojiPickerFocusInput || m.quickCur != 4 {
		t.Fatalf("Up from first catalog row should focus search and preserve quick column; focus=%v quickCur=%d", m.focus, m.quickCur)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.zone != emojiPickerZoneQuick || m.quickCur != 4 {
		t.Fatalf("Up from search should return to quick at preserved column; zone=%v quickCur=%d", m.zone, m.quickCur)
	}
}

func TestEmojiPicker_SearchModeArrowsIncludeSearchAsVerticalSection(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	m, _ = m.Update(emojiPickerRunes("face"))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != emojiPickerFocusGrid || m.zone != emojiPickerZoneBody {
		t.Fatalf("Down from search should move to filtered results; zone=%v focus=%v", m.zone, m.focus)
	}
	m.bodyCur = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("Up from first filtered result should focus search; focus=%v", m.focus)
	}
}

func TestEmojiPicker_SearchModeTabTogglesInputAndResults(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)
	m, _ = m.Update(emojiPickerRunes("face"))
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("typing should focus input, got %v", m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != emojiPickerFocusGrid || m.zone != emojiPickerZoneBody {
		t.Fatalf("Tab in search mode should move to filtered results; zone=%v focus=%v", m.zone, m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("second Tab in search mode should return to input; focus=%v", m.focus)
	}
}

func TestEmojiPicker_TabSeparatesInputAndGridKeyOwnership(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("face"))
	if m.focus != emojiPickerFocusInput {
		t.Fatalf("typing should focus input, got %v", m.focus)
	}
	cursorBefore := m.bodyCur
	posBefore := m.input.Position()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != emojiPickerFocusGrid {
		t.Fatalf("Tab should move focus to grid, got %v", m.focus)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.bodyCur == cursorBefore {
		t.Fatal("right arrow should move emoji cursor when grid is focused")
	}
	if m.input.Position() != posBefore {
		t.Fatal("grid navigation should not move the search input cursor")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.input.Position() >= posBefore {
		t.Fatalf("left arrow should move text cursor when input is focused; pos=%d before=%d", m.input.Position(), posBefore)
	}
}

func TestEmojiPicker_LeakedMouseEscapeDoesNotEnterSearch(t *testing.T) {
	m := NewEmojiPicker()
	m.Show(DisplayMessage{ID: "msg_1"}, "")
	m.SetViewport(80, 18)

	m, _ = m.Update(emojiPickerRunes("[<65;51;37M"))
	if got := m.input.Value(); got != "" {
		t.Fatalf("leaked mouse bytes entered search input: %q", got)
	}
	m, _ = m.Update(emojiPickerRunes("a"))
	if got := m.input.Value(); got != "a" {
		t.Fatalf("normal typing after filtered mouse escape = %q, want a", got)
	}
}

func TestAppEmojiPicker_RendersAsOverlayWithShell(t *testing.T) {
	a := newEmojiOverlayTestApp()
	a.emojiPicker.Show(DisplayMessage{ID: "msg_1"}, "")

	out := stripANSI(a.View())
	for _, want := range []string{"React", "Quick", "# general", "E2E encrypted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("emoji overlay should keep app shell and picker visible; missing %q:\n%s", want, out)
		}
	}
}

func TestAppEmojiPicker_ClearsKittyPreviewLayer(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	a := newEmojiOverlayTestApp()
	a.emojiPicker.Show(DisplayMessage{ID: "msg_1"}, "")

	out := a.View()
	if !strings.HasPrefix(out, "\x1b_Ga=d,") {
		t.Fatalf("emoji overlay should prepend kitty delete escape so preview images do not show through, got prefix %q", truncateForLog(out, 24))
	}
	if strings.Contains(out, "\x1b_Ga=T,") {
		t.Fatal("emoji overlay frame should not contain a kitty placement while modal is visible")
	}
}

func newEmojiOverlayTestApp() App {
	a := App{
		connected:   true,
		width:       100,
		height:      30,
		sidebar:     NewSidebar(),
		messages:    NewMessages(),
		input:       NewInput(),
		statusBar:   NewStatusBar(),
		emojiPicker: NewEmojiPicker(),
		roomPins:    make(map[string][]string),
		muted:       make(map[string]bool),
	}
	a.sidebar.SetRooms([]string{"general"})
	a.messages.SetContext("general", "", "")
	a.statusBar.SetConnected(true)
	return a
}

func TestAppEmojiPicker_MouseIsSwallowed(t *testing.T) {
	a := App{
		width:       100,
		height:      30,
		emojiPicker: NewEmojiPicker(),
	}
	a.emojiPicker.Show(DisplayMessage{ID: "msg_1"}, "")

	model, cmd := a.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 10, Y: 10})
	if cmd != nil {
		t.Fatal("emoji picker mouse swallow should not emit a command")
	}
	got := model.(App)
	if got.emojiPicker.input.Value() != "" {
		t.Fatalf("mouse event should not mutate picker search input, got %q", got.emojiPicker.input.Value())
	}
	if !got.emojiPicker.IsVisible() {
		t.Fatal("mouse event should not close the emoji picker")
	}
}

func emojiPickerBodyIndex(t *testing.T, m EmojiPickerModel, glyph string) int {
	t.Helper()
	for i, entry := range m.body {
		if entry.Emoji == glyph {
			return i
		}
	}
	t.Fatalf("emoji %q not found in picker body", glyph)
	return 0
}

func assertBlankLineBetween(t *testing.T, view, first, second string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	firstIdx, secondIdx := -1, -1
	for i, line := range lines {
		if firstIdx < 0 && strings.Contains(line, first) {
			firstIdx = i
			continue
		}
		if firstIdx >= 0 && strings.Contains(line, second) {
			secondIdx = i
			break
		}
	}
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("could not find %q then %q in picker view:\n%s", first, second, view)
	}
	if secondIdx-firstIdx != 2 {
		t.Fatalf("expected one blank line between %q and %q, got line gap %d:\n%s", first, second, secondIdx-firstIdx, view)
	}
}
