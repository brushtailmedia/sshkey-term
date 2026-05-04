package tui

import (
	"testing"
)

// TestComputeLayout_NonZeroDimensions is the regression guard for the
// 2026-04-25 bug: prior to extracting the layout calculation into
// computeLayout, View() assigned `a.layout = Layout{...}` with a value
// receiver, which discarded the assignment. Mouse handlers then read
// `a.layout` and saw zero-valued rectangles, so every HitTest returned
// "" and clicks were silently dropped.
//
// This test pins the contract: given non-zero dimensions, computeLayout
// returns a Layout with non-zero panel rectangles. If anyone re-introduces
// the value-receiver assignment pattern (or moves layout state back onto
// the App struct without proper plumbing), this test still passes — but
// the broader assertion is that the layout is a pure function of inputs,
// callable from any context without state-shuffling.
func TestComputeLayout_NonZeroDimensions(t *testing.T) {
	// Realistic terminal size: 120x40. Member panel hidden.
	layout := computeLayout(120, 40, false)

	if layout.SidebarX1 == 0 || layout.SidebarY1 == 0 {
		t.Errorf("sidebar rect collapsed: SidebarX1=%d SidebarY1=%d", layout.SidebarX1, layout.SidebarY1)
	}
	if layout.MessagesX1 == 0 || layout.MessagesY1 == 0 {
		t.Errorf("messages rect collapsed: MessagesX1=%d MessagesY1=%d", layout.MessagesX1, layout.MessagesY1)
	}
	if layout.InputX1 == 0 || layout.InputY1 == 0 {
		t.Errorf("input rect collapsed: InputX1=%d InputY1=%d", layout.InputX1, layout.InputY1)
	}
	if layout.StatusY == 0 {
		t.Errorf("status row collapsed: StatusY=%d", layout.StatusY)
	}
	if layout.SidebarWidth == 0 || layout.MessagesWidth == 0 {
		t.Errorf("widths collapsed: SidebarWidth=%d MessagesWidth=%d", layout.SidebarWidth, layout.MessagesWidth)
	}
}

// TestComputeLayout_HitTestReachesEachPanel exercises the full
// click-routing pipeline: given a known terminal geometry, points at
// the centre of each panel must HitTest to the expected panel name.
// This is the test that would have failed pre-fix — every center
// point would have hit "" because all rectangles were 0x0.
func TestComputeLayout_HitTestReachesEachPanel(t *testing.T) {
	// 120 wide × 40 tall, member panel visible (so all four panels exist).
	layout := computeLayout(120, 40, true)

	cases := []struct {
		name string
		x, y int
		want string
	}{
		{"sidebar center", 10, 10, "sidebar"},
		{"messages center",
			(layout.MessagesX0 + layout.MessagesX1) / 2,
			(layout.MessagesY0 + layout.MessagesY1) / 2,
			"messages"},
		{"input center",
			(layout.InputX0 + layout.InputX1) / 2,
			(layout.InputY0 + layout.InputY1) / 2,
			"input"},
		{"members center",
			(layout.MemberX0 + layout.MemberX1) / 2,
			(layout.MemberY0 + layout.MemberY1) / 2,
			"members"},
		{"status row", 60, layout.StatusY, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := layout.HitTest(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("HitTest(%d, %d) = %q, want %q", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

// TestComputeLayout_MemberPanelToggleAffectsLayout verifies the second
// trigger that requires layout recomputation: toggling member panel
// visibility shifts the messages-pane right boundary because the member
// column takes 18 columns when visible. If a future refactor caches
// the layout based only on (width, height) and forgets to invalidate
// on visibility flag flips, this test catches it.
func TestComputeLayout_MemberPanelToggleAffectsLayout(t *testing.T) {
	hidden := computeLayout(120, 40, false)
	shown := computeLayout(120, 40, true)

	if hidden.MessagesWidth == shown.MessagesWidth {
		t.Errorf("messages width unchanged when member panel toggled: hidden=%d shown=%d",
			hidden.MessagesWidth, shown.MessagesWidth)
	}
	if hidden.MemberWidth != 0 {
		t.Errorf("MemberWidth should be 0 when hidden, got %d", hidden.MemberWidth)
	}
	if shown.MemberWidth == 0 {
		t.Errorf("MemberWidth should be non-zero when shown, got %d", shown.MemberWidth)
	}
}
