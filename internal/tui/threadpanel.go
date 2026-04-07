package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var threadPanelStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#7C3AED"))

// ThreadPanelModel shows a threaded view — a root message and all replies to it.
type ThreadPanelModel struct {
	visible  bool
	rootID   string            // message ID of the thread root
	messages []DisplayMessage  // root + replies in chronological order
	cursor   int
}

func (t *ThreadPanelModel) IsVisible() bool {
	return t.visible
}

// Show opens the thread panel for a given message. Collects the root
// message and all replies from the message list.
func (t *ThreadPanelModel) Show(rootID string, allMessages []DisplayMessage) {
	t.rootID = rootID
	t.messages = nil
	t.cursor = 0

	// Find the root message
	var root *DisplayMessage
	for i := range allMessages {
		if allMessages[i].ID == rootID {
			root = &allMessages[i]
			break
		}
	}

	if root == nil {
		return
	}

	// Collect: root first, then all messages replying to this root, chronological
	t.messages = append(t.messages, *root)
	for _, msg := range allMessages {
		if msg.ReplyTo == rootID && msg.ID != rootID {
			t.messages = append(t.messages, msg)
		}
	}

	t.visible = true
}

func (t *ThreadPanelModel) Hide() {
	t.visible = false
}

func (t ThreadPanelModel) Update(msg tea.KeyMsg) (ThreadPanelModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "t":
		t.Hide()
		return t, nil
	case "up", "k":
		if t.cursor > 0 {
			t.cursor--
		}
	case "down", "j":
		if t.cursor < len(t.messages)-1 {
			t.cursor++
		}
	case "r":
		// Reply to thread root from within the thread panel
		if len(t.messages) > 0 {
			return t, func() tea.Msg {
				return MessageAction{Action: "reply", Msg: t.messages[0]}
			}
		}
	}
	return t, nil
}

func (t ThreadPanelModel) View(width, height int) string {
	if !t.visible || len(t.messages) == 0 {
		return ""
	}

	var b strings.Builder

	replyCount := len(t.messages) - 1
	header := fmt.Sprintf(" Thread (%d %s)", replyCount, pluralize(replyCount, "reply", "replies"))
	b.WriteString(searchHeaderStyle.Render(header))
	b.WriteString("\n\n")

	visibleHeight := height - 6 // borders + header + footer
	start := 0
	if t.cursor >= 0 && t.cursor >= start+visibleHeight {
		start = t.cursor - visibleHeight + 1
	}

	for i := start; i < len(t.messages) && i < start+visibleHeight; i++ {
		msg := t.messages[i]

		var line string
		if msg.Deleted {
			tombstone := "message deleted"
			if msg.DeletedBy != "" && msg.DeletedBy != msg.FromID {
				tombstone = "message removed by " + msg.DeletedBy
			}
			line = systemMsgStyle.Render(" ── " + tombstone + " ──")
		} else {
			ts := time.Unix(msg.TS, 0).Format("3:04 PM")
			from := usernameStyle.Render(msg.From)
			line = " " + from + "  " + timestampStyle.Render(ts) + "\n"

			body := " " + msg.Body
			if len(body) > width-4 {
				body = body[:width-7] + "..."
			}
			line += body
		}

		if i == 0 {
			// Root message — subtle indicator
			label := "root"
			if msg.Deleted {
				label = "root · deleted"
			}
			line = " " + searchHeaderStyle.Render(label) + "\n" + line
		}

		if i == t.cursor {
			line = selectedMsgStyle.Width(width - 4).Render(line)
		}

		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render(" r=reply  Esc/t=close"))

	return threadPanelStyle.Width(width).Height(height).Render(b.String())
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
