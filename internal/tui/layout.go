package tui

// Layout stores the computed panel boundaries — used both by mouse
// hit testing (HitTest, *ItemAt) and by the renderer (View consumes
// the *Width/*X*/*Y* fields when sizing panels). The struct is the
// single source of truth for panel geometry; computeLayout below is
// the pure function that produces it from window dimensions plus the
// member-panel visibility flag.
type Layout struct {
	SidebarX0, SidebarX1 int
	SidebarY0, SidebarY1 int

	MessagesX0, MessagesX1 int
	MessagesY0, MessagesY1 int

	InputX0, InputX1 int
	InputY0, InputY1 int

	MemberX0, MemberX1 int
	MemberY0, MemberY1 int

	StatusY int

	// Panel dimensions (for item positioning)
	SidebarWidth  int
	MessagesWidth int
	MemberWidth   int
	Height        int
}

// HitTest returns which panel a coordinate falls in.
func (l Layout) HitTest(x, y int) string {
	if x < 0 || y < 0 || x >= l.MemberX1+l.MemberWidth || y > l.StatusY+1 {
		return ""
	}
	if y >= l.StatusY && y <= l.StatusY+1 {
		return "status"
	}
	if x >= l.SidebarX0 && x < l.SidebarX1 && y >= l.SidebarY0 && y < l.SidebarY1 {
		return "sidebar"
	}
	if x >= l.MemberX0 && x < l.MemberX1 && y >= l.MemberY0 && y < l.MemberY1 {
		return "members"
	}
	if x >= l.InputX0 && x < l.InputX1 && y >= l.InputY0 && y < l.InputY1 {
		return "input"
	}
	if x >= l.MessagesX0 && x < l.MessagesX1 && y >= l.MessagesY0 && y < l.MessagesY1 {
		return "messages"
	}
	return ""
}

// SidebarItemAt returns the index of the sidebar item at the given Y coordinate.
// Returns -1 if out of range.
func (l Layout) SidebarItemAt(y int) int {
	if y < l.SidebarY0 || y >= l.SidebarY1 {
		return -1
	}
	return y - l.SidebarY0 - 1 // -1 for border
}

// MessageItemAt returns the approximate message index at the given Y coordinate.
// This is an estimate since messages can be multi-line.
func (l Layout) MessageItemAt(y int) int {
	if y < l.MessagesY0 || y >= l.MessagesY1 {
		return -1
	}
	return y - l.MessagesY0 - 1
}

// MemberItemAt returns the member index at the given Y coordinate.
func (l Layout) MemberItemAt(y int) int {
	if y < l.MemberY0 || y >= l.MemberY1 {
		return -1
	}
	return y - l.MemberY0 - 2 // -2 for border + header
}

// computeLayout returns the panel boundaries for the given window
// dimensions and visibility flags. Pure function — no App state, so
// callers in both Update (mouse handlers) and View (rendering)
// produce identical Layouts from identical inputs.
//
// History: pre-2026-04-25 the layout was assigned as `a.layout = Layout{...}`
// inside View() with a value receiver, which meant the assignment landed
// on a throwaway copy. Mouse handlers read `a.layout` and saw zero-valued
// rectangles — every HitTest returned "" and every click was silently
// dropped. Lifting layout calculation out into this function and calling
// it on-demand from each consumer fixed the bug; no shared state to drift.
//
// layoutInputContentWidth returns the cells available for the
// textinput's pan-window value (i.e. the textinput.Width arg). This
// is the panel inner content width minus the prompt and the cursor
// cell. Used by App on tea.WindowSizeMsg to keep the textinput's
// horizontal pan size in sync with the current panel width — see
// InputModel.SetTextInputWidth and the off-by-one history in
// input.go's View comment.
//
//	mainWidth (from computeLayout)        // panel inner width
//	- 2 for inputStyle.Padding(0,1)       // padding cells
//	- 2 for the "> " prompt                // prompt cells
//	- 1 for the cursor cell                // cursor cell
//	= mainWidth - 5
func layoutInputContentWidth(termWidth int, memberPanelVisible bool) int {
	l := computeLayout(termWidth, 1, memberPanelVisible) // height irrelevant
	w := l.MessagesWidth - 5
	if w < 1 {
		w = 1
	}
	return w
}

// Width/height are the terminal dimensions from tea.WindowSizeMsg.
// memberPanelVisible toggles the right-side member column (18 cols when
// shown, 0 when hidden).
//
// Width budget — pre-2026-05-04 the math was off by 2 (no member) or
// 4 (with member), which made the composed body row 2 cells wider
// than the terminal. The right border of the messages pane fell off
// the visible canvas (and clipped/wrapped, displacing the top border
// too — the user's "no top or right border on messages pane" report).
//
// Correct accounting for a row of joinHorizontal(sidebar, mainPanel):
//
//	sidebarWidth + 2     // sidebar inner + left/right borders
//	+ 1                  // " " gap inserted by joinHorizontal
//	+ mainWidth + 2      // messages inner + left/right borders
//	= sidebarWidth + mainWidth + 5
//
// Setting that equal to terminal width gives mainWidth = width -
// sidebarWidth - 5. With member panel a second joinHorizontal adds
// another 1-cell gap and the member panel's outer width
// (memberWidth + 2), so mainWidth = width - sidebarWidth - memberWidth
// - 8.
//
// Column boundaries follow the same accounting: messages start one
// cell to the right of the sidebar's outer edge (sidebarWidth + 3),
// not directly adjacent to it (sidebarWidth + 2). The same +1 carries
// through to InputX0 and MemberX0.
func computeLayout(width, height int, memberPanelVisible bool) Layout {
	sidebarWidth := 20
	memberWidth := 0
	if memberPanelVisible {
		memberWidth = 18
	}
	statusBarHeight := 1
	inputHeight := 3

	mainWidth := width - sidebarWidth - 5 // sidebar+borders + gap + messages borders
	if memberWidth > 0 {
		mainWidth -= memberWidth + 3 // gap + member borders + member content
	}
	if mainWidth < 1 {
		mainWidth = 1
	}
	mainHeight := height - statusBarHeight - inputHeight - 2

	// Messages outer (border-inclusive) span starts one cell after
	// sidebar's outer edge — that one cell is the joinHorizontal gap.
	messagesX0 := sidebarWidth + 3
	messagesX1 := messagesX0 + mainWidth + 2 // +2 for left/right borders

	// Body height (rows above the status bar). The status bar takes
	// statusBarHeight rows at the bottom (StatusY = height-1 for a
	// 1-row bar), so the body spans rows 0..bodyEnd-1 = height -
	// statusBarHeight rows total.
	//
	// History note: pre-2026-05-04 the *Y1 fields used `height -
	// statusBarHeight - 1`, an off-by-one that gave sidebar/member
	// inner content 1 row taller than mainPanel's combined
	// (messages + input) rows. The renderer's rounded border doubled
	// that into a 1-row TOTAL overflow (sidebar 40 rows vs mainPanel
	// 39 rows in a 40-row terminal), pushing the composed screen to
	// 41 rows. Terminal scrolled the top row off, eating the top
	// borders of every panel. Fixed by using `height - statusBarHeight`
	// here so all panels' bottom edges line up at row height-2 and
	// the body fits exactly into (height - 1) visible rows.
	bodyEnd := height - statusBarHeight

	return Layout{
		SidebarX0: 0, SidebarX1: sidebarWidth + 2,
		SidebarY0: 0, SidebarY1: bodyEnd,
		SidebarWidth: sidebarWidth,

		MessagesX0: messagesX0, MessagesX1: messagesX1,
		MessagesY0: 0, MessagesY1: mainHeight + 2,
		MessagesWidth: mainWidth,

		InputX0: messagesX0, InputX1: messagesX1,
		InputY0: mainHeight + 2, InputY1: bodyEnd,

		// Member panel sits one cell after messages' outer edge (the
		// second joinHorizontal gap).
		MemberX0: messagesX1 + 1, MemberX1: width,
		MemberY0: 0, MemberY1: bodyEnd,
		MemberWidth: memberWidth,

		StatusY: height - 1,
		Height:  height,
	}
}
