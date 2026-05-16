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

// Mouse clicks on this screen are intentionally NOT routed to
// the [r]/[c]/[q] buttons. The screen's primary content is the
// long public-key string the user needs to send out-of-band to
// the server admin; supporting mouse-drag-select for that string
// (as a clipboard fallback when OSC 52 fails — see View() comment)
// requires that mouse-release events on the key text do NOT
// trigger button actions. The three keyboard shortcuts remain
// the canonical (and only) action paths on this dialog.
//
// Click absorption is enforced one level up in App.handleMouse —
// when connectFailed.IsVisible() the handler returns without
// dispatching to any other dialog or panel.

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

	// Frame on what actually failed:
	//   - "account retired": server rejected a retired login.
	//   - transport failure (the TCP dial never reached the
	//     server: connection refused / no such host / timeout /
	//     network unreachable): the connection never got to the
	//     server, so the key was NOT queued for approval — the
	//     "Pending Approval / send your key to the admin" copy
	//     would be actively misleading here.
	//   - everything else: key-authorization rejection (unknown /
	//     pending / blocked fingerprint) — the common new-user
	//     case, framed as Pending Approval. The client can't
	//     distinguish pending from blocked on the wire; both
	//     surface as the same generic rejection.
	// "dial tcp" is Go's net-package prefix for any TCP dial
	// failure and cannot appear in an SSH auth rejection or the
	// server's "account retired" / "key not authorized" strings,
	// so this never mis-frames the legitimate Pending Approval
	// case.
	retired := strings.Contains(c.errMsg, "account retired")
	unreachable := strings.Contains(c.errMsg, "dial tcp") ||
		strings.Contains(c.errMsg, "connection refused") ||
		strings.Contains(c.errMsg, "no such host") ||
		strings.Contains(c.errMsg, "i/o timeout") ||
		strings.Contains(c.errMsg, "network is unreachable") ||
		strings.Contains(c.errMsg, "no route to host")

	var b strings.Builder
	if retired {
		b.WriteString(searchHeaderStyle.Render(" Account Retired"))
		b.WriteString("\n\n")
		b.WriteString("  This account has been retired by the server\n")
		b.WriteString("  operator. Logins are no longer accepted.\n\n")
		b.WriteString("  If you believe this is in error, contact the\n")
		b.WriteString("  server operator out of band.\n\n")
	} else if unreachable {
		b.WriteString(searchHeaderStyle.Render(" Cannot Reach Server"))
		b.WriteString("\n\n")
		b.WriteString("  The connection attempt failed before the\n")
		b.WriteString("  server could respond — your key was NOT sent\n")
		b.WriteString("  or queued for approval.\n\n")
		b.WriteString("  Likely causes: the server isn't running, the\n")
		b.WriteString("  host or port is wrong, or a network/firewall\n")
		b.WriteString("  issue.\n\n")
		b.WriteString("  Error:\n")
		b.WriteString("  " + helpDescStyle.Render(c.errMsg) + "\n\n")
		b.WriteString("  Press [r] to retry once it's reachable.\n\n")
	} else {
		b.WriteString(searchHeaderStyle.Render(" Pending Approval"))
		b.WriteString("\n\n")
		b.WriteString("  Your key isn't authorized on this server yet.\n")
		b.WriteString("  Your fingerprint has been added to the server's\n")
		b.WriteString("  pending-keys queue. Send your public key (below)\n")
		b.WriteString("  to the server operator and ask them to approve it.\n\n")
		b.WriteString("  Once approved, press [r] to retry.\n\n")
	}

	// Fingerprint + public key + [c] are only meaningful when the
	// server actually received the connection and the user needs
	// to send their key to the admin. For an unreachable server
	// the connection never arrived — showing/copying the key would
	// be the same misleading "send your key" affordance the header
	// fix removes, so omit it and keep the screen on the diagnostic.
	if !unreachable {
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
	}

	b.WriteString("  " + searchHeaderStyle.Render("[r]") + " Retry connection\n")
	if !unreachable {
		b.WriteString("  " + searchHeaderStyle.Render("[c]") + " Copy public key to clipboard\n")
	}
	b.WriteString("  " + searchHeaderStyle.Render("[q]") + " Quit\n")

	if c.copied {
		b.WriteString("\n  " + checkStyle.Render("Public key copied to clipboard"))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
