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

	// The server returns "account retired" for retired logins and
	// "key not authorized" for everything else (unknown / pending /
	// blocked fingerprints). The client can't distinguish pending
	// from blocked on the wire — both surface as the same generic
	// rejection — so we frame the common case (new user, queued for
	// approval) and branch separately for the retired case where
	// retry will not help.
	retired := strings.Contains(c.errMsg, "account retired")

	var b strings.Builder
	if retired {
		b.WriteString(searchHeaderStyle.Render(" Account Retired"))
		b.WriteString("\n\n")
		b.WriteString("  This account has been retired by the server\n")
		b.WriteString("  operator. Logins are no longer accepted.\n\n")
		b.WriteString("  If you believe this is in error, contact the\n")
		b.WriteString("  server operator out of band.\n\n")
	} else {
		b.WriteString(searchHeaderStyle.Render(" Pending Approval"))
		b.WriteString("\n\n")
		b.WriteString("  Your key isn't authorized on this server yet.\n")
		b.WriteString("  Your fingerprint has been added to the server's\n")
		b.WriteString("  pending-keys queue. Send your public key (below)\n")
		b.WriteString("  to the server operator and ask them to approve it.\n\n")
		b.WriteString("  Once approved, press [r] to retry.\n\n")
	}

	b.WriteString("  Fingerprint:\n")
	b.WriteString("  " + searchHeaderStyle.Render(c.fingerprint) + "\n\n")

	if c.pubKey != "" {
		// Render the FULL key — lipgloss wraps to the dialog width.
		// Truncating with "..." was hostile UX: if the OSC 52 copy
		// fails (common in tmux without passthrough or in terminals
		// that don't support OSC 52), the user has nothing to fall
		// back on. Showing the full key lets them mouse-select-copy
		// or transcribe manually as a last resort.
		b.WriteString("  Public key:\n")
		b.WriteString("  " + helpDescStyle.Render(c.pubKey) + "\n\n")
	}

	b.WriteString("  " + searchHeaderStyle.Render("[r]") + " Retry connection\n")
	b.WriteString("  " + searchHeaderStyle.Render("[c]") + " Copy public key to clipboard\n")
	b.WriteString("  " + searchHeaderStyle.Render("[q]") + " Quit\n")

	if c.copied {
		b.WriteString("\n  " + checkStyle.Render("Public key copied to clipboard"))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
