package tui

import (
	"os"
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

// TestApp_UpdateWrapper_PersistsPreviewPathForRastermClear is the
// regression guard for the rasterm-doesn't-clear-on-deselect bug.
//
// Pre-fix, the sidebar preview path was computed in App.View — a
// value-receiver method whose mutations were discarded on return.
// SetPreviewImagePath's transition detection reads the previous
// path; with View's writes lost between frames, the previous path
// was always "" so the non-empty → empty transition was never
// detected. The kitty delete escape was never queued, and rasterm
// placements stayed on screen forever after deselect.
//
// Post-fix, the path is written at the end of App.Update via the
// wrapper. Update returns the modified model to bubbletea, so the
// mutation persists. This test simulates two Update calls and
// asserts the second's transition (path → "") flags a clear.
func TestApp_UpdateWrapper_PersistsPreviewPathForRastermClear(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.messages.SetContext("room_test", "", "")
	a.messages.SetFilesDir(t.TempDir())

	// Seed a downloaded image attachment so SelectedImagePath returns
	// a non-empty path. The file just needs to exist; the helper does
	// an os.Stat to decide whether to surface the path. Cursor sits on
	// the message with the attachment.
	imgFile := a.messages.filesDir + "/file_preview"
	if err := os.WriteFile(imgFile, []byte("fake-image-bytes"), 0600); err != nil {
		t.Fatalf("seed image file: %v", err)
	}
	a.messages.messages = []DisplayMessage{{
		ID: "msg_with_image",
		Attachments: []DisplayAttachment{
			{FileID: "file_preview", IsImage: true},
		},
	}}
	a.messages.cursor = 0
	a.focus = FocusMessages

	// Frame 1: focus on messages, cursor on image — Update should
	// land previewImagePath = the seeded path on the persistent model.
	model1, _ := a.Update(struct{}{}) // benign no-op msg
	a1, ok := model1.(App)
	if !ok {
		t.Fatal("Update returned non-App model")
	}
	if a1.sidebar.previewImagePath == "" {
		t.Fatal("Update should have populated sidebar.previewImagePath when image is selected")
	}
	if a1.sidebar.pendingRastermClear {
		t.Fatal("frame 1 (path goes empty → non-empty) should not flag a rasterm clear")
	}

	// Frame 2: cursor moves off the image (simulate by clearing it
	// from the message). Now SelectedImagePath returns "" and the
	// computed preview path is "". Update's wrapper detects the
	// transition and flags the rasterm clear.
	a1.messages.messages[0].Attachments = nil
	model2, _ := a1.Update(struct{}{})
	a2 := model2.(App)
	if a2.sidebar.previewImagePath != "" {
		t.Errorf("frame 2: previewImagePath should be empty, got %q", a2.sidebar.previewImagePath)
	}
	if !a2.sidebar.pendingRastermClear {
		t.Error("frame 2 (path goes non-empty → empty under rasterm) should flag a rasterm clear; this is the regression guard for the View-side mutation-loss bug")
	}
}

// TestApp_ComputePreviewPath_PreservedOnFocusShiftToInput is the
// regression guard for "image preview survives clicking the input
// bar." Common flow: user is reading a message with an image
// attachment, clicks the input to compose a reply about it. The
// image should stay visible during composition.
//
// Pre-fix, computePreviewPath gated the path on FocusMessages, so
// any focus shift away from the messages pane (including to the
// input bar) would return "" and clear the preview. Modal-overlay
// is still a clearing trigger; focus alone is not.
func TestApp_ComputePreviewPath_PreservedOnFocusShiftToInput(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_test", "", "")
	a.messages.SetFilesDir(t.TempDir())

	imgFile := a.messages.filesDir + "/file_compose"
	if err := os.WriteFile(imgFile, []byte("fake-image-bytes"), 0600); err != nil {
		t.Fatalf("seed image file: %v", err)
	}
	a.messages.messages = []DisplayMessage{{
		ID: "msg_with_image",
		Attachments: []DisplayAttachment{
			{FileID: "file_compose", IsImage: true},
		},
	}}
	a.messages.cursor = 0

	for _, focus := range []Focus{FocusMessages, FocusInput, FocusSidebar, FocusMembers} {
		a.focus = focus
		got := a.computePreviewPath()
		if got == "" {
			t.Errorf("focus=%v: computePreviewPath should NOT clear when an image is under the cursor; got empty", focus)
		}
	}

	// Sanity: modal visibility DOES clear, regardless of focus.
	a.focus = FocusInput
	a.help.Toggle() // open help (a modal)
	if got := a.computePreviewPath(); got != "" {
		t.Errorf("modal open: computePreviewPath should clear, got %q", got)
	}
	a.help.Toggle() // close
}

// TestAppView_PrependsRastermClearEscape verifies that App.View
// emits the kitty delete escape when sidebar.pendingRastermClear is
// set, regardless of which render path App takes.
//
// Pre-2026-05-08, the consume-and-emit lived inside sidebar.View().
// That worked for normal-render frames but broke when a full-screen
// modal (settings, infoPanel, addServer, etc.) early-returned the
// modal's view at the top of App.View — sidebar.View was never
// called, so the queued escape was never emitted, and the rasterm
// placement persisted in the kitty graphics layer behind the modal
// text. Hoisting the emit to App.View covers every render path.
//
// The escape stays in the rendered output for the frame that has
// the flag set; Update clears the flag at the start of the next
// event so View doesn't re-emit on subsequent frames.
func TestAppView_PrependsRastermClearEscape(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	a, _ := newEditAppHarness(t)
	a.width = appMinWidth
	a.height = appMinHeight
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"general"})
	a.sidebar.pendingRastermClear = true

	out := a.View()
	if !strings.HasPrefix(out, "\x1b_Ga=d") {
		t.Errorf("App.View output should start with kitty delete escape when pendingRastermClear is set, got prefix %q",
			truncateForLog(out, 32))
	}
}

// TestAppView_DoesNotPrependEscapeWhenFlagUnset confirms the inverse
// — when no clear is queued, App.View doesn't emit a stray escape
// that could disrupt other rendering.
func TestAppView_DoesNotPrependEscapeWhenFlagUnset(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	a, _ := newEditAppHarness(t)
	a.width = appMinWidth
	a.height = appMinHeight
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"general"})
	a.sidebar.pendingRastermClear = false

	out := a.View()
	if strings.HasPrefix(out, "\x1b_Ga=d") {
		t.Errorf("App.View should NOT prepend escape when pendingRastermClear is false, got prefix %q",
			truncateForLog(out, 32))
	}
}

// TestApp_UpdateClearsPendingRastermClearAtStart verifies the
// "consume by Update" half of the contract: Update at the start of
// the next event resets pendingRastermClear so this frame's View
// (which already emitted the escape) doesn't re-emit on subsequent
// frames.
func TestApp_UpdateClearsPendingRastermClearAtStart(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.sidebar.pendingRastermClear = true

	// A no-op msg should still trigger the clear-at-start logic via
	// the Update wrapper.
	model, _ := a.Update(struct{}{})
	updated := model.(App)

	if updated.sidebar.pendingRastermClear {
		t.Error("Update should clear pendingRastermClear at start so View doesn't re-emit the escape on subsequent frames")
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
