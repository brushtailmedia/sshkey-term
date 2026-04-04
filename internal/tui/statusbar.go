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

	statusDisconnected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#EF4444")).Render("●")

	errorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B"))
)

// StatusBarModel manages the bottom status bar.
type StatusBarModel struct {
	username  string
	admin     bool
	connected bool
	errorMsg  string
	errorTime time.Time
}

func NewStatusBar() StatusBarModel {
	return StatusBarModel{}
}

func (s *StatusBarModel) SetUser(username string, admin bool) {
	s.username = username
	s.admin = admin
}

func (s *StatusBarModel) SetConnected(connected bool) {
	s.connected = connected
}

func (s *StatusBarModel) SetError(msg string) {
	s.errorMsg = msg
	s.errorTime = time.Now()
}

func (s StatusBarModel) View(width int) string {
	// Left side: encryption + connection status
	dot := statusDisconnected
	if s.connected {
		dot = statusConnected
	}

	left := statusBarStyle.Render(" E2E encrypted") + " " + dot

	// Right side: user
	right := ""
	if s.username != "" {
		right = s.username
		if s.admin {
			right += " (admin)"
		}
		right = statusBarStyle.Render(right + " ") + statusConnected
	}

	// Error (fades after 5 seconds)
	mid := ""
	if s.errorMsg != "" && time.Since(s.errorTime) < 5*time.Second {
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
