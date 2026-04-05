package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
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
	// ReactionsByUser tracks all reaction_ids per (user, emoji) on this message.
	// user -> emoji -> []reaction_id. Display count = distinct users per emoji.
	// The current user's own entries are used for the "Remove my reaction" UX.
	ReactionsByUser map[string]map[string][]string
	Attachments     []DisplayAttachment
	IsSystem        bool
	SystemText      string
}

// DisplayReactions returns the emoji→count map for rendering, counting the
// number of distinct users per emoji (not reaction events).
func (d *DisplayMessage) DisplayReactions() map[string]int {
	if d.ReactionsByUser == nil {
		return nil
	}
	counts := make(map[string]int)
	for _, byEmoji := range d.ReactionsByUser {
		for emoji, ids := range byEmoji {
			if len(ids) > 0 {
				counts[emoji]++
			}
		}
	}
	return counts
}

// UserHasReacted reports whether the given user has at least one reaction
// with the given emoji on this message.
func (d *DisplayMessage) UserHasReacted(user, emoji string) bool {
	if d.ReactionsByUser == nil {
		return false
	}
	return len(d.ReactionsByUser[user][emoji]) > 0
}

// UserReactionIDs returns the reaction_ids for the given user and emoji
// (used to send unreact). Returns nil if the user hasn't reacted with this
// emoji.
func (d *DisplayMessage) UserReactionIDs(user, emoji string) []string {
	if d.ReactionsByUser == nil {
		return nil
	}
	return d.ReactionsByUser[user][emoji]
}

// UserEmojis returns the set of distinct emojis the given user has reacted
// with on this message.
func (d *DisplayMessage) UserEmojis(user string) []string {
	if d.ReactionsByUser == nil {
		return nil
	}
	var emojis []string
	for emoji, ids := range d.ReactionsByUser[user] {
		if len(ids) > 0 {
			emojis = append(emojis, emoji)
		}
	}
	return emojis
}

type DisplayAttachment struct {
	FileID    string
	Name      string
	Size      int64
	Mime      string
	IsImage   bool
	LocalPath string // set after download
}

// MessagesModel manages the message stream.
type MessagesModel struct {
	messages       []DisplayMessage
	room           string
	conversation   string
	cursor         int  // selected message index (-1 = none)
	scrollOffset   int
	typingUsers    map[string]time.Time // user -> last typing time
	currentUser    string               // for @mention highlighting
	loadingHistory bool
	hasMore        bool              // server indicated more history available
	unreadFromID   string            // first unread message ID (for divider)
	retired        map[string]bool   // username -> account retired
}

func NewMessages() MessagesModel {
	return MessagesModel{
		cursor:      -1,
		typingUsers: make(map[string]time.Time),
		hasMore:     true,
		retired:     make(map[string]bool),
	}
}

// SetRetired updates the set of known-retired users. Called by the app when
// user_retired / retired_users events arrive. Used by View() to append a
// [retired] marker next to historical sender names.
func (m *MessagesModel) SetRetired(users map[string]string) {
	m.retired = make(map[string]bool, len(users))
	for user := range users {
		m.retired[user] = true
	}
}

// MarkRetired adds a single user to the retired set (on user_retired event).
func (m *MessagesModel) MarkRetired(user string) {
	if m.retired == nil {
		m.retired = make(map[string]bool)
	}
	m.retired[user] = true
}

func (m *MessagesModel) SetContext(room, conversation string) {
	m.room = room
	m.conversation = conversation
	m.messages = nil
	m.cursor = -1
	m.scrollOffset = 0
	m.unreadFromID = ""
}

// SetUnreadFrom sets the first unread message ID for the divider.
func (m *MessagesModel) SetUnreadFrom(msgID string) {
	m.unreadFromID = msgID
}

// LoadFromDB populates the message list from the local DB.
func (m *MessagesModel) LoadFromDB(c *client.Client) {
	if c == nil {
		return
	}

	var stored []storeMsg
	var err error

	if m.room != "" {
		stored, err = loadRoom(c, m.room)
	} else if m.conversation != "" {
		stored, err = loadConv(c, m.conversation)
	}

	if err != nil || len(stored) == 0 {
		return
	}

	m.messages = nil
	for _, s := range stored {
		m.messages = append(m.messages, DisplayMessage{
			ID:           s.ID,
			From:         s.Sender,
			Body:         s.Body,
			TS:           s.TS,
			Room:         s.Room,
			Conversation: s.Conversation,
			ReplyTo:      s.ReplyTo,
			Mentions:     s.Mentions,
		})
	}
}

type storeMsg = store.StoredMessage

func loadRoom(c *client.Client, room string) ([]store.StoredMessage, error) {
	return c.LoadRoomMessages(room, 200)
}

func loadConv(c *client.Client, conv string) ([]store.StoredMessage, error) {
	return c.LoadConvMessages(conv, 200)
}

// requestHistory sends a history request for older messages.
func (m *MessagesModel) requestHistory() tea.Cmd {
	if !m.hasMore || m.loadingHistory || len(m.messages) == 0 {
		return nil
	}

	firstMsg := m.messages[0]
	room := m.room
	conv := m.conversation
	beforeID := firstMsg.ID

	m.loadingHistory = true

	return func() tea.Msg {
		return HistoryRequestMsg{
			Room:         room,
			Conversation: conv,
			BeforeID:     beforeID,
		}
	}
}

// LatestMessageID returns the ID of the most recent message, or empty if none.
func (m *MessagesModel) LatestMessageID() string {
	if len(m.messages) == 0 {
		return ""
	}
	// Find the latest non-system message
	for i := len(m.messages) - 1; i >= 0; i-- {
		if !m.messages[i].IsSystem && m.messages[i].ID != "" {
			return m.messages[i].ID
		}
	}
	return ""
}

// PrependMessages adds older messages at the top (from history response).
func (m *MessagesModel) PrependMessages(msgs []DisplayMessage, hasMore bool) {
	m.messages = append(msgs, m.messages...)
	m.cursor += len(msgs) // keep cursor on the same message
	m.loadingHistory = false
	m.hasMore = hasMore
}

func (m *MessagesModel) AddRoomMessage(msg protocol.Message, c *client.Client) {
	if msg.Room != m.room {
		return // not the active room
	}

	body := "(encrypted)"
	replyTo := ""
	var mentions []string

	var attachments []DisplayAttachment

	if c != nil {
		payload, err := c.DecryptRoomMessage(msg.Room, msg.Epoch, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
			for _, a := range payload.Attachments {
				attachments = append(attachments, DisplayAttachment{
					FileID:  a.FileID,
					Name:    a.Name,
					Size:    a.Size,
					Mime:    a.Mime,
					IsImage: isImageMime(a.Mime),
				})
			}
		}
	}

	m.messages = append(m.messages, DisplayMessage{
		ID:          msg.ID,
		From:        msg.From,
		Body:        body,
		TS:          msg.TS,
		Room:        msg.Room,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
}

func (m *MessagesModel) AddDMMessage(msg protocol.DM, c *client.Client) {
	if msg.Conversation != m.conversation {
		return
	}

	body := "(encrypted)"
	replyTo := ""
	var mentions []string
	var attachments []DisplayAttachment

	if c != nil {
		payload, err := c.DecryptDMMessage(msg.WrappedKeys, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
			for _, a := range payload.Attachments {
				attachments = append(attachments, DisplayAttachment{
					FileID:  a.FileID,
					Name:    a.Name,
					Size:    a.Size,
					Mime:    a.Mime,
					IsImage: isImageMime(a.Mime),
				})
			}
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
		Attachments:  attachments,
	})
}

// isImageMime returns true for image mime types.
func isImageMime(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
		return true
	}
	return false
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
	// Legacy — use AddReactionDecrypted instead. Records with "?" as the
	// emoji since decryption info isn't available here.
	m.addReactionRecord(r.ID, r.ReactionID, r.User, "?")
}

// AddReactionDecrypted decrypts the reaction and adds it to the target message.
func (m *MessagesModel) AddReactionDecrypted(r protocol.Reaction, c *client.Client) {
	if c == nil {
		m.AddReaction(r)
		return
	}

	var emoji string

	if r.Room != "" {
		// Room reaction — decrypt with epoch key
		dr, err := c.DecryptRoomReaction(r.Room, r.Epoch, r.Payload)
		if err == nil {
			emoji = dr.Emoji
			// Verify target matches envelope
			if dr.Target != r.ID {
				return // server tampering — reaction re-targeted
			}
		}
	} else if r.Conversation != "" {
		// DM reaction — decrypt with per-message key
		dr, err := c.DecryptDMReaction(r.WrappedKeys, r.Payload)
		if err == nil {
			emoji = dr.Emoji
			if dr.Target != r.ID {
				return
			}
		}
	}

	if emoji == "" {
		emoji = "?"
	}

	m.addReactionRecord(r.ID, r.ReactionID, r.User, emoji)
}

// addReactionRecord is the shared path for storing an incoming reaction.
// Updates both the per-message ReactionsByUser index and the package-level
// tracker used by RemoveReaction.
func (m *MessagesModel) addReactionRecord(msgID, reactionID, user, emoji string) {
	reactionTracker[reactionID] = reactionMeta{msgID: msgID, user: user, emoji: emoji}
	for i, msg := range m.messages {
		if msg.ID != msgID {
			continue
		}
		if m.messages[i].ReactionsByUser == nil {
			m.messages[i].ReactionsByUser = make(map[string]map[string][]string)
		}
		byUser := m.messages[i].ReactionsByUser
		if byUser[user] == nil {
			byUser[user] = make(map[string][]string)
		}
		byUser[user][emoji] = append(byUser[user][emoji], reactionID)
		return
	}
}

// reactionMeta records everything needed to undo a reaction.
type reactionMeta struct {
	msgID string
	user  string
	emoji string
}

// reactionTracker maps reaction_id -> metadata, package-level so lookups
// work across message model instances. Cleared entries on reaction_removed.
var reactionTracker = make(map[string]reactionMeta)

func (m *MessagesModel) RemoveReaction(reactionID string) {
	tracked, ok := reactionTracker[reactionID]
	if !ok {
		return
	}
	delete(reactionTracker, reactionID)

	for i, msg := range m.messages {
		if msg.ID != tracked.msgID || m.messages[i].ReactionsByUser == nil {
			continue
		}
		byEmoji := m.messages[i].ReactionsByUser[tracked.user]
		if byEmoji == nil {
			return
		}
		ids := byEmoji[tracked.emoji]
		// Remove the specific reactionID from the slice
		for j, id := range ids {
			if id == reactionID {
				ids = append(ids[:j], ids[j+1:]...)
				break
			}
		}
		if len(ids) == 0 {
			delete(byEmoji, tracked.emoji)
			if len(byEmoji) == 0 {
				delete(m.messages[i].ReactionsByUser, tracked.user)
			}
		} else {
			byEmoji[tracked.emoji] = ids
		}
		return
	}
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
	Action string // "reply", "delete", "pin", "copy", "react", "unreact", ...
	Msg    DisplayMessage
	Data   string // optional payload (e.g., emoji for unreact)
}

// HistoryRequestMsg is sent when the user scrolls to the top and needs older messages.
type HistoryRequestMsg struct {
	Room         string
	Conversation string
	BeforeID     string
}

func (m MessagesModel) Update(msg tea.KeyMsg) (MessagesModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			// At the top — request history
			if m.cursor == 0 && len(m.messages) > 0 && !m.loadingHistory {
				return m, m.requestHistory()
			}
		} else if m.cursor == -1 && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		}
	case "pageup":
		// Jump up a page and request history if near top
		m.cursor -= 20
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor == 0 && len(m.messages) > 0 && !m.loadingHistory {
			return m, m.requestHistory()
		}
		return m, nil
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
	case "o": // open attachment
		if sel := m.SelectedMessage(); sel != nil && len(sel.Attachments) > 0 {
			return m, func() tea.Msg {
				return MessageAction{Action: "open_attachment", Msg: *sel}
			}
		}
	case "s": // save attachment
		if sel := m.SelectedMessage(); sel != nil && len(sel.Attachments) > 0 {
			return m, func() tea.Msg {
				return MessageAction{Action: "save_attachment", Msg: *sel}
			}
		}
	case "e": // react (emoji picker)
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "react", Msg: *sel}
			}
		}
	case "u": // unreact — remove one of current user's reactions
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				// Empty Data means "pick first emoji user has reacted with";
				// app handler resolves to the specific reaction_id.
				return MessageAction{Action: "unreact", Msg: *sel}
			}
		}
	case "enter": // open context menu on selected message (keyboard path)
		if sel := m.SelectedMessage(); sel != nil {
			return m, func() tea.Msg {
				return MessageAction{Action: "open_menu", Msg: *sel}
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

	unreadShown := false
	prevSender := ""
	prevTS := int64(0)
	for i := start; i < len(m.messages); i++ {
		msg := m.messages[i]

		// Unread divider
		if !unreadShown && m.unreadFromID != "" && msg.ID == m.unreadFromID {
			divider := systemMsgStyle.Render(" ── new messages ──────────────────────────")
			b.WriteString(divider + "\n")
			unreadShown = true
		}

		if msg.IsSystem {
			line := systemMsgStyle.Render(" ── " + msg.SystemText + " ──")
			b.WriteString(line)
			prevSender = ""
			prevTS = 0
		} else {
			// Group consecutive messages from the same sender within 5 minutes
			showHeader := true
			if msg.From == prevSender && !msg.IsSystem && msg.TS-prevTS < 300 {
				showHeader = false
			}
			prevSender = msg.From
			prevTS = msg.TS

			// Highlight @mentions in the body
			body := " " + highlightLinks(highlightMentions(msg.Body, m.currentUser))

			// Check if this message mentions the current user
			isMentioned := false
			for _, mention := range msg.Mentions {
				if mention == m.currentUser {
					isMentioned = true
					break
				}
			}

			var line string
			if showHeader {
				ts := time.Unix(msg.TS, 0).Format("3:04 PM")
				header := usernameStyle.Render(msg.From)
				if m.retired[msg.From] {
					header += " " + helpDescStyle.Render("[retired]")
				}
				header += "  " + timestampStyle.Render(ts)
				line = " " + header + "\n" + body
			} else {
				line = body
			}

			if isMentioned {
				line = mentionBorder.Render(line)
			}

			if msg.ReplyTo != "" {
				line += "\n " + replyRefStyle.Render("  ↳ re: "+msg.ReplyTo)
			}

			if counts := msg.DisplayReactions(); len(counts) > 0 {
				// Sort emojis deterministically so reactions don't jitter
				// between renders.
				emojis := make([]string, 0, len(counts))
				for e := range counts {
					emojis = append(emojis, e)
				}
				sort.Strings(emojis)
				var reactions []string
				for _, emoji := range emojis {
					reactions = append(reactions, reactionStyle.Render(fmt.Sprintf("%s %d", emoji, counts[emoji])))
				}
				line += "\n   " + strings.Join(reactions, "  ")
			}

			// Attachments
			for _, att := range msg.Attachments {
				if att.IsImage && att.LocalPath != "" && CanRenderImages() {
					// Inline image rendering
					// Image takes up most of the panel — width minus padding, height up to 2/3 of visible area
				imgMaxRows := visibleHeight * 2 / 3
				if imgMaxRows < 10 {
					imgMaxRows = 10
				}
				imgStr := RenderImageInline(att.LocalPath, width-8, imgMaxRows)
					if imgStr != "" {
						line += "\n" + imgStr
						line += "\n " + fmt.Sprintf("%s (%s)", att.Name, formatSize(att.Size))
					} else {
						line += "\n " + fmt.Sprintf("🖼 %s (%s)", att.Name, formatSize(att.Size))
					}
				} else if att.IsImage {
					line += "\n " + fmt.Sprintf("🖼 %s (%s)", att.Name, formatSize(att.Size))
				} else {
					line += "\n " + fmt.Sprintf("📎 %s (%s)", att.Name, formatSize(att.Size))
				}
				if i == m.cursor && focused {
					line += timestampStyle.Render("  o=open  s=save")
				}
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

func formatSize(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
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
