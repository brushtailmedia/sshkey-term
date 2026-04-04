package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	urlRegex = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)

	linkStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7C3AED")).
		Underline(true)
)

// highlightLinks detects URLs in text and renders them with link styling.
// On terminals that support OSC 8, links are clickable.
func highlightLinks(text string) string {
	matches := urlRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var b strings.Builder
	prev := 0
	for _, m := range matches {
		b.WriteString(text[prev:m[0]])

		url := text[m[0]:m[1]]
		// OSC 8 hyperlink: \e]8;;URL\e\\TEXT\e]8;;\e\\
		// Falls back to just styled text on terminals that don't support it
		b.WriteString("\x1b]8;;" + url + "\x1b\\" + linkStyle.Render(url) + "\x1b]8;;\x1b\\")

		prev = m[1]
	}
	b.WriteString(text[prev:])

	return b.String()
}

// ExtractURLs returns all URLs found in the text.
func ExtractURLs(text string) []string {
	return urlRegex.FindAllString(text, -1)
}
