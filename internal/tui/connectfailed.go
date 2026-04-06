package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ConnectFailedModel shows a first-run connection failure overlay with
// guidance about sharing the public key with the server admin.
type ConnectFailedModel struct {
	visible     bool
	errMsg      string
	fingerprint string
	pubKey      string
	copied      bool
}

// ConnectFailedRetryMsg is sent when the user presses r to retry.
type ConnectFailedRetryMsg struct{}

func (c *ConnectFailedModel) Show(errMsg, fingerprint, pubKey string) {
	c.visible = true
	c.errMsg = errMsg
	c.fingerprint = fingerprint
	c.pubKey = pubKey
	c.copied = false
}

func (c *ConnectFailedModel) Hide()          { c.visible = false }
func (c *ConnectFailedModel) IsVisible() bool { return c.visible }

func (c ConnectFailedModel) HandleMouse(msg tea.MouseMsg) (ConnectFailedModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return c, nil
	}
	// Match click target by rendered view content rather than hardcoded Y offsets.
	// dialogStyle: border(1) + padding(1) = content starts at Y=2.
	const contentY = 2
	lineIdx := msg.Y - contentY
	if lineIdx < 0 {
		return c, nil
	}

	view := c.View(80) // width doesn't affect line structure
	lines := strings.Split(view, "\n")
	if lineIdx >= len(lines) {
		return c, nil
	}
	line := lines[lineIdx]

	if strings.Contains(line, "[r]") || strings.Contains(line, "Retry") {
		return c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	}
	if strings.Contains(line, "[c]") || strings.Contains(line, "Copy public key") {
		return c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	}
	if strings.Contains(line, "[q]") || strings.Contains(line, "Quit") {
		return c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	}
	return c, nil
}

func (c ConnectFailedModel) Update(msg tea.KeyMsg) (ConnectFailedModel, tea.Cmd) {
	switch msg.String() {
	case "r":
		c.visible = false
		return c, func() tea.Msg { return ConnectFailedRetryMsg{} }
	case "c":
		if c.pubKey != "" {
			CopyToClipboard(c.pubKey)
			c.copied = true
		}
	case "q", "esc":
		return c, tea.Quit
	}
	return c, nil
}

func (c ConnectFailedModel) View(width int) string {
	if !c.visible {
		return ""
	}

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" Connection Failed"))
	b.WriteString("\n\n")
	b.WriteString("  The server rejected your key.\n")
	b.WriteString("  Your admin may not have added you yet,\n")
	b.WriteString("  or the account may have been retired.\n\n")

	b.WriteString("  Fingerprint:\n")
	b.WriteString("  " + searchHeaderStyle.Render(c.fingerprint) + "\n\n")

	if c.pubKey != "" {
		display := c.pubKey
		if len(display) > 50 {
			display = display[:50] + "..."
		}
		b.WriteString("  Public key:\n")
		b.WriteString("  " + helpDescStyle.Render(display) + "\n\n")
	}

	b.WriteString("  " + searchHeaderStyle.Render("[r]") + " Retry connection\n")
	b.WriteString("  " + searchHeaderStyle.Render("[c]") + " Copy public key to clipboard\n")
	b.WriteString("  " + searchHeaderStyle.Render("[q]") + " Quit\n")

	if c.copied {
		b.WriteString("\n  " + checkStyle.Render("Public key copied to clipboard"))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
