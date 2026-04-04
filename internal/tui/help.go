package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	helpStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(1, 2)

	helpHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))

	helpKeyStyle = lipgloss.NewStyle().
		Bold(true)

	helpDescStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B"))
)

// HelpModel manages the help overlay.
type HelpModel struct {
	visible bool
}

func (h *HelpModel) Toggle() {
	h.visible = !h.visible
}

func (h *HelpModel) IsVisible() bool {
	return h.visible
}

func (h *HelpModel) Hide() {
	h.visible = false
}

func (h HelpModel) View(width, height int) string {
	if !h.visible {
		return ""
	}

	col1 := []struct{ key, desc string }{
		{"Tab", "toggle sidebar focus"},
		{"Ctrl+K", "quick switch"},
		{"Ctrl+N", "new conversation"},
		{"Ctrl+M", "member panel"},
		{"Ctrl+P", "pinned messages"},
		{"Ctrl+I", "room/group info"},
		{"Ctrl+F", "search"},
		{"Ctrl+,", "settings"},
		{"Ctrl+1-9", "switch server"},
		{"Alt+↑/↓", "prev/next room"},
		{"↑/↓ j/k", "navigate"},
		{"PgUp/PgDn", "scroll history"},
	}

	col2 := []struct{ key, desc string }{
		{"r", "reply to selected"},
		{"e", "react (emoji)"},
		{"p", "pin/unpin (rooms)"},
		{"d", "delete (own only)"},
		{"c", "copy text"},
		{"Enter", "context menu"},
		{"", ""},
		{"Enter", "send message"},
		{"Shift+Enter", "newline"},
		{"Tab", "autocomplete"},
		{"Esc", "cancel / back"},
		{"/", "command mode"},
	}

	var b strings.Builder
	b.WriteString(helpHeaderStyle.Render("  Help — sshkey-chat"))
	b.WriteString("\n\n")

	leftTitle := helpHeaderStyle.Render("  Navigation")
	rightTitle := helpHeaderStyle.Render("  Messages & Input")
	b.WriteString(leftTitle + strings.Repeat(" ", 28-lipgloss.Width(leftTitle)) + rightTitle)
	b.WriteString("\n")
	b.WriteString("  " + strings.Repeat("─", 24) + "    " + strings.Repeat("─", 24))
	b.WriteString("\n")

	maxRows := len(col1)
	if len(col2) > maxRows {
		maxRows = len(col2)
	}

	for i := 0; i < maxRows; i++ {
		left := ""
		right := ""

		if i < len(col1) && col1[i].key != "" {
			left = "  " + helpKeyStyle.Render(padRight(col1[i].key, 12)) + " " + helpDescStyle.Render(col1[i].desc)
		}
		if i < len(col2) && col2[i].key != "" {
			right = "  " + helpKeyStyle.Render(padRight(col2[i].key, 12)) + " " + helpDescStyle.Render(col2[i].desc)
		}

		leftPadded := left + strings.Repeat(" ", 28-visibleWidth(left))
		b.WriteString(leftPadded + right + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  Press Esc or ? to close"))

	content := b.String()
	return helpStyle.Width(width - 4).Render(content)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func visibleWidth(s string) int {
	return lipgloss.Width(s)
}
