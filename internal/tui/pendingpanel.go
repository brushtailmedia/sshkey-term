package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// PendingPanelModel displays the list of pending (unapproved) SSH keys.
// Admin-only overlay, opened via /pending. Read-only — approve/reject is
// handled via sshkey-ctl on the server. The high-value action here is `c`
// to copy the selected key's public key for pasting into `sshkey-ctl approve`.
type PendingPanelModel struct {
	visible bool
	keys    []protocol.PendingKeyEntry
	cursor  int
	// notice is a transient confirmation ("Public key copied…"), cleared on
	// navigation and on Show so it never lingers against the wrong row.
	notice string
}

func (p *PendingPanelModel) Show(keys []protocol.PendingKeyEntry) {
	p.visible = true
	p.keys = keys
	p.cursor = 0
	p.notice = ""
}

func (p *PendingPanelModel) Hide() {
	p.visible = false
}

func (p *PendingPanelModel) IsVisible() bool {
	return p.visible
}

func (p PendingPanelModel) Update(msg tea.KeyMsg) (PendingPanelModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.Hide()
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			p.notice = ""
		}
	case "down", "j":
		if p.cursor < len(p.keys)-1 {
			p.cursor++
			p.notice = ""
		}
	case "c":
		// Copy the selected pending key's public key. Approval still happens
		// via `sshkey-ctl approve` on the server, so copying the full
		// authorized-keys line is the high-value admin action from here.
		if p.cursor >= 0 && p.cursor < len(p.keys) {
			if pk := strings.TrimSpace(p.keys[p.cursor].PubKey); pk != "" {
				CopyToClipboard(pk)
				p.notice = "Public key copied to clipboard."
			} else {
				p.notice = "No public key stored for this entry."
			}
		}
	}
	return p, nil
}

func (p PendingPanelModel) View(width, height int) string {
	if !p.visible {
		return ""
	}

	innerRows := height - 4 // dialogStyle border + vertical padding.
	if innerRows <= 0 {
		innerRows = 24
	}
	if innerRows < 6 {
		innerRows = 6
	}

	lines := []string{
		searchHeaderStyle.Render(fmt.Sprintf(" Pending Keys (%d)", len(p.keys))),
		"",
	}
	footer := helpDescStyle.Render(" ↑/↓=navigate  c=copy public key  Esc=close")
	if len(p.keys) == 0 {
		lines = append(lines, helpDescStyle.Render("  No pending keys."), "", helpDescStyle.Render(" Esc=close"))
		return dialogStyle.Width(width - 4).Render(strings.Join(clampLines(lines, innerRows), "\n"))
	}

	// Defensive clamp: the list can shrink between renders (a refresh after an
	// approval), so never index past the end.
	cursor := p.cursor
	if cursor >= len(p.keys) {
		cursor = len(p.keys) - 1
	}

	availableBodyRows := innerRows - len(lines) - 1 // reserve footer.
	if availableBodyRows < 1 {
		availableBodyRows = 1
	}
	listRows := availableBodyRows / 2
	if listRows < 1 {
		listRows = 1
	}
	if listRows > 8 {
		listRows = 8
	}
	if listRows > len(p.keys) {
		listRows = len(p.keys)
	}
	start := cursor - listRows/2
	if start < 0 {
		start = 0
	}
	if start+listRows > len(p.keys) {
		start = len(p.keys) - listRows
	}
	if start < 0 {
		start = 0
	}
	end := start + listRows
	if end > len(p.keys) {
		end = len(p.keys)
	}

	// Navigable list — one compact line per pending key: requested name,
	// truncated fingerprint, attempt count. Full details for the selected row
	// render below.
	if start > 0 {
		lines = append(lines, helpDescStyle.Render(fmt.Sprintf("  ... %d more above ...", start)))
	}
	for idx := start; idx < end; idx++ {
		k := p.keys[idx]
		fp := k.Fingerprint
		if len(fp) > 24 {
			fp = fp[:24] + "..."
		}
		attempts := "attempt"
		if k.Attempts != 1 {
			attempts = "attempts"
		}
		name := k.RequestedUsername
		if name == "" {
			name = "(no name)"
		}
		line := fmt.Sprintf("  %-22s  %s  %d %s", truncate(name, 22), fp, k.Attempts, attempts)
		if idx == cursor {
			line = completionSelectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if end < len(p.keys) {
		lines = append(lines, helpDescStyle.Render(fmt.Sprintf("  ... %d more below ...", len(p.keys)-end)))
	}

	// Detail block for the selected key — full fingerprint, the advisory
	// requested display name, seen timestamps, and the wrapped public key.
	sel := p.keys[cursor]
	detail := []string{
		"",
		helpDescStyle.Render("  ── Selected key ──"),
	}

	reqName := sel.RequestedUsername
	if reqName == "" {
		reqName = "(none — connected without a requested name)"
	}
	detail = append(detail,
		fmt.Sprintf("  Requested name: %s", reqName),
		fmt.Sprintf("  Fingerprint:    %s", sel.Fingerprint),
		fmt.Sprintf("  Attempts:       %d", sel.Attempts),
		fmt.Sprintf("  First seen:     %s", shortPendingTime(sel.FirstSeen)),
		fmt.Sprintf("  Last seen:      %s", shortPendingTime(sel.LastSeen)),
	)

	detail = append(detail, "  Public key:")
	inner := width - 8
	if inner < 24 {
		inner = 24
	}
	if pk := strings.TrimSpace(sel.PubKey); pk != "" {
		wrapped := lipgloss.NewStyle().Width(inner).Render(pk)
		for _, ln := range strings.Split(wrapped, "\n") {
			detail = append(detail, "    "+ln)
		}
	} else {
		detail = append(detail, helpDescStyle.Render("    (unavailable — key recorded before the server stored it)"))
	}

	if p.notice != "" {
		detail = append(detail, "", "  "+checkStyle.Render(p.notice))
	}

	detailBudget := innerRows - len(lines) - 1 // reserve footer.
	if detailBudget > 0 {
		if len(detail) > detailBudget {
			if detailBudget == 1 {
				detail = []string{helpDescStyle.Render("  ... selected key details truncated ...")}
			} else {
				detail = append(detail[:detailBudget-1], helpDescStyle.Render("  ... selected key details truncated ..."))
			}
		}
		lines = append(lines, detail...)
	}
	lines = append(lines, footer)

	return dialogStyle.Width(width - 4).Render(strings.Join(clampLines(lines, innerRows), "\n"))
}

func clampLines(lines []string, max int) []string {
	if max < 1 {
		max = 1
	}
	if len(lines) <= max {
		return lines
	}
	return lines[:max]
}

// shortPendingTime trims an ISO-8601 timestamp to "2006-01-02 15:04" for
// compact display. Returns the input unchanged if it's shorter than expected.
func shortPendingTime(ts string) string {
	if len(ts) > 16 {
		ts = ts[:16] // "2006-01-02T15:04"
		ts = strings.Replace(ts, "T", " ", 1)
	}
	return ts
}
