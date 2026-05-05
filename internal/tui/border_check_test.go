package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestPanelComposition_FitsInsideTerminal pins the central layout
// invariant that the user reported as broken: the composed body row
// (sidebar + " " + mainPanel [+ " " + member]) must fit within the
// terminal width. Pre-2026-05-04 the mainWidth math was off by 2 (no
// member) and 4 (with member), pushing the messages pane's right
// border off-screen — visually the pane appeared to have no top or
// right border because the overflow tail was being clipped/wrapped
// by the terminal.
//
// This test also pins the MessagesX0 column boundary at sidebarWidth +
// 3 (one cell to the right of sidebar's outer edge, accounting for
// the joinHorizontal gap). The pre-fix value was sidebarWidth + 2,
// which placed MessagesX0 inside the gap column itself.
func TestPanelComposition_FitsInsideTerminal(t *testing.T) {
	cases := []struct {
		name           string
		width, height  int
		memberVisible  bool
		wantMessagesX0 int
	}{
		{"120x40 no member", 120, 40, false, 23},
		{"120x40 with member", 120, 40, true, 23},
		{"80x24 no member", 80, 24, false, 23},
		{"80x24 with member", 80, 24, true, 23},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := computeLayout(tc.width, tc.height, tc.memberVisible)

			composed := (layout.SidebarWidth + 2) + 1 + (layout.MessagesWidth + 2)
			if tc.memberVisible {
				composed += 1 + (layout.MemberWidth + 2)
			}
			if composed != tc.width {
				t.Errorf("composed body row = %d, want %d (term width)", composed, tc.width)
			}

			if layout.MessagesX0 != tc.wantMessagesX0 {
				t.Errorf("MessagesX0 = %d, want %d", layout.MessagesX0, tc.wantMessagesX0)
			}

			// MessagesX1 (exclusive) should equal MessagesX0 + outer width.
			wantMessagesX1 := layout.MessagesX0 + layout.MessagesWidth + 2
			if layout.MessagesX1 != wantMessagesX1 {
				t.Errorf("MessagesX1 = %d, want %d", layout.MessagesX1, wantMessagesX1)
			}

			if tc.memberVisible {
				// MemberX0 should sit one cell after MessagesX1 (the
				// second joinHorizontal gap).
				if layout.MemberX0 != layout.MessagesX1+1 {
					t.Errorf("MemberX0 = %d, want MessagesX1+1 = %d", layout.MemberX0, layout.MessagesX1+1)
				}
				// MemberX1 should equal terminal width (right edge).
				if layout.MemberX1 != tc.width {
					t.Errorf("MemberX1 = %d, want %d", layout.MemberX1, tc.width)
				}
			}
		})
	}
}

// TestPanelComposition_FullScreenFitsTerminalHeight pins the
// regression for the missing-top-borders bug. Sidebar/messages/input/
// status combined output must equal exactly the terminal height —
// not less (leaves the bottom row blank) and not more (top row
// scrolls off, eating panel top borders).
//
// Pre-2026-05-04 sidebar's inner content height was off by 1
// (height - statusBarHeight - 1 instead of -2), so its border-
// inclusive output was 1 row taller than mainPanel's combined
// messages+input rows. body = max(40, 39) = 40, then body + "\n" +
// status = 41, overflowing a 40-row terminal by 1 row.
func TestPanelComposition_FullScreenFitsTerminalHeight(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
	}{
		{"120x40", 120, 40},
		{"80x24", 80, 24},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := computeLayout(tc.width, tc.height, false)

			// Replicate the production app.go View height computations.
			sidebarInner := tc.height - 1 - 2 // -1 status, -2 borders
			sidebarOuter := sidebarInner + 2

			messagesOuter := layout.MessagesY1 - layout.MessagesY0 // 36
			inputOuter := 3                                        // input is 3 rows
			mainPanelRows := messagesOuter + inputOuter            // joined with "\n", 36+3=39

			bodyRows := sidebarOuter
			if mainPanelRows > bodyRows {
				bodyRows = mainPanelRows
			}

			// screen = body + "\n" + status, status is 1 row.
			screenRows := bodyRows + 1

			if screenRows != tc.height {
				t.Errorf("screen rows = %d, want %d (terminal height); sidebar=%d main=%d body=%d",
					screenRows, tc.height, sidebarOuter, mainPanelRows, bodyRows)
			}
		})
	}
}

// TestPanelComposition_TopBorderInRow0 inspects the FULL composed
// body — sidebar + messages joined horizontally — and verifies row 0
// of the result contains BOTH top borders side-by-side. The user's
// screenshot shows the messages pane has a right border (after the
// width fix) but the top border is still missing — meaning row 0 of
// the composed body is missing the messages-pane "╭───╮" segment.
// This test pins what row 0 should look like.
func TestPanelComposition_TopBorderInRow0(t *testing.T) {
	const termWidth = 120
	const termHeight = 40

	layout := computeLayout(termWidth, termHeight, false)

	m := NewMessages()
	m.SetContext("general", "", "")
	mainHeight := layout.MessagesY1 - 2
	messages := m.View(layout.MessagesWidth, mainHeight, false)
	messageRows := strings.Split(messages, "\n")
	t.Logf("messages: %d rows", len(messageRows))
	if len(messageRows) > 0 {
		stripped := stripANSIForOverlay(messageRows[0])
		t.Logf("messages row 0: %q", stripped)
	}

	// Now render the sidebar at its expected dimensions and join.
	// Sidebar.View takes inner-content height (the lipgloss Height
	// arg); the panel's outer/visible height is that + 2 for the
	// rounded border. SidebarY1 - SidebarY0 is the panel's outer
	// height, so we subtract 2 to get the inner.
	sb := NewSidebar()
	sidebarInnerHeight := layout.SidebarY1 - layout.SidebarY0 - 2
	sidebar := sb.View(layout.SidebarWidth, sidebarInnerHeight, true)
	sidebarRows := strings.Split(sidebar, "\n")
	t.Logf("sidebar: %d rows, row 0: %q", len(sidebarRows), stripANSIForOverlay(sidebarRows[0]))

	// Mimic mainPanel = messages + "\n" + input. We don't render input
	// here (don't need it for this assertion); just check messages rows
	// directly.
	mainPanel := messages
	body := joinH(sidebar, mainPanel)
	bodyRows := strings.Split(body, "\n")
	t.Logf("body: %d rows", len(bodyRows))
	if len(bodyRows) > 0 {
		row0 := stripANSIForOverlay(bodyRows[0])
		t.Logf("body row 0: %q", row0)
		if !strings.Contains(row0, "╮") {
			t.Errorf("body row 0 missing messages-pane top-right corner ╮: %q", row0)
		}
		if !strings.Contains(row0, "╭") {
			t.Errorf("body row 0 missing top-left corner ╭: %q", row0)
		}
	}
}

// joinH replicates app.go's joinHorizontal locally so the test can
// exercise the same composition path.
func joinH(left, right string) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	result := ""
	for i := 0; i < maxLines; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		if i > 0 {
			result += "\n"
		}
		result += l + " " + r
	}
	return result
}

// TestPanelComposition_MessagesBorderSurvives renders the messages
// pane at the layout's MessagesWidth and checks that the rendered
// border is the expected outer width — confirms View + computeLayout
// agree on the column accounting.
func TestPanelComposition_MessagesBorderSurvives(t *testing.T) {
	const termWidth = 120
	const termHeight = 40

	layout := computeLayout(termWidth, termHeight, false)

	m := NewMessages()
	m.SetContext("general", "", "")
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "Alice", Body: "hello", TS: 1000, Room: "general"},
	}
	mainHeight := layout.MessagesY1 - 2
	messages := m.View(layout.MessagesWidth, mainHeight, false)
	messageRows := strings.Split(messages, "\n")
	if len(messageRows) == 0 {
		t.Fatal("messages view rendered no rows")
	}

	first := messageRows[0]
	gotWidth := ansi.StringWidth(first)
	wantWidth := layout.MessagesWidth + 2 // borders
	if gotWidth != wantWidth {
		t.Errorf("messages row 0 width = %d, want %d (panel width including borders)", gotWidth, wantWidth)
	}

	// Composed row must fit terminal.
	composed := (layout.SidebarWidth + 2) + 1 + gotWidth
	if composed > termWidth {
		t.Errorf("composed row width %d exceeds terminal width %d", composed, termWidth)
	}
}

// TestPanelComposition_LongSidebarRowsDoNotPushTopBorderOffscreen guards a
// regression where long sidebar rows wrapped, making the sidebar taller than
// the body budget and causing terminal scroll by one row (missing top borders).
func TestPanelComposition_LongSidebarRowsDoNotPushTopBorderOffscreen(t *testing.T) {
	const termWidth = 120
	const termHeight = 40

	layout := computeLayout(termWidth, termHeight, false)

	sb := NewSidebar()
	sb.selfUserID = "usr_self"
	sb.SetRooms([]string{"room_with_a_very_long_name_that_would_otherwise_wrap_in_sidebar"})
	sb.SetGroups([]protocol.GroupInfo{
		{
			ID:      "grp_1",
			Members: []string{"usr_X39baHmKonsL4SyQVUmbU", "usr_partner_with_long_name"},
		},
	})
	sb.SetDMs([]protocol.DMInfo{
		{
			ID:      "dm_1",
			Members: []string{"usr_self", "usr_other_with_an_extremely_long_display_name_0123456789"},
		},
	})
	sb.resolveName = func(u string) string { return u }
	sb.resolveRoomName = func(r string) string { return r }

	sidebarInnerHeight := layout.SidebarY1 - layout.SidebarY0 - 2
	sidebar := sb.View(layout.SidebarWidth, sidebarInnerHeight, true)

	m := NewMessages()
	m.SetContext("general", "", "")
	mainHeight := layout.MessagesY1 - 2
	messages := m.View(layout.MessagesWidth, mainHeight, false)

	body := joinH(sidebar, messages)
	bodyRows := strings.Split(body, "\n")
	// Body (before status bar) must fit the body budget exactly.
	if len(bodyRows) > layout.SidebarY1-layout.SidebarY0 {
		t.Fatalf("body rows = %d, want <= %d; long sidebar rows are wrapping",
			len(bodyRows), layout.SidebarY1-layout.SidebarY0)
	}
	if len(bodyRows) == 0 {
		t.Fatal("empty body")
	}
	row0 := stripANSIForOverlay(bodyRows[0])
	if !strings.Contains(row0, "╭") || !strings.Contains(row0, "╮") {
		t.Fatalf("row 0 missing top borders: %q", row0)
	}
}
