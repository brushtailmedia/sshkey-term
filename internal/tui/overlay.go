package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlay places dialog on top of screen at column col, row row, without
// shifting any of the surrounding layout. Both screen and dialog are
// ANSI-styled strings; col/row are measured in visible terminal cells
// (not byte offsets), so wide characters and emojis count as 2.
//
// Cells of dialog opaque-replace the corresponding cells of screen —
// there is no transparency. Lines of screen that don't intersect the
// dialog's row range pass through untouched.
//
// If placing the dialog at (col, row) would push it off the right or
// bottom edge of the terminal, the position is clamped left/up so the
// dialog stays fully visible. terminalWidth and terminalHeight bound
// the visible canvas.
//
// History: prior to 2026-05-04 the App View rendered the context menu
// and member menu by appending `screen + "\n" + menu.View()`, which
// pushed the entire UI up as the menu grew taller and made the menu
// always appear at the bottom regardless of where the user clicked.
// This function is the proper terminal-overlay solution: it preserves
// the existing screen layout and lets menus pop in-place at the click
// (or keyboard-anchor) point.
func overlay(screen, dialog string, col, row, terminalWidth, terminalHeight int) string {
	if dialog == "" {
		return screen
	}

	// Strip a trailing newline if present so we don't draw an extra
	// blank row below the dialog content.
	dialog = strings.TrimRight(dialog, "\n")
	dialogLines := strings.Split(dialog, "\n")
	dialogHeight := len(dialogLines)
	if dialogHeight == 0 {
		return screen
	}

	dialogWidth := 0
	for _, dl := range dialogLines {
		if w := ansi.StringWidth(dl); w > dialogWidth {
			dialogWidth = w
		}
	}
	if dialogWidth == 0 {
		return screen
	}

	// Clamp the position so the dialog stays on-screen. Right-edge and
	// bottom-edge overflow shifts left/up; left-edge and top-edge
	// underflow clamps to 0.
	if col+dialogWidth > terminalWidth {
		col = terminalWidth - dialogWidth
	}
	if col < 0 {
		col = 0
	}
	if row+dialogHeight > terminalHeight {
		row = terminalHeight - dialogHeight
	}
	if row < 0 {
		row = 0
	}

	screenLines := strings.Split(screen, "\n")

	for i, dl := range dialogLines {
		targetRow := row + i
		// If the screen has fewer rows than we need (e.g. empty or
		// short content), grow it with empty rows so the dialog still
		// renders at its requested position.
		for len(screenLines) <= targetRow {
			screenLines = append(screenLines, "")
		}
		screenLine := screenLines[targetRow]
		screenLineWidth := ansi.StringWidth(screenLine)

		// Pad the screen line to col with spaces if it's shorter than
		// the dialog's left edge — otherwise the splice would emit a
		// short left part and leave the dialog floating with no
		// background underneath.
		if screenLineWidth < col {
			screenLine = screenLine + strings.Repeat(" ", col-screenLineWidth)
			screenLineWidth = col
		}

		// Pad the dialog line to dialogWidth so all rows have a
		// consistent right-edge — required so the right-part splice
		// joins cleanly without column drift.
		dlWidth := ansi.StringWidth(dl)
		if dlWidth < dialogWidth {
			dl = dl + strings.Repeat(" ", dialogWidth-dlWidth)
		}

		// Splice: leftPart (everything before col) + dialog line +
		// rightPart (everything from col+dialogWidth onwards).
		leftPart := ansi.Cut(screenLine, 0, col)
		rightPart := ""
		if screenLineWidth > col+dialogWidth {
			// Cut keeps every escape sequence from the cells it removes, so
			// a style that began left of the dialog still applies to the
			// right part. That is right for SGR and wrong for a graphics
			// placement: the sidebar preview's kitty escape would be
			// emitted a second time after the dialog and the terminal
			// would draw the image there — the "image jumps to the right
			// of the popup" bug. Drop graphics escapes from the right part;
			// the left part keeps the one that is physically there.
			rightPart = stripGraphicsEscapes(ansi.Cut(screenLine, col+dialogWidth, screenLineWidth))
		}
		screenLines[targetRow] = leftPart + dl + rightPart
	}

	return strings.Join(screenLines, "\n")
}

// stripGraphicsEscapes removes APC sequences (ESC _ … ESC \), the kitty
// graphics protocol's envelope, from s. Used on the part of a row that
// follows a spliced dialog, where ansi.Cut carries escapes over from the
// cells it removed: a style should carry, an image placement must not.
func stripGraphicsEscapes(s string) string {
	if !strings.Contains(s, "\x1b_") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "\x1b_")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		end := strings.Index(s[start:], "\x1b\\")
		if end < 0 {
			// Unterminated: drop the rest, it is not text.
			return b.String()
		}
		s = s[start+end+2:]
	}
}

// overlayCentered splices dialog at the centre of a width×height screen —
// the compact confirm prompts' placement (quit, delete message).
func overlayCentered(screen, dialog string, width, height int) string {
	if dialog == "" {
		return screen
	}
	lines := strings.Split(strings.TrimRight(dialog, "\n"), "\n")
	dialogW := 0
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > dialogW {
			dialogW = w
		}
	}
	col := (width - dialogW) / 2
	row := (height - len(lines)) / 2
	if col < 0 {
		col = 0
	}
	if row < 0 {
		row = 0
	}
	return overlay(screen, dialog, col, row, width, height)
}

// padFrameHeight pads frame with empty rows up to height lines, so the
// frame never SHRINKS between renders. Bubble Tea's alt-screen renderer
// (v1.3.10, standard_renderer.go flush) skips writing any line identical to
// the previous frame's line at that index, and when the new frame has fewer
// lines than the last it then erases from the cursor to the end of the
// screen. If the new frame's last line is one it skipped, the cursor is
// still at the START of that line and the erase wipes it. Seen as the info
// panel's bottom border vanishing after a quit dialog drawn below the
// 13-line panel was cancelled. A frame that is always the terminal's height
// never takes that path.
func padFrameHeight(frame string, height int) string {
	if height <= 0 {
		return frame
	}
	n := strings.Count(frame, "\n") + 1
	if n >= height {
		return frame
	}
	return frame + strings.Repeat("\n", height-n)
}
