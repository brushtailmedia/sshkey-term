package tui

import (
	"strings"
	"testing"
)

func TestSidebarView_RendersFixedPreviewSection(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"general", "support"})

	out := stripANSI(s.View(20, 30, false))
	if !strings.Contains(out, "├") || !strings.Contains(out, "┤") {
		t.Fatalf("sidebar should render preview divider row, got:\n%s", out)
	}
}

func TestBuildPreviewImageRows_RastermRowContainsEscapeAndIsCentered(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	rows := buildPreviewImageRows(srcPath, 20, 8)
	if len(rows) == 0 {
		t.Fatal("expected preview rows")
	}
	escRow := -1
	for i, r := range rows {
		if strings.Contains(r, "\x1b_G") {
			escRow = i
			break
		}
	}
	if escRow < 0 {
		t.Fatal("expected one raster preview row to contain kitty escape")
	}
	// With 20x8 pane and a square test image, raster fit is 16x8 with
	// 1:2 cell aspect, so it should horizontally center (hPad=2) and
	// land at top row (vPad=0).
	if escRow != 0 {
		t.Fatalf("raster escape row index = %d, want 0", escRow)
	}
	if !strings.HasPrefix(rows[escRow], strings.Repeat(" ", 2)+"\x1b_G") {
		t.Fatalf("expected centered raster row with 2-space left pad, got %q", truncateForLog(rows[escRow], 24))
	}
}

func TestBuildPreviewImageRows_BlockFallbackStillCentered(t *testing.T) {
	withRastermProtocol(t, rastermNone)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	rows := buildPreviewImageRows(srcPath, 40, 8)
	found := false
	for _, r := range rows {
		if r == "" {
			continue
		}
		found = true
		if !strings.HasPrefix(r, " ") {
			t.Fatalf("block-char fallback row should keep left padding (centering), got %q", truncateForLog(r, 24))
		}
		break
	}
	if !found {
		t.Fatal("expected at least one non-empty fallback image row")
	}
}

// TestSidebarSetPreviewImagePath_NoClearWhenRastermDisabled verifies
// that the rasterm-clear pending flag stays false when rasterm isn't
// the active encoder. Block-char "clears" naturally because bubbletea
// overwrites text cells; emitting the kitty delete escape there is
// harmless but not needed.
func TestSidebarSetPreviewImagePath_NoClearWhenRastermDisabled(t *testing.T) {
	withRastermProtocol(t, rastermNone)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/image.png")
	s.SetPreviewImagePath("")

	if s.pendingRastermClear {
		t.Error("pendingRastermClear should be false when rasterm is not capable")
	}
}

// TestSidebarSetPreviewImagePath_FlagsClearOnDeselectWhenRasterm
// verifies that transitioning from "showing image" to "no image"
// while rasterm is the active encoder sets pendingRastermClear so
// the next View() emits the kitty delete escape.
func TestSidebarSetPreviewImagePath_FlagsClearOnDeselectWhenRasterm(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/image.png")
	if s.pendingRastermClear {
		t.Error("setting an image path should not flag a clear")
	}
	s.SetPreviewImagePath("")

	if !s.pendingRastermClear {
		t.Error("transitioning non-empty → empty under rasterm should set pendingRastermClear")
	}
}

// TestSidebarSetPreviewImagePath_NoClearOnNonEmptyToNonEmpty verifies
// that swapping one image for another doesn't trigger the clear flag.
// The kitty placement uses a fixed image-id, so a fresh placement
// atomically replaces the prior one; emitting a delete in between
// would risk a one-frame "blank preview" flicker.
func TestSidebarSetPreviewImagePath_NoClearOnNonEmptyToNonEmpty(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/first.png")
	s.SetPreviewImagePath("/path/to/second.png")

	if s.pendingRastermClear {
		t.Error("non-empty → non-empty should not flag a clear; new placement replaces prior")
	}
}

// TestSidebarSetPreviewImagePath_ResetsClearOnReSelect verifies that
// re-selecting an image after a stale clear was queued cancels the
// clear (so we don't accidentally erase the new placement).
func TestSidebarSetPreviewImagePath_ResetsClearOnReSelect(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/first.png")
	s.SetPreviewImagePath("") // queue clear
	if !s.pendingRastermClear {
		t.Fatal("precondition: clear should be queued")
	}
	s.SetPreviewImagePath("/second.png") // re-select before View consumed the flag
	if s.pendingRastermClear {
		t.Error("re-selecting an image should cancel a pending clear")
	}
}

// TestSidebarView_PrependsRastermClearEscape verifies that View()
// emits the kitty delete escape when pendingRastermClear is set, AND
// resets the flag so subsequent renders don't repeat the escape.
func TestSidebarView_PrependsRastermClearEscape(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetRooms([]string{"general"})
	s.SetPreviewImagePath("/img.png")
	s.SetPreviewImagePath("")
	if !s.pendingRastermClear {
		t.Fatal("precondition: clear should be queued")
	}

	out := s.View(20, 30, false)
	if !strings.HasPrefix(out, "\x1b_Ga=d") {
		t.Errorf("View output should start with kitty delete escape, got prefix %q",
			truncateForLog(out, 20))
	}
	if s.pendingRastermClear {
		t.Error("View should reset pendingRastermClear after consuming it")
	}

	// Second render: no escape should be emitted (flag was cleared).
	out2 := s.View(20, 30, false)
	if strings.HasPrefix(out2, "\x1b_Ga=d") {
		t.Errorf("second render after clear consumed should NOT prepend escape, got prefix %q",
			truncateForLog(out2, 20))
	}
}

func truncateForLog(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestSidebarCursorAtRow_PreviewRowsAreNotSelectable(t *testing.T) {
	s := NewSidebar()
	s.SetRooms([]string{"general", "support"})
	s.cursor = 0

	height := 30
	listRows, previewRows := sidebarSectionHeights(height)
	if previewRows == 0 {
		t.Fatalf("expected preview section to be enabled for height=%d", height)
	}

	dividerRow := listRows
	if got := s.CursorAtRow(dividerRow, height); got != -1 {
		t.Fatalf("divider row should not select an item, got %d", got)
	}
	firstPreviewRow := listRows + 1
	if got := s.CursorAtRow(firstPreviewRow, height); got != -1 {
		t.Fatalf("preview row should not select an item, got %d", got)
	}
}

func TestSidebarCursorAtRow_ScrollWindowMapsVisibleRows(t *testing.T) {
	s := NewSidebar()
	rooms := make([]string, 20)
	for i := range rooms {
		rooms[i] = "room_" + strings.Repeat("x", i%3)
	}
	s.SetRooms(rooms)
	s.cursor = 15
	s.updateSelection()

	// Height chosen so listRows = 11; with cursor 15 and 20 rooms,
	// the scroll window starts at room 5 (15 - 11 + 1 = 5), so the
	// top visible row should map to cursor 5. Computed from
	// constants so the test stays correct across future
	// sidebarPreviewFixedRows tweaks.
	height := sidebarPreviewFixedRows + sidebarPreviewDividerRows + 11
	got := s.CursorAtRow(0, height)
	if got != 5 {
		t.Fatalf("top visible list row should map to cursor 5 after scroll, got %d", got)
	}
}

func TestMouseClickInSidebarPreviewOnlyFocusesSidebar(t *testing.T) {
	a := App{
		sidebar: NewSidebar(),
		width:   120,
		height:  40,
	}
	a.sidebar.SetRooms([]string{"general", "support"})
	a.sidebar.cursor = 1
	a.sidebar.updateSelection()

	layout := computeLayout(a.width, a.height, false)
	sidebarInnerHeight := layout.SidebarY1 - layout.SidebarY0 - 2
	listRows, previewRows := sidebarSectionHeights(sidebarInnerHeight)
	if previewRows == 0 {
		t.Fatalf("expected preview section to be enabled")
	}

	// Click first row of preview section (after divider).
	x := layout.SidebarX0 + 1
	y := layout.SidebarY0 + 1 + listRows + 1

	model, _ := a.handleMouseClick(x, y)
	updated := model.(App)

	if updated.focus != FocusSidebar {
		t.Fatalf("focus = %v, want FocusSidebar", updated.focus)
	}
	if updated.sidebar.cursor != 1 {
		t.Fatalf("cursor changed on preview click: got %d want 1", updated.sidebar.cursor)
	}
}
