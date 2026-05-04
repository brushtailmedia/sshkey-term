package tui

// Layout stores the computed panel boundaries for mouse hit testing.
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
// Width/height are the terminal dimensions from tea.WindowSizeMsg.
// memberPanelVisible toggles the right-side member column (18 cols when
// shown, 0 when hidden).
func computeLayout(width, height int, memberPanelVisible bool) Layout {
	sidebarWidth := 20
	memberWidth := 0
	if memberPanelVisible {
		memberWidth = 18
	}
	statusBarHeight := 1
	inputHeight := 3
	mainWidth := width - sidebarWidth - memberWidth - 3 // borders
	if memberWidth > 0 {
		mainWidth -= 1 // extra gap
	}
	mainHeight := height - statusBarHeight - inputHeight - 2

	return Layout{
		SidebarX0: 0, SidebarX1: sidebarWidth + 2,
		SidebarY0: 0, SidebarY1: height - statusBarHeight - 1,
		SidebarWidth: sidebarWidth,

		MessagesX0: sidebarWidth + 2, MessagesX1: sidebarWidth + 2 + mainWidth + 2,
		MessagesY0: 0, MessagesY1: mainHeight + 2,
		MessagesWidth: mainWidth,

		InputX0: sidebarWidth + 2, InputX1: sidebarWidth + 2 + mainWidth + 2,
		InputY0: mainHeight + 2, InputY1: height - statusBarHeight - 1,

		MemberX0: sidebarWidth + 2 + mainWidth + 3, MemberX1: width,
		MemberY0: 0, MemberY1: height - statusBarHeight - 1,
		MemberWidth: memberWidth,

		StatusY: height - 1,
		Height:  height,
	}
}
