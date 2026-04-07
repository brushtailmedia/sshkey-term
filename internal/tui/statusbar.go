package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	statusBarStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B"))

	statusConnected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E")).Render("●")

	statusReconnecting = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B")).Render("●")

	statusDisconnected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#EF4444")).Render("●")

	errorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B"))
)

// StatusBarModel manages the bottom status bar.
type StatusBarModel struct {
	username     string
	admin        bool
	hasPending   bool
	connected    bool
	reconnecting bool
	reconnAttempt int
	errorMsg     string
}

func NewStatusBar() StatusBarModel {
	return StatusBarModel{}
}

func (s *StatusBarModel) SetUser(username string, admin bool) {
	s.username = username
	s.admin = admin
}

func (s *StatusBarModel) SetPending(has bool) {
	s.hasPending = has
}

func (s *StatusBarModel) SetConnected(connected bool) {
	s.connected = connected
	if connected {
		s.reconnecting = false
	}
}

func (s *StatusBarModel) SetReconnecting(attempt int, nextRetry time.Duration) {
	s.reconnecting = true
	s.reconnAttempt = attempt
	s.connected = false
}

func (s *StatusBarModel) SetError(msg string) {
	s.errorMsg = msg
}

func (s *StatusBarModel) ClearError() {
	s.errorMsg = ""
}

func (s StatusBarModel) View(width int) string {
	// Left side: encryption + connection status
	dot := statusDisconnected
	connLabel := ""
	if s.connected {
		dot = statusConnected
	} else if s.reconnecting {
		dot = statusReconnecting
		connLabel = fmt.Sprintf(" (reconnecting #%d)", s.reconnAttempt)
	} else {
		connLabel = " (offline)"
	}

	left := statusBarStyle.Render(" E2E encrypted") + " " + dot + statusBarStyle.Render(connLabel)

	// Right side: user
	right := ""
	if s.username != "" {
		right = s.username
		if s.admin {
			right += " (admin)"
			if s.hasPending {
				right += " " + statusReconnecting + statusBarStyle.Render(" pending")
			}
		}
		right = statusBarStyle.Render(right + " ") + statusConnected
	}

	// Error (persists until next user action clears it)
	mid := ""
	if s.errorMsg != "" {
		mid = errorStyle.Render("  ⚠ " + s.errorMsg)
	}

	// Pad to width
	leftLen := lipgloss.Width(left)
	midLen := lipgloss.Width(mid)
	rightLen := lipgloss.Width(right)
	padding := width - leftLen - midLen - rightLen
	if padding < 1 {
		padding = 1
	}

	return left + mid + fmt.Sprintf("%*s", padding, "") + right
}
