package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PickerModel is the shared single-select modal picker (spec:
// shared-picker-widget.md, decisions #1–#13). It is the bare-form
// affordance for picker verbs — typed `verb @user` keeps its existing
// direct path and never opens this. The widget is deliberately dumb:
// `App` injects a candidate list and a PickerRequest; on Enter the
// widget echoes the request back with the selected item ID via
// PickerSelectedMsg, and `App` alone interprets it (#5). It never
// knows whether an ID is a user or a group (#4).
//
// Shape (cursor/render) is borrowed from LastAdminPickerModel; the
// viewport scrolling and the type-to-filter input are net-new (no
// existing widget had them — shared-picker-widget.md §4).
type PickerModel struct {
	visible  bool
	req      PickerRequest
	all      []PickerItem // full injected candidate set
	filtered []PickerItem // all, narrowed by `filter` (== all when filter == "")
	filter   string       // live filter text (only mutated when req.ShowFilter)
	cursor   int          // index into `filtered`
	scroll   int          // index of the first visible row in `filtered`

	// Same defensive state machine as InputModel: some terminal/tmux setups
	// leak SGR mouse bytes as KeyRunes. The picker has a filter input, so it
	// needs to drop those bytes too instead of treating scroll as typed text.
	mouseSeqState int
	mouseSeqBuf   []rune
}

// pickerVisibleRows is the viewport height (item rows shown at once).
// Lists longer than this scroll; the cursor is always kept visible.
const pickerVisibleRows = 12

// Match the scroll feel used by help/messages panels: a wheel notch moves a
// small chunk, while cursor/viewport clamping keeps the selected row visible.
const pickerMouseWheelStep = 3

// PickerItem is one selectable row. ID is opaque to the widget
// (userID or groupID — #4). Primary is rendered and filtered;
// Secondary is render-only (e.g. "retired"); Search holds extra
// filter-only alias terms (never rendered).
type PickerItem struct {
	ID        string
	Primary   string
	Secondary string
	Search    []string
}

// PickerSource records where the picker was opened from so App can
// route the selection correctly (e.g. member-panel add-to-group
// selects a group ID but still needs the subject user).
type PickerSource string

const (
	PickerSourceSlash       PickerSource = "slash"
	PickerSourceInfoPanel   PickerSource = "info_panel"
	PickerSourceMemberPanel PickerSource = "member_panel"
)

// PickerRequest is the originating context, injected at Show and
// echoed back in PickerSelectedMsg. App owns all verb knowledge.
type PickerRequest struct {
	Verb            string
	Source          PickerSource
	Room            string
	Group           string
	DM              string
	ShowFilter      bool
	SubjectUserID   string // member-panel add-to-group: the user being added
	SubjectUserName string
}

// PickerSelectedMsg is emitted on Enter. App.Update switches on
// Request.Verb (+ Source) to the right post-resolution step.
type PickerSelectedMsg struct {
	Request    PickerRequest
	SelectedID string
}

func (m *PickerModel) Show(req PickerRequest, items []PickerItem) {
	m.visible = true
	m.req = req
	m.all = items
	m.filter = ""
	m.cursor = 0
	m.scroll = 0
	m.mouseSeqState = 0
	m.mouseSeqBuf = nil
	m.applyFilter()
}

func (m *PickerModel) Hide() {
	m.visible = false
	m.req = PickerRequest{}
	m.all = nil
	m.filtered = nil
	m.filter = ""
	m.cursor = 0
	m.scroll = 0
	m.mouseSeqState = 0
	m.mouseSeqBuf = nil
}

func (m *PickerModel) IsVisible() bool {
	return m.visible
}

// applyFilter rebuilds `filtered` from `all` using a case-insensitive
// substring match of `filter` against Primary + Search. An empty
// filter is the identity (filtered == all). Resets cursor/scroll.
func (m *PickerModel) applyFilter() {
	if m.filter == "" {
		m.filtered = m.all
		m.cursor = 0
		m.scroll = 0
		return
	}
	needle := strings.ToLower(m.filter)
	out := make([]PickerItem, 0, len(m.all))
	for _, it := range m.all {
		if strings.Contains(strings.ToLower(it.Primary), needle) {
			out = append(out, it)
			continue
		}
		matched := false
		for _, s := range it.Search {
			if strings.Contains(strings.ToLower(s), needle) {
				matched = true
				break
			}
		}
		if matched {
			out = append(out, it)
		}
	}
	m.filtered = out
	m.cursor = 0
	m.scroll = 0
}

func (m *PickerModel) moveCursor(delta int) {
	n := len(m.filtered)
	if n == 0 {
		m.cursor = 0
		m.scroll = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	m.clampScroll()
}

// clampScroll keeps the cursor inside the visible viewport window.
func (m *PickerModel) clampScroll() {
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+pickerVisibleRows {
		m.scroll = m.cursor - pickerVisibleRows + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *PickerModel) filterLeakedMouseRunes(runes []rune) []rune {
	var out []rune
	for _, r := range runes {
		for {
			again := false
			switch m.mouseSeqState {
			case 0:
				if r == '[' {
					m.mouseSeqState = 1
				} else {
					out = append(out, r)
				}
			case 1:
				if r == '<' {
					m.mouseSeqState = 2
					m.mouseSeqBuf = m.mouseSeqBuf[:0]
					break
				}
				out = append(out, '[')
				m.mouseSeqState = 0
				again = true
			case 2:
				switch {
				case r >= '0' && r <= '9', r == ';':
					m.mouseSeqBuf = append(m.mouseSeqBuf, r)
				case r == 'M' || r == 'm':
					m.mouseSeqState = 0
					m.mouseSeqBuf = m.mouseSeqBuf[:0]
				default:
					out = append(out, '[', '<')
					out = append(out, m.mouseSeqBuf...)
					m.mouseSeqBuf = m.mouseSeqBuf[:0]
					m.mouseSeqState = 0
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

func (m *PickerModel) flushHeldMouseRunesToFilter() {
	switch m.mouseSeqState {
	case 1:
		m.filter += "["
	case 2:
		m.filter += "[<" + string(m.mouseSeqBuf)
		m.mouseSeqBuf = m.mouseSeqBuf[:0]
	}
	m.mouseSeqState = 0
}

func (m PickerModel) Update(msg tea.KeyMsg) (PickerModel, tea.Cmd) {
	if m.req.ShowFilter {
		if msg.Type == tea.KeyRunes {
			msg.Runes = m.filterLeakedMouseRunes(msg.Runes)
			if len(msg.Runes) == 0 {
				return m, nil
			}
		} else if m.mouseSeqState != 0 {
			m.flushHeldMouseRunesToFilter()
			m.applyFilter()
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.Hide()
		return m, nil
	case tea.KeyEnter:
		if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
			return m, nil
		}
		sel := m.filtered[m.cursor]
		req := m.req
		m.Hide()
		return m, func() tea.Msg {
			return PickerSelectedMsg{Request: req, SelectedID: sel.ID}
		}
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown:
		m.moveCursor(1)
		return m, nil
	case tea.KeyPgUp:
		m.moveCursor(-pickerVisibleRows)
		return m, nil
	case tea.KeyPgDown:
		m.moveCursor(pickerVisibleRows)
		return m, nil
	case tea.KeyHome:
		m.cursor = 0
		m.clampScroll()
		return m, nil
	case tea.KeyEnd:
		if n := len(m.filtered); n > 0 {
			m.cursor = n - 1
		}
		m.clampScroll()
		return m, nil
	case tea.KeyBackspace:
		if m.req.ShowFilter && m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.applyFilter()
		}
		return m, nil
	case tea.KeySpace:
		if m.req.ShowFilter {
			m.filter += " "
			m.applyFilter()
		}
		return m, nil
	case tea.KeyRunes:
		if m.req.ShowFilter {
			m.filter += string(msg.Runes)
			m.applyFilter()
			return m, nil
		}
		// No filter for this verb: vim-style nav + quick-close,
		// mirroring LastAdminPicker/StatusPicker ergonomics.
		switch string(msg.Runes) {
		case "k":
			m.moveCursor(-1)
		case "j":
			m.moveCursor(1)
		case "q":
			m.Hide()
		}
		return m, nil
	}
	return m, nil
}

// HandleMouse consumes mouse events while the picker is visible. Wheel events
// move the highlighted row; clicks intentionally do not select yet, matching
// the safer "mouse moves focus/selection, Enter confirms" convention used by
// several other panels.
func (m PickerModel) HandleMouse(msg tea.MouseMsg) (PickerModel, tea.Cmd) {
	if !m.visible {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.moveCursor(-pickerMouseWheelStep)
	case tea.MouseButtonWheelDown:
		m.moveCursor(pickerMouseWheelStep)
	}
	return m, nil
}

func (m PickerModel) View(width int) string {
	if !m.visible {
		return ""
	}
	_ = width // Dialog auto-sizes like StatusPicker; overlay() clamps position.
	var b strings.Builder

	title := " " + m.req.Verb
	if title == " " {
		title = " Select"
	}
	b.WriteString(searchHeaderStyle.Render(title))
	b.WriteString("\n\n")

	if m.req.ShowFilter {
		shown := m.filter
		if shown == "" {
			shown = helpDescStyle.Render("type to filter")
		}
		b.WriteString("  " + shown + "\n\n")
	}

	if len(m.filtered) == 0 {
		b.WriteString("  " + helpDescStyle.Render("No matches.") + "\n\n")
		b.WriteString(helpDescStyle.Render("  Esc=cancel"))
		return dialogStyle.Render(b.String())
	}

	if m.scroll > 0 {
		b.WriteString(helpDescStyle.Render("  ↑ more") + "\n")
	}
	end := m.scroll + pickerVisibleRows
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	for i := m.scroll; i < end; i++ {
		it := m.filtered[i]
		line := "    " + it.Primary
		if it.Secondary != "" {
			line += "  (" + it.Secondary + ")"
		}
		if i == m.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(m.filtered) {
		b.WriteString(helpDescStyle.Render("  ↓ more") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  ↑/↓=navigate  Enter=select  Esc=cancel"))
	return dialogStyle.Render(b.String())
}
