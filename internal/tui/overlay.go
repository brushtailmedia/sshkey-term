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
			rightPart = ansi.Cut(screenLine, col+dialogWidth, screenLineWidth)
		}
		screenLines[targetRow] = leftPart + dl + rightPart
	}

	return strings.Join(screenLines, "\n")
}
