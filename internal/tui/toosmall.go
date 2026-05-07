package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderTerminalTooSmall returns the bouncer screen shown when the
// terminal's reported dimensions are below appMinWidth × appMinHeight.
// Drawn instead of the normal layout so the user sees a friendly
// "resize me" message rather than a half-broken UI with truncated
// borders and a status bar that's scrolled off the bottom.
//
// The render itself has to survive at very small sizes (the user is
// already at a sub-minimum size; the bouncer can't refuse to render).
// Strategy:
//
//   - At width >= ~30 and height >= ~5, draw a centered multi-line
//     block with the title, the requirement ("Need 80×24"), the
//     current size, and a hint to resize.
//   - Below that, fall back to a single short line ("80×24") that
//     fits in any terminal.
//   - At extreme sizes (1×1, 2×2), still return SOMETHING non-empty
//     so bubbletea doesn't render an unbounded blank.
//
// We deliberately don't use the dialog/border styles — those would
// pull lipgloss border characters that take up cells we don't have
// to spare. Plain text + a couple of colors is enough.
func renderTerminalTooSmall(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B"))

	// Lines we'd ideally show. Each is a logical line; we pick a
	// subset based on available width and height.
	title := titleStyle.Render("Terminal too small")
	req := fmt.Sprintf("Needs at least %d × %d", appMinWidth, appMinHeight)
	have := fmt.Sprintf("Have %d × %d", width, height)
	hint := hintStyle.Render("Resize and this screen goes away.")

	// Tier 1: full layout. Need ~30 cols + 5 rows.
	if width >= 30 && height >= 5 {
		body := title + "\n\n" + req + "\n" + have + "\n\n" + hint
		return centerInBox(body, width, height)
	}

	// Tier 2: compact two-liner. Need ~20 cols + 2 rows.
	if width >= 20 && height >= 2 {
		body := title + "\n" + req
		return centerInBox(body, width, height)
	}

	// Tier 3: single-line "80×24" fits anywhere with width >= visible
	// cell count of the string. `len()` is byte count — `×` (U+00D7)
	// is 2 bytes UTF-8 but 1 cell on screen — so use lipgloss.Width
	// for the cell-aware threshold.
	bare := fmt.Sprintf("%d×%d", appMinWidth, appMinHeight)
	if width >= lipgloss.Width(bare) {
		return centerInBox(bare, width, height)
	}

	// Tier 4: width < 5 (extreme). Return as much of "80×24" as fits
	// followed by trailing newlines to fill the height. Won't look
	// good but won't be empty.
	return strings.Repeat("\n", height-1)
}

// centerInBox places `content` (potentially multi-line) at the
// vertical and horizontal center of a width × height area, padding
// with empty rows above/below and spaces to the left of each line.
//
// `lipgloss.Place` would do this in one line but pads with whatever
// background style is set; we want the terminal background to come
// through unchanged so the bouncer doesn't look like a colored modal
// over a black canvas. Manual centering keeps it simple.
func centerInBox(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	// Drop trailing empty-line artifact from a final '\n' (rare in
	// our usage but harmless to handle).
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	contentH := len(lines)
	topPad := 0
	if contentH < height {
		topPad = (height - contentH) / 2
	}
	bottomPad := height - contentH - topPad
	if bottomPad < 0 {
		bottomPad = 0
	}

	var b strings.Builder
	for i := 0; i < topPad; i++ {
		b.WriteByte('\n')
	}
	for i, line := range lines {
		w := lipgloss.Width(line)
		hPad := 0
		if w < width {
			hPad = (width - w) / 2
		}
		b.WriteString(strings.Repeat(" ", hPad))
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	for i := 0; i < bottomPad; i++ {
		b.WriteByte('\n')
	}
	return b.String()
}
