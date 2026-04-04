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
	if y >= l.StatusY {
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
