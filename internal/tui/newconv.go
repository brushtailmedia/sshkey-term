package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

var (
	dialogStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(1, 2)

	checkStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E"))
)

// NewConvModel manages the new conversation dialog.
type NewConvModel struct {
	visible      bool
	memberInput  textinput.Model
	nameInput    textinput.Model
	allMembers   []string          // all known users (nanoids)
	selected     map[string]bool   // selected members (nanoids)
	suggestions  []string          // filtered member list (nanoids)
	suggCursor   int
	focusName    bool              // true when focus is on the name field
	preselected  []string          // members pre-selected (nanoids)
	resolveName  func(string) string // nanoid → display name (set by App)
}

// CreateConvMsg is sent when the user confirms the dialog.
type CreateConvMsg struct {
	Members []string
	Name    string
}

func NewNewConv() NewConvModel {
	mi := textinput.New()
	mi.Placeholder = "Type to add members..."
	mi.Prompt = ""

	ni := textinput.New()
	ni.Placeholder = "Group name (optional)"
	ni.Prompt = ""

	return NewConvModel{
		memberInput: mi,
		nameInput:   ni,
		selected:    make(map[string]bool),
	}
}

func (n *NewConvModel) Show(allMembers []string, preselected ...string) {
	n.visible = true
	n.allMembers = allMembers
	n.selected = make(map[string]bool)
	n.memberInput.SetValue("")
	n.nameInput.SetValue("")
	n.memberInput.Focus()
	n.focusName = false
	n.suggCursor = 0

	for _, m := range preselected {
		n.selected[m] = true
	}
	n.preselected = preselected
	n.updateSuggestions()
}

func (n *NewConvModel) Hide() {
	n.visible = false
	n.memberInput.Blur()
	n.nameInput.Blur()
}

func (n *NewConvModel) IsVisible() bool {
	return n.visible
}

func (n *NewConvModel) updateSuggestions() {
	query := strings.ToLower(n.memberInput.Value())
	n.suggestions = nil

	for _, m := range n.allMembers {
		if n.selected[m] {
			continue // already selected
		}
		// Match against display name (what user types) not nanoid
		displayName := m
		if n.resolveName != nil {
			displayName = n.resolveName(m)
		}
		if query == "" || strings.Contains(strings.ToLower(displayName), query) {
			n.suggestions = append(n.suggestions, m)
		}
	}
	if n.suggCursor >= len(n.suggestions) {
		n.suggCursor = 0
	}
}

func (n NewConvModel) Update(msg tea.KeyMsg, c *client.Client) (NewConvModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		n.Hide()
		return n, nil

	case "tab":
		n.focusName = !n.focusName
		if n.focusName {
			n.memberInput.Blur()
			n.nameInput.Focus()
		} else {
			n.nameInput.Blur()
			n.memberInput.Focus()
		}
		return n, nil

	case "ctrl+enter":
		return n, n.create(c)

	case "enter":
		if n.focusName {
			// Create on Enter in name field
			return n, n.create(c)
		}
		// Toggle member selection
		if len(n.suggestions) > 0 && n.suggCursor < len(n.suggestions) {
			member := n.suggestions[n.suggCursor]
			n.selected[member] = true
			n.memberInput.SetValue("")
			n.updateSuggestions()
		}
		return n, nil

	case "up":
		if !n.focusName && n.suggCursor > 0 {
			n.suggCursor--
		}
		return n, nil

	case "down":
		if !n.focusName && n.suggCursor < len(n.suggestions)-1 {
			n.suggCursor++
		}
		return n, nil

	case "backspace":
		if !n.focusName && n.memberInput.Value() == "" {
			// Remove last selected member
			var last string
			for m := range n.selected {
				last = m
			}
			if last != "" {
				delete(n.selected, last)
				n.updateSuggestions()
			}
			return n, nil
		}
	}

	// Update the focused input
	var cmd tea.Cmd
	if n.focusName {
		n.nameInput, cmd = n.nameInput.Update(msg)
	} else {
		n.memberInput, cmd = n.memberInput.Update(msg)
		n.updateSuggestions()
	}

	return n, cmd
}

func (n *NewConvModel) create(c *client.Client) tea.Cmd {
	members := n.selectedList()
	if len(members) == 0 {
		return nil
	}

	name := strings.TrimSpace(n.nameInput.Value())

	n.Hide()

	return func() tea.Msg {
		if c != nil {
			c.CreateDM(members, name)
		}
		return CreateConvMsg{Members: members, Name: name}
	}
}

func (n *NewConvModel) selectedList() []string {
	var members []string
	for m := range n.selected {
		members = append(members, m)
	}
	return members
}

func (n NewConvModel) View(width int) string {
	if !n.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render("New Conversation"))
	b.WriteString("\n\n")

	// Selected members
	if len(n.selected) > 0 {
		var tags []string
		for m := range n.selected {
			displayName := m
			if n.resolveName != nil {
				displayName = n.resolveName(m)
			}
			tags = append(tags, checkStyle.Render("✓")+" "+displayName)
		}
		b.WriteString(" " + strings.Join(tags, ", "))
		b.WriteString("\n")
	}

	// Member input
	b.WriteString("\n Add members: " + n.memberInput.View())
	b.WriteString("\n\n")

	// Suggestions
	b.WriteString(" Members:\n")
	for i, m := range n.suggestions {
		if i >= 8 {
			break
		}
		displayName := m
		if n.resolveName != nil {
			displayName = n.resolveName(m)
		}
		prefix := "   "
		if n.selected[m] {
			prefix = " " + checkStyle.Render("✓") + " "
		}
		line := prefix + displayName
		if i == n.suggCursor && !n.focusName {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	// Name field (only show for 2+ members)
	if len(n.selected) > 1 {
		b.WriteString("\n Group name: " + n.nameInput.View())
	}

	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render(" Enter=add  Tab=name field  Ctrl+Enter=create  Esc=cancel"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
