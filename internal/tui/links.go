package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	urlRegex = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)

	linkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#875FFF")).
			Underline(true)
)

// linkSpans finds the URLs in text as [start, end) byte spans, with the
// punctuation that follows a URL in prose — ")", ".", ",", ";", ":", "!",
// "?", quotes — trimmed off the end, so "(see https://x.y/z)." yields
// https://x.y/z. A ")" is kept when the URL itself opened the paren
// (Wikipedia-style "Foo_(bar)"). Shared by the highlighter and the
// actions, so what is underlined is exactly what opens or copies.
func linkSpans(text string) [][2]int {
	var spans [][2]int
	for _, m := range urlRegex.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		for end > start {
			c := text[end-1]
			if c == ')' && strings.Count(text[start:end], "(") >= strings.Count(text[start:end], ")") {
				break
			}
			if strings.IndexByte(".,;:!?)'\"", c) < 0 {
				break
			}
			end--
		}
		if end > start {
			spans = append(spans, [2]int{start, end})
		}
	}
	return spans
}

// extractLinks returns the distinct URLs in text, in order, trimmed as
// linkSpans trims them.
func extractLinks(text string) []string {
	var links []string
	seen := map[string]bool{}
	for _, s := range linkSpans(text) {
		u := text[s[0]:s[1]]
		if !seen[u] {
			seen[u] = true
			links = append(links, u)
		}
	}
	return links
}

// highlightLinks detects URLs in text and renders them with link styling.
// On terminals that support OSC 8, links are clickable.
func highlightLinks(text string) string {
	matches := linkSpans(text)
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
