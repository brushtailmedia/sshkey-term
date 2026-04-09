package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	completionStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(0, 1)

	completionSelectedStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#7C3AED")).
		Foreground(lipgloss.Color("#FFFFFF"))

	completionDescStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B"))
)

// CompletionItem is a single completion suggestion.
type CompletionItem struct {
	Text        string // what gets inserted
	Display     string // what shows in the popup
	Description string // optional description
}

// CompletionModel manages the inline completion popup.
type CompletionModel struct {
	items   []CompletionItem
	cursor  int
	visible bool
	prefix  string // the trigger prefix (e.g., "@bo", "/re")
}

// Items returns the completion items (for testing).
func (c *CompletionModel) Items() []CompletionItem {
	return c.items
}

// Complete returns completion items for the current input.
// Returns nil if no completion is active.
func Complete(input string, cursorPos int, members []MemberEntry) *CompletionModel {
	if cursorPos == 0 || len(input) == 0 {
		return nil
	}

	// Find the word being typed (from last space or start)
	start := cursorPos - 1
	for start > 0 && input[start-1] != ' ' {
		start--
	}
	word := input[start:cursorPos]

	if len(word) < 2 {
		return nil
	}

	switch {
	case strings.HasPrefix(word, "@"):
		return completeMentions(word, members)
	case strings.HasPrefix(word, "/") && start == 0:
		return completeCommands(word)
	default:
		return nil
	}
}

func completeMentions(prefix string, members []MemberEntry) *CompletionModel {
	query := strings.ToLower(prefix[1:]) // strip @
	var items []CompletionItem

	for _, m := range members {
		if strings.HasPrefix(strings.ToLower(m.DisplayName), query) {
			items = append(items, CompletionItem{
				Text:    "@" + m.DisplayName + " ",
				Display: "@" + m.DisplayName,
			})
		}
	}

	if len(items) == 0 {
		return nil
	}
	if len(items) > 5 {
		items = items[:5]
	}

	return &CompletionModel{items: items, visible: true, prefix: prefix}
}

func completeCommands(prefix string) *CompletionModel {
	commands := []CompletionItem{
		{Text: "/reply ", Display: "/reply", Description: "reply to message"},
		{Text: "/react ", Display: "/react", Description: "add reaction"},
		{Text: "/pin", Display: "/pin", Description: "pin message"},
		{Text: "/delete", Display: "/delete", Description: "delete message"},
		{Text: "/rename ", Display: "/rename", Description: "rename group"},
		{Text: "/upload ", Display: "/upload", Description: "upload file"},
		{Text: "/verify ", Display: "/verify", Description: "verify user"},
		{Text: "/unverify ", Display: "/unverify", Description: "remove verification"},
		{Text: "/mute", Display: "/mute", Description: "toggle mute"},
		{Text: "/search ", Display: "/search", Description: "search messages"},
		{Text: "/leave", Display: "/leave", Description: "leave room or group"},
		{Text: "/settings", Display: "/settings", Description: "open settings"},
		{Text: "/pending", Display: "/pending", Description: "pending keys (admin)"},
		{Text: "/mykey", Display: "/mykey", Description: "copy public key"},
		{Text: "/help", Display: "/help", Description: "show help"},
	}

	query := strings.ToLower(prefix)
	var items []CompletionItem
	for _, c := range commands {
		if strings.HasPrefix(c.Display, query) {
			items = append(items, c)
		}
	}

	if len(items) == 0 {
		return nil
	}
	if len(items) > 5 {
		items = items[:5]
	}

	return &CompletionModel{items: items, visible: true, prefix: prefix}
}

// Selected returns the currently selected completion item.
func (c *CompletionModel) Selected() CompletionItem {
	if c == nil || len(c.items) == 0 {
		return CompletionItem{}
	}
	return c.items[c.cursor]
}

// Next moves to the next completion item.
func (c *CompletionModel) Next() {
	if c == nil {
		return
	}
	c.cursor = (c.cursor + 1) % len(c.items)
}

// Prev moves to the previous completion item.
func (c *CompletionModel) Prev() {
	if c == nil {
		return
	}
	c.cursor--
	if c.cursor < 0 {
		c.cursor = len(c.items) - 1
	}
}

// View renders the completion popup.
func (c *CompletionModel) View(width int) string {
	if c == nil || !c.visible || len(c.items) == 0 {
		return ""
	}

	var lines []string
	for i, item := range c.items {
		line := item.Display
		if item.Description != "" {
			line += "  " + completionDescStyle.Render(item.Description)
		}

		if i == c.cursor {
			line = completionSelectedStyle.Render(line)
		}
		lines = append(lines, "  "+line)
	}

	return completionStyle.Width(width - 4).Render(strings.Join(lines, "\n"))
}
