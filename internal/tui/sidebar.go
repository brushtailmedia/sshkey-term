package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

var (
	sidebarStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B"))

	sidebarFocusedStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED"))

	sidebarHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#64748B"))

	selectedStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#7C3AED")).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true)

	unreadStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7C3AED")).
		Bold(true)

	onlineDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("●")
	offlineDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B")).Render("○")
)

// SidebarModel manages the sidebar panel.
type SidebarModel struct {
	rooms         []string
	conversations []protocol.ConversationInfo
	unread        map[string]int    // room/conv -> count
	online        map[string]bool   // user -> online
	retired       map[string]bool   // user -> retired
	cursor        int               // position in the combined list
	selectedRoom  string
	selectedConv  string
	resolveName   func(string) string // nanoid → display name (set by App)

	// For message forwarding (set by App)
	msgCh chan ServerMsg
	errCh chan error
}

func NewSidebar() SidebarModel {
	return SidebarModel{
		unread:  make(map[string]int),
		online:  make(map[string]bool),
		retired: make(map[string]bool),
	}
}

// MarkRetired flags a user as retired. Used to render [retired] on any 1:1
// DM the user is the other party in.
func (s *SidebarModel) MarkRetired(user string) {
	if s.retired == nil {
		s.retired = make(map[string]bool)
	}
	s.retired[user] = true
}

func (s *SidebarModel) SetRooms(rooms []string) {
	s.rooms = rooms
	if s.selectedRoom == "" && len(rooms) > 0 {
		s.selectedRoom = rooms[0]
	}
}

func (s *SidebarModel) SetConversations(convs []protocol.ConversationInfo) {
	s.conversations = convs
}

// AddConversation appends a new conversation if it doesn't already exist.
func (s *SidebarModel) AddConversation(conv protocol.ConversationInfo) {
	for _, c := range s.conversations {
		if c.ID == conv.ID {
			return // already present (dedup)
		}
	}
	s.conversations = append(s.conversations, conv)
}

// RemoveConversation removes a conversation by ID (e.g., after /leave).
func (s *SidebarModel) RemoveConversation(convID string) {
	for i, c := range s.conversations {
		if c.ID == convID {
			s.conversations = append(s.conversations[:i], s.conversations[i+1:]...)
			if s.selectedConv == convID {
				s.selectedConv = ""
			}
			return
		}
	}
}

// RenameConversation updates the display name for a conversation.
func (s *SidebarModel) RenameConversation(convID, name string) {
	for i, c := range s.conversations {
		if c.ID == convID {
			s.conversations[i].Name = name
			return
		}
	}
}

func (s *SidebarModel) SetUnread(room string, count int) {
	s.unread[room] = count
}

func (s *SidebarModel) SetUnreadConv(conv string, count int) {
	s.unread[conv] = count
}

func (s *SidebarModel) SetOnline(user string, online bool) {
	s.online[user] = online
}

func (s *SidebarModel) SelectedRoom() string {
	return s.selectedRoom
}

func (s *SidebarModel) SelectedConv() string {
	return s.selectedConv
}

func (s SidebarModel) totalItems() int {
	return len(s.rooms) + len(s.conversations)
}

func (s SidebarModel) Update(msg tea.KeyMsg, c *client.Client) (SidebarModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		s.updateSelection()
	case "down", "j":
		if s.cursor < s.totalItems()-1 {
			s.cursor++
		}
		s.updateSelection()
	case "enter":
		s.updateSelection()
	}
	return s, nil
}

func (s *SidebarModel) updateSelection() {
	if s.cursor < len(s.rooms) {
		s.selectedRoom = s.rooms[s.cursor]
		s.selectedConv = ""
	} else {
		idx := s.cursor - len(s.rooms)
		if idx < len(s.conversations) {
			s.selectedConv = s.conversations[idx].ID
			s.selectedRoom = ""
		}
	}
}

func (s SidebarModel) View(width, height int, focused bool) string {
	var b strings.Builder

	// Rooms header
	b.WriteString(sidebarHeaderStyle.Render(" Rooms"))
	b.WriteString("\n")

	for i, room := range s.rooms {
		line := " # " + room
		if count, ok := s.unread[room]; ok && count > 0 {
			line += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}

		if i == s.cursor && focused {
			line = selectedStyle.Width(width - 2).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	// Messages header
	b.WriteString("\n")
	b.WriteString(sidebarHeaderStyle.Render(" Messages"))
	b.WriteString("\n")

	for i, conv := range s.conversations {
		name := conv.Name
		if name == "" {
			// Show member display names for unnamed conversations
			var names []string
			for _, m := range conv.Members {
				displayName := m
				if s.resolveName != nil {
					displayName = s.resolveName(m)
				}
				names = append(names, displayName)
			}
			name = strings.Join(names, ", ")
			if len(name) > width-6 {
				name = name[:width-9] + "..."
			}
		}

		dot := offlineDot
		// For 1:1 DMs, show online status of the other user
		otherRetired := false
		if len(conv.Members) == 2 {
			for _, m := range conv.Members {
				if s.online[m] {
					dot = onlineDot
				}
				if s.retired[m] {
					otherRetired = true
				}
			}
		}

		line := " " + dot + " " + name
		if otherRetired {
			line += " " + helpDescStyle.Render("[retired]")
		}
		if count, ok := s.unread[conv.ID]; ok && count > 0 {
			line += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}

		idx := len(s.rooms) + i
		if idx == s.cursor && focused {
			line = selectedStyle.Width(width - 2).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	// Pad to fill height
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < height-2 {
		content += "\n"
		lines++
	}

	style := sidebarStyle
	if focused {
		style = sidebarFocusedStyle
	}

	return style.Width(width).Height(height).Render(content)
}
