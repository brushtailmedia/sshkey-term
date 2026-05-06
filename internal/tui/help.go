package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
	scroll            int
}

type helpRow struct {
	key  string
	desc string
}

type helpSection struct {
	title string
	rows  []helpRow
}

// SetContext updates the help overlay's context-awareness flags.
// Called from app.go before Show/Toggle so the help list reflects
// the current group state.
func (h *HelpModel) SetContext(showAdminCommands bool) {
	h.showAdminCommands = showAdminCommands
}

func (h *HelpModel) Toggle() {
	h.visible = !h.visible
	if h.visible {
		h.scroll = 0
	}
}

func (h *HelpModel) IsVisible() bool {
	return h.visible
}

func (h *HelpModel) Hide() {
	h.visible = false
}

// Update handles help-overlay-local navigation for scrolling.
func (h *HelpModel) Update(msg tea.Msg, width, height int) tea.Cmd {
	if !h.visible {
		return nil
	}
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "esc", "?":
			h.Hide()
			return nil
		case "up", "k":
			h.scroll--
		case "down", "j":
			h.scroll++
		case "pageup", "pgup":
			h.scroll -= h.pageRows(height)
		case "pagedown", "pgdown":
			h.scroll += h.pageRows(height)
		case "home":
			h.scroll = 0
		case "end":
			h.scroll = h.maxScroll(width, height)
		}
	case tea.MouseMsg:
		if m.Action != tea.MouseActionPress {
			break
		}
		switch m.Button {
		case tea.MouseButtonWheelUp:
			h.scroll -= 3
		case tea.MouseButtonWheelDown:
			h.scroll += 3
		}
	}
	h.clampScroll(width, height)
	return nil
}

func (h HelpModel) View(width, height int) string {
	if !h.visible {
		return ""
	}

	content := h.renderContent(width)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	rows := h.pageRows(height)
	maxScroll := len(lines) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := h.scroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + rows
	if end > len(lines) {
		end = len(lines)
	}
	visible := strings.Join(lines[scroll:end], "\n")

	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	return helpStyle.Width(innerWidth).Render(visible)
}

func (h HelpModel) renderContent(width int) string {
	_ = width

	navRows := []helpRow{
		{"tab", "toggle sidebar focus"},
		{"Ctrl+k", "quick switch"},
		{"Ctrl+n", "new group DM"},
		{"Ctrl+m", "member panel"},
		{"Ctrl+p", "pinned messages"},
		{"i / Ctrl+i", "room/group/DM info"},
		{"Ctrl+f", "search"},
		{"Ctrl+,", "settings"},
		{"Ctrl+1-9", "switch server"},
		{"Alt/Option+↑/↓", "prev/next room"},
		{"↑/↓ j/k", "navigate"},
		{"pgup/pgdn", "scroll history"},
	}
	msgRows := []helpRow{
		{"r", "reply to selected"},
		{"e", "react (emoji)"},
		{"u", "remove my reaction"},
		{"p", "pin/unpin (rooms)"},
		{"d", "delete (own only)"},
		{"g", "go to parent (reply)"},
		{"t", "thread view"},
		{"c", "copy text"},
		{"Enter", "context menu"},
	}
	memberRows := []helpRow{
		{"Enter", "open member menu"},
		{"m", "message directly"},
	}
	inputRows := []helpRow{
		{"Enter", "send message"},
		{"Shift+Enter", "newline"},
		{"Tab", "autocomplete"},
		{"Esc", "cancel / back"},
		{"/", "command mode"},
	}

	contentWidth := width - 8
	if contentWidth < 40 {
		contentWidth = 40
	}
	colGap := 4
	colWidth := (contentWidth - colGap) / 2
	if colWidth < 18 {
		colWidth = 18
	}
	leftCol := renderHelpColumn(colWidth, []helpSection{
		{title: "Navigation", rows: navRows},
	})
	rightCol := renderHelpColumn(colWidth, []helpSection{
		{title: "Message Actions", rows: msgRows},
		{title: "Member Panel", rows: memberRows},
		{title: "Input", rows: inputRows},
	})

	var b strings.Builder
	b.WriteString(helpHeaderStyle.Render("  Help — sshkey-chat"))
	b.WriteString("\n\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftCol, spaces(colGap), rightCol))
	b.WriteString("\n")

	// Slash commands. Phase 14: admin-gated verbs are included only
	// when showAdminCommands is true (local user is an admin of the
	// currently-active group). Status commands that don't mutate
	// state (/members, /admins, /role, /whoami, /info, /audit)
	// are always shown.
	type cmdEntry struct {
		cmd, desc string
		adminOnly bool
	}
	commands := []cmdEntry{
		{cmd: "/help or /?", desc: "this screen"},
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
		{cmd: "/members", desc: "open member panel"},
		{cmd: "/admins", desc: "list group admins"},
		{cmd: "/role <user>", desc: "show a user's role"},
		{cmd: "/whoami", desc: "show your own role"},
		{cmd: "/info", desc: "open info panel"},
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

	cmdWidth := 0
	for _, c := range commands {
		if c.adminOnly && !h.showAdminCommands {
			continue
		}
		if w := visibleWidth(c.cmd); w > cmdWidth {
			cmdWidth = w
		}
	}
	cmdWidth++
	if cmdWidth < 12 {
		cmdWidth = 12
	}

	b.WriteString("\n")
	b.WriteString("  " + helpHeaderStyle.Render("Slash Commands"))
	b.WriteString("\n")
	b.WriteString("  " + strings.Repeat("─", maxInt(24, contentWidth-4)))
	b.WriteString("\n")
	for _, c := range commands {
		if c.adminOnly && !h.showAdminCommands {
			continue
		}
		b.WriteString("  " + helpKeyStyle.Render(padRight(c.cmd, cmdWidth)) + " " + helpDescStyle.Render(c.desc) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  Press Esc or ? to close"))
	return b.String()
}

func (h *HelpModel) clampScroll(width, height int) {
	if h.scroll < 0 {
		h.scroll = 0
	}
	maxScroll := h.maxScroll(width, height)
	if h.scroll > maxScroll {
		h.scroll = maxScroll
	}
}

func (h HelpModel) maxScroll(width, height int) int {
	content := h.renderContent(width)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 0 {
		return 0
	}
	max := len(lines) - h.pageRows(height)
	if max < 0 {
		return 0
	}
	return max
}

func (h HelpModel) pageRows(height int) int {
	rows := height - 4 // 2 borders + 2 vertical padding rows from helpStyle
	if rows < 1 {
		rows = 1
	}
	return rows
}

func renderHelpColumn(width int, sections []helpSection) string {
	if width < 12 {
		width = 12
	}

	keyWidth := 0
	for _, sec := range sections {
		for _, row := range sec.rows {
			if w := visibleWidth(row.key); w > keyWidth {
				keyWidth = w
			}
		}
	}
	if keyWidth < 4 {
		keyWidth = 4
	}
	if keyWidth > width/2 {
		keyWidth = width / 2
	}

	var b strings.Builder
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(" " + helpHeaderStyle.Render(sec.title))
		b.WriteString("\n")
		b.WriteString(" " + strings.Repeat("─", maxInt(8, width-2)))
		b.WriteString("\n")
		for _, row := range sec.rows {
			b.WriteString(" " + helpKeyStyle.Render(padRight(row.key, keyWidth)) + " " + helpDescStyle.Render(row.desc))
			b.WriteString("\n")
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

func padRight(s string, n int) string {
	w := visibleWidth(s)
	if w >= n {
		return s
	}
	return s + spaces(n-w)
}

// spaces returns a run of n space characters, clamping negative inputs
// to zero. Wraps strings.Repeat so callers can compute pad widths
// arithmetically (e.g. budget - measured) without guarding every site
// against negative results — the panic that occurred was exactly this
// pattern in the help layout (strings.Repeat panics on negative).
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

func visibleWidth(s string) int {
	return lipgloss.Width(s)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
