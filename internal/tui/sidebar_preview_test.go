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
