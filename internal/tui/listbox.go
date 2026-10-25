package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// listRow is one row of a popup list: a label and an optional description
// in the metadata gray.
type listRow struct {
	label string
	desc  string
}

// renderListBox is the one popup-list renderer. The context menu, the
// member menu and the completion popup all draw through it, so the three
// cannot drift: the rounded purple box with the popup's tight padding, rows
// padded to one width so the app-wide selection band is uniform, and the
// description in the metadata gray after the label. width 0 renders the
// natural width; a positive width is the box's outer width. maxRows > 0
// shows a window of that many rows kept around the cursor, with "↑ N more"
// and "↓ N more" markers for the rest (the pickers' convention); 0 shows
// every row.
func renderListBox(rows []listRow, cursor, width, maxRows int) string {
	if len(rows) == 0 {
		return ""
	}
	start, end := 0, len(rows)
	if maxRows > 0 && len(rows) > maxRows {
		start = cursor - maxRows + 1
		if start < 0 {
			start = 0
		}
		end = start + maxRows
		if end > len(rows) {
			end = len(rows)
			start = end - maxRows
		}
	}
	visible := rows[start:end]
	// inner is the room for a row inside a capped box: the border takes
	// two cells and the padding two more. A row longer than that is cut
	// with an ellipsis rather than wrapped — a wrapped row would break the
	// one-row-per-item window and the selection band.
	inner := 0
	if width > 0 {
		inner = width - 4
		if inner < 1 {
			inner = 1
		}
	}
	plain := make([]string, len(visible))
	maxWidth := 0
	for i, r := range visible {
		plain[i] = "  " + r.label
		if r.desc != "" {
			plain[i] += "  " + r.desc
		}
		if inner > 0 {
			plain[i] = ansi.Truncate(plain[i], inner, "…")
		}
		if w := lipgloss.Width(plain[i]); w > maxWidth {
			maxWidth = w
		}
	}
	var lines []string
	if start > 0 {
		lines = append(lines, helpDescStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
	}
	for i, r := range visible {
		line := "  " + r.label
		if r.desc != "" {
			line += "  " + completionDescStyle.Render(r.desc)
		}
		if inner > 0 {
			line = ansi.Truncate(line, inner, "…")
		}
		line += strings.Repeat(" ", maxWidth-lipgloss.Width(plain[i]))
		if start+i == cursor {
			line = completionSelectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if end < len(rows) {
		lines = append(lines, helpDescStyle.Render(fmt.Sprintf("  ↓ %d more", len(rows)-end)))
	}
	style := completionStyle
	if width > 0 {
		style = style.Width(width - 2) // the border takes two cells
	}
	return style.Render(strings.Join(lines, "\n"))
}
