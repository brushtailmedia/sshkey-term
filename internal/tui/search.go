package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

var (
	searchHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C3AED"))

	searchResultStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B"))

	searchMatchStyle = lipgloss.NewStyle().
		Bold(true)
)

// SearchModel manages the search page.
type SearchModel struct {
	visible     bool
	input       textinput.Model
	results     []store.StoredMessage
	cursor      int
	lastQuery   string
	resolveName func(string) string // nanoid → display name
	hasFTS      bool                // true if FTS5 is available
}

func NewSearch() SearchModel {
	ti := textinput.New()
	ti.Placeholder = "Search messages..."
	ti.Prompt = "> "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	return SearchModel{input: ti}
}

func (s *SearchModel) Show() {
	s.visible = true
	s.input.Focus()
	s.results = nil
	s.cursor = 0
	s.lastQuery = ""
	s.input.SetValue("")
}

func (s *SearchModel) Hide() {
	s.visible = false
	s.input.Blur()
}

func (s *SearchModel) IsVisible() bool {
	return s.visible
}

// SetFTS updates whether FTS5 full-text search is available.
func (s *SearchModel) SetFTS(hasFTS bool) {
	s.hasFTS = hasFTS
}

// SelectedResult returns the selected search result (for jump-to-message).
type SearchJumpMsg struct {
	Room         string
	Conversation string
	MessageID    string
}

func (s SearchModel) Update(msg tea.KeyMsg, c *client.Client) (SearchModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.Hide()
		return s, nil
	case "enter":
		if s.cursor < len(s.results) {
			r := s.results[s.cursor]
			return s, func() tea.Msg {
				return SearchJumpMsg{
					Room:         r.Room,
					Conversation: r.Conversation,
					MessageID:    r.ID,
				}
			}
		}
	case "up":
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "down":
		if s.cursor < len(s.results)-1 {
			s.cursor++
		}
		return s, nil
	}

	// Update text input
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)

	// Search on change (debounced by checking if query changed)
	query := strings.TrimSpace(s.input.Value())
	if query != s.lastQuery && len(query) >= 2 && c != nil {
		s.lastQuery = query
		results, err := c.SearchMessages(query, 50)
		if err == nil {
			s.results = results
			s.cursor = 0
		}
	}

	return s, cmd
}

func (s SearchModel) View(width, height int) string {
	if !s.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Search"))
	b.WriteString("\n\n")
	b.WriteString(" " + s.input.View())
	b.WriteString("\n\n")

	if len(s.results) > 0 {
		b.WriteString(searchResultStyle.Render(fmt.Sprintf(" ─── %d results ───", len(s.results))))
		b.WriteString("\n\n")

		visibleResults := height - 8
		if visibleResults < 5 {
			visibleResults = 5
		}

		start := 0
		if s.cursor >= visibleResults {
			start = s.cursor - visibleResults + 1
		}

		for i := start; i < len(s.results) && i < start+visibleResults; i++ {
			r := s.results[i]

			location := r.Room
			if location == "" {
				location = r.Conversation
			}
			ts := time.Unix(r.TS, 0).Format("Jan 2")

			sender := r.Sender
			if s.resolveName != nil {
				sender = s.resolveName(r.Sender)
			}
			header := fmt.Sprintf(" %s in %s · %s", searchMatchStyle.Render(sender), location, ts)
			body := " " + truncate(r.Body, width-4)

			line := header + "\n" + body + "\n"

			if i == s.cursor {
				line = selectedMsgStyle.Width(width - 2).Render(line)
			}

			b.WriteString(line)
		}
	} else if s.lastQuery != "" {
		b.WriteString(searchResultStyle.Render(" No results"))
	}

	if !s.hasFTS {
		b.WriteString("\n")
		b.WriteString(helpDescStyle.Render(" Basic search — build with CGO_CFLAGS=\"-DSQLITE_ENABLE_FTS5\" for better results"))
	}

	// Pad to fill
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < height-2 {
		content += "\n"
		lines++
	}

	return messagesPanelStyle.Width(width).Height(height).Render(content)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
