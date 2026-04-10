package tui

import (
	"strings"
	"time"

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
	textInput      textinput.Model
	replyTo        string           // message ID being replied to
	replyText      string           // preview of the message being replied to
	lastTypingSent time.Time        // throttle typing indicators
	completion     *CompletionModel  // active completion popup
	members        []MemberEntry    // current room/group members for @completion
	pendingCmd     *SlashCommandMsg // slash command needing app-level handling
	didSend        bool             // true after a message was sent (cleared by DidSend)
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

func (i InputModel) Update(msg tea.KeyMsg, c *client.Client, room, group, dm string) (InputModel, tea.Cmd) {
	// Handle completion popup if active
	if i.completion != nil && i.completion.visible {
		switch msg.String() {
		case "tab", "enter":
			// Accept completion
			sel := i.completion.Selected()
			text := i.textInput.Value()
			// Replace the prefix with the completion
			pos := strings.LastIndex(text, i.completion.prefix)
			if pos >= 0 {
				newText := text[:pos] + sel.Text
				i.textInput.SetValue(newText)
				i.textInput.SetCursor(len(newText))
			}
			i.completion = nil
			return i, nil
		case "up":
			i.completion.Prev()
			return i, nil
		case "down":
			i.completion.Next()
			return i, nil
		case "esc":
			i.completion = nil
			return i, nil
		}
		// Any other key dismisses completion and falls through
		i.completion = nil
	}

	switch msg.String() {
	case "tab":
		// Trigger completion
		text := i.textInput.Value()
		pos := i.textInput.Position()
		i.completion = Complete(text, pos, i.members)
		return i, nil
	case "enter":
		text := strings.TrimSpace(i.textInput.Value())
		if text == "" {
			return i, nil
		}

		// Handle slash commands
		if strings.HasPrefix(text, "/") {
			i.handleCommand(text, c, room, group, dm)
			i.textInput.Reset()
			i.clearReply()
			i.didSend = true
			return i, nil
		}

		// Send message — extract @mentions from body
		if c != nil {
			mentions := i.ExtractMentions(text)
			if room != "" {
				c.SendRoomMessage(room, text, i.replyTo, mentions)
			} else if group != "" {
				c.SendGroupMessage(group, text, i.replyTo, mentions)
			} else if dm != "" {
				c.SendDMMessage(dm, text, i.replyTo, mentions)
			}
			i.didSend = true
		}

		i.textInput.Reset()
		i.clearReply()
		return i, nil
	}

	// Send typing indicator (throttled to 1 per second)
	if c != nil && time.Since(i.lastTypingSent) > time.Second {
		text := i.textInput.Value()
		if len(text) > 0 && !strings.HasPrefix(text, "/") {
			c.SendTyping(room, group, dm)
			i.lastTypingSent = time.Now()
		}
	}

	var cmd tea.Cmd
	i.textInput, cmd = i.textInput.Update(msg)
	return i, cmd
}

func (i *InputModel) SetReply(msgID, previewText string) {
	i.replyTo = msgID
	i.replyText = previewText
}

// Value returns the current input text. Used by the app to inspect input
// (e.g., to check if a slash command is pending before allowing send).
func (i InputModel) Value() string {
	return i.textInput.Value()
}

func (i *InputModel) clearReply() {
	i.replyTo = ""
	i.replyText = ""
}

// SlashCommandMsg is sent to the app when the user types a slash command that needs app-level handling.
type SlashCommandMsg struct {
	Command string
	Arg     string
	Room    string
	Group   string
	DM      string
}

func (i *InputModel) handleCommand(text string, c *client.Client, room, group, dm string) {
	parts := strings.SplitN(text, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch cmd {
	case "/typing":
		if c != nil {
			c.SendTyping(room, group, dm)
		}
	case "/leave":
		// Route to the app — it will show the confirmation dialog and
		// send the leave_group / leave_room message on confirm. /leave
		// is valid in group DM and room contexts. 1:1 DMs reject with a
		// status bar message pointing the user at /delete.
		if group != "" {
			i.pendingCmd = &SlashCommandMsg{Command: cmd, Arg: arg, Room: room, Group: group}
		} else if room != "" {
			i.pendingCmd = &SlashCommandMsg{Command: cmd, Arg: arg, Room: room}
		} else if dm != "" {
			// 1:1 DMs don't have /leave — route to app for the rejection message
			i.pendingCmd = &SlashCommandMsg{Command: "/leave_dm_rejected", DM: dm}
		}
	case "/delete":
		// Context-aware delete. All three contexts (1:1 DM, group DM,
		// room) are wired end-to-end: the app layer opens a confirmation
		// dialog and, on confirm, sends the appropriate delete verb
		// (leave_dm for 1:1, delete_group for groups, delete_room for
		// rooms) and waits for the server echo before touching local
		// state. Room /delete works for both active and retired rooms;
		// the dialog wording changes based on IsRoomRetired.
		i.pendingCmd = &SlashCommandMsg{Command: cmd, Arg: arg, Room: room, Group: group, DM: dm}
	case "/rename":
		if c != nil && group != "" && arg != "" {
			c.Enc().Encode(map[string]string{
				"type": "rename_group", "group": group, "name": arg,
			})
		}
	case "/mute":
		// Handled via info panel toggle — just set a flag
	case "/verify", "/unverify", "/search", "/settings", "/help", "/pending", "/mykey":
		// These need to be handled at the app level
		i.pendingCmd = &SlashCommandMsg{Command: cmd, Arg: arg, Room: room, Group: group, DM: dm}
	case "/upload":
		if arg != "" {
			i.pendingCmd = &SlashCommandMsg{Command: cmd, Arg: arg, Room: room, Group: group, DM: dm}
		}
	}
}

// PendingCommand returns and clears a pending slash command that needs app-level handling.
func (i *InputModel) PendingCommand() *SlashCommandMsg {
	cmd := i.pendingCmd
	i.pendingCmd = nil
	return cmd
}

// DidSend returns true if a message or command was sent during the last Update.
// Resets the flag on read.
func (i *InputModel) DidSend() bool {
	sent := i.didSend
	i.didSend = false
	return sent
}

// MemberEntry holds a user ID (nanoid) and display name for @completion.
type MemberEntry struct {
	UserID      string // nanoid — sent in protocol mentions array
	DisplayName string // human-visible — shown in completion popup + body
}

// SetMembers updates the member list for @completion.
func (i *InputModel) SetMembers(members []MemberEntry) {
	i.members = members
}

// ExtractMentions scans the message body for @displayName patterns and
// returns the corresponding nanoid usernames for the protocol mentions array.
// Only matches when the @ is at a word boundary (start of string or after whitespace).
func (i *InputModel) ExtractMentions(body string) []string {
	var mentions []string
	seen := make(map[string]bool)
	for _, m := range i.members {
		if seen[m.UserID] {
			continue
		}
		target := "@" + m.DisplayName
		if containsMention(body, target) {
			mentions = append(mentions, m.UserID)
			seen[m.UserID] = true
		}
	}
	return mentions
}

// containsMention checks if body contains target at a word boundary.
// The @ must be at the start of the string or preceded by whitespace.
func containsMention(body, target string) bool {
	idx := 0
	for {
		pos := strings.Index(body[idx:], target)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		// Check word boundary: @ must be at start or after whitespace
		if absPos == 0 || body[absPos-1] == ' ' || body[absPos-1] == '\n' || body[absPos-1] == '\t' {
			// Check trailing boundary: must end at string end or non-alphanumeric
			end := absPos + len(target)
			if end >= len(body) || body[end] == ' ' || body[end] == '\n' || body[end] == '\t' || body[end] == ',' || body[end] == '.' || body[end] == '!' || body[end] == '?' || body[end] == ':' || body[end] == ';' {
				return true
			}
		}
		idx = absPos + 1
		if idx >= len(body) {
			return false
		}
	}
}

func (i InputModel) View(width int, focused bool) string {
	var b strings.Builder

	// Completion popup (above input)
	if i.completion != nil && i.completion.visible {
		b.WriteString(i.completion.View(width))
		b.WriteString("\n")
	}

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
