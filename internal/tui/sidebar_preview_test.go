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

func TestBuildPreviewImageRowsFromValue_RastermRowContainsEscapeAndIsCentered(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	// Synthesize the rendered escape via RenderImageInline (the same
	// path the async render Cmd takes). Then exercise the formatting
	// helper directly — that's the unit under test now that the file-
	// reading version of buildPreviewImageRows is gone.
	rendered := RenderImageInline(srcPath, 20, 8)
	if rendered == "" {
		t.Fatal("RenderImageInline returned empty; cannot exercise centering")
	}
	rows := buildPreviewImageRowsFromValue(rendered, 20, 8)
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

func TestBuildPreviewImageRowsFromValue_BlockFallbackStillCentered(t *testing.T) {
	withRastermProtocol(t, rastermNone)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	rendered := RenderImageInline(srcPath, 40, 8)
	if rendered == "" {
		t.Fatal("RenderImageInline returned empty; cannot exercise centering")
	}
	rows := buildPreviewImageRowsFromValue(rendered, 40, 8)
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

// TestBuildPreviewImageRowsFromValue_EmptyValueIsBlank documents the
// async-render-in-flight case: between SetPreviewImagePath setting a
// non-empty path and the render Cmd's msg landing, previewRenderValue
// is "". The formatting helper returns blank rows for this state.
func TestBuildPreviewImageRowsFromValue_EmptyValueIsBlank(t *testing.T) {
	rows := buildPreviewImageRowsFromValue("", 20, 8)
	if len(rows) != 8 {
		t.Fatalf("expected 8 blank rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r != "" {
			t.Errorf("row %d should be blank, got %q", i, r)
		}
	}
}

// TestBuildPreviewContent_PrependsDeleteEscapeOnPlaceholder verifies
// the stateless rasterm-clear emission: when the preview pane has
// no image (placeholder render) and rasterm-kitty is the active
// encoder, the first row carries the kitty delete-by-id escape so
// any prior placement gets removed from the graphics layer.
func TestBuildPreviewContent_PrependsDeleteEscapeOnPlaceholder(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	rows := s.buildPreviewContent(20, 8)
	if len(rows) == 0 {
		t.Fatal("expected non-empty preview rows")
	}
	if !strings.HasPrefix(rows[0], "\x1b_Ga=d,") {
		t.Errorf("placeholder frame under rasterm-kitty should start with kitty delete escape, got %q", truncateForLog(rows[0], 40))
	}
}

// TestBuildPreviewContent_NoDeleteEscapeWhenKittyPlacementPresent
// verifies the inverse: when the preview pane has a populated
// previewRenderValue carrying a kitty placement, no extra delete
// escape is prepended (the new placement atomically replaces the
// prior via fixed image-id, no delete needed).
//
// We pre-render via RenderImageInline and seed previewRenderValue
// directly — the async dispatch path normally lands the value via
// previewRenderReadyMsg in updateInner, but for this unit test we
// just want buildPreviewContent's inspection logic exercised.
func TestBuildPreviewContent_NoDeleteEscapeWhenKittyPlacementPresent(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	rendered := RenderImageInline(srcPath, 20, 8)
	if rendered == "" {
		t.Fatal("RenderImageInline returned empty; cannot exercise placement detection")
	}

	s := NewSidebar()
	s.SetPreviewImagePath(srcPath)
	s.previewRenderValue = rendered
	rows := s.buildPreviewContent(20, 8)
	for _, r := range rows {
		if strings.Contains(r, "\x1b_Ga=d,") {
			t.Errorf("frame with kitty placement should not also carry a delete escape, got row %q", truncateForLog(r, 40))
		}
	}
}

// TestBuildPreviewContent_DeleteEscapeWhilePreviewRendering verifies
// the in-flight render case: previewImagePath is set but the Cmd's
// result hasn't landed yet (previewRenderValue == ""). The pane
// renders blank rows; the stateless delete escape fires because no
// kitty placement is present in the body. This is the visual
// "image clears immediately, replacement appears next frame" UX on
// the A→B transition with a fresh selection.
func TestBuildPreviewContent_DeleteEscapeWhilePreviewRendering(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/some-image.png")
	// previewRenderValue intentionally NOT set — simulates Cmd in flight.

	rows := s.buildPreviewContent(20, 8)
	if len(rows) == 0 {
		t.Fatal("expected non-empty preview rows")
	}
	if !strings.HasPrefix(rows[0], "\x1b_Ga=d,") {
		t.Errorf("rendering-in-flight frame should start with kitty delete escape (no placement to skip the prepend), got %q", truncateForLog(rows[0], 40))
	}
}

// TestBuildPreviewContent_NoDeleteEscapeOnNonKittyTerminals verifies
// the emission is gated on rastermKitty specifically — iTerm and
// non-rasterm terminals don't get the kitty escape (iTerm doesn't
// process it; the bytes would just be wasted in either case).
func TestBuildPreviewContent_NoDeleteEscapeOnNonKittyTerminals(t *testing.T) {
	for _, proto := range []rastermProtocol{rastermNone, rastermIterm} {
		withRastermProtocol(t, proto)

		s := NewSidebar()
		rows := s.buildPreviewContent(20, 8)
		for _, r := range rows {
			if strings.Contains(r, "\x1b_Ga=d,") {
				t.Errorf("rasterm protocol %v should not emit kitty delete escape, got row %q", proto, truncateForLog(r, 40))
			}
		}
	}
}

// TestRequestPreviewRender_ClearsStateWhenNoPreview verifies that
// passing previewImagePath = "" through RequestPreviewRender resets
// the render state and returns nil (no Cmd dispatch needed).
func TestRequestPreviewRender_ClearsStateWhenNoPreview(t *testing.T) {
	s := NewSidebar()
	s.previewImagePath = ""
	s.previewRenderKey = previewRenderKey{path: "/stale.png", maxCols: 10, maxRows: 5}
	s.previewRenderValue = "stale-value"
	s.previewRendering = true

	cmd := s.RequestPreviewRender(20, 8)
	if cmd != nil {
		t.Error("no preview wanted should return nil Cmd")
	}
	if s.previewRenderKey != (previewRenderKey{}) {
		t.Errorf("expected zero key, got %+v", s.previewRenderKey)
	}
	if s.previewRenderValue != "" {
		t.Errorf("expected empty value, got %q", s.previewRenderValue)
	}
	if s.previewRendering {
		t.Error("expected previewRendering = false")
	}
}

// TestRequestPreviewRender_CacheHitSyncPopulates exercises the
// fast-path: when the in-memory cache already holds a render for
// the desired key, RequestPreviewRender populates previewRenderValue
// synchronously and returns nil — no goroutine round-trip, no
// flicker on common-case scrolling.
func TestRequestPreviewRender_CacheHitSyncPopulates(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	withCleanInlineImageEnv(t)
	srcPath := writeImageWithThumbs(t, t.TempDir())

	// Prime the cache by running a render through RenderImageInline
	// (the same call the Cmd would make).
	primed := RenderImageInline(srcPath, 20, 8)
	if primed == "" {
		t.Fatal("RenderImageInline returned empty; cache not primed")
	}

	s := NewSidebar()
	s.SetPreviewImagePath(srcPath)
	cmd := s.RequestPreviewRender(20, 8)
	if cmd != nil {
		t.Error("cache hit should return nil Cmd (sync populate)")
	}
	if s.previewRenderValue != primed {
		t.Errorf("previewRenderValue should equal cached render; got len=%d, want len=%d",
			len(s.previewRenderValue), len(primed))
	}
	if s.previewRendering {
		t.Error("cache hit should leave previewRendering = false")
	}
}

// TestRequestPreviewRender_CacheMissDispatchesCmd exercises the
// async path: cache miss returns a non-nil Cmd, marks rendering,
// clears any stale value.
func TestRequestPreviewRender_CacheMissDispatchesCmd(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/uncached.png")
	s.previewRenderValue = "stale-value-from-prior-key"

	cmd := s.RequestPreviewRender(20, 8)
	if cmd == nil {
		t.Fatal("cache miss should return non-nil Cmd")
	}
	if !s.previewRendering {
		t.Error("cache miss should set previewRendering = true")
	}
	if s.previewRenderValue != "" {
		t.Errorf("cache miss should clear stale value, got %q", s.previewRenderValue)
	}
	if s.previewRenderKey.path != "/path/to/uncached.png" {
		t.Errorf("expected key path = /path/to/uncached.png, got %q", s.previewRenderKey.path)
	}
}

// TestRequestPreviewRender_SameKeyNoDispatch verifies the no-op
// case: requesting the same key twice in a row returns nil the
// second time (no redundant Cmd dispatched, no state churn).
func TestRequestPreviewRender_SameKeyNoDispatch(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSidebar()
	s.SetPreviewImagePath("/path/to/some.png")

	// First call: cache miss, dispatches Cmd.
	first := s.RequestPreviewRender(20, 8)
	if first == nil {
		t.Fatal("first call should return Cmd")
	}

	// Second call with same path/dims: should no-op.
	second := s.RequestPreviewRender(20, 8)
	if second != nil {
		t.Error("same-key second call should return nil")
	}
}

// TestApp_UpdateWrapper_PersistsPreviewPath is the regression guard
// for the path-mutation-loss bug. Pre-fix, the sidebar preview path
// was computed in App.View — a value-receiver method whose mutations
// were discarded on return — so previewImagePath on the persistent
// App was always "". Post-fix, the path is written at the end of
// App.Update via the wrapper. Update returns the modified model to
// bubbletea, so the mutation persists. This test simulates two
// Update calls and asserts the path correctly tracks selection.
//
// Note: pre-2026-05-11 this test also asserted a `pendingRastermClear`
// flag was set on the empty-transition. That flag was removed when
// the rasterm clear became stateless (emitted inside
// buildPreviewContent based on whether the rendered output carries
// a kitty placement). The path-persistence half of the regression
// guard remains valid — buildPreviewContent reads previewImagePath
// to decide what to render.
func TestApp_UpdateWrapper_PersistsPreviewPath(t *testing.T) {
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

	// Frame 2: cursor moves off the image (simulate by clearing the
	// attachment from the message). Now SelectedImagePath returns ""
	// and the wrapper's SetPreviewImagePath call should clear the
	// persistent path.
	a1.messages.messages[0].Attachments = nil
	model2, _ := a1.Update(struct{}{})
	a2 := model2.(App)
	if a2.sidebar.previewImagePath != "" {
		t.Errorf("frame 2: previewImagePath should be empty, got %q", a2.sidebar.previewImagePath)
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

// TestAppView_PrependsKittyDeleteWhenBodyLacksPlacement verifies the
// stateless rasterm clear at App.View level: when the rendered body
// doesn't carry a kitty placement (default state with no image
// selected), App.View prepends a delete escape so any prior
// placement gets cleared from the kitty graphics layer.
//
// Covers the modal-render scope: full-screen modals (settings,
// infoPanel, addServer, search, etc.) early-return their own view
// in App.View, bypassing buildPreviewContent's parallel emission.
// Without the App.View fallback, opening a modal while a kitty
// image was on-screen would leave the placement persisted behind
// the modal text.
func TestAppView_PrependsKittyDeleteWhenBodyLacksPlacement(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	a, _ := newEditAppHarness(t)
	a.width = appMinWidth
	a.height = appMinHeight
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"general"})

	out := a.View()
	if !strings.HasPrefix(out, "\x1b_Ga=d") {
		t.Errorf("App.View output should start with kitty delete escape when body has no kitty placement, got prefix %q",
			truncateForLog(out, 32))
	}
}

// TestAppView_NoKittyDeleteOnNonKittyTerminals confirms the gating:
// the delete escape only goes to kitty-protocol terminals. iTerm
// doesn't process kitty escapes (they'd be wasted bytes), and
// non-rasterm terminals don't have a graphics layer to clear in
// the first place.
func TestAppView_NoKittyDeleteOnNonKittyTerminals(t *testing.T) {
	for _, proto := range []rastermProtocol{rastermNone, rastermIterm} {
		withRastermProtocol(t, proto)

		a, _ := newEditAppHarness(t)
		a.width = appMinWidth
		a.height = appMinHeight
		a.sidebar = NewSidebar()
		a.sidebar.SetRooms([]string{"general"})

		out := a.View()
		if strings.HasPrefix(out, "\x1b_Ga=d") {
			t.Errorf("rasterm protocol %v: App.View should NOT prepend kitty delete escape, got prefix %q",
				proto, truncateForLog(out, 32))
		}
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
