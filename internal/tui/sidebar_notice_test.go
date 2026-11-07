package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// The bug these guard: the "image too large" text was returned by the
// image renderer as a 44-cell line and placed in the preview area as if
// it were an image. The sidebar panel's lipgloss Width wraps any row
// wider than the pane, the panel grew by two rows, and the frame's top
// border scrolled off the terminal. The renderer now returns a typed
// reason instead of text, and the sidebar draws the notice like its
// "no image selected" label: wrapped, centred, never wider than the
// pane, so the panel keeps its exact height.

func TestPreviewNoticeFor_MapsEveryReasonInOnePlace(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"too large", client.ErrImageTooLarge, previewTooLargeNotice},
		{"too large, wrapped", fmt.Errorf("decode: %w", client.ErrImageTooLarge), previewTooLargeNotice},
		{"bad header", fmt.Errorf("%w: png: short", client.ErrImageHeader), previewUndecodableNotice},
		{"missing file", &fs.PathError{Op: "stat", Path: "x", Err: fs.ErrNotExist}, previewNotCachedNotice},
		{"images off", errInlineImagesOff, previewImagesOffNotice},
		{"anything else", errors.New("png: invalid checksum"), previewUnavailableNotice},
	}
	for _, c := range cases {
		if got := previewNoticeFor(c.err); got != c.want {
			t.Errorf("%s: previewNoticeFor = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBuildPreviewNotice_WrapsCentresAndNeverExceedsWidth(t *testing.T) {
	const text = "image too large for preview"
	for _, width := range []int{40, 27, 26, 20, 12, 8, 5, 3} {
		rows := buildPreviewNotice(width, 7, text)
		if len(rows) != 7 {
			t.Fatalf("width %d: %d rows, want 7", width, len(rows))
		}
		var seen []string
		for i, r := range rows {
			if w := ansi.StringWidth(r); w > width {
				t.Errorf("width %d: row %d measures %d cells: %q", width, i, w, ansi.Strip(r))
			}
			if plain := strings.TrimSpace(ansi.Strip(r)); plain != "" {
				seen = append(seen, plain)
				// Centred: the left pad is half the slack, so the text
				// starts no later than that.
				lead := len(ansi.Strip(r)) - len(strings.TrimLeft(ansi.Strip(r), " "))
				if want := (width - ansi.StringWidth(plain)) / 2; lead != want {
					t.Errorf("width %d: row %d left pad %d, want %d", width, i, lead, want)
				}
			}
		}
		// Every word survives, in order, with no word split unless it
		// was wider than the pane on its own.
		joined := strings.Join(seen, " ")
		if width >= 7 && joined != text {
			t.Errorf("width %d: wrapped text %q, want %q", width, joined, text)
		}
		if width >= 27 && len(seen) != 1 {
			t.Errorf("width %d: %d lines, want 1 (text fits)", width, len(seen))
		}
	}
}

func TestBuildPreviewNotice_EdgesAndOverflowRows(t *testing.T) {
	if rows := buildPreviewNotice(0, 5, "x"); len(rows) != 5 {
		t.Fatalf("zero width: %d rows", len(rows))
	}
	if rows := buildPreviewNotice(10, 0, "x"); len(rows) != 0 {
		t.Fatalf("zero rows: %d rows", len(rows))
	}
	// An empty notice is blank rows, so a reason mapped to "" draws nothing.
	for i, r := range buildPreviewNotice(10, 3, "") {
		if r != "" {
			t.Errorf("empty notice row %d = %q, want blank", i, r)
		}
	}
	// More lines than rows: the first `rows` lines are kept, nothing
	// spills, and every row is still within width.
	rows := buildPreviewNotice(4, 2, "one two three four five")
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	for i, r := range rows {
		if ansi.StringWidth(r) > 4 {
			t.Errorf("row %d measures %d cells", i, ansi.StringWidth(r))
		}
	}
	if strings.TrimSpace(ansi.Strip(rows[0])) != "one" || strings.TrimSpace(ansi.Strip(rows[1])) != "two" {
		t.Errorf("rows = %q", []string{ansi.Strip(rows[0]), ansi.Strip(rows[1])})
	}
	// The notice is styled like the "no image selected" label.
	got := buildPreviewNotice(30, 1, "hello")[0]
	if want := previewLabelStyle.Render("hello"); !strings.HasSuffix(got, want) {
		t.Errorf("notice row %q does not end with the label-styled text %q", got, want)
	}
}

// TestBuildPreviewContent_NoRowEverExceedsWidth is the guard for the
// bug class rather than the instance: whatever state the preview area
// is in, every row it emits fits the pane.
func TestBuildPreviewContent_NoRowEverExceedsWidth(t *testing.T) {
	const path = "/x/file_bigAAAAAAAAAAAAAAAAAA"
	states := map[string]SidebarModel{
		"no image":    {},
		"too large":   {previewImagePath: path, previewErr: client.ErrImageTooLarge},
		"unavailable": {previewImagePath: path, previewErr: errors.New("png: invalid checksum")},
		"not cached":  {previewImagePath: path, previewErr: fs.ErrNotExist},
		"rendering":   {previewImagePath: path, previewRendering: true},
		"rendered":    {previewImagePath: path, previewRenderValue: "▀▀▀▀\n▀▀▀▀"},
	}
	for name, s := range states {
		for _, width := range []int{30, 20, 12, 6} {
			rows := s.buildPreviewContent(width, 9)
			if len(rows) != 9 {
				t.Errorf("%s width %d: %d rows, want 9", name, width, len(rows))
			}
			for i, r := range rows {
				if w := ansi.StringWidth(r); w > width {
					t.Errorf("%s width %d: row %d measures %d cells: %q", name, width, i, w, ansi.Strip(r))
				}
			}
		}
	}
}

// TestSidebarView_TooLargeNoticeKeepsTheExactHeight renders the whole
// sidebar panel in the too-large state at a content width narrower
// than the notice and compares it with the no-image panel at the same
// size: the same number of rows (the frame cannot grow), every row the
// same width, the top border still the first row, the notice visible.
// (View's width/height are the content box; the rounded border adds a
// row above and below and a column each side.)
func TestSidebarView_TooLargeNoticeKeepsTheExactHeight(t *testing.T) {
	const width, height = 20, 30
	base := NewSidebar()
	base.SetRooms([]string{"general"})
	baseLines := strings.Split(base.View(width, height, false), "\n")
	if len(baseLines) != height+2 {
		t.Fatalf("baseline sidebar rendered %d rows, want %d (content + two border rows)", len(baseLines), height+2)
	}
	baseWidth := ansi.StringWidth(baseLines[0])

	s := NewSidebar()
	s.SetRooms([]string{"general"})
	s.SetPreviewImagePath("/x/file_bigAAAAAAAAAAAAAAAAAA")
	s.previewRenderKey = previewRenderKey{path: "/x/file_bigAAAAAAAAAAAAAAAAAA", maxCols: width, maxRows: 8}
	s.previewErr = client.ErrImageTooLarge

	out := s.View(width, height, false)
	lines := strings.Split(out, "\n")
	if len(lines) != len(baseLines) {
		t.Fatalf("too-large sidebar rendered %d rows, baseline %d:\n%s", len(lines), len(baseLines), ansi.Strip(out))
	}
	if top := ansi.Strip(lines[0]); !strings.Contains(top, "─") || top != ansi.Strip(baseLines[0]) {
		t.Fatalf("first row is not the top border: %q", top)
	}
	// At this width the notice wraps after "for", so look for the two
	// halves on their own rows rather than the whole phrase.
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "image too large for") || !strings.Contains(plain, "preview") {
		t.Fatalf("notice missing from the panel:\n%s", plain)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != baseWidth {
			t.Errorf("row %d measures %d cells, baseline rows measure %d: %q", i, w, baseWidth, ansi.Strip(l))
		}
	}
}

// TestPreviewRenderReady_ReasonLandsOnTheSidebarAndClearsOnNewKey goes
// through the real update path: the wrapper derives the preview path
// from the selected message on every update, so the test selects a
// message with a cached image, lets the wrapper compute the render key,
// then delivers the off-thread result with its reason for that key.
// The reason lands only for the current key, survives a stale result,
// and clears when the selection moves to another image or to none.
func TestPreviewRenderReady_ReasonLandsOnTheSidebarAndClearsOnNewKey(t *testing.T) {
	a := newTestApp(t)
	a.messages.SetContext("room_test", "", "")
	a.messages.SetFilesDir(t.TempDir())
	first := "file_bigAAAAAAAAAAAAAAAAAA"
	second := "file_nextAAAAAAAAAAAAAAAAA"
	for _, id := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(a.messages.filesDir, id), []byte("fake-image-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.messages.messages = []DisplayMessage{
		{ID: "msg_first", Attachments: []DisplayAttachment{{FileID: first, IsImage: true}}},
		{ID: "msg_second", Attachments: []DisplayAttachment{{FileID: second, IsImage: true}}},
	}
	a.messages.cursor = 0

	// One benign update lets the wrapper set the preview path and key.
	model, _ := a.Update(previewRenderReadyMsg{})
	na := model.(App)
	key := na.sidebar.previewRenderKey
	if key.path != filepath.Join(a.messages.filesDir, first) {
		t.Fatalf("wrapper did not derive the preview key from the selected message: %+v", key)
	}

	model, _ = na.Update(previewRenderReadyMsg{key: key, err: client.ErrImageTooLarge})
	na = model.(App)
	if !errors.Is(na.sidebar.previewErr, client.ErrImageTooLarge) || na.sidebar.previewRendering || na.sidebar.previewRenderValue != "" {
		t.Fatalf("after the refusal: err=%v rendering=%v value=%q", na.sidebar.previewErr, na.sidebar.previewRendering, na.sidebar.previewRenderValue)
	}

	// A stale result for another key changes nothing.
	stale := key
	stale.path = "/elsewhere/file_oldAAAAAAAAAAAAAAAAAA"
	model, _ = na.Update(previewRenderReadyMsg{key: stale, value: "▀▀"})
	na = model.(App)
	if !errors.Is(na.sidebar.previewErr, client.ErrImageTooLarge) || na.sidebar.previewRenderValue != "" {
		t.Fatalf("stale result landed: err=%v value=%q", na.sidebar.previewErr, na.sidebar.previewRenderValue)
	}

	// Moving the cursor to another image clears the reason for its render.
	na.messages.cursor = 1
	model, _ = na.Update(previewRenderReadyMsg{})
	na = model.(App)
	if na.sidebar.previewErr != nil {
		t.Fatalf("reason survived selecting another image: %v", na.sidebar.previewErr)
	}
	if na.sidebar.previewRenderKey.path != filepath.Join(a.messages.filesDir, second) {
		t.Fatalf("key did not follow the selection: %+v", na.sidebar.previewRenderKey)
	}

	// Deselecting clears it too.
	na.sidebar.previewErr = client.ErrImageTooLarge
	na.messages.messages = nil
	na.messages.cursor = -1
	model, _ = na.Update(previewRenderReadyMsg{})
	na = model.(App)
	if na.sidebar.previewErr != nil || na.sidebar.previewImagePath != "" {
		t.Fatalf("reason survived clearing the selection: err=%v path=%q", na.sidebar.previewErr, na.sidebar.previewImagePath)
	}
}

// TestRenderPreviewCmd_CarriesTheReason runs the real command against a
// crafted header and a missing file and checks the messages it produces.
func TestRenderPreviewCmd_CarriesTheReason(t *testing.T) {
	t.Setenv("SSHKEY_NO_INLINE_IMAGES", "")
	resetInlineImageRenderCache()
	defer resetInlineImageRenderCache()

	path := filepath.Join(t.TempDir(), "file_bigAAAAAAAAAAAAAAAAAA")
	if err := os.WriteFile(path, craftedPNGHeader(30000, 30000), 0o600); err != nil {
		t.Fatal(err)
	}
	key := previewRenderKey{path: path, maxCols: 20, maxRows: 8}
	msg, ok := renderPreviewCmd(key)().(previewRenderReadyMsg)
	if !ok {
		t.Fatal("renderPreviewCmd did not return a previewRenderReadyMsg")
	}
	if msg.key != key || !errors.Is(msg.err, client.ErrImageTooLarge) || msg.value != "" {
		t.Fatalf("msg = %+v, want the key, ErrImageTooLarge and no value", msg)
	}

	gone := previewRenderKey{path: filepath.Join(t.TempDir(), "file_goneAAAAAAAAAAAAAAAAA"), maxCols: 20, maxRows: 8}
	msg = renderPreviewCmd(gone)().(previewRenderReadyMsg)
	if !errors.Is(msg.err, fs.ErrNotExist) || previewNoticeFor(msg.err) != previewNotCachedNotice {
		t.Fatalf("missing file: err=%v notice=%q", msg.err, previewNoticeFor(msg.err))
	}
}
