package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

var (
	usernameStyle = lipgloss.NewStyle().Bold(true)

	timestampStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B"))

	systemMsgStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B")).
		Italic(true)

	replyRefStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748B")).
		Italic(true)

	reactionStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7C3AED"))

	mentionStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7C3AED")).
		Bold(true)

	mentionBorder = lipgloss.NewStyle().
		BorderStyle(lipgloss.Border{Left: "▐"}).
		BorderForeground(lipgloss.Color("#7C3AED"))

	messagesPanelStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B"))

	messagesFocusedStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED"))

	selectedMsgStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#1E1E2E"))
)

// DisplayMessage is a message ready for rendering.
type DisplayMessage struct {
	ID           string
	From         string
	Body         string // decrypted body (or "(encrypted)" if not decryptable)
	TS           int64
	Room         string
	Conversation string
	ReplyTo      string
	Mentions     []string
	Reactions    map[string]int // emoji -> count
	IsSystem     bool
	SystemText   string
}

// MessagesModel manages the message stream.
type MessagesModel struct {
	messages     []DisplayMessage
	room         string
	conversation string
	cursor       int  // selected message index (-1 = none)
	scrollOffset int
	typingUsers  map[string]time.Time // user -> last typing time
	currentUser  string               // for @mention highlighting
}

func NewMessages() MessagesModel {
	return MessagesModel{
		cursor:      -1,
		typingUsers: make(map[string]time.Time),
	}
}

func (m *MessagesModel) SetContext(room, conversation string) {
	m.room = room
	m.conversation = conversation
	m.messages = nil // clear when switching — will be repopulated from local DB later
	m.cursor = -1
	m.scrollOffset = 0
}

func (m *MessagesModel) AddRoomMessage(msg protocol.Message, c *client.Client) {
	if msg.Room != m.room {
		return // not the active room
	}

	body := "(encrypted)"
	replyTo := ""
	var mentions []string

	if c != nil {
		payload, err := c.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
		}
	}

	m.messages = append(m.messages, DisplayMessage{
		ID:       msg.ID,
		From:     msg.From,
		Body:     body,
		TS:       msg.TS,
		Room:     msg.Room,
		ReplyTo:  replyTo,
		Mentions: mentions,
	})
}

func (m *MessagesModel) AddDMMessage(msg protocol.DM, c *client.Client) {
	if msg.Conversation != m.conversation {
		return
	}

	body := "(encrypted)"
	replyTo := ""
	var mentions []string

	if c != nil {
		payload, err := c.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
		}
	}

	m.messages = append(m.messages, DisplayMessage{
		ID:           msg.ID,
		From:         msg.From,
		Body:         body,
		TS:           msg.TS,
		Conversation: msg.Conversation,
		ReplyTo:      replyTo,
		Mentions:     mentions,
	})
}

func (m *MessagesModel) AddSystemMessage(text string) {
	m.messages = append(m.messages, DisplayMessage{
		IsSystem:   true,
		SystemText: text,
		TS:         time.Now().Unix(),
	})
}

func (m *MessagesModel) RemoveMessage(id string) {
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return
		}
	}
}

func (m *MessagesModel) AddReaction(r protocol.Reaction) {
	for i, msg := range m.messages {
		if msg.ID == r.ID {
			if m.messages[i].Reactions == nil {
				m.messages[i].Reactions = make(map[string]int)
			}
			// TODO: decrypt reaction emoji
			m.messages[i].Reactions["?"] ++ // placeholder until decryption
			return
		}
	}
}

func (m *MessagesModel) RemoveReaction(reactionID string) {
	// TODO: track reaction IDs to remove specific reactions
}

func (m *MessagesModel) SetTyping(user, room, conversation string) {
	if room == m.room || conversation == m.conversation {
		m.typingUsers[user] = time.Now()
	}
}

func (m *MessagesModel) SelectedMessage() *DisplayMessage {
	if m.cursor >= 0 && m.cursor < len(m.messages) {
		return &m.messages[m.cursor]
	}
	return nil
}

// MessageAction is returned when the user performs an action on a selected message.
type MessageAction struct {
	Action string // "reply", "delete", "pin", "copy"
	Msg    DisplayMessage
}

func (m MessagesModel) Update(msg tea.KeyMsg) (MessagesModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		} else if m.cursor == -1 && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		}
	case "down", "j":
		if m.cursor < len(m.messages)-1 {
			m.cursor++
		}
	case "r": // reply
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "reply", Msg: *sel}
			}
		}
	case "d": // delete
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "delete", Msg: *sel}
			}
		}
	case "p": // pin
		if sel := m.SelectedMessage(); sel != nil && m.room != "" {
			return m, func() tea.Msg {
				return MessageAction{Action: "pin", Msg: *sel}
			}
		}
	case "c": // copy
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "copy", Msg: *sel}
			}
		}
	case "e": // react (emoji picker)
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "react", Msg: *sel}
			}
		}
	}
	return m, nil
}

func (m MessagesModel) View(width, height int, focused bool) string {
	var b strings.Builder

	// Header
	title := m.room
	if title == "" {
		title = m.conversation
	}
	if title == "" {
		title = "no room selected"
	}

	// Visible messages (bottom-aligned)
	visibleHeight := height - 2 // borders
	start := 0
	if len(m.messages) > visibleHeight {
		start = len(m.messages) - visibleHeight
	}

	for i := start; i < len(m.messages); i++ {
		msg := m.messages[i]

		if msg.IsSystem {
			line := systemMsgStyle.Render(" ── " + msg.SystemText + " ──")
			b.WriteString(line)
		} else {
			ts := time.Unix(msg.TS, 0).Format("3:04 PM")
			header := usernameStyle.Render(msg.From) + "  " + timestampStyle.Render(ts)

			// Highlight @mentions in the body
			body := " " + highlightMentions(msg.Body, m.currentUser)

			// Check if this message mentions the current user
			isMentioned := false
			for _, mention := range msg.Mentions {
				if mention == m.currentUser {
					isMentioned = true
					break
				}
			}

			line := " " + header + "\n" + body
			if isMentioned {
				line = mentionBorder.Render(line)
			}

			if msg.ReplyTo != "" {
				line += "\n " + replyRefStyle.Render("  ↳ re: "+msg.ReplyTo)
			}

			if len(msg.Reactions) > 0 {
				var reactions []string
				for emoji, count := range msg.Reactions {
					reactions = append(reactions, reactionStyle.Render(fmt.Sprintf("%s %d", emoji, count)))
				}
				line += "\n   " + strings.Join(reactions, "  ")
			}

			if i == m.cursor && focused {
				line = selectedMsgStyle.Width(width - 2).Render(line)
			}

			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	// Typing indicator
	var typingNames []string
	cutoff := time.Now().Add(-5 * time.Second)
	for user, t := range m.typingUsers {
		if t.After(cutoff) {
			typingNames = append(typingNames, user)
		}
	}
	if len(typingNames) > 0 {
		typing := strings.Join(typingNames, " and ")
		b.WriteString(systemMsgStyle.Render(fmt.Sprintf(" ── %s is typing... ──", typing)))
		b.WriteString("\n")
	}

	// Pad to height
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < visibleHeight {
		content = "\n" + content
		lines++
	}

	style := messagesPanelStyle
	if focused {
		style = messagesFocusedStyle
	}

	return style.Width(width).Height(height).Render(content)
}

// highlightMentions replaces @username with styled version.
func highlightMentions(body, currentUser string) string {
	if currentUser == "" {
		return body
	}
	// Highlight current user's @mention in accent
	target := "@" + currentUser
	if strings.Contains(body, target) {
		body = strings.ReplaceAll(body, target, mentionStyle.Render(target))
	}
	return body
}
