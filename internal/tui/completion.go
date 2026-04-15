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
//
// Phase 14: when the input starts with an admin verb (/add, /kick,
// /promote, /demote, /transfer, /role), @-mentions complete against
// the CURRENT GROUP's member list rather than the full mention
// pool. This prevents admins from trying to kick someone who isn't
// in the group (which would fail with ErrUnknownGroup post-check).
// For /add specifically, the target is NOT yet a member, so the
// opposite filter applies — complete against everyone EXCEPT the
// current group's members.
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

// CompleteWithContext is the Phase 14 variant of Complete that takes
// additional context about the current command being typed. It
// applies member-scoped filtering for admin verbs:
//
//   - /kick, /promote, /demote, /transfer, /role → only current
//     group members (these verbs require the target to BE a member)
//   - /add → only users who are NOT current group members (add
//     target must not already be a member)
//   - All other commands fall through to regular @-mention completion
//
// groupMembers is the member list of the currently-active group.
// nonMemberPool is the broader profile pool used for /add completion.
// Empty groupMembers or empty pool disables the respective filter.
func CompleteWithContext(input string, cursorPos int, groupMembers []MemberEntry, nonMemberPool []MemberEntry) *CompletionModel {
	if cursorPos == 0 || len(input) == 0 {
		return nil
	}
	start := cursorPos - 1
	for start > 0 && input[start-1] != ' ' {
		start--
	}
	word := input[start:cursorPos]
	if len(word) < 2 {
		return nil
	}

	// Command completion (first word).
	if strings.HasPrefix(word, "/") && start == 0 {
		return completeCommands(word)
	}

	// @-mention completion — check if this is the argument slot of
	// an admin verb and pick the right data source.
	if strings.HasPrefix(word, "@") {
		verb := leadingCommand(input)
		switch verb {
		case "/kick", "/promote", "/demote", "/transfer", "/role":
			// Current-group members only.
			return completeMentions(word, groupMembers)
		case "/add":
			// Non-members only (target must not already be in the group).
			return completeMentions(word, nonMemberPool)
		default:
			return completeMentions(word, groupMembers)
		}
	}
	return nil
}

// leadingCommand extracts the leading slash command from an input
// line, or returns "" if the line doesn't start with one. Used by
// CompleteWithContext to detect admin-verb completion contexts.
func leadingCommand(input string) string {
	if !strings.HasPrefix(input, "/") {
		return ""
	}
	if space := strings.IndexByte(input, ' '); space > 0 {
		return input[:space]
	}
	return input
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
		{Text: "/rename ", Display: "/rename", Description: "rename group (admin)"},
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
		// Phase 14 admin verbs
		{Text: "/add ", Display: "/add", Description: "add member (admin)"},
		{Text: "/kick ", Display: "/kick", Description: "remove member (admin)"},
		{Text: "/promote ", Display: "/promote", Description: "promote to admin"},
		{Text: "/demote ", Display: "/demote", Description: "demote from admin"},
		{Text: "/transfer ", Display: "/transfer", Description: "promote + leave"},
		// Phase 14 status commands
		{Text: "/members", Display: "/members", Description: "list members"},
		{Text: "/admins", Display: "/admins", Description: "list admins"},
		{Text: "/role ", Display: "/role", Description: "show user's role"},
		{Text: "/whoami", Display: "/whoami", Description: "show your role"},
		{Text: "/groupinfo", Display: "/groupinfo", Description: "group info panel"},
		{Text: "/audit", Display: "/audit", Description: "recent admin actions"},
		{Text: "/undo", Display: "/undo", Description: "revert last kick (30s)"},
		// Phase 14 creation
		{Text: "/groupcreate ", Display: "/groupcreate", Description: "create group DM"},
		{Text: "/dmcreate ", Display: "/dmcreate", Description: "create 1:1 DM"},
		// Phase 18 topic (read-only; rooms only)
		{Text: "/topic", Display: "/topic", Description: "show room topic"},
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
