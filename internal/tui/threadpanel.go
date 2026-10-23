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
type ThreadPanelModel struct {
	visible     bool
	rootID      string              // message ID of the thread root
	messages    []DisplayMessage    // the root's subtree, depth-first, root first, cut at maxThreadDepth
	depths      []int               // depths[i] is messages[i]'s depth below the root (0 = root)
	hidden      []int               // hidden[i] is how many descendants of messages[i] the cap cut
	hiddenTotal int                 // sum of hidden; the header counts them as replies
	cursor      int                 // ROW index: 0 is the ".." row when hasParent, else the root
	resolveName func(string) string // user ID → display name, for reactor lists (set by App)
	selfUserID  string              // the local user's ID; own name green, own reactions bold, listed as "you" (set by App)
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
	t.visible = true
	return true
}

// Select moves the cursor to the row showing message id. When that message
// is hidden below the cap, the cursor goes to its nearest ancestor that is
// a row — the capped row whose count covers it — so one `t` there reaches
// it. An id outside this tree leaves the cursor alone. Called after Show:
// from the pane, to land on the message `t` was pressed on; after a
// drill-down, on the new root; after `..`, on the row you came from.
func (t *ThreadPanelModel) Select(id string) {
	visited := map[string]bool{}
	for id != "" && !visited[id] {
		visited[id] = true
		for i := range t.messages {
			if t.messages[i].ID == id {
				t.cursor = i + t.rowOffset()
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
	return indentFor(depth) + helpDescStyle.Render("↳") + " " +
		timestampStyle.Render(fmt.Sprintf("%d more %s", n, pluralize(n, "reply", "replies")))
}

// Refresh re-collects the open thread from the current message list so a
// reaction or reply that arrives while the panel is up shows without
// closing and reopening it. Keeps the cursor where it was, bounded. No-op
// when the panel is hidden.
func (t *ThreadPanelModel) Refresh(allMessages []DisplayMessage) {
	if !t.visible {
		return
	}
	cursor := t.cursor
	t.Show(t.rootID, allMessages)
	if cursor < t.rowCount() {
		t.cursor = cursor
	} else if n := t.rowCount(); n > 0 {
		t.cursor = n - 1
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
	switch msg.String() {
	case "esc", "q":
		t.Hide()
		return t, nil
	case "up", "k":
		if t.cursor > 0 {
			t.cursor--
		}
	case "down", "j":
		if t.cursor < t.rowCount()-1 {
			t.cursor++
		}
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
	}
	return t, nil
}

func (t ThreadPanelModel) View(width, height int) string {
	if !t.visible || len(t.messages) == 0 {
		return ""
	}

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
	footer := " t=thread  Enter=go to  r=reply  e=react  Esc/q=close"
	footerRows := lipgloss.Height(lipgloss.NewStyle().Width(contentWidth).Render(footer))
	// Header is two rows (title + blank); footer is a blank row + its rows.
	bodyRows := contentHeight - 3 - footerRows
	if bodyRows < 1 {
		bodyRows = 1
	}

	// Render every row first so the window below can budget by ROWS, not
	// by row count — an entry can span several rows (header, body, one
	// line per reaction emoji).
	n := t.rowCount()
	entries := make([]string, n)
	rows := make([]int, n)
	clamp := lipgloss.NewStyle().MaxWidth(contentWidth)
	for row := 0; row < n; row++ {
		e := clamp.Render(t.renderRow(row, contentWidth))
		entries[row] = e
		rows[row] = strings.Count(e, "\n") + 1
	}

	// Window: drop rows from the top until the cursor's row fits, then take
	// whole rows while the budget allows. A single row taller than the
	// budget is cut to the budget so the frame still fits.
	cursor := t.cursor
	if cursor < 0 || cursor >= n {
		cursor = 0
	}
	start := 0
	for used := sumRows(rows[start : cursor+1]); used > bodyRows && start < cursor; used = sumRows(rows[start : cursor+1]) {
		start++
	}

	var b strings.Builder
	replyCount := len(t.messages) - 1 + t.hiddenTotal // the whole subtree, shown and hidden
	header := fmt.Sprintf(" Thread (%d %s)", replyCount, pluralize(replyCount, "reply", "replies"))
	b.WriteString(searchHeaderStyle.Render(header))
	b.WriteString("\n\n")

	used := 0
	for row := start; row < n; row++ {
		e := entries[row]
		r := rows[row]
		if used+r > bodyRows {
			if row != start {
				break
			}
			lines := strings.Split(e, "\n")
			e = strings.Join(lines[:bodyRows], "\n")
			r = bodyRows
		}
		b.WriteString(e + "\n")
		used += r
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render(footer))

	return threadPanelStyle.
		Width(contentWidth).
		Height(contentHeight).
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

	var line string
	if msg.Deleted {
		tombstone := "message deleted"
		if msg.DeletedBy != "" && msg.DeletedBy != msg.FromID {
			tombstone = "message removed by " + msg.DeletedBy
		}
		line = arrow + systemMsgStyle.Render(" ── "+tombstone+" ──")
	} else {
		ts := time.Unix(msg.TS, 0).Format("3:04 PM")
		// Same sender-name rule as the pane: your own replies read green
		// in a long thread; retired senders carry the pane's marker.
		from := t.nameStyleFor(msg.FromID).Render(msg.From)
		if t.retired[msg.FromID] {
			from += " " + helpDescStyle.Render("[retired]")
		}
		header := arrow + " " + from + "  " + timestampStyle.Render(ts)
		line = header + "\n"

		// Body and reaction rows align under the name: one column in at
		// depth 0, two past the arrow below it.
		bodyIndent := " "
		if depth > 0 {
			bodyIndent = indent + "  "
		}
		body := bodyIndent + msg.Body
		if limit := contentWidth - 2; limit > len(bodyIndent)+3 && len(body) > limit {
			body = body[:limit-3] + "..."
		}
		line += body

		// Reactions in full: this is the detail view, so every reactor is
		// listed — chips in the accent purple, names in the metadata gray,
		// one emoji per line, never truncated (the pane truncates and
		// points here with "t=thread").
		if parts, _ := reactionParts(&msg, t.selfUserID, t.resolveName, true, 0); len(parts) > 0 {
			for _, p := range parts {
				line += "\n" + bodyIndent + "  " + p
			}
		}
	}

	// Hidden replies beneath a row at the cap: the stub sits where the
	// first hidden child would, as the last line of this row's block (so
	// the selection hint below attaches to it). Rows whose children are
	// visible get nothing — the children are right there. A deleted row at
	// the cap keeps its stub, so the conversation beneath it is not lost.
	if c := t.hidden[idx]; c > 0 {
		line += "\n" + moreRepliesStub(depth+1, c)
	}

	if row == t.cursor {
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

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
