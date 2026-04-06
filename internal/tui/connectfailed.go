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
	// dialogStyle: border(1) + padding(1) = content at Y=2
	// Layout: header(1) + blank(1) + 2 lines text + blank(1) + fingerprint(2) + blank(1)
	// + pubkey(2) + blank(1) = actions at ~Y=13
	lineY := msg.Y - 2
	if lineY >= 11 && lineY <= 11 {
		return c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	}
	if lineY >= 12 && lineY <= 12 {
		return c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	}
	if lineY >= 13 && lineY <= 13 {
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
