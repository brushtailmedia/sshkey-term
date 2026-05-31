package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// EmojiPickerModel manages the emoji reaction picker overlay.
type EmojiPickerModel struct {
	visible   bool
	input     textinput.Model
	remove    []emojiEntry
	quick     []emojiEntry
	body      []emojiEntry
	rows      []emojiPickerRow
	focus     emojiPickerFocus
	zone      emojiPickerZone
	removeCur int
	quickCur  int
	bodyCur   int
	offset    int
	cols      int
	bodyRows  int
	viewWidth int
	viewPortW int
	viewPortH int

	// Same defensive state machine as PickerModel/InputModel: some tmux/terminal
	// setups leak SGR mouse bytes as KeyRunes. The picker has a focused search
	// input, so drop those bytes instead of letting scroll events become text.
	mouseSeqState int
	mouseSeqBuf   []rune

	targetMsg     DisplayMessage // message being reacted to
	currentUserID string
}

// EmojiSelectedMsg is sent when the user picks an emoji.
type EmojiSelectedMsg struct {
	Emoji  string
	Target DisplayMessage
	Remove bool
}

type emojiPickerFocus int

const (
	emojiPickerFocusGrid emojiPickerFocus = iota
	emojiPickerFocusInput
)

type emojiPickerZone int

const (
	emojiPickerZoneRemove emojiPickerZone = iota
	emojiPickerZoneQuick
	emojiPickerZoneBody
)

type emojiPickerRowKind int

const (
	emojiPickerRowHeader emojiPickerRowKind = iota
	emojiPickerRowGrid
	emojiPickerRowEmpty
	emojiPickerRowSpacer
)

type emojiPickerRow struct {
	kind  emojiPickerRowKind
	title string
	items []emojiEntry
	start int // index into EmojiPickerModel.body for grid rows
}

const (
	emojiPickerMinContentWidth = 36
	emojiPickerMaxContentWidth = 74
	emojiPickerMinBodyRows     = 4
	emojiPickerMaxBodyRows     = 12
	emojiPickerMaxCols         = 8
	emojiPickerCellWidth       = 4
)

func NewEmojiPicker() EmojiPickerModel {
	ti := textinput.New()
	ti.Placeholder = "type to search..."
	ti.Prompt = ""
	ti.CharLimit = 64
	ti.Width = 28
	ti.Blur()
	m := EmojiPickerModel{input: ti}
	m.quick = quickReactionEntries()
	m.configure(80, 18)
	m.rebuild()
	return m
}

func (e *EmojiPickerModel) Show(target DisplayMessage, currentUserID string) {
	e.visible = true
	e.targetMsg = target
	e.currentUserID = currentUserID
	e.input.SetValue("")
	e.input.Blur()
	e.focus = emojiPickerFocusGrid
	e.zone = emojiPickerZoneQuick
	e.removeCur = 0
	e.quickCur = 0
	e.bodyCur = 0
	e.offset = 0
	e.mouseSeqState = 0
	e.mouseSeqBuf = nil
	e.remove = nil
	if currentUserID != "" {
		e.remove = emojiEntriesForGlyphs(target.UserEmojis(currentUserID))
	}
	e.quick = quickReactionEntries()
	e.rebuild()
}

func (e *EmojiPickerModel) Hide() {
	e.visible = false
	e.input.Blur()
	e.mouseSeqState = 0
	e.mouseSeqBuf = nil
}

func (e *EmojiPickerModel) IsVisible() bool {
	return e.visible
}

// SetViewport records the picker sizing hints derived from the messages pane.
// App.Update calls this before dispatching picker keys so page-size navigation
// matches the next render. View also re-applies the same math on a render copy.
func (e *EmojiPickerModel) SetViewport(width, height int) {
	e.configure(width, height)
	e.rebuild()
}

func (e *EmojiPickerModel) configure(width, height int) {
	if width <= 0 {
		width = emojiPickerMaxContentWidth + 8
	}
	if height <= 0 {
		height = emojiPickerMaxBodyRows + 10
	}

	// Leave room for dialog border/padding and the messages-pane inset. On
	// normal app sizes this gives the picker the nav-popup feel; on unusually
	// narrow terminals it shrinks instead of forcing wrapped emoji rows.
	contentWidth := maxInt(12, width-8)
	if contentWidth > emojiPickerMaxContentWidth {
		contentWidth = emojiPickerMaxContentWidth
	}
	if contentWidth < emojiPickerMinContentWidth && width >= emojiPickerMinContentWidth+8 {
		contentWidth = emojiPickerMinContentWidth
	}

	cols := contentWidth / emojiPickerCellWidth
	if cols < 1 {
		cols = 1
	}
	if cols > emojiPickerMaxCols {
		cols = emojiPickerMaxCols
	}

	// Fixed rows: title, quick, search, selected-name and footer, including the
	// deliberate spacer lines between those regions. This tracks the nav-popup
	// overlay family: compact shell, with only the catalog body scrolling.
	bodyRows := height - 14
	if len(e.remove) > 0 {
		bodyRows -= 2
	}
	if bodyRows < emojiPickerMinBodyRows {
		bodyRows = emojiPickerMinBodyRows
	}
	if bodyRows > emojiPickerMaxBodyRows {
		bodyRows = emojiPickerMaxBodyRows
	}

	e.viewWidth = contentWidth
	e.viewPortW = width
	e.viewPortH = height
	e.cols = cols
	e.bodyRows = bodyRows
	e.input.Width = maxInt(1, contentWidth-len("Search "))
}

func (e EmojiPickerModel) Update(msg tea.KeyMsg) (EmojiPickerModel, tea.Cmd) {
	e.configure(e.viewPortW, e.viewPortH)
	e.rebuild()

	if msg.Type == tea.KeyRunes {
		msg.Runes = e.filterLeakedMouseRunes(msg.Runes)
		if len(msg.Runes) == 0 {
			return e, nil
		}
	} else if e.mouseSeqState != 0 {
		e.flushHeldMouseRunesToInput()
		e.rebuildAfterQueryChange()
	}

	switch msg.Type {
	case tea.KeyEsc:
		e.Hide()
		return e, nil
	case tea.KeyTab:
		e.advanceTab(1)
		return e, nil
	case tea.KeyShiftTab:
		e.advanceTab(-1)
		return e, nil
	case tea.KeyEnter:
		return e.selectCurrent()
	}

	if e.focus == emojiPickerFocusInput && (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) {
		e.moveFromInput(msg.Type)
		e.rebuild()
		return e, nil
	}

	if e.focus == emojiPickerFocusInput {
		return e.updateInput(msg)
	}

	if msg.Type == tea.KeyRunes && hasPrintableRunes(msg.Runes) {
		e.focusInput()
		return e.updateInput(msg)
	}

	switch msg.Type {
	case tea.KeyLeft:
		e.moveLeft()
	case tea.KeyRight:
		e.moveRight()
	case tea.KeyUp:
		e.moveUp()
	case tea.KeyDown:
		e.moveDown()
	case tea.KeyPgUp:
		query := strings.TrimSpace(e.input.Value())
		if query == "" && e.zone == emojiPickerZoneRemove {
			break
		}
		if e.zone != emojiPickerZoneQuick || query != "" {
			if !e.moveBodyRows(-e.bodyRows) && query == "" {
				e.zone = emojiPickerZoneQuick
				e.quickCur = 0
			}
		}
	case tea.KeyPgDown:
		query := strings.TrimSpace(e.input.Value())
		if e.zone == emojiPickerZoneRemove && query == "" {
			e.zone = emojiPickerZoneQuick
		} else if e.zone == emojiPickerZoneQuick && query == "" {
			e.moveToFirstBody()
		} else {
			e.moveBodyRows(e.bodyRows)
		}
	case tea.KeyHome:
		e.moveHome()
	case tea.KeyEnd:
		e.moveEnd()
	}

	e.rebuild()
	return e, nil
}

func (e EmojiPickerModel) updateInput(msg tea.KeyMsg) (EmojiPickerModel, tea.Cmd) {
	before := e.input.Value()
	e.input.Focus()
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	if before != e.input.Value() {
		e.rebuildAfterQueryChange()
	} else {
		e.rebuild()
	}
	return e, cmd
}

func (e *EmojiPickerModel) rebuildAfterQueryChange() {
	if strings.TrimSpace(e.input.Value()) == "" {
		e.zone = emojiPickerZoneQuick
	} else {
		e.zone = emojiPickerZoneBody
		e.bodyCur = 0
		e.offset = 0
	}
	e.rebuild()
}

func (e *EmojiPickerModel) rebuild() {
	if e.cols <= 0 {
		e.configure(e.viewPortW, e.viewPortH)
	}
	if len(e.quick) == 0 {
		e.quick = quickReactionEntries()
	}

	query := strings.TrimSpace(e.input.Value())
	if query != "" {
		e.body = FilterEmoji(query)
		e.rows = buildEmojiRows("Results", e.body, e.cols)
		e.zone = emojiPickerZoneBody
	} else {
		e.body = nil
		e.rows = nil
		byCategory := make(map[string][]emojiEntry)
		for _, entry := range AllEmoji() {
			cat := emojiCategory(entry)
			byCategory[cat] = append(byCategory[cat], entry)
		}
		for _, cat := range emojiCategoryOrder {
			items := byCategory[cat]
			if len(items) == 0 {
				continue
			}
			if len(e.rows) > 0 {
				e.rows = append(e.rows, emojiPickerRow{kind: emojiPickerRowSpacer})
			}
			start := len(e.body)
			e.body = append(e.body, items...)
			rows := buildEmojiRowsWithStart(cat, items, e.cols, start)
			e.rows = append(e.rows, rows...)
		}
	}

	e.removeCur = clampInt(e.removeCur, 0, maxInt(0, len(e.remove)-1))
	e.quickCur = clampInt(e.quickCur, 0, maxInt(0, len(e.quick)-1))
	if len(e.body) == 0 {
		e.bodyCur = 0
		e.offset = 0
	} else {
		e.bodyCur = clampInt(e.bodyCur, 0, len(e.body)-1)
		e.ensureBodyCursorVisible()
	}

	if query != "" && e.zone == emojiPickerZoneQuick {
		e.zone = emojiPickerZoneBody
	}
	if query != "" && e.zone == emojiPickerZoneRemove {
		e.zone = emojiPickerZoneBody
	}
	if e.zone == emojiPickerZoneRemove && len(e.remove) == 0 {
		e.zone = emojiPickerZoneQuick
	}
	if e.zone == emojiPickerZoneBody && len(e.body) == 0 && query == "" {
		e.zone = emojiPickerZoneQuick
	}
	e.clampOffset()
}

func buildEmojiRows(title string, items []emojiEntry, cols int) []emojiPickerRow {
	return buildEmojiRowsWithStart(title, items, cols, 0)
}

func buildEmojiRowsWithStart(title string, items []emojiEntry, cols, start int) []emojiPickerRow {
	if cols <= 0 {
		cols = 1
	}
	rows := []emojiPickerRow{{kind: emojiPickerRowHeader, title: title}}
	if len(items) == 0 {
		return append(rows, emojiPickerRow{kind: emojiPickerRowEmpty})
	}
	for i := 0; i < len(items); i += cols {
		end := i + cols
		if end > len(items) {
			end = len(items)
		}
		rows = append(rows, emojiPickerRow{
			kind:  emojiPickerRowGrid,
			items: items[i:end],
			start: start + i,
		})
	}
	return rows
}

func (e *EmojiPickerModel) advanceTab(dir int) {
	if dir == 0 {
		dir = 1
	}
	query := strings.TrimSpace(e.input.Value())
	if query != "" {
		// Search mode has only two meaningful sections: search input and the
		// filtered results body. Categories are hidden while filtering.
		if e.focus == emojiPickerFocusInput {
			e.focusGrid()
			e.zone = emojiPickerZoneBody
			if len(e.body) > 0 {
				e.bodyCur = clampInt(e.bodyCur, 0, len(e.body)-1)
				e.ensureBodyCursorVisible()
			}
			return
		}
		e.focusInput()
		return
	}

	if dir < 0 {
		e.advanceTabBackward()
		return
	}
	e.advanceTabForward()
}

func (e *EmojiPickerModel) advanceTabForward() {
	if e.zone == emojiPickerZoneRemove && e.focus != emojiPickerFocusInput {
		e.focusGrid()
		e.zone = emojiPickerZoneQuick
		return
	}
	if e.zone == emojiPickerZoneQuick && e.focus != emojiPickerFocusInput {
		e.focusInput()
		return
	}
	starts := e.categoryStarts()
	if len(starts) == 0 {
		e.focusGrid()
		e.zone = emojiPickerZoneQuick
		return
	}
	if e.focus == emojiPickerFocusInput {
		e.selectCategoryStart(starts[0])
		return
	}
	idx := e.currentCategoryIndex(starts)
	if idx >= 0 && idx+1 < len(starts) {
		e.selectCategoryStart(starts[idx+1])
		return
	}
	e.focusGrid()
	if len(e.remove) > 0 {
		e.zone = emojiPickerZoneRemove
		return
	}
	e.zone = emojiPickerZoneQuick
}

func (e *EmojiPickerModel) advanceTabBackward() {
	starts := e.categoryStarts()
	if e.zone == emojiPickerZoneRemove && e.focus != emojiPickerFocusInput {
		if len(starts) > 0 {
			e.selectCategoryStart(starts[len(starts)-1])
			return
		}
		e.focusInput()
		return
	}
	if e.zone == emojiPickerZoneQuick && e.focus != emojiPickerFocusInput {
		if len(e.remove) > 0 {
			e.zone = emojiPickerZoneRemove
			e.removeCur = clampInt(e.removeCur, 0, len(e.remove)-1)
			return
		}
		if len(starts) > 0 {
			e.selectCategoryStart(starts[len(starts)-1])
			return
		}
		e.focusInput()
		return
	}
	if e.focus == emojiPickerFocusInput {
		e.focusGrid()
		e.zone = emojiPickerZoneQuick
		return
	}
	idx := e.currentCategoryIndex(starts)
	if idx > 0 {
		e.selectCategoryStart(starts[idx-1])
		return
	}
	e.focusInput()
}

func (e EmojiPickerModel) categoryStarts() []int {
	starts := make([]int, 0, len(emojiCategoryOrder))
	pendingHeader := false
	for _, row := range e.rows {
		switch row.kind {
		case emojiPickerRowHeader:
			pendingHeader = true
		case emojiPickerRowGrid:
			if pendingHeader {
				starts = append(starts, row.start)
				pendingHeader = false
			}
		case emojiPickerRowEmpty, emojiPickerRowSpacer:
		}
	}
	return starts
}

func (e EmojiPickerModel) currentCategoryIndex(starts []int) int {
	idx := -1
	for i, start := range starts {
		if e.bodyCur < start {
			break
		}
		idx = i
	}
	return idx
}

func (e *EmojiPickerModel) selectCategoryStart(start int) {
	if len(e.body) == 0 {
		return
	}
	e.focusGrid()
	e.zone = emojiPickerZoneBody
	e.bodyCur = clampInt(start, 0, len(e.body)-1)
	e.ensureBodyCursorVisible()
}

func (e *EmojiPickerModel) focusInput() {
	e.focus = emojiPickerFocusInput
	e.input.Focus()
}

func (e *EmojiPickerModel) focusGrid() {
	e.focus = emojiPickerFocusGrid
	e.input.Blur()
}

func (e *EmojiPickerModel) moveFromInput(key tea.KeyType) {
	query := strings.TrimSpace(e.input.Value())
	switch key {
	case tea.KeyUp:
		if query == "" {
			e.focusGrid()
			e.zone = emojiPickerZoneQuick
		}
	case tea.KeyDown:
		if len(e.body) == 0 {
			return
		}
		e.focusGrid()
		e.zone = emojiPickerZoneBody
		if query == "" {
			e.bodyCur = e.firstBodyIndexAtColumn(e.quickCur)
		} else {
			e.bodyCur = clampInt(e.bodyCur, 0, len(e.body)-1)
		}
		e.ensureBodyCursorVisible()
	}
}

func (e EmojiPickerModel) selectCurrent() (EmojiPickerModel, tea.Cmd) {
	entry, ok := e.selectedEntry()
	if !ok {
		return e, nil
	}
	target := e.targetMsg
	remove := strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneRemove
	e.Hide()
	return e, func() tea.Msg {
		return EmojiSelectedMsg{Emoji: entry.Emoji, Target: target, Remove: remove}
	}
}

func (e EmojiPickerModel) selectedEntry() (emojiEntry, bool) {
	query := strings.TrimSpace(e.input.Value())
	if query == "" && e.zone == emojiPickerZoneRemove {
		if e.removeCur >= 0 && e.removeCur < len(e.remove) {
			return e.remove[e.removeCur], true
		}
		return emojiEntry{}, false
	}
	if query == "" && e.zone == emojiPickerZoneQuick {
		if e.quickCur >= 0 && e.quickCur < len(e.quick) {
			return e.quick[e.quickCur], true
		}
		return emojiEntry{}, false
	}
	if e.bodyCur >= 0 && e.bodyCur < len(e.body) {
		return e.body[e.bodyCur], true
	}
	return emojiEntry{}, false
}

func (e *EmojiPickerModel) moveLeft() {
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneRemove {
		e.removeCur = clampInt(e.removeCur-1, 0, maxInt(0, len(e.remove)-1))
		return
	}
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneQuick {
		e.quickCur = clampInt(e.quickCur-1, 0, maxInt(0, len(e.quick)-1))
		return
	}
	e.moveBody(-1)
}

func (e *EmojiPickerModel) moveRight() {
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneRemove {
		e.removeCur = clampInt(e.removeCur+1, 0, maxInt(0, len(e.remove)-1))
		return
	}
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneQuick {
		e.quickCur = clampInt(e.quickCur+1, 0, maxInt(0, len(e.quick)-1))
		return
	}
	e.moveBody(1)
}

func (e *EmojiPickerModel) moveUp() {
	if e.zone == emojiPickerZoneRemove {
		return
	}
	if e.zone == emojiPickerZoneQuick {
		if len(e.remove) > 0 {
			e.zone = emojiPickerZoneRemove
			e.removeCur = clampInt(e.quickCur, 0, maxInt(0, len(e.remove)-1))
		}
		return
	}
	if !e.moveBodyRows(-1) {
		_, col, ok := e.currentBodyPosition()
		if ok {
			e.quickCur = clampInt(col, 0, maxInt(0, len(e.quick)-1))
		}
		e.focusInput()
	}
}

func (e *EmojiPickerModel) moveDown() {
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneRemove {
		e.zone = emojiPickerZoneQuick
		e.quickCur = clampInt(e.removeCur, 0, maxInt(0, len(e.quick)-1))
		return
	}
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneQuick {
		e.focusInput()
		return
	}
	e.moveBodyRows(1)
}

func (e *EmojiPickerModel) moveToFirstBody() {
	if len(e.body) == 0 {
		return
	}
	e.focusGrid()
	e.zone = emojiPickerZoneBody
	starts := e.categoryStarts()
	if len(starts) > 0 {
		e.bodyCur = e.firstBodyIndexAtColumn(e.quickCur)
	} else {
		e.bodyCur = 0
	}
	e.ensureBodyCursorVisible()
}

func (e EmojiPickerModel) firstBodyIndexAtColumn(col int) int {
	if len(e.body) == 0 {
		return 0
	}
	for _, row := range e.rows {
		if row.kind != emojiPickerRowGrid || len(row.items) == 0 {
			continue
		}
		return row.start + clampInt(col, 0, len(row.items)-1)
	}
	return 0
}

func (e *EmojiPickerModel) moveBody(delta int) {
	if len(e.body) == 0 {
		e.bodyCur = 0
		e.offset = 0
		return
	}
	e.zone = emojiPickerZoneBody
	e.bodyCur = clampInt(e.bodyCur+delta, 0, len(e.body)-1)
	e.ensureBodyCursorVisible()
}

func (e *EmojiPickerModel) moveBodyRows(deltaRows int) bool {
	if len(e.body) == 0 || deltaRows == 0 {
		return false
	}
	rowIdx, col, ok := e.currentBodyPosition()
	if !ok {
		return false
	}
	target := rowIdx
	steps := deltaRows
	step := 1
	if steps < 0 {
		step = -1
		steps = -steps
	}
	for steps > 0 {
		next := e.nextGridRow(target, step)
		if next < 0 {
			return false
		}
		target = next
		steps--
	}
	row := e.rows[target]
	if len(row.items) == 0 {
		return false
	}
	if col >= len(row.items) {
		col = len(row.items) - 1
	}
	e.zone = emojiPickerZoneBody
	e.bodyCur = row.start + col
	e.ensureBodyCursorVisible()
	return true
}

func (e EmojiPickerModel) currentBodyPosition() (rowIdx, col int, ok bool) {
	for i, row := range e.rows {
		if row.kind != emojiPickerRowGrid {
			continue
		}
		if e.bodyCur >= row.start && e.bodyCur < row.start+len(row.items) {
			return i, e.bodyCur - row.start, true
		}
	}
	return 0, 0, false
}

func (e EmojiPickerModel) nextGridRow(from, step int) int {
	for i := from + step; i >= 0 && i < len(e.rows); i += step {
		if e.rows[i].kind == emojiPickerRowGrid {
			return i
		}
	}
	return -1
}

func (e *EmojiPickerModel) moveHome() {
	if strings.TrimSpace(e.input.Value()) == "" {
		if len(e.remove) > 0 {
			e.zone = emojiPickerZoneRemove
			e.removeCur = 0
		} else {
			e.zone = emojiPickerZoneQuick
			e.quickCur = 0
		}
		return
	}
	if len(e.body) > 0 {
		e.zone = emojiPickerZoneBody
		e.bodyCur = 0
		e.ensureBodyCursorVisible()
	}
}

func (e *EmojiPickerModel) moveEnd() {
	if len(e.body) > 0 {
		e.zone = emojiPickerZoneBody
		e.bodyCur = len(e.body) - 1
		e.ensureBodyCursorVisible()
		return
	}
	if len(e.quick) > 0 {
		e.zone = emojiPickerZoneQuick
		e.quickCur = len(e.quick) - 1
	} else if len(e.remove) > 0 {
		e.zone = emojiPickerZoneRemove
		e.removeCur = len(e.remove) - 1
	}
}

func (e *EmojiPickerModel) ensureBodyCursorVisible() {
	rowIdx := e.rowIndexForBodyCursor()
	if rowIdx < 0 {
		e.clampOffset()
		return
	}
	if rowIdx < e.offset {
		e.offset = rowIdx
	}
	if rowIdx >= e.offset+e.bodyRows {
		e.offset = rowIdx - e.bodyRows + 1
	}
	e.clampOffset()
}

func (e *EmojiPickerModel) clampOffset() {
	maxOffset := len(e.rows) - e.bodyRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	e.offset = clampInt(e.offset, 0, maxOffset)
}

func (e EmojiPickerModel) rowIndexForBodyCursor() int {
	for i, row := range e.rows {
		if row.kind != emojiPickerRowGrid {
			continue
		}
		if e.bodyCur >= row.start && e.bodyCur < row.start+len(row.items) {
			return i
		}
	}
	return -1
}

func (e *EmojiPickerModel) filterLeakedMouseRunes(runes []rune) []rune {
	var out []rune
	for _, r := range runes {
		for {
			again := false
			switch e.mouseSeqState {
			case 0:
				if r == '[' {
					e.mouseSeqState = 1
				} else {
					out = append(out, r)
				}
			case 1:
				if r == '<' {
					e.mouseSeqState = 2
					e.mouseSeqBuf = e.mouseSeqBuf[:0]
					break
				}
				out = append(out, '[')
				e.mouseSeqState = 0
				again = true
			case 2:
				switch {
				case r >= '0' && r <= '9', r == ';':
					e.mouseSeqBuf = append(e.mouseSeqBuf, r)
				case r == 'M' || r == 'm':
					e.mouseSeqState = 0
					e.mouseSeqBuf = e.mouseSeqBuf[:0]
				default:
					out = append(out, '[', '<')
					out = append(out, e.mouseSeqBuf...)
					e.mouseSeqBuf = e.mouseSeqBuf[:0]
					e.mouseSeqState = 0
					again = true
				}
			}
			if !again {
				break
			}
		}
	}
	return out
}

func (e *EmojiPickerModel) flushHeldMouseRunesToInput() {
	var held string
	switch e.mouseSeqState {
	case 1:
		held = "["
	case 2:
		held = "[<" + string(e.mouseSeqBuf)
		e.mouseSeqBuf = e.mouseSeqBuf[:0]
	}
	e.mouseSeqState = 0
	if held == "" {
		return
	}
	e.input.Focus()
	e.input, _ = e.input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(held)})
}

func hasPrintableRunes(runes []rune) bool {
	for _, r := range runes {
		if !unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func (e EmojiPickerModel) View(width, height int) string {
	if !e.visible {
		return ""
	}
	e.configure(width, height)
	e.rebuild()

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" React"))
	b.WriteString("\n\n")
	if len(e.remove) > 0 {
		b.WriteString(e.renderRemoveRow())
		b.WriteString("\n\n")
	}
	b.WriteString(e.renderQuickRow())
	b.WriteString("\n\n")
	b.WriteString(e.renderSearchRow())
	b.WriteString("\n\n")
	b.WriteString(e.renderBodyRows())
	b.WriteString("\n\n")
	b.WriteString(e.renderSelectedName())
	b.WriteString("\n\n")
	b.WriteString(e.renderFooter())

	return dialogStyle.Width(e.viewWidth).Render(b.String())
}

func (e EmojiPickerModel) renderRemoveRow() string {
	var b strings.Builder
	b.WriteString(helpDescStyle.Render("Remove "))
	query := strings.TrimSpace(e.input.Value())
	for i, entry := range e.remove {
		cell := padEmojiCell(" "+entry.Emoji+" ", emojiPickerCellWidth)
		if query == "" && e.zone == emojiPickerZoneRemove && i == e.removeCur {
			cell = completionSelectedStyle.Render(cell)
		}
		b.WriteString(cell)
	}
	return b.String()
}

func (e EmojiPickerModel) renderQuickRow() string {
	var b strings.Builder
	b.WriteString(helpDescStyle.Render("Quick "))
	query := strings.TrimSpace(e.input.Value())
	for i, entry := range e.quick {
		cell := padEmojiCell(" "+entry.Emoji+" ", emojiPickerCellWidth)
		if query == "" && e.zone == emojiPickerZoneQuick && i == e.quickCur {
			cell = completionSelectedStyle.Render(cell)
		}
		b.WriteString(cell)
	}
	return b.String()
}

func (e EmojiPickerModel) renderSearchRow() string {
	input := e.input
	input.Width = maxInt(1, e.viewWidth-len("Search "))
	if e.focus == emojiPickerFocusInput {
		input.Focus()
	} else {
		input.Blur()
	}
	return helpDescStyle.Render("Search ") + input.View()
}

func (e EmojiPickerModel) renderBodyRows() string {
	if e.bodyRows <= 0 {
		return ""
	}
	var b strings.Builder
	end := e.offset + e.bodyRows
	if end > len(e.rows) {
		end = len(e.rows)
	}
	for i := e.offset; i < end; i++ {
		row := e.rows[i]
		switch row.kind {
		case emojiPickerRowHeader:
			b.WriteString(helpDescStyle.Render(row.title))
		case emojiPickerRowEmpty:
			b.WriteString(helpDescStyle.Render("  no matches"))
		case emojiPickerRowSpacer:
			// Intentional empty spacer between category blocks.
		case emojiPickerRowGrid:
			b.WriteString("  ")
			for j, entry := range row.items {
				idx := row.start + j
				cell := padEmojiCell(" "+entry.Emoji+" ", emojiPickerCellWidth)
				if e.zone == emojiPickerZoneBody && idx == e.bodyCur {
					cell = completionSelectedStyle.Render(cell)
				}
				b.WriteString(cell)
			}
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (e EmojiPickerModel) renderSelectedName() string {
	entry, ok := e.selectedEntry()
	if !ok {
		return helpDescStyle.Render("no emoji selected")
	}
	if strings.TrimSpace(e.input.Value()) == "" && e.zone == emojiPickerZoneRemove {
		return checkStyle.Render("remove " + ansi.Truncate(entry.Name, maxInt(1, e.viewWidth-len("remove ")), "…"))
	}
	return checkStyle.Render(ansi.Truncate(entry.Name, e.viewWidth, "…"))
}

func (e EmojiPickerModel) renderFooter() string {
	var footer string
	if e.focus == emojiPickerFocusInput {
		footer = "Tab=next section  Enter=select  Esc=cancel"
	} else if e.viewWidth < 52 {
		footer = "Tab=next section  Enter=select  Esc=cancel"
	} else {
		footer = "←→↑↓ navigate  PgUp/PgDn page  Tab=next section  Enter=select  Esc=cancel"
	}
	return helpDescStyle.Render(ansi.Truncate(footer, e.viewWidth, "…"))
}

func padEmojiCell(s string, width int) string {
	if w := ansi.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func clampInt(v, low, high int) int {
	if high < low {
		low, high = high, low
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
