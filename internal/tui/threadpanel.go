package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var threadPanelStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#875FFF"))

// replyIndent is the thread view's indent unit: three columns per depth
// level. The arrow sits in the metadata gray so it reads as structure.
const replyIndent = "   "

// indentFor is the left margin of a row at depth: depth 0 is the view's
// top — the root of a top-level view, or ".." in a nested one.
func indentFor(depth int) string { return strings.Repeat(replyIndent, depth) }

// maxThreadDepth is how many levels below the view's root render inline.
// Three keeps the indent legible without guide lines and covers ordinary
// back-and-forth (A, B, A, B is depth three); deeper rows are reached by
// re-rooting on the row at the cap, which shows their count. Tunable; a
// setting only if it proves contentious on real threads.
const maxThreadDepth = 3

// ThreadPanelModel shows a thread as a tree: the root and its replies,
// depth-first (each reply followed by its own replies, siblings in time
// order), maxThreadDepth levels below the root. Deeper rows are hidden, not
// flattened — a row at the cap with descendants gets a gray `↳ N more
// replies` stub one level below it, where the first hidden reply would
// sit, and `t` re-roots the view there for fresh levels; a `..` row at the top
// of a nested view opens the parent's thread (the file picker's `../`
// idiom) on `t` or Enter. Enter on a message closes the view and goes to
// that message in the pane. `r` replies to and `e` reacts on the
// highlighted message at any depth; both close the view first (see
// Update). Esc/q close.
//
// Depth drives the indent, three columns per level, and an arrow marks
// every row below the top, so the root needs no label: the one row without
// an arrow is the root. In a nested view the root sits under `..` with an
// arrow of its own (it is a reply to what `..` stands for) and its subtree
// steps in from there, so arrows never line up across levels. The cap
// counts from the view's root, not from `..`.
//
// Scrolling follows the pane: the window position (top) is independent of
// the cursor. Up/down move the cursor and pull the window only at its
// edges; PageUp/PageDown scroll the window a screen without moving the
// cursor; Home scrolls to the top; End puts the cursor on the last row at
// the bottom. Bodies wrap to the width left after the indent, with the
// indent on every line, exactly as the pane wraps.
type ThreadPanelModel struct {
	visible     bool
	rootID      string           // message ID of the thread root
	messages    []DisplayMessage // the root's subtree, depth-first, root first, cut at maxThreadDepth
	depths      []int            // depths[i] is messages[i]'s depth below the root (0 = root)
	hidden      []int            // hidden[i] is how many descendants of messages[i] the cap cut
	hiddenTotal int              // sum of hidden; the header counts them as replies
	cursor      int              // ROW index: 0 is the ".." row when hasParent, else the root

	// top is the first visible row — the window position, independent of
	// the cursor and persisted across renders, as the pane's viewport
	// offset is. Keys move it only when the cursor would leave the window,
	// or on the page/Home/End keys; the renderer never stores it.
	top int
	// followCursor tells the renderer to pull the window to the cursor
	// (Show, Select and the cursor keys set it; the renderer cannot store
	// the result). The page keys and Home clear it: they scroll the window
	// deliberately and the cursor may leave it — the pane's rule.
	followCursor bool
	// width/height are the last size the App handed us (SetSize) so the
	// key handler can lay rows out: the window needs row heights, and
	// heights need the width. Zero until the first key.
	width, height int

	// conversation names where the thread lives, for the header — "in
	// general", "with Bob" — because the view is full-screen and hides the
	// pane's title. Set by the App when it opens the view; "" leaves the
	// header as "Thread (N replies)".
	conversation string
	// notice is a one-key confirmation shown in place of the footer —
	// "copied link" — because the status bar is hidden behind the
	// full-screen view. Cleared by the next key.
	notice string

	resolveName func(string) string // user ID → display name, for reactor lists (set by App)
	selfUserID  string              // the local user's ID; own name green, own reactions bold, listed as "you" (set by App)
	editableID  string              // the one message the user may edit right now, or "" (set by App, as the pane's); drives the a=edit hint and the a key
	currentUser string              // the viewer's display name, for @mention highlighting (set by App, as the pane's)
	retired     map[string]bool     // retired senders get the pane's "[retired]" marker (shared map, set by App at Show/Refresh)

	replyCounts map[string]int    // direct-reply count per message ID, from the full list at Show/Refresh
	parentOf    map[string]string // message ID → ReplyTo, from the full list; lets Select climb out of the hidden part

	// Parent context when the root is itself a reply AND its parent is in
	// the loaded list — drives the selectable ".." row (bare, like a file
	// picker's "../": the content above is one keypress away).
	hasParent bool
	parentID  string
}

// SetRetired shares the message pane's retired-user set so the thread view
// marks retired senders exactly as the pane does. The App passes the pane's
// map at Show and Refresh time; it is read-only here.
func (t *ThreadPanelModel) SetRetired(retired map[string]bool) {
	t.retired = retired
}

// nameStyleFor is the thread view's call to senderNameStyle — the single
// rule for sender names across the UI.
func (t *ThreadPanelModel) nameStyleFor(fromID string) lipgloss.Style {
	return senderNameStyle(fromID, t.selfUserID)
}

// SetResolveName points the panel's reactor-name resolver at the client's
// display-name lookup. Wired by App alongside the message pane's resolver.
func (t *ThreadPanelModel) SetResolveName(resolve func(string) string) {
	t.resolveName = resolve
}

// SetSelfUserID records the local user's ID so their own reactions render
// bold and appear as "you" in reactor lists. Called from the same three
// sites as the sidebar's and the message pane's setters.
func (t *ThreadPanelModel) SetSelfUserID(id string) {
	t.selfUserID = id
}

// SetEditableMessageID mirrors the pane's setter: the one message the user
// may edit right now, or "", for the `a=edit` row hint and the `a` key.
func (t *ThreadPanelModel) SetEditableMessageID(id string) {
	t.editableID = id
}

// SetCurrentUser records the viewer's display name so @mentions of them
// highlight in bodies as they do in the pane. Set wherever the pane's is.
func (t *ThreadPanelModel) SetCurrentUser(name string) {
	t.currentUser = name
}

// SetConversation names the conversation the thread lives in, as the
// header shows it after "Thread": "in general" for a room or group, "with
// Bob" for a DM. The App sets it when it opens the view.
func (t *ThreadPanelModel) SetConversation(label string) {
	t.conversation = label
}

// SetSize records the terminal size the view is drawn at, so the key
// handler can lay rows out and keep the window right. The App calls it
// before routing a key here (the emoji picker's SetViewport pattern).
func (t *ThreadPanelModel) SetSize(width, height int) {
	t.width, t.height = width, height
}

func (t *ThreadPanelModel) IsVisible() bool {
	return t.visible
}

// rowOffset is 1 when the ".." row occupies row 0.
func (t ThreadPanelModel) rowOffset() int {
	if t.hasParent {
		return 1
	}
	return 0
}

// rowCount is the number of selectable rows: the optional ".." row plus
// the root and its replies.
func (t ThreadPanelModel) rowCount() int {
	return len(t.messages) + t.rowOffset()
}

// msgAt maps a row to a message index (0 = root); ok is false for the ".."
// row and out-of-range rows.
func (t ThreadPanelModel) msgAt(row int) (int, bool) {
	idx := row - t.rowOffset()
	if idx < 0 || idx >= len(t.messages) {
		return 0, false
	}
	return idx, true
}

// Show opens the thread panel for a given message: the root's subtree
// depth-first to the cap, the count hidden under each row at the cap, a
// direct-reply count per message, and the parent context when the root is
// itself a reply. A root that is not in the list is refused — the panel
// keeps whatever it was showing, never a blank view — and Show reports
// false so the caller does not go on to Select in the old view.
func (t *ThreadPanelModel) Show(rootID string, allMessages []DisplayMessage) bool {
	rootIdx := -1
	for i := range allMessages {
		if allMessages[i].ID == rootID {
			rootIdx = i
			break
		}
	}
	if rootIdx < 0 {
		return false
	}
	root := &allMessages[rootIdx]

	// Children index over the full list, in list (chronological) order, the
	// direct-reply counts openTarget uses, and the parent map Select climbs.
	children := make(map[string][]int)
	counts := make(map[string]int)
	parentOf := make(map[string]string)
	for i, msg := range allMessages {
		if msg.ReplyTo != "" {
			children[msg.ReplyTo] = append(children[msg.ReplyTo], i)
			counts[msg.ReplyTo]++
			parentOf[msg.ID] = msg.ReplyTo
		}
	}

	// Depth-first walk from the root, cut at maxThreadDepth: a row at the
	// cap claims its whole hidden subtree as a count instead of rows. The
	// visited set guards against a malformed reply chain looping — IDs are
	// server-authored, but the view must terminate regardless.
	var messages []DisplayMessage
	var depths, hidden []int
	hiddenTotal := 0
	visited := map[string]bool{rootID: true}
	var walk func(idx, depth int)
	walk = func(idx, depth int) {
		m := allMessages[idx]
		at := len(messages)
		messages = append(messages, m)
		depths = append(depths, depth)
		hidden = append(hidden, 0)
		if depth >= maxThreadDepth {
			hidden[at] = countDescendants(m.ID, children, allMessages, visited)
			hiddenTotal += hidden[at]
			return
		}
		for _, c := range children[m.ID] {
			if id := allMessages[c].ID; !visited[id] {
				visited[id] = true
				walk(c, depth+1)
			}
		}
	}
	walk(rootIdx, 0)

	t.hasParent, t.parentID = false, ""
	if root.ReplyTo != "" {
		for i := range allMessages {
			if allMessages[i].ID == root.ReplyTo {
				t.hasParent, t.parentID = true, root.ReplyTo
				break
			}
		}
	}

	t.rootID = rootID
	t.messages = messages
	t.depths = depths
	t.hidden = hidden
	t.hiddenTotal = hiddenTotal
	t.replyCounts = counts
	t.parentOf = parentOf
	t.cursor = t.rowOffset() // start on the root, not on ".."
	t.top = 0
	t.followCursor = true
	t.visible = true
	return true
}

// Select moves the cursor to the row showing message id. When that message
// is hidden below the cap, the cursor goes to its nearest ancestor that is
// a row — the capped row whose count covers it — so one `t` there reaches
// it. An id outside this tree leaves the cursor alone. Called after Show:
// from the pane, to land on the message `t` was pressed on; after a
// drill-down or `..`, on the new root. The window follows on the next
// render (the renderer keeps the cursor visible without storing anything).
func (t *ThreadPanelModel) Select(id string) {
	visited := map[string]bool{}
	for id != "" && !visited[id] {
		visited[id] = true
		for i := range t.messages {
			if t.messages[i].ID == id {
				t.cursor = i + t.rowOffset()
				t.followCursor = true
				return
			}
		}
		id = t.parentOf[id]
	}
}

// countDescendants counts every reply under id, recursively, marking each
// visited so a row at the cap claims a descendant once and a loop cannot
// recur.
func countDescendants(id string, children map[string][]int, all []DisplayMessage, visited map[string]bool) int {
	n := 0
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[cur] {
			cid := all[c].ID
			if visited[cid] {
				continue
			}
			visited[cid] = true
			n++
			stack = append(stack, cid)
		}
	}
	return n
}

// moreRepliesStub is the placeholder for the replies hidden beneath a row
// at the cap, drawn where the first hidden child would sit — one level
// deeper, under the same gray arrow every reply has — so the reader sees
// WHERE the missing replies are. (A count on the capped row's own header
// read as that row's count, or as "one more after this".) The selection
// hint lands on this line, so it reads as "press t to see these".
func moreRepliesStub(depth, n int) string {
	// "↳ N more replies ↓": the ↳ says a reply lives here, the trailing ↓
	// says the thread continues below the cap — the same glyph the pickers
	// use for "more below the fold", in the same arrow gray. A passive cue:
	// the t=thread hint shows only while the row is highlighted.
	return indentFor(depth) + helpDescStyle.Render("↳") + " " +
		timestampStyle.Render(fmt.Sprintf("%d more %s", n, pluralize(n, "reply", "replies"))) +
		" " + helpDescStyle.Render("↓")
}

// Refresh re-collects the open thread from the current message list so a
// reaction or reply that arrives while the panel is up shows without
// closing and reopening it. The cursor stays on the same MESSAGE (by ID —
// a reply arriving above it shifts every row below by one, and a row index
// would land on a different message), or on ".." if it was there; the
// window position is kept, bounded. No-op when the panel is hidden.
func (t *ThreadPanelModel) Refresh(allMessages []DisplayMessage) {
	if !t.visible {
		return
	}
	onParent := t.hasParent && t.cursor == 0
	id := ""
	if idx, ok := t.msgAt(t.cursor); ok {
		id = t.messages[idx].ID
	}
	top := t.top
	t.Show(t.rootID, allMessages)
	if n := t.rowCount(); top < n {
		t.top = top
	} else if n > 0 {
		t.top = n - 1
	}
	switch {
	case onParent && t.hasParent:
		t.cursor = 0
	case id != "":
		t.Select(id)
	}
}

func (t *ThreadPanelModel) Hide() {
	t.visible = false
}

// openTarget returns the root ID of the thread the highlighted row points
// to: the parent for the ".." row, a reply's own ID when it has replies;
// "" when the row opens nothing.
func (t ThreadPanelModel) openTarget() string {
	if t.hasParent && t.cursor == 0 {
		return t.parentID
	}
	idx, ok := t.msgAt(t.cursor)
	if !ok || idx == 0 {
		return ""
	}
	if t.replyCounts[t.messages[idx].ID] > 0 {
		return t.messages[idx].ID
	}
	return ""
}

// openHighlighted emits the "thread" action for the row the cursor points
// to — the parent thread on "..", a reply's own thread when it has replies
// — or nothing. The App shows this panel again with the new root: the
// "close and open" of drilling down or going up. Msg names what the App
// selects in the new view, and in both directions that is the new root:
// drilling down, the reply you opened; going up via "..", the parent
// itself — the row under its own "..", or the top. So repeated climbing is
// always `k` then Enter, and the landing row matches a fresh open. (Owner,
// 2026-10-24: landing on the reply you came from left the cursor several
// rows below "..".) Only the ID is needed, so that is all Msg carries.
func (t ThreadPanelModel) openHighlighted() (ThreadPanelModel, tea.Cmd) {
	target := t.openTarget()
	if target == "" {
		return t, nil
	}
	return t, func() tea.Msg {
		return MessageAction{Action: "thread", Msg: DisplayMessage{ID: target}, Data: target}
	}
}

func (t ThreadPanelModel) Update(msg tea.KeyMsg) (ThreadPanelModel, tea.Cmd) {
	t.notice = "" // any key clears the last confirmation
	switch msg.String() {
	case "esc", "q":
		t.Hide()
		return t, nil
	case "c", "y":
		// Copy the highlighted message's text (y: vim muscle memory). The
		// App does the copying; the footer confirms, since the status bar
		// is hidden behind the view.
		idx, ok := t.msgAt(t.cursor)
		if !ok || t.messages[idx].Deleted {
			return t, nil
		}
		target := t.messages[idx]
		t.notice = "copied message text"
		return t, func() tea.Msg {
			return MessageAction{Action: "copy", Msg: target}
		}
	case "C", "Y", "o":
		// Links: C/Y copies, o opens — the attachment first when there is
		// one, as in the pane. One link acts in place with a footer
		// notice; several need the App's picker, an overlay this
		// full-screen view would hide, so the view closes first (as `e`
		// does). Over SSH the browser is on the other machine: the App
		// copies instead, and the notice says so.
		idx, ok := t.msgAt(t.cursor)
		if !ok || t.messages[idx].Deleted {
			return t, nil
		}
		target := t.messages[idx]
		if msg.String() == "o" && len(target.Attachments) > 0 {
			t.notice = "opening attachment"
			return t, func() tea.Msg {
				return MessageAction{Action: "open_attachment", Msg: target}
			}
		}
		links := extractLinks(target.Body)
		if len(links) == 0 {
			return t, nil
		}
		action := "copy_link"
		if msg.String() == "o" {
			action = "open_link"
		}
		if len(links) > 1 {
			t.Hide()
		} else {
			t.notice = linkNotice(action, links[0])
		}
		return t, func() tea.Msg {
			return MessageAction{Action: action, Msg: target}
		}
	case "up", "k":
		if t.cursor > 0 {
			t.cursor--
		}
		t.keepCursorVisible()
	case "down", "j":
		if t.cursor < t.rowCount()-1 {
			t.cursor++
		}
		t.keepCursorVisible()
	case "pageup", "pgup":
		// Pure window scroll, cursor unchanged — the pane's rule. The
		// cursor may leave the window; the next cursor key pulls it back.
		t.pageUp()
	case "pagedown", "pgdown":
		t.pageDown()
	case "home":
		// Pure scroll to the top, cursor unchanged (the pane also probes
		// for older history here; a thread has nothing to load).
		t.top = 0
		t.followCursor = false
	case "end":
		// "Back to the latest": cursor on the last row, window at the
		// bottom — as the pane's End.
		if n := t.rowCount(); n > 0 {
			t.cursor = n - 1
		}
		t.keepCursorVisible()
	case "t":
		// `t` opens: the parent thread on "..", a reply's own thread when
		// it has replies.
		return t.openHighlighted()
	case "enter":
		// Enter goes there: on ".." up to the parent thread, as `t`; on a
		// message, out of the view to that message in the pane — a
		// navigation jump, so the App lands focus on the pane with the
		// cursor on it. The view closes first (it draws full-screen).
		if t.hasParent && t.cursor == 0 {
			return t.openHighlighted()
		}
		idx, ok := t.msgAt(t.cursor)
		if !ok {
			return t, nil
		}
		target := t.messages[idx]
		t.Hide()
		return t, func() tea.Msg {
			return MessageAction{Action: "goto", Msg: target}
		}
	case "r", "e":
		// Reply to, or react on, the highlighted message. The view closes
		// FIRST: it draws full-screen and takes every key, so leaving it
		// open would hide the input's reply line (or the picker) and
		// swallow the typing — the "reply looks broken" symptom. The App's
		// reply/react handlers do the rest once the action arrives: reply
		// context + input focus, or the picker on this exact message. The
		// ".." row is navigation and tombstones take neither key, as in
		// the pane.
		idx, ok := t.msgAt(t.cursor)
		if !ok || t.messages[idx].Deleted {
			return t, nil
		}
		target := t.messages[idx]
		action := "reply"
		if msg.String() == "e" {
			action = "react"
		}
		t.Hide()
		return t, func() tea.Msg {
			return MessageAction{Action: action, Msg: target}
		}
	case "a":
		// Edit the highlighted message when it is the one the server will
		// accept an edit for (the App sets it, as for the pane). Closes
		// first, as r and e do, so the input's edit line is in front.
		idx, ok := t.msgAt(t.cursor)
		if !ok || t.messages[idx].Deleted || t.messages[idx].ID == "" || t.messages[idx].ID != t.editableID {
			return t, nil
		}
		target := t.messages[idx]
		t.Hide()
		return t, func() tea.Msg {
			return MessageAction{Action: "edit", Msg: target}
		}
	}
	return t, nil
}

// threadLayout is the one computation the key handler (window position)
// and the renderer (what to draw) both need: every row rendered at a size,
// with its height, plus the body-row budget.
type threadLayout struct {
	contentWidth, contentHeight int
	footer                      string
	bodyRows                    int
	entries                     []string
	rows                        []int
}

func (t ThreadPanelModel) layout(width, height int) threadLayout {
	// The panel is drawn full-screen at (width × height). lipgloss's Width
	// and Height size the CONTENT box; the rounded border adds one column
	// on each side and one row top and bottom. Content is width-2 ×
	// height-2, and the final render is clamped so it can never exceed the
	// terminal even if an entry wraps.
	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	contentHeight := height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	// One footer. At narrow widths it wraps, and its wrapped height is
	// measured rather than assumed, so a narrow panel never pushes the
	// bottom border off the frame.
	footer := " t=thread  Enter=go to  r=reply  e=react  q/Esc=close"
	footerRows := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(footer))
	// Header is two rows (title + blank); footer is a blank row + its rows.
	bodyRows := contentHeight - 3 - footerRows
	if bodyRows < 1 {
		bodyRows = 1
	}

	// Render every row so the window can budget by ROWS, not by row count
	// — an entry can span several rows (header, wrapped body, one line per
	// attachment and per reaction emoji, the stub).
	n := t.rowCount()
	entries := make([]string, n)
	rows := make([]int, n)
	clamp := lipgloss.NewStyle().MaxWidth(contentWidth)
	for row := 0; row < n; row++ {
		e := clamp.Render(t.renderRow(row, contentWidth))
		entries[row] = e
		rows[row] = strings.Count(e, "\n") + 1
	}
	return threadLayout{contentWidth: contentWidth, contentHeight: contentHeight, footer: footer, bodyRows: bodyRows, entries: entries, rows: rows}
}

// windowStart returns where the window must start to show the cursor,
// moving top the least: up to the cursor's row when it is above the
// window; down until the cursor's last line is the window's last line
// when it is below; a row taller than the budget pins its first line at
// the top. Inside the window, top comes back unchanged — the pane's rule:
// the cursor travels within the window and only pulls it at the edges.
func windowStart(top, cursor int, rows []int, bodyRows int) int {
	n := len(rows)
	if n == 0 {
		return 0
	}
	top = clampInt(top, 0, n-1)
	cursor = clampInt(cursor, 0, n-1)
	if cursor < top {
		return cursor
	}
	if rows[cursor] > bodyRows {
		return cursor
	}
	for sumRows(rows[top:cursor+1]) > bodyRows {
		top++
	}
	return top
}

// lastVisibleRow is the last row that fits entirely in a window starting
// at start; at least start itself, even when it is taller than the budget.
func lastVisibleRow(start int, rows []int, bodyRows int) int {
	used := 0
	last := start
	for i := start; i < len(rows); i++ {
		if used+rows[i] > bodyRows {
			break
		}
		used += rows[i]
		last = i
	}
	return last
}

// bottomStart is the earliest start at which the window still shows the
// last row — where a scroll to the bottom stops, as the pane's viewport
// stops with its last line at the bottom.
func bottomStart(rows []int, bodyRows int) int {
	n := len(rows)
	if n == 0 {
		return 0
	}
	start := n - 1
	used := rows[start]
	for start > 0 && used+rows[start-1] <= bodyRows {
		start--
		used += rows[start]
	}
	return start
}

// keepCursorVisible persists the window position the renderer would use,
// so that the cursor's later moves within it scroll nothing. Without a size
// yet (no key has reached us) it does nothing — the renderer still shows
// the cursor on its own.
func (t *ThreadPanelModel) keepCursorVisible() {
	t.followCursor = true
	if t.width <= 0 || t.height <= 0 {
		return
	}
	l := t.layout(t.width, t.height)
	t.top = windowStart(t.top, t.cursor, l.rows, l.bodyRows)
}

// drawnTop is the window position as the renderer shows it: the persisted
// top, pulled to the cursor only while followCursor is set. The page keys
// start from here, so paging twice pages twice — normalising through
// keepCursorVisible would drag the window back to the cursor first.
func (t ThreadPanelModel) drawnTop(l threadLayout) int {
	n := len(l.rows)
	if n == 0 {
		return 0
	}
	top := clampInt(t.top, 0, n-1)
	if t.followCursor {
		top = windowStart(top, t.cursor, l.rows, l.bodyRows)
	}
	return top
}

// pageDown moves the window one screen down: the new top is the first row
// not fully visible now, stopping where the last row is at the bottom.
func (t *ThreadPanelModel) pageDown() {
	if t.width <= 0 || t.height <= 0 {
		return
	}
	l := t.layout(t.width, t.height)
	if len(l.rows) == 0 {
		return
	}
	t.top = t.drawnTop(l)
	next := lastVisibleRow(t.top, l.rows, l.bodyRows) + 1
	if bottom := bottomStart(l.rows, l.bodyRows); next > bottom {
		next = bottom
	}
	if next > t.top {
		t.top = next
	}
	t.followCursor = false
}

// pageUp moves the window one screen up: the new top is the earliest row
// such that the rows from it up to the current top fill one screen.
func (t *ThreadPanelModel) pageUp() {
	if t.width <= 0 || t.height <= 0 {
		return
	}
	l := t.layout(t.width, t.height)
	if len(l.rows) == 0 {
		return
	}
	t.top = t.drawnTop(l)
	t.followCursor = false
	if t.top == 0 {
		return
	}
	top := t.top
	used := 0
	for top > 0 && used+l.rows[top-1] <= l.bodyRows {
		top--
		used += l.rows[top]
	}
	if top == t.top {
		top-- // the row above alone is taller than a screen: still move
	}
	t.top = top
	t.followCursor = false
}

func (t ThreadPanelModel) View(width, height int) string {
	if !t.visible || len(t.messages) == 0 {
		return ""
	}
	l := t.layout(width, height)
	n := len(l.rows)
	cursor := clampInt(t.cursor, 0, n-1)
	// The window as persisted by the keys. While followCursor is set
	// (Show, Select, the cursor keys) it is pulled to the cursor here —
	// not stored, View is a value receiver — so the cursor is on screen
	// right after Show+Select or a resize. After a page key or Home the
	// window stays where it was put and the cursor may be off screen, as
	// in the pane.
	start := clampInt(t.top, 0, n-1)
	if t.followCursor {
		start = windowStart(t.top, cursor, l.rows, l.bodyRows)
	}

	var b strings.Builder
	replyCount := len(t.messages) - 1 + t.hiddenTotal // the whole subtree, shown and hidden
	header := " Thread"
	if t.conversation != "" {
		header += " " + t.conversation
	}
	header += fmt.Sprintf(" (%d %s)", replyCount, pluralize(replyCount, "reply", "replies"))
	b.WriteString(searchHeaderStyle.Render(header))
	b.WriteString("\n\n")

	// Take whole rows while the budget allows. A single row taller than the
	// budget is cut to the budget so the frame still fits.
	used := 0
	for row := start; row < n; row++ {
		e := l.entries[row]
		r := l.rows[row]
		if used+r > l.bodyRows {
			if row != start {
				break
			}
			lines := strings.Split(e, "\n")
			e = strings.Join(lines[:l.bodyRows], "\n")
			r = l.bodyRows
		}
		b.WriteString(e + "\n")
		used += r
	}

	b.WriteString("\n")
	if t.notice != "" {
		b.WriteString(helpDescStyle.Render(" " + t.notice))
	} else {
		b.WriteString(helpDescStyle.Render(l.footer))
	}

	return threadPanelStyle.
		Width(l.contentWidth).
		Height(l.contentHeight).
		MaxWidth(width).
		MaxHeight(height).
		Render(b.String())
}

// sumRows totals a slice of per-row counts.
func sumRows(rows []int) int {
	n := 0
	for _, r := range rows {
		n += r
	}
	return n
}

// wrapBody renders a message body for the view the way the pane renders
// its own: mentions and links highlighted, then wrapped to the width left
// after the indent — measured in cells, never bytes, so a rune is never
// split and a long token (a URL) breaks rather than overflowing — with the
// indent on every line, including the lines of a multi-line body.
func wrapBody(body, indent string, width int, currentUser string) string {
	w := width - lipgloss.Width(indent)
	if w < 8 {
		w = 8 // at absurd widths the frame clamp cuts; never wrap to nothing
	}
	styled := highlightLinks(highlightMentions(body, currentUser))
	wrapped := lipgloss.NewStyle().Width(w).Render(styled)
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		lines[i] = indent + strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// renderRow renders one selectable row of the view: the ".." parent row,
// the root, or a reply (with its reply count and, when highlighted and it
// has replies, the "t=thread" hint), indented by depth under a gray arrow.
// contentWidth is the panel's inner width.
func (t ThreadPanelModel) renderRow(row, contentWidth int) string {
	if t.hasParent && row == 0 {
		return t.renderParentRow(row, contentWidth)
	}
	idx, ok := t.msgAt(row)
	if !ok {
		return ""
	}
	msg := t.messages[idx]

	// Depth: the view's top row is 0 — the root of a top-level view, or
	// ".." in a nested one, where the root is a reply to it and sits at 1.
	// Each reply is one level below its parent. Every row below depth 0
	// gets the arrow; the root, having none, needs no label.
	depth := t.rowOffset() + t.depths[idx]
	indent := indentFor(depth)
	arrow := ""
	if depth > 0 {
		// Arrow in the metadata gray: structure, not content.
		arrow = indent + helpDescStyle.Render("↳")
	}

	var parts []string
	if msg.Deleted {
		tombstone := "message deleted"
		if msg.DeletedBy != "" && msg.DeletedBy != msg.FromID {
			tombstone = "message removed by " + msg.DeletedBy
		}
		parts = append(parts, arrow+systemMsgStyle.Render(" ── "+tombstone+" ──"))
	} else {
		ts := time.Unix(msg.TS, 0).Format("3:04 PM")
		// Same sender-name rule as the pane: your own replies read green
		// in a long thread; retired senders carry the pane's marker.
		from := t.nameStyleFor(msg.FromID).Render(msg.From)
		if t.retired[msg.FromID] {
			from += " " + helpDescStyle.Render("[retired]")
		}
		header := arrow + " " + from + "  " + timestampStyle.Render(ts)
		if msg.EditedAt > 0 {
			header += " " + helpDescStyle.Render("(edited)")
		}
		parts = append(parts, header)

		// Body, attachments and reaction rows align under the name: one
		// column in at depth 0, two past the arrow below it. The band is
		// two columns narrower than the content, so the body wraps to that.
		bodyIndent := " "
		if depth > 0 {
			bodyIndent = indent + "  "
		}
		if msg.Body != "" {
			parts = append(parts, wrapBody(msg.Body, bodyIndent, contentWidth-2, t.currentUser))
		}
		// Attachments as the pane shows them: a glyph and the size (the
		// file name is the body on uploads from this client). Opening and
		// saving live in the pane — Enter goes there.
		for _, att := range msg.Attachments {
			glyph := filePlaceholderGlyph
			if att.IsImage {
				glyph = imagePlaceholderGlyph
			}
			parts = append(parts, bodyIndent+fmt.Sprintf("%s (%s)", glyph, formatSize(att.Size)))
		}
		// Reactions in full: this is the detail view, so every reactor is
		// listed — chips in the accent purple, names in the metadata gray,
		// one emoji per line, never truncated (the pane truncates and
		// points here with "t=thread").
		if rparts, _ := reactionParts(&msg, t.selfUserID, t.resolveName, true, 0); len(rparts) > 0 {
			for _, p := range rparts {
				parts = append(parts, bodyIndent+"  "+p)
			}
		}
	}

	// Hidden replies beneath a row at the cap: the stub sits where the
	// first hidden child would, as the last line of this row's block (so
	// the selection hint below attaches to it). Rows whose children are
	// visible get nothing — the children are right there. A deleted row at
	// the cap keeps its stub, so the conversation beneath it is not lost.
	if c := t.hidden[idx]; c > 0 {
		parts = append(parts, moreRepliesStub(depth+1, c))
	}
	line := strings.Join(parts, "\n")

	if row == t.cursor {
		// `a=edit` on the one editable message, first in the chain as in
		// the pane.
		if msg.ID != "" && msg.ID == t.editableID {
			line += timestampStyle.Render("  a=edit")
		}
		if idx != 0 && t.replyCounts[msg.ID] > 0 {
			// Same hint tier as the pane: only on the highlighted row, at
			// the end of its block. The hint follows what `t` does — it
			// shows on any reply that has replies, whether they are
			// visible below it or hidden by the cap (the count, by
			// contrast, marks hidden rows only). The root takes no hint:
			// re-rooting on it is a no-op.
			line += timestampStyle.Render("  t=thread")
		}
		// Band two columns narrower than the content so a sliver of panel
		// shows at the right edge.
		line = renderSelected(selectedMsgStyle, contentWidth-2, line)
	}

	return line
}

// renderParentRow renders the ".." row of a nested view: the way up, bare,
// in the metadata gray — the file picker's "../". No arrow and no preview
// of the parent: static text above the root pulls the eye off the content,
// and the parent is one keypress away. The selection-only hint says where
// the row leads; the keys are already in the footer. The band applies like
// any other row.
func (t ThreadPanelModel) renderParentRow(row, contentWidth int) string {
	line := helpDescStyle.Render(" ..")
	if row == t.cursor {
		line += timestampStyle.Render("  go up one level")
		line = renderSelected(selectedMsgStyle, contentWidth-2, line)
	}
	return line
}

// linkNotice is the footer confirmation for a link action on one link:
// what happened, and to which link (shortened to its host and path start).
func linkNotice(action, link string) string {
	short := strings.TrimPrefix(strings.TrimPrefix(link, "https://"), "http://")
	if lipgloss.Width(short) > 40 {
		short = short[:37] + "..."
	}
	switch {
	case action == "open_link" && remoteSession():
		return "remote session: link copied, open it locally"
	case action == "open_link":
		return "opening " + short
	default:
		return "copied link " + short
	}
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
