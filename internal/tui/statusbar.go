package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/keygen"
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

	// Phase 16 Gap 4 live strength hint palette (Phase 18 doc sync
	// note: used by wizard.go and addserver.go for the one-line
	// indicator under the passphrase input field):
	//   - strengthHintBlockStyle: red — hard reject on submit
	//   - strengthHintWarnStyle: amber — borderline, requires
	//     press-Enter-again confirmation on submit
	//   - strengthHintPassStyle: green dim — passes silently
	strengthHintBlockStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#EF4444"))
	strengthHintWarnStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B"))
	strengthHintPassStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E"))
)

// renderStrengthHint formats a keygen.LiveHint for display under a
// passphrase input field. Returns an empty string for HintHidden so
// the caller can concatenate unconditionally without testing the
// tier. Used by wizard.go viewKeyGenerate and addserver.go viewGen.
func renderStrengthHint(h keygen.LiveHint) string {
	switch h.Tier {
	case keygen.HintBlock:
		return strengthHintBlockStyle.Render(h.Text)
	case keygen.HintWarn:
		return strengthHintWarnStyle.Render(h.Text)
	case keygen.HintPass:
		return strengthHintPassStyle.Render(h.Text)
	default:
		return ""
	}
}

// StatusBarModel manages the bottom status bar.
type StatusBarModel struct {
	username      string
	admin         bool
	hasPending    bool
	connected     bool
	reconnecting  bool
	reconnAttempt int
	errorMsg      string

	// refreshingUntil (Phase 17c Step 6) holds the earliest time at
	// which the "refreshing…" keypress-ack indicator may disappear.
	// Set by SetRefreshing(d) to now+d so the indicator stays visible
	// for AT LEAST d regardless of how fast the server responds.
	// Cleared inspection is driven by View rendering; no explicit
	// timer is needed — a tea.Tick after d triggers a repaint.
	refreshingUntil time.Time
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

// SetRefreshing shows the "refreshing…" keypress-ack indicator for at
// least minDur. If the server responds within minDur the indicator
// still remains visible until the timer elapses — prevents flicker
// from fast server responses and gives the user visual confirmation
// that their keypress registered.
//
// Called from the TUI's refresh-key handlers. Pair with
// ClearRefreshing() on response arrival; ClearRefreshing is a hint
// that the indicator CAN hide (it will respect the minDur floor set
// here).
//
// Phase 17c Step 6.
func (s *StatusBarModel) SetRefreshing(minDur time.Duration) {
	target := time.Now().Add(minDur)
	if target.After(s.refreshingUntil) {
		s.refreshingUntil = target
	}
}

// ClearRefreshing signals that the refresh operation completed (e.g.
// room_members_list or device_list arrived, or an error came back).
// If the minimum-duration window from SetRefreshing has elapsed the
// indicator hides on next View; otherwise the indicator remains
// visible until the floor is reached.
//
// Currently a no-op because the "clear" semantics are handled
// entirely by the time.Now() check in View — once minDur has elapsed,
// View no longer renders the indicator regardless of whether
// ClearRefreshing was called. Kept as an explicit API so callers
// document their intent at the receive site (future polish could
// make this tighten the floor to "hide now if possible").
//
// Phase 17c Step 6.
func (s *StatusBarModel) ClearRefreshing() {
	// Intentional no-op — see doc comment. The View method's
	// time-based check handles visibility transparently.
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
	// OR refreshing indicator (takes precedence over refreshing — an
	// error is the more important signal when both are true).
	mid := ""
	if s.errorMsg != "" {
		mid = errorStyle.Render("  ⚠ " + s.errorMsg)
	} else if time.Now().Before(s.refreshingUntil) {
		// Phase 17c Step 6: keypress-ack indicator. Minimum 200ms
		// visibility is enforced by SetRefreshing's floor.
		mid = statusBarStyle.Render("  refreshing…")
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
