package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// sgrReset is the full SGR reset lipgloss/termenv emit at the end of every
// styled segment.
const sgrReset = "\x1b[0m"

// renderSelected renders content as a selected row or block whose band —
// the style's background plus any foreground/attributes it sets — survives
// nested styles.
//
// lipgloss nests by wrapping: an inner Foreground-only segment (a timestamp,
// a reactor name, a key hint, a reply preview, a bold name) ends with a full
// SGR reset, and that reset also cancels the OUTER band. Every cell after
// the first inner segment on a line therefore showed the terminal's own
// background until lipgloss re-armed the band for its padding — the hole
// visible on "5:17 AM", "you", "t=thread" and reply previews inside a
// highlighted row. This re-arms the band after every reset inside each line
// and closes each line with a reset so nothing leaks into the next row.
//
// width > 0 applies Width wrapping/padding; 0 leaves the line's width alone
// (the pinned bar). Under a color profile that emits no sequences (tests,
// dumb terminals) the result is lipgloss's plain render.
func renderSelected(style lipgloss.Style, width int, content string) string {
	render := style
	if width > 0 {
		render = render.Width(width)
	}
	out := render.Render(content)

	seq := styleSequence(style)
	if seq == "" || !strings.Contains(out, sgrReset) {
		return out
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, sgrReset) {
			lines[i] = strings.ReplaceAll(l, sgrReset, sgrReset+seq) + sgrReset
		}
	}
	return strings.Join(lines, "\n")
}

// styleSequence returns the SGR sequence lipgloss emits for style under the
// current color profile, or "" when the profile emits none. It is probed by
// rendering a sentinel so it tracks lipgloss's own encoding (true color vs
// 256 colors) instead of hand-building escapes. Styles whose prefix is not
// purely SGR (padding, margins, borders) yield "" and get no re-arming —
// the selection styles in this package are color-only.
func styleSequence(style lipgloss.Style) string {
	probe := style.Render("x")
	i := strings.Index(probe, "x")
	if i <= 0 {
		return ""
	}
	prefix := probe[:i]
	if !isSGROnly(prefix) {
		return ""
	}
	return prefix
}

// isSGROnly reports whether s consists solely of one or more "ESC [ … m"
// sequences.
func isSGROnly(s string) bool {
	for len(s) > 0 {
		if !strings.HasPrefix(s, "\x1b[") {
			return false
		}
		end := strings.IndexByte(s, 'm')
		if end < 0 {
			return false
		}
		for _, c := range s[2:end] {
			if c != ';' && (c < '0' || c > '9') {
				return false
			}
		}
		s = s[end+1:]
	}
	return true
}
