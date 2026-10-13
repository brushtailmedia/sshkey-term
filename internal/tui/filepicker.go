package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// FilePickerModel is the keyboard-only tree-explorer picker behind bare
// `/upload` / `/u` (spec: docs/planning/open/upload-file-picker.md). Unlike
// PickerModel it OWNS directory state and performs I/O — exactly one
// os.ReadDir per expand, cached for the life of one Show. Sizing follows
// the emoji picker (dynamic body rows, pinned shell); key handling follows
// PickerModel's plain-string filter. No mouse handling by design: App's
// keyboard-only consume list eats mouse events while this is visible.
type FilePickerModel struct {
	visible bool
	req     FilePickerRequest

	root     string               // absolute; survives Hide (G2 session memory)
	expanded map[string]bool      // absolute dir → expanded
	children map[string][]fpEntry // absolute dir → listing (the ONE ReadDir)
	errs     map[string]error     // absolute dir → ReadDir error (placeholder row)

	filter string
	rows   []fpRow // flattened + filtered render rows; rebuilt on every change
	cursor int
	scroll int

	bodyRows     int
	contentWidth int
	viewPortW    int
	viewPortH    int

	// Same defensive state machine as PickerModel (picker.go): some
	// terminal/tmux setups leak SGR mouse bytes as KeyRunes; a widget with a
	// text filter must drop them or scroll gestures become filter text.
	mouseSeqState int
	mouseSeqBuf   []rune
}

// FilePickerRequest is the context captured at open and echoed back in
// FilePickerSelectedMsg. App owns all routing. Gen is a.connGen at open:
// a selection that arrives under a different connection generation is
// dropped by App, so a picked file can never be sent to a server other
// than the one the user was looking at when the picker opened (same
// stale-generation rule UploadResultEvent already uses).
type FilePickerRequest struct {
	Room, Group, DM string
	Title           string
	Gen             uint64
}

// FilePickerSelectedMsg is emitted on Enter-on-file.
type FilePickerSelectedMsg struct {
	Request FilePickerRequest
	Path    string
}

type fpEntry struct {
	name  string
	path  string // absolute
	isDir bool   // after the G5 follow-stat
	size  int64  // -1 = unknown (G6 cap)
}

type fpRowKind int

const (
	fpRowParent      fpRowKind = iota // "../"
	fpRowDir                          // chevron + name/
	fpRowFile                         // name  (size)
	fpRowPlaceholder                  // "(empty)" / "(permission denied)": focusable, inert
)

type fpRow struct {
	kind   fpRowKind
	entry  fpEntry
	depth  int
	label  string // placeholder text
	parent string // containing dir (absolute); "" for the parent row
}

const (
	filePickerMinBodyRows     = 4
	filePickerMaxBodyRows     = 20
	filePickerChromeRows      = 14 // title, blanks, breadcrumb, filter, up to two indicators, footer, border+padding
	filePickerMinContentWidth = 30
	filePickerMaxContentWidth = 74
)

// filePickerStatCap is GATE G6; a var so tests can lower it.
var filePickerStatCap = 500

// Show loads the root listing and opens the dialog. An unreadable root
// returns the error and leaves the widget hidden (GATE G9 / rule #10).
func (m *FilePickerModel) Show(req FilePickerRequest, root string) error {
	m.expanded = map[string]bool{}
	m.children = map[string][]fpEntry{}
	m.errs = map[string]error{}
	m.filter = ""
	m.cursor, m.scroll = 0, 0
	m.mouseSeqState, m.mouseSeqBuf = 0, nil
	if m.bodyRows == 0 {
		m.SetViewport(0, 0) // defaults until App sizes us
	}
	if err := m.loadDir(root); err != nil {
		return err
	}
	m.req = req
	m.root = root
	m.visible = true
	m.rebuild()
	return nil
}

// Hide closes the dialog and drops every cached listing (the next Show
// re-reads — a file added between opens must appear). `root` is kept on
// purpose: GATE G2 session memory via LastRoot().
func (m *FilePickerModel) Hide() {
	m.visible = false
	m.req = FilePickerRequest{}
	m.expanded, m.children, m.errs = nil, nil, nil
	m.filter = ""
	m.rows = nil
	m.cursor, m.scroll = 0, 0
	m.mouseSeqState, m.mouseSeqBuf = 0, nil
}

func (m *FilePickerModel) IsVisible() bool  { return m.visible }
func (m *FilePickerModel) LastRoot() string { return m.root }

// SetViewport mirrors EmojiPickerModel.configure: derive content width and
// body rows from the messages-pane box. App calls it in the key intercept
// before every Update and on a render copy in View.
func (m *FilePickerModel) SetViewport(width, height int) {
	if width <= 0 {
		width = filePickerMaxContentWidth + 8
	}
	if height <= 0 {
		height = filePickerMaxBodyRows + filePickerChromeRows
	}
	cw := maxInt(12, width-8)
	if cw > filePickerMaxContentWidth {
		cw = filePickerMaxContentWidth
	}
	if cw < filePickerMinContentWidth && width >= filePickerMinContentWidth+8 {
		cw = filePickerMinContentWidth
	}
	br := height - filePickerChromeRows
	if br < filePickerMinBodyRows {
		br = filePickerMinBodyRows
	}
	if br > filePickerMaxBodyRows {
		br = filePickerMaxBodyRows
	}
	m.contentWidth, m.bodyRows, m.viewPortW, m.viewPortH = cw, br, width, height
	m.clampScroll()
}

// loadDir performs THE ONE ReadDir for dir and caches the classified,
// sorted listing (G1 hidden-file skip, G5 follow-stat, G6 cap, G7 sort).
// A failed read is cached too (rendered as a placeholder) and returned so
// Show can refuse an unreadable root.
func (m *FilePickerModel) loadDir(dir string) error {
	if _, seen := m.children[dir]; seen {
		return m.errs[dir]
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		m.children[dir] = nil
		m.errs[dir] = err
		return err
	}
	doStat := len(ents) <= filePickerStatCap
	out := make([]fpEntry, 0, len(ents))
	for _, de := range ents {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		e := fpEntry{name: name, path: filepath.Join(dir, name), isDir: de.IsDir(), size: -1}
		if doStat {
			if fi, err := os.Stat(e.path); err == nil { // follows symlinks (G5)
				e.isDir = fi.IsDir()
				if !e.isDir {
					e.size = fi.Size()
				}
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir {
			return out[i].isDir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	m.children[dir] = out
	return nil
}

// rebuild re-flattens the tree into render rows, applies the filter and
// re-clamps cursor/scroll — the scroll invariant. Call after EVERY change.
func (m *FilePickerModel) rebuild() {
	needle := strings.ToLower(m.filter)
	var rows []fpRow
	if needle == "" && filepath.Dir(m.root) != m.root { // no "../" at the fs root
		rows = append(rows, fpRow{kind: fpRowParent})
	}
	rows, _ = m.flattenDir(rows, m.root, 0, needle)
	m.rows = rows
	m.clampCursor()
}

// flattenDir appends dir's rows. With a non-empty needle a row survives
// only if its name matches or (dirs) any LOADED descendant matches, so
// ancestors of matches stay visible; collapsed dirs are never descended
// (loaded-rows-only scope). Placeholders render only when unfiltered.
func (m *FilePickerModel) flattenDir(rows []fpRow, dir string, depth int, needle string) ([]fpRow, bool) {
	if needle == "" {
		if m.errs[dir] != nil {
			return append(rows, fpRow{kind: fpRowPlaceholder, depth: depth, label: "(permission denied)", parent: dir}), false
		}
		if len(m.children[dir]) == 0 {
			return append(rows, fpRow{kind: fpRowPlaceholder, depth: depth, label: "(empty)", parent: dir}), false
		}
	}
	matched := false
	for _, e := range m.children[dir] {
		match := needle == "" || strings.Contains(strings.ToLower(e.name), needle)
		switch {
		case e.isDir && m.expanded[e.path]:
			sub, subMatch := m.flattenDir(nil, e.path, depth+1, needle)
			if match || subMatch {
				rows = append(rows, fpRow{kind: fpRowDir, entry: e, depth: depth, parent: dir})
				rows = append(rows, sub...)
				matched = true
			}
		case match:
			kind := fpRowFile
			if e.isDir {
				kind = fpRowDir
			}
			rows = append(rows, fpRow{kind: kind, entry: e, depth: depth, parent: dir})
			matched = true
		}
	}
	return rows, matched
}

func (m *FilePickerModel) clampCursor() {
	n := len(m.rows)
	if n == 0 {
		m.cursor, m.scroll = 0, 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.clampScroll()
}

// clampScroll keeps the cursor inside the body window AND the window
// inside the row slice (so a collapse never leaves blank rows).
func (m *FilePickerModel) clampScroll() {
	rows := m.bodyRows
	if rows < 1 {
		rows = 1
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
	if max := len(m.rows) - rows; m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *FilePickerModel) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
}

func (m *FilePickerModel) setFilter(f string) {
	m.filter = f
	m.cursor, m.scroll = 0, 0 // PickerModel.applyFilter resets too
	m.rebuild()
}

func (m *FilePickerModel) toggle(dir string) {
	if m.expanded[dir] {
		delete(m.expanded, dir)
	} else {
		m.expanded[dir] = true
		_ = m.loadDir(dir) // failure renders as "(permission denied)"
	}
	m.rebuild() // cursor index unchanged: expand-at-bottom keeps the cursor stationary
}

func (m *FilePickerModel) expandCursor() {
	if m.cursor < len(m.rows) {
		if r := m.rows[m.cursor]; r.kind == fpRowDir && !m.expanded[r.entry.path] {
			m.toggle(r.entry.path)
		}
	}
}

// collapseOrJump is VS Code's ←: collapse an expanded dir, else jump the
// cursor to the containing dir's row (no-op for top-level rows and for the
// inert placeholder rows).
func (m *FilePickerModel) collapseOrJump() {
	if m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	if r.kind == fpRowPlaceholder || r.kind == fpRowParent {
		return
	}
	if r.kind == fpRowDir && m.expanded[r.entry.path] {
		m.toggle(r.entry.path)
		return
	}
	if r.parent == "" || r.parent == m.root {
		return
	}
	for i, cand := range m.rows {
		if cand.kind == fpRowDir && cand.entry.path == r.parent {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
}

// rerootUp is Enter on "../": the parent becomes root, the old root stays
// expanded so its loaded subtree nests intact, and the cursor lands on it.
func (m *FilePickerModel) rerootUp() {
	old := m.root
	parent := filepath.Dir(old)
	if parent == old {
		return
	}
	m.root = parent
	m.expanded[old] = true
	_ = m.loadDir(parent) // GATE G9: an unreadable parent renders a placeholder; "../" still works
	m.rebuild()
	for i, r := range m.rows {
		if r.kind == fpRowDir && r.entry.path == old {
			m.cursor = i
			break
		}
	}
	m.clampScroll()
}

func (m FilePickerModel) activate() (FilePickerModel, tea.Cmd) {
	if m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	switch r.kind {
	case fpRowParent:
		m.rerootUp()
	case fpRowDir:
		m.toggle(r.entry.path)
	case fpRowFile:
		req, path := m.req, r.entry.path
		m.Hide() // Hide FIRST, then emit — PickerModel's shape
		return m, func() tea.Msg { return FilePickerSelectedMsg{Request: req, Path: path} }
	}
	return m, nil
}

func (m FilePickerModel) Update(msg tea.KeyMsg) (FilePickerModel, tea.Cmd) {
	// Leak defense first, unconditional (PickerModel gates on ShowFilter;
	// this filter is always on).
	if msg.Type == tea.KeyRunes {
		msg.Runes = m.filterLeakedMouseRunes(msg.Runes)
		if len(msg.Runes) == 0 {
			return m, nil
		}
	} else if m.mouseSeqState != 0 {
		m.flushHeldMouseRunesToFilter()
		m.rebuild()
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.Hide() // App also handles Esc above us; keep both (house pattern)
	case tea.KeyUp:
		m.moveCursor(-1)
	case tea.KeyDown:
		m.moveCursor(1)
	case tea.KeyPgUp:
		m.moveCursor(-m.bodyRows)
	case tea.KeyPgDown:
		m.moveCursor(m.bodyRows)
	case tea.KeyHome:
		m.cursor = 0
		m.clampCursor()
	case tea.KeyEnd:
		m.cursor = len(m.rows) - 1
		m.clampCursor()
	case tea.KeyRight:
		m.expandCursor()
	case tea.KeyLeft:
		m.collapseOrJump()
	case tea.KeyEnter:
		return m.activate()
	case tea.KeyBackspace:
		if m.filter != "" {
			r := []rune(m.filter)
			m.setFilter(string(r[:len(r)-1]))
		} // empty filter: Backspace is a no-op — it NEVER ascends
	case tea.KeySpace:
		m.setFilter(m.filter + " ")
	case tea.KeyRunes:
		m.setFilter(m.filter + string(msg.Runes))
	}
	return m, nil
}

// View renders the pinned shell around a bodyRows-tall window of rows.
func (m FilePickerModel) View() string {
	if !m.visible {
		return ""
	}
	textWidth := m.contentWidth - 4 // inside the dialog's horizontal padding
	if textWidth < 8 {
		textWidth = 8
	}
	var b strings.Builder
	title := m.req.Title
	if title == "" {
		title = "Upload file"
	}
	b.WriteString(searchHeaderStyle.Render(" " + truncateRunes(title, textWidth-1)))
	b.WriteString("\n\n")
	b.WriteString("  " + m.breadcrumb(textWidth-2) + "\n")
	if m.filter == "" {
		b.WriteString("  " + helpDescStyle.Render("type to filter visible") + "\n\n")
	} else {
		b.WriteString("  " + tailRunes(m.filter, textWidth-2) + "\n\n")
	}
	end := m.scroll + m.bodyRows
	if end > len(m.rows) {
		end = len(m.rows)
	}
	if m.scroll > 0 {
		b.WriteString(helpDescStyle.Render("  ↑ more") + "\n")
	}
	for i := m.scroll; i < end; i++ {
		line := m.renderRow(m.rows[i], textWidth)
		if i == m.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(m.rows) {
		b.WriteString(helpDescStyle.Render("  ↓ more") + "\n")
	}
	b.WriteString("\n")
	if textWidth >= 38 {
		b.WriteString(helpDescStyle.Render("  ↑/↓=navigate  →=expand  ←=collapse") + "\n")
		b.WriteString(helpDescStyle.Render("  Enter=open/upload  Esc=cancel"))
	} else {
		// Narrow terminals: one compact footer line so nothing wraps.
		b.WriteString(helpDescStyle.Render("  ↑↓ move  →← fold  Enter  Esc"))
	}
	return dialogStyle.Width(m.contentWidth).Render(b.String())
}

func (m FilePickerModel) renderRow(r fpRow, textWidth int) string {
	indent := strings.Repeat("  ", r.depth)
	switch r.kind {
	case fpRowParent:
		return "    " + indent + "../"
	case fpRowPlaceholder:
		return "    " + indent + helpDescStyle.Render(r.label)
	case fpRowDir:
		chev := "▸ "
		if m.expanded[r.entry.path] {
			chev = "▾ "
		}
		avail := textWidth - 4 - len(indent) - 1 // "  " + chevron, trailing "/"
		return "  " + indent + chev + truncateRunes(r.entry.name, avail) + "/"
	default:
		size := ""
		if r.entry.size >= 0 {
			size = "  (" + formatSize(r.entry.size) + ")"
		}
		avail := textWidth - 4 - len(indent) - lipgloss.Width(size) // GATE G8: suffix always kept
		return "    " + indent + truncateRunes(r.entry.name, avail) + size
	}
}

// breadcrumb shows the root with $HOME folded to "~" and, in a remote
// session, a user@host: prefix (same SSH_CLIENT/SSH_TTY detection as the
// typed route's hint). Over-long paths are trimmed from the LEFT.
func (m FilePickerModel) breadcrumb(width int) string {
	p := m.root
	// Home comes from the centralized path helper (path-centralization: no
	// direct os.UserHomeDir outside internal/config); "~" means unresolved.
	if home := config.ExpandUserPath("~"); home != "~" && home != "" {
		if p == home {
			p = "~"
		} else if strings.HasPrefix(p, home+string(os.PathSeparator)) {
			p = "~" + p[len(home):]
		}
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
		host, _ := os.Hostname()
		if u := os.Getenv("USER"); u != "" {
			p = u + "@" + host + ":" + p
		} else {
			p = host + ":" + p
		}
	}
	return tailRunes(p, width)
}

// truncateRunes shortens s to at most max runes, appending "…" when it
// cuts. Guards tiny widths (never slices negative). The existing byte-based
// `truncate` in search.go splits multibyte names mid-rune — do not use it.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(rs[:max-1]) + "…"
}

// tailRunes keeps the LAST max runes of s, prefixing "…" when it cuts.
func tailRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(rs[len(rs)-(max-1):])
}

// filterLeakedMouseRunes / flushHeldMouseRunesToFilter are verbatim copies
// of PickerModel's pair (picker.go): some terminal/tmux setups deliver SGR
// mouse sequences as KeyRunes; this state machine drops a complete
// "[<…M/m" sequence and lets anything else through as typed text.
func (m *FilePickerModel) filterLeakedMouseRunes(runes []rune) []rune {
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

func (m *FilePickerModel) flushHeldMouseRunesToFilter() {
	switch m.mouseSeqState {
	case 1:
		m.filter += "["
	case 2:
		m.filter += "[<" + string(m.mouseSeqBuf)
		m.mouseSeqBuf = m.mouseSeqBuf[:0]
	}
	m.mouseSeqState = 0
}
