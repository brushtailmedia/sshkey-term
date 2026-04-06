package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

var (
	memberPanelStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B"))

	memberPanelFocusedStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED"))
)

// MemberPanelModel manages the persistent right-side member panel.
type MemberPanelModel struct {
	visible bool
	members []memberPanelEntry
	cursor  int
	focused bool
}

type memberPanelEntry struct {
	User        string
	DisplayName string
	Online      bool
	Verified    bool
	Retired     bool
}

func NewMemberPanel() MemberPanelModel {
	return MemberPanelModel{}
}

func (m *MemberPanelModel) Toggle() {
	m.visible = !m.visible
}

func (m *MemberPanelModel) IsVisible() bool {
	return m.visible
}

func (m *MemberPanelModel) SetFocused(focused bool) {
	m.focused = focused
}

// Refresh updates the member list for the current room or conversation.
func (m *MemberPanelModel) Refresh(room, conversation string, c *client.Client, online map[string]bool) {
	m.members = nil
	m.cursor = 0

	if c == nil {
		return
	}

	if conversation != "" {
		// DM/group members
		members := c.ConvMembers(conversation)
		for _, user := range members {
			p := c.Profile(user)
			displayName := user
			if p != nil {
				displayName = p.DisplayName
			}
			retired, _ := c.IsRetired(user)
			m.members = append(m.members, memberPanelEntry{
				User:        user,
				DisplayName: displayName,
				Online:      online[user],
				Retired:     retired,
			})
		}
	} else if room != "" {
		// Room members will be populated by SetRoomMembers when the server
		// responds to room_members. The caller sends the request.
	}

	// Check verified status from store
	if c.Store() != nil {
		for i, mem := range m.members {
			_, verified, err := c.Store().GetPinnedKey(mem.User)
			if err == nil {
				m.members[i].Verified = verified
			}
		}
	}
}

// SetRoomMembers populates the member list from a server room_members_list response.
func (m *MemberPanelModel) SetRoomMembers(members []string, c *client.Client, online map[string]bool) {
	m.members = nil
	for _, user := range members {
		p := c.Profile(user)
		displayName := user
		if p != nil {
			displayName = p.DisplayName
		}
		retired, _ := c.IsRetired(user)
		m.members = append(m.members, memberPanelEntry{
			User:        user,
			DisplayName: displayName,
			Online:      online[user],
			Retired:     retired,
		})
	}
	// Update verified status
	if c.Store() != nil {
		for i, mem := range m.members {
			_, verified, err := c.Store().GetPinnedKey(mem.User)
			if err == nil {
				m.members[i].Verified = verified
			}
		}
	}
}

// SelectedUser returns the currently selected member's username.
func (m *MemberPanelModel) SelectedUser() string {
	if m.cursor >= 0 && m.cursor < len(m.members) {
		return m.members[m.cursor].User
	}
	return ""
}

// MemberNames returns all member usernames (for @completion).
func (m *MemberPanelModel) MemberNames() []string {
	names := make([]string, len(m.members))
	for i, mem := range m.members {
		names[i] = mem.User
	}
	return names
}

// MemberEntries returns username + display name pairs for @completion.
func (m *MemberPanelModel) MemberEntries() []MemberEntry {
	entries := make([]MemberEntry, len(m.members))
	for i, mem := range m.members {
		entries[i] = MemberEntry{Username: mem.User, DisplayName: mem.DisplayName}
	}
	return entries
}

func (m MemberPanelModel) Update(msg tea.KeyMsg) (MemberPanelModel, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.members)-1 {
			m.cursor++
		}
	case "enter":
		// Open member menu (keyboard equivalent of right-click on the
		// member). Consistent with Enter on a message opening the
		// context menu.
		if m.cursor < len(m.members) {
			user := m.members[m.cursor].User
			return m, func() tea.Msg {
				return MemberActionMsg{Action: "menu", User: user}
			}
		}
	case "m":
		// Direct message (bypass the menu). Kept as a shortcut for
		// the most common action.
		if m.cursor < len(m.members) {
			user := m.members[m.cursor].User
			return m, func() tea.Msg {
				return MemberActionMsg{Action: "message", User: user}
			}
		}
	}
	return m, nil
}

func (m MemberPanelModel) View(width, height int) string {
	if !m.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(sidebarHeaderStyle.Render(" Members"))
	b.WriteString("\n")

	for i, mem := range m.members {
		dot := offlineDot
		if mem.Online {
			dot = onlineDot
		}

		name := mem.DisplayName
		if mem.Verified {
			name = checkStyle.Render("✓") + " " + name
		}
		if mem.Retired {
			name += " " + helpDescStyle.Render("[retired]")
		}

		line := " " + dot + " " + name

		if i == m.cursor && m.focused {
			line = selectedStyle.Width(width - 2).Render(line)
		}

		b.WriteString(line + "\n")
	}

	// Pad to height
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < height-2 {
		content += "\n"
		lines++
	}

	style := memberPanelStyle
	if m.focused {
		style = memberPanelFocusedStyle
	}

	return style.Width(width).Height(height).Render(content)
}
