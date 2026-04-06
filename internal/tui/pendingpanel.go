package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// PendingPanelModel displays the list of pending (unapproved) SSH keys.
// Admin-only overlay, opened via /pending. Read-only — approve/reject is
// handled via sshkey-ctl on the server.
type PendingPanelModel struct {
	visible bool
	keys    []protocol.PendingKeyEntry
	cursor  int
}

func (p *PendingPanelModel) Show(keys []protocol.PendingKeyEntry) {
	p.visible = true
	p.keys = keys
	p.cursor = 0
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
		}
	case "down", "j":
		if p.cursor < len(p.keys)-1 {
			p.cursor++
		}
	}
	return p, nil
}

func (p PendingPanelModel) View(width int) string {
	if !p.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" Pending Keys (%d)", len(p.keys))))
	b.WriteString("\n\n")

	if len(p.keys) == 0 {
		b.WriteString(helpDescStyle.Render("  No pending keys."))
		b.WriteString("\n")
	} else {
		for idx, k := range p.keys {
			// Truncate fingerprint for display (SHA256:xxxx...xxxx)
			fp := k.Fingerprint
			if len(fp) > 24 {
				fp = fp[:24] + "..."
			}

			attempts := "attempt"
			if k.Attempts != 1 {
				attempts = "attempts"
			}

			// Parse and format the timestamp (show date + time)
			ts := k.FirstSeen
			if len(ts) > 16 {
				ts = ts[:16] // "2006-01-02T15:04" from ISO 8601
				ts = strings.Replace(ts, "T", " ", 1)
			}

			line := fmt.Sprintf("  %s  %d %s  %s", fp, k.Attempts, attempts, ts)

			if idx == p.cursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render(" Esc=close"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
