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

// HelpModel manages the help overlay. Phase 14 added context-aware
// rendering: the showAdminCommands flag controls whether in-group
// admin verbs (/add, /kick, /promote, /demote, /transfer, /rename)
// appear in the slash-command list. Set by the App based on the
// local user's admin status in the currently-active group. When the
// user is not an admin (or not in a group context), admin verbs are
// hidden so the help list doesn't advertise commands the server
// would reject anyway.
type HelpModel struct {
	visible           bool
	showAdminCommands bool
}

// SetContext updates the help overlay's context-awareness flags.
// Called from app.go before Show/Toggle so the help list reflects
// the current group state.
func (h *HelpModel) SetContext(showAdminCommands bool) {
	h.showAdminCommands = showAdminCommands
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
		{"Ctrl+N", "new group DM"},
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
		{"u", "remove my reaction"},
		{"p", "pin/unpin (rooms)"},
		{"d", "delete (own only)"},
		{"g", "go to parent (reply)"},
		{"t", "thread view"},
		{"c", "copy text"},
		{"Enter", "context menu"},
		{"", ""},
		{"m member panel:", ""},
		{"Enter", "open member menu"},
		{"m", "message directly"},
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

	// Slash commands. Phase 14: admin-gated verbs are included only
	// when showAdminCommands is true (local user is an admin of the
	// currently-active group). Status commands that don't mutate
	// state (/members, /admins, /role, /whoami, /groupinfo, /audit)
	// are always shown.
	type cmdEntry struct {
		cmd, desc string
		adminOnly bool
	}
	commands := []cmdEntry{
		{cmd: "/help", desc: "this screen"},
		{cmd: "/search <query>", desc: "search messages"},
		{cmd: "/upload <path>", desc: "upload a file"},
		{cmd: "/verify <user>", desc: "verify safety number"},
		{cmd: "/leave", desc: "leave room or group DM"},
		{cmd: "/delete", desc: "delete conversation from your view"},
		{cmd: "/mute", desc: "toggle mute"},
		{cmd: "/topic", desc: "show room topic (rooms only)"},
		{cmd: "/settings", desc: "open settings"},
		{cmd: "/unverify <user>", desc: "remove verification"},
		{cmd: "/whois <user>", desc: "show user's fingerprint + verified state"},
		{cmd: "/pending", desc: "pending keys (admin)"},
		{cmd: "/mykey", desc: "copy public key"},
		// Phase 14 status commands (group-context, any member)
		{cmd: "/members", desc: "list group members"},
		{cmd: "/admins", desc: "list group admins"},
		{cmd: "/role <user>", desc: "show a user's role"},
		{cmd: "/whoami", desc: "show your own role"},
		{cmd: "/groupinfo", desc: "open group info panel"},
		{cmd: "/audit [N]", desc: "recent admin actions"},
		// Phase 14 admin verbs (group-context, admin-only)
		{cmd: "/add <user>", desc: "add member to group", adminOnly: true},
		{cmd: "/kick <user>", desc: "remove member from group", adminOnly: true},
		{cmd: "/promote <user>", desc: "promote member to admin", adminOnly: true},
		{cmd: "/demote <user>", desc: "demote admin to member", adminOnly: true},
		{cmd: "/transfer <user>", desc: "promote + leave (hand off)", adminOnly: true},
		{cmd: "/rename <name>", desc: "rename group DM", adminOnly: true},
		{cmd: "/undo", desc: "revert last kick (30s)", adminOnly: true},
	}

	b.WriteString("\n")
	b.WriteString("  " + helpHeaderStyle.Render("Slash Commands"))
	b.WriteString("\n")
	b.WriteString("  " + strings.Repeat("─", 52))
	b.WriteString("\n")
	for _, c := range commands {
		if c.adminOnly && !h.showAdminCommands {
			continue
		}
		b.WriteString("  " + helpKeyStyle.Render(padRight(c.cmd, 20)) + " " + helpDescStyle.Render(c.desc) + "\n")
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
