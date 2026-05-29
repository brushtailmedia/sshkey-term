package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// navGroup buckets the Ctrl+g continuations into columns in the which-key popup.
type navGroup int

const (
	navGroupGoTo navGroup = iota
	navGroupPanels
	navGroupServers
)

// navBinding is one Ctrl+g continuation. `keys` is the *display* form
// ("k", "1-9"); for the digit range, dispatch and the parity test expand it
// to the digits 1..9.
type navBinding struct {
	keys  string
	desc  string
	group navGroup
}

// navBindings is the single source of truth for the Ctrl+g continuations: it
// feeds the which-key popup, the Ctrl+g rows of the help panel (help.go), and
// the TestNavBindings_AllDispatched parity test. The cancel keys
// (g / esc / ctrl+g) are intentionally absent — they render as the popup's
// footer rather than as selectable entries.
var navBindings = []navBinding{
	{"k", "quick switch", navGroupGoTo},
	{"n", "new conversation", navGroupGoTo},
	{"/", "search", navGroupGoTo},
	{"m", "members", navGroupPanels},
	{"i", "info", navGroupPanels},
	{"s", "settings", navGroupPanels},
	{"d", "your devices", navGroupPanels},
	{"p", "your profile", navGroupPanels},
	{"h", "prev server", navGroupServers},
	{"l", "next server", navGroupServers},
	{"j", "server switcher", navGroupServers},
	{"1-9", "server number", navGroupServers},
}

var navGroupOrder = []navGroup{navGroupGoTo, navGroupPanels, navGroupServers}

var navGroupTitles = map[navGroup]string{
	navGroupGoTo:    "go to",
	navGroupPanels:  "panels",
	navGroupServers: "servers",
}

// padNavKey right-pads a display key so descriptions align within a column.
// Keys are ASCII, so byte length is fine; the widest is "1-9" (3).
func padNavKey(k string) string {
	const w = 3
	if len(k) >= w {
		return k
	}
	return k + strings.Repeat(" ", w-len(k))
}

// renderNavPopup builds the which-key popup: grouped key→desc columns with a
// cancel footer, inside a bordered box. It is render-only — dispatch stays in
// handleNavModeKey; the popup never intercepts keys.
func renderNavPopup() string {
	cols := make([]string, 0, len(navGroupOrder))
	for _, g := range navGroupOrder {
		lines := []string{helpDescStyle.Render(navGroupTitles[g])}
		for _, nb := range navBindings {
			if nb.group != g {
				continue
			}
			lines = append(lines, helpKeyStyle.Render(padNavKey(nb.keys))+"  "+nb.desc)
		}
		cols = append(cols, lipgloss.NewStyle().MarginRight(3).Render(strings.Join(lines, "\n")))
	}
	columns := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	header := searchHeaderStyle.Render("Ctrl+g") + helpDescStyle.Render("  navigation")
	footer := helpDescStyle.Render("esc · g  cancel")
	return dialogStyle.Render(header + "\n\n" + columns + "\n\n" + footer)
}
