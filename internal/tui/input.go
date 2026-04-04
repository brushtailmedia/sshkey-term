package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

var (
	inputStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B")).
		Padding(0, 1)

	inputFocusedStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(0, 1)
)

// InputModel manages the text input bar.
type InputModel struct {
	textInput textinput.Model
	replyTo   string // message ID being replied to
	replyText string // preview of the message being replied to
}

func NewInput() InputModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = 16000 // under 16KB
	ti.Prompt = "> "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	return InputModel{
		textInput: ti,
	}
}

func (i InputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (i InputModel) Update(msg tea.KeyMsg, c *client.Client, room, conversation string) (InputModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := strings.TrimSpace(i.textInput.Value())
		if text == "" {
			return i, nil
		}

		// Handle slash commands
		if strings.HasPrefix(text, "/") {
			i.handleCommand(text, c, room, conversation)
			i.textInput.Reset()
			i.clearReply()
			return i, nil
		}

		// Send message
		if c != nil {
			if room != "" {
				c.SendRoomMessage(room, text, i.replyTo, nil)
			} else if conversation != "" {
				c.SendDMMessage(conversation, text, i.replyTo, nil)
			}
		}

		i.textInput.Reset()
		i.clearReply()
		return i, nil
	}

	var cmd tea.Cmd
	i.textInput, cmd = i.textInput.Update(msg)
	return i, cmd
}

func (i *InputModel) SetReply(msgID, previewText string) {
	i.replyTo = msgID
	i.replyText = previewText
}

func (i *InputModel) clearReply() {
	i.replyTo = ""
	i.replyText = ""
}

func (i *InputModel) handleCommand(text string, c *client.Client, room, conversation string) {
	parts := strings.SplitN(text, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch cmd {
	case "/typing":
		if c != nil {
			c.SendTyping(room, conversation)
		}
	case "/leave":
		if c != nil && conversation != "" {
			c.CreateDM(nil, "") // TODO: send leave_conversation
		}
	case "/rename":
		if c != nil && conversation != "" && arg != "" {
			// TODO: send rename_conversation
			_ = arg
		}
	}
}

func (i InputModel) View(width int, focused bool) string {
	var b strings.Builder

	if i.replyTo != "" {
		preview := i.replyText
		if len(preview) > width-20 {
			preview = preview[:width-23] + "..."
		}
		b.WriteString(replyRefStyle.Render(" ↳ replying to: " + preview))
		b.WriteString("\n")
	}

	b.WriteString(i.textInput.View())

	style := inputStyle
	if focused {
		style = inputFocusedStyle
	}

	return style.Width(width).Render(b.String())
}
