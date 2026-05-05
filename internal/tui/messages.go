package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
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
	ID       string
	FromID   string // raw username (nanoid) for logic/comparison
	From     string // display name for rendering
	Body     string // decrypted body (or "(encrypted)" if not decryptable)
	TS       int64
	EditedAt int64 // Phase 15: 0 if never edited, else server's edit wall clock (for "(edited)" marker)
	Room     string
	Group    string
	DM       string
	ReplyTo  string
	Mentions []string
	// ReactionsByUser tracks all reaction_ids per (user, emoji) on this message.
	// user -> emoji -> []reaction_id. Display count = distinct users per emoji.
	// The current user's own entries are used for the "Remove my reaction" UX.
	ReactionsByUser map[string]map[string][]string
	Attachments     []DisplayAttachment
	IsSystem        bool
	SystemText      string
	Deleted         bool
	DeletedBy       string
	// Phase 14 coalescing metadata. Populated when IsSystem is true
	// and the row was created by AddCoalescingSystemMessage for an
	// admin-initiated group event (join, promote, demote, removed).
	// Empty otherwise — regular system messages (typing, retirement,
	// self-leave) never coalesce. See AddCoalescingSystemMessage for
	// the merge rules.
	coalesceVerb    string
	coalesceByID    string
	coalesceByName  string
	coalesceGroup   string
	coalesceTargets []string
	coalesceFirstTS int64
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
	FileID     string
	Name       string
	Size       int64
	Mime       string
	IsImage    bool
	DecryptKey []byte // key to decrypt the downloaded file (epoch key for rooms, per-file K_file for DMs)
}

// MessagesModel manages the message stream.
//
// Scroll model (post-2026-04-25 refactor): the message body is rendered
// into a `bubbles/viewport.Model` that owns the bounded scroll region.
// Cursor and viewport scroll are decoupled — `cursor` is the highlighted
// selected message (used for keyboard actions like reply/react/delete),
// while `viewport.YOffset` is independent scroll position. Mouse wheel
// scrolls the viewport without moving the cursor; arrow keys move the
// cursor and the viewport scrolls only if the cursor would otherwise
// go off-screen. Auto-scroll-to-bottom on new message fires only when
// the user is already at the bottom (preserves reading position
// during history browsing).
//
// Pre-refactor a manual scrollOffset+cursor system was conflated with
// the messages-render loop having no upper bound — scrolling up enough
// blew past the pane height and corrupted the surrounding layout.
type MessagesModel struct {
	messages         []DisplayMessage
	room             string
	group            string
	dm               string
	roomTopic        string               // Phase 18: current room topic, rendered in the two-line header above the stream. Empty for groups/DMs/topicless rooms.
	cursor           int                  // selected message index (-1 = none)
	typingUsers      map[string]time.Time // user -> last typing time
	currentUser      string               // display name — for @mention highlighting in body
	currentUserID    string               // nanoid — for mention detection in payload
	resolveName      func(string) string  // user nanoid → display name (set by App)
	resolveRoomName  func(string) string  // room nanoid → display name (set by App)
	resolveGroupName func(string) string  // group nanoid → display name (set by App)
	loadingHistory   bool
	hasMore          bool            // server indicated more history available
	unreadFromID     string          // first unread message ID (for divider)
	retired          map[string]bool // userID -> account retired
	left             bool            // current context is archived (read-only, user has left)
	roomRetired      bool            // current context is a retired room (archived by admin)
	filesDir         string          // <dataDir>/files — set by App after connect; used to derive per-attachment cached path for inline-image render

	// viewport owns the scrollable message-stream region. Width and
	// Height are set by View() each render to track terminal resize +
	// member-panel-toggle changes. Content is set by RefreshContent()
	// (called from the App on any message-slice change) — including
	// the auto-scroll-to-bottom-if-was-at-bottom rule.
	viewport viewport.Model

	// rowMap[i] is the 0-indexed content row at which message i begins.
	// Rebuilt each RefreshContent call from buildContent's second return.
	// Used by MessageAtViewportRow to translate a click coordinate back
	// to a message index — the click handler in app.go reads this via
	// the accessor method, doesn't reach in directly.
	rowMap []int

	// pinnedBar is the rendered pinned-messages bar (PinnedBarModel.View
	// output). Sticks at the top of the panel above the viewport, OUTSIDE
	// the scroll region — always visible regardless of how far the user
	// has scrolled. Set by App.View via SetPinnedBar before each render;
	// empty string when there are no pins or the model is in collapsed-
	// state-without-pins.
	pinnedBar string
}

// SetFilesDir wires the per-server file cache directory into the render
// path so the inline-image branch can check whether an attachment has
// been downloaded without consulting any DB or in-memory map. Called
// from App after the client connects.
func (m *MessagesModel) SetFilesDir(dir string) {
	m.filesDir = dir
}

// attachmentLocalPath returns the cached plaintext path for an
// attachment if the file has been downloaded, else "". DownloadFile
// writes decrypted bytes to <filesDir>/<fileID> deterministically, so
// the cache-on-disk is the single source of truth for "is this locally
// available?" — no need to persist a LocalPath field on the stored
// attachment row. If the cache is evicted the next render drops back
// to the 🖼 placeholder naturally.
func (m *MessagesModel) attachmentLocalPath(fileID string) string {
	if m.filesDir == "" || fileID == "" {
		return ""
	}
	path := filepath.Join(m.filesDir, fileID)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func NewMessages() MessagesModel {
	// Initial viewport size is a reasonable default; View() resizes
	// every render to the actual panel dimensions. Mouse wheel
	// support comes for free via viewport.Update(tea.MouseMsg).
	vp := viewport.New(80, 24)
	return MessagesModel{
		cursor:      -1,
		typingUsers: make(map[string]time.Time),
		hasMore:     true,
		retired:     make(map[string]bool),
		viewport:    vp,
	}
}

// ScrollToMessage sets the cursor to the message with the given ID.
// Returns true if the message was found.
// ScrollToMessage moves the selection cursor to the message with the
// given ID and scrolls the viewport so the row is visible. Returns
// true if the message was found in the loaded buffer.
//
// Pre-2026-05-05 this only set the cursor — name was misleading
// because the viewport position didn't change. Callers (pinned-bar
// jump, reply-to-parent navigation) needed a follow-up scroll. Now
// the function does both: cursor + scroll, via the existing
// ensureCursorVisible helper which respects multi-row messages and
// the rowMap.
func (m *MessagesModel) ScrollToMessage(msgID string) bool {
	for i, msg := range m.messages {
		if msg.ID == msgID {
			m.cursor = i
			m.ensureCursorVisible()
			return true
		}
	}
	return false
}

// selectedMessageRowSpan returns the inclusive [start,end] content-row span for
// the currently selected message. Rows are in viewport-content coordinates
// (same space as viewport.YOffset), not message indexes.
func (m MessagesModel) selectedMessageRowSpan() (start, end int, ok bool) {
	if m.cursor < 0 || m.cursor >= len(m.messages) || len(m.rowMap) != len(m.messages) {
		return 0, 0, false
	}
	start = m.rowMap[m.cursor]
	if m.cursor+1 < len(m.rowMap) {
		end = m.rowMap[m.cursor+1] - 1
	} else {
		end = m.viewport.TotalLineCount() - 1
	}
	if end < start {
		end = start
	}
	return start, end, true
}

// ensureCursorVisible scrolls the viewport so the selected message remains
// visible after keyboard navigation. Uses rowMap-derived content rows, not
// message indexes, so wrapped/multi-line messages behave correctly.
func (m *MessagesModel) ensureCursorVisible() {
	start, end, ok := m.selectedMessageRowSpan()
	if !ok || m.viewport.Height <= 0 {
		return
	}
	visibleTop := m.viewport.YOffset
	visibleBottom := visibleTop + m.viewport.Height - 1
	if start < visibleTop {
		m.viewport.ScrollUp(visibleTop - start)
		return
	}
	if end > visibleBottom {
		msgHeight := end - start + 1
		// If a single message is taller than the viewport, pin its first row
		// at the top so navigation always lands on the start of the message.
		if msgHeight > m.viewport.Height {
			m.viewport.ScrollDown(start - visibleTop)
			return
		}
		m.viewport.ScrollDown(end - visibleBottom)
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

func (m *MessagesModel) SetContext(room, group, dm string) {
	m.room = room
	m.group = group
	m.dm = dm
	m.roomTopic = "" // Phase 18: caller should call SetRoomTopic after when the new context is a room with a topic
	m.messages = nil
	m.cursor = -1
	m.viewport.GotoBottom() // reset scroll position for new context
	m.unreadFromID = ""
	m.hasMore = true
	m.loadingHistory = false
	m.left = false        // caller should call SetLeft after if the new context is archived
	m.roomRetired = false // caller should call SetRoomRetired after if the new context is a retired room
	// Clear typing indicators on context switch. SetTyping only inserts
	// entries that match the current context, but stale entries from the
	// previous context can linger (entries time out via the 5-second
	// cutoff at render time, not on context switch). Without this clear,
	// switching from group_X to group_Y while carol's typing entry is
	// still recent would briefly display "carol is typing" in group_Y
	// where carol is not typing — the per-context typing namespace bug.
	for k := range m.typingUsers {
		delete(m.typingUsers, k)
	}
}

// SetRoomTopic stores the current room topic for rendering in the two-line
// header above the message stream. Phase 18. Empty string omits the topic
// line entirely — groups and 1:1 DMs always pass "" since they have no
// topics. Rooms without a topic also pass "".
func (m *MessagesModel) SetRoomTopic(topic string) {
	m.roomTopic = topic
}

// RoomTopic returns the current room topic (read-only accessor used by
// /topic slash command). Empty string when no topic is set or the context
// is not a room.
func (m *MessagesModel) RoomTopic() string {
	return m.roomTopic
}

// SetLeft marks the current context as archived (read-only). When true,
// the messages view renders a "you left this group" indicator and the
// input bar should be disabled by the caller.
func (m *MessagesModel) SetLeft(left bool) {
	m.left = left
}

// IsLeft returns true if the current messages context is archived.
func (m *MessagesModel) IsLeft() bool {
	return m.left
}

// SetRoomRetired marks the current context as a retired room (Phase
// 12). When true, the read-only banner renders different wording
// ("this room was archived by an admin") to differentiate from a
// self-leave. Orthogonal to left: a room may be both retired and left,
// but the retired wording takes precedence because it's the cause.
func (m *MessagesModel) SetRoomRetired(retired bool) {
	m.roomRetired = retired
}

// IsRoomRetired returns true if the current context is a retired room.
func (m *MessagesModel) IsRoomRetired() bool {
	return m.roomRetired
}

// SetUnreadFrom sets the first unread message ID for the divider.
func (m *MessagesModel) SetUnreadFrom(msgID string) {
	m.unreadFromID = msgID
}

// LoadFromDB populates the message list from the local DB, including
// persisted reactions.
func (m *MessagesModel) LoadFromDB(c *client.Client) {
	if c == nil {
		return
	}

	var stored []storeMsg
	var err error

	if m.room != "" {
		stored, err = loadRoom(c, m.room)
	} else if m.group != "" {
		stored, err = loadGroup(c, m.group)
	} else if m.dm != "" {
		stored, err = loadDM(c, m.dm)
	}

	if err != nil || len(stored) == 0 {
		return
	}

	m.messages = nil
	msgIDs := make([]string, 0, len(stored))
	for _, s := range stored {
		from := s.Sender
		if c != nil {
			from = c.DisplayName(s.Sender)
		}
		var attachments []DisplayAttachment
		for _, a := range s.Attachments {
			key, _ := base64.StdEncoding.DecodeString(a.DecryptKey)
			attachments = append(attachments, DisplayAttachment{
				FileID:     a.FileID,
				Name:       a.Name,
				Size:       a.Size,
				Mime:       a.Mime,
				IsImage:    isImageMime(a.Mime),
				DecryptKey: key,
			})
		}

		m.messages = append(m.messages, DisplayMessage{
			ID:          s.ID,
			FromID:      s.Sender,
			From:        from,
			Body:        s.Body,
			TS:          s.TS,
			EditedAt:    s.EditedAt, // Phase 15
			Room:        s.Room,
			Group:       s.Group,
			DM:          s.DM,
			ReplyTo:     s.ReplyTo,
			Mentions:    s.Mentions,
			Deleted:     s.Deleted,
			DeletedBy:   s.DeletedBy,
			Attachments: attachments,
		})
		if s.ID != "" {
			msgIDs = append(msgIDs, s.ID)
		}
	}

	// Load persisted reactions and apply to loaded messages
	if st := c.Store(); st != nil && len(msgIDs) > 0 {
		reactions, err := st.GetReactionsForMessages(msgIDs)
		if err == nil {
			for _, r := range reactions {
				m.addReactionRecord(r.MessageID, r.ReactionID, r.User, r.Emoji)
			}
		}
	}

	// Restore unread divider from persisted read position
	if st := c.Store(); st != nil {
		target := m.room
		if target == "" {
			target = m.group
		}
		if target == "" {
			target = m.dm
		}
		if target != "" {
			if lastRead, err := st.GetReadPosition(target); err == nil && lastRead != "" {
				// Set divider after the last-read message
				found := false
				for _, msg := range m.messages {
					if found && msg.ID != "" && !msg.IsSystem {
						m.unreadFromID = msg.ID
						break
					}
					if msg.ID == lastRead {
						found = true
					}
				}
			}
		}
	}
}

type storeMsg = store.StoredMessage

func loadRoom(c *client.Client, room string) ([]store.StoredMessage, error) {
	return c.LoadRoomMessages(room, 200)
}

func loadGroup(c *client.Client, group string) ([]store.StoredMessage, error) {
	return c.LoadGroupMessages(group, 200)
}

func loadDM(c *client.Client, dm string) ([]store.StoredMessage, error) {
	return c.LoadDMMessages(dm, 200)
}

// requestHistory sends a history request for older messages.
func (m *MessagesModel) requestHistory() tea.Cmd {
	if !m.hasMore || m.loadingHistory || len(m.messages) == 0 {
		return nil
	}

	firstMsg := m.messages[0]
	room := m.room
	group := m.group
	dm := m.dm
	beforeID := firstMsg.ID

	m.loadingHistory = true

	return func() tea.Msg {
		return HistoryRequestMsg{
			Room:     room,
			Group:    group,
			DM:       dm,
			BeforeID: beforeID,
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
				// file_epoch may differ from msg.Epoch if the file was
				// uploaded during a different epoch (rare, but handle it).
				fileEpoch := a.FileEpoch
				if fileEpoch == 0 {
					fileEpoch = msg.Epoch
				}
				attachments = append(attachments, DisplayAttachment{
					FileID:     a.FileID,
					Name:       a.Name,
					Size:       a.Size,
					Mime:       a.Mime,
					IsImage:    isImageMime(a.Mime),
					DecryptKey: c.RoomEpochKey(msg.Room, fileEpoch),
				})
			}
		}
	}

	from := msg.From
	if c != nil {
		from = c.DisplayName(msg.From)
	}

	m.messages = append(m.messages, DisplayMessage{
		ID:          msg.ID,
		FromID:      msg.From,
		From:        from,
		Body:        body,
		TS:          msg.TS,
		Room:        msg.Room,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
}

func (m *MessagesModel) AddGroupMessage(msg protocol.GroupMessage, c *client.Client) {
	if msg.Group != m.group {
		return
	}

	body := "(encrypted)"
	replyTo := ""
	var mentions []string
	var attachments []DisplayAttachment

	if c != nil {
		payload, err := c.DecryptGroupMessage(msg.WrappedKeys, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
			for _, a := range payload.Attachments {
				// Design A: each attachment carries its own base64 K_file.
				decKey, _ := base64.StdEncoding.DecodeString(a.FileKey)
				attachments = append(attachments, DisplayAttachment{
					FileID:     a.FileID,
					Name:       a.Name,
					Size:       a.Size,
					Mime:       a.Mime,
					IsImage:    isImageMime(a.Mime),
					DecryptKey: decKey,
				})
			}
		}
	}

	from := msg.From
	if c != nil {
		from = c.DisplayName(msg.From)
	}

	m.messages = append(m.messages, DisplayMessage{
		ID:          msg.ID,
		FromID:      msg.From,
		From:        from,
		Body:        body,
		TS:          msg.TS,
		Group:       msg.Group,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	})
}

// buildDisplayMsg creates a DisplayMessage from a room protocol message without appending it.
// Used by history prepend where messages go to the front, not the back.
func (m *MessagesModel) buildDisplayMsg(msg protocol.Message, c *client.Client) DisplayMessage {
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
				fileEpoch := a.FileEpoch
				if fileEpoch == 0 {
					fileEpoch = msg.Epoch
				}
				attachments = append(attachments, DisplayAttachment{
					FileID:     a.FileID,
					Name:       a.Name,
					Size:       a.Size,
					Mime:       a.Mime,
					IsImage:    isImageMime(a.Mime),
					DecryptKey: c.RoomEpochKey(msg.Room, fileEpoch),
				})
			}
		}
	}

	from := msg.From
	if c != nil {
		from = c.DisplayName(msg.From)
	}

	return DisplayMessage{
		ID:          msg.ID,
		FromID:      msg.From,
		From:        from,
		Body:        body,
		TS:          msg.TS,
		Room:        msg.Room,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	}
}

// buildDisplayGroup creates a DisplayMessage from a group DM protocol message without appending it.
func (m *MessagesModel) buildDisplayGroup(msg protocol.GroupMessage, c *client.Client) DisplayMessage {
	body := "(encrypted)"
	replyTo := ""
	var mentions []string
	var attachments []DisplayAttachment

	if c != nil {
		payload, err := c.DecryptGroupMessage(msg.WrappedKeys, msg.Payload)
		if err == nil {
			body = payload.Body
			replyTo = payload.ReplyTo
			mentions = payload.Mentions
			for _, a := range payload.Attachments {
				decKey, _ := base64.StdEncoding.DecodeString(a.FileKey)
				attachments = append(attachments, DisplayAttachment{
					FileID:     a.FileID,
					Name:       a.Name,
					Size:       a.Size,
					Mime:       a.Mime,
					IsImage:    isImageMime(a.Mime),
					DecryptKey: decKey,
				})
			}
		}
	}

	from := msg.From
	if c != nil {
		from = c.DisplayName(msg.From)
	}

	return DisplayMessage{
		ID:          msg.ID,
		FromID:      msg.From,
		From:        from,
		Body:        body,
		TS:          msg.TS,
		Group:       msg.Group,
		ReplyTo:     replyTo,
		Mentions:    mentions,
		Attachments: attachments,
	}
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
	// Plain system message append — no coalescing. Used for non-admin
	// events (e.g. typing, reconnect notices) and fallback cases
	// where the caller didn't provide coalescing metadata.
	m.messages = append(m.messages, DisplayMessage{
		IsSystem:   true,
		SystemText: text,
		TS:         time.Now().Unix(),
	})
}

// AddCoalescingSystemMessage is the Phase 14 variant used by the
// group_event dispatch path. Same output as AddSystemMessage when
// consecutive events differ — but when the last system message was
// the same (admin, verb) pair within 10 seconds AND targeted the
// same group, the existing row is REPLACED with a collapsed form.
//
// Coalescing rules (from groups_admin.md "Client-side coalescing"):
//
//   - Window: 10 seconds from the first event of a series
//   - Only collapses same admin + same verb (join, promote, demote,
//     leave:removed). leave with empty reason (self-leave) and
//     retirement events are NEVER coalesced — they represent
//     user-initiated or account-level actions that each deserve
//     their own system message.
//   - Max 3 targets shown by name, then "and N more"
//   - Individual rows are STILL persisted to the local group_events
//     table in un-coalesced form (the client layer does that before
//     this call). /audit shows the un-coalesced history.
//
// Parameters:
//
//   - verb: "join" | "promote" | "demote" | "removed"
//   - byID: the acting admin's user ID (empty string disables coalescing)
//   - byName: pre-resolved display name for the acting admin (used in the collapsed text)
//   - targetName: pre-resolved display name for this event's target
//   - groupID: used as a partition key — events in different groups
//     never coalesce even if verb+admin match
//   - renderSingle: the full text for this single event ("alice added bob to the group")
//   - renderJoined: given a joined list of target names like "bob, carol, and dave", returns the coalesced text ("alice added bob, carol, and dave to the group")
func (m *MessagesModel) AddCoalescingSystemMessage(
	verb, byID, byName, targetName, groupID, renderSingle string,
	renderJoined func(joined string) string,
) {
	now := time.Now().Unix()

	// Guard: no coalescing without a stable acting admin (empty
	// byID means self-leave or retirement — always individual rows).
	if byID == "" {
		m.AddSystemMessage(renderSingle)
		return
	}

	// Check the last row for coalescing eligibility.
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.IsSystem &&
			last.coalesceVerb == verb &&
			last.coalesceByID == byID &&
			last.coalesceGroup == groupID &&
			now-last.coalesceFirstTS <= 10 {
			// Extend the existing coalesced row instead of adding a new one.
			last.coalesceTargets = append(last.coalesceTargets, targetName)
			last.SystemText = renderJoined(joinCoalesced(last.coalesceTargets))
			last.TS = now
			return
		}
	}

	// First event in a potential series — store metadata alongside
	// the text so the NEXT event can coalesce into this row.
	m.messages = append(m.messages, DisplayMessage{
		IsSystem:        true,
		SystemText:      renderSingle,
		TS:              now,
		coalesceVerb:    verb,
		coalesceByID:    byID,
		coalesceByName:  byName,
		coalesceGroup:   groupID,
		coalesceTargets: []string{targetName},
		coalesceFirstTS: now,
	})
}

// joinCoalesced formats the list of target names per the plan:
// up to 3 names shown, then "and N more" for overflow. Oxford-comma
// style separators for the 3-name case.
func joinCoalesced(names []string) string {
	switch n := len(names); n {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	case 3:
		return names[0] + ", " + names[1] + ", and " + names[2]
	default:
		return names[0] + ", " + names[1] + ", " + names[2] +
			fmt.Sprintf(", and %d more", n-3)
	}
}

// MarkDeleted flags a message as deleted in-place. The message stays in the
// list and renders as a tombstone. Reactions are cleaned up.
// File cleanup is handled by the client layer (store.DeleteMessage returns
// file IDs from the DB, client deletes cached files).
func (m *MessagesModel) MarkDeleted(id, deletedBy string) {
	for i, msg := range m.messages {
		if msg.ID == id {
			// Clean up reaction tracker entries
			for _, byEmoji := range msg.ReactionsByUser {
				for _, ids := range byEmoji {
					for _, rid := range ids {
						delete(reactionTracker, rid)
					}
				}
			}
			m.messages[i].Deleted = true
			m.messages[i].DeletedBy = deletedBy
			m.messages[i].Body = ""
			m.messages[i].ReactionsByUser = nil
			m.messages[i].Attachments = nil
			return
		}
	}
}

// ApplyEdit updates an in-memory DisplayMessage's body and edited_at
// when an `edited` / `group_edited` / `dm_edited` envelope arrives.
// Phase 15. Also clears reaction state on the edited message ID per
// Decision log Q12: clients unconditionally clear reactions when
// they receive an edit event, matching the server-side reaction
// delete that happened in the same transaction as the payload replace.
// Mentions are re-extracted from the new body for highlight rendering.
// Safe to call with an ID that isn't in the loaded message list — it's
// a no-op in that case (the store was updated separately by the
// dispatch path and will pick up the new row on next LoadFromDB).
func (m *MessagesModel) ApplyEdit(id, newBody string, editedAt int64) {
	for i, msg := range m.messages {
		if msg.ID != id {
			continue
		}
		// Clean up reaction tracker entries — matches the MarkDeleted
		// reaction cleanup pattern. Keeps the package-level tracker
		// consistent with the rendered state.
		for _, byEmoji := range msg.ReactionsByUser {
			for _, ids := range byEmoji {
				for _, rid := range ids {
					delete(reactionTracker, rid)
				}
			}
		}
		m.messages[i].Body = newBody
		m.messages[i].EditedAt = editedAt
		m.messages[i].ReactionsByUser = nil
		// Re-extract mentions from the new body for highlight rendering.
		// Simple scan — matches the client-side extractor in client/edit.go.
		m.messages[i].Mentions = extractMentionsInline(newBody)
		return
	}
}

// extractMentionsInline is a TUI-local copy of the client's mention
// extractor. Keeps the TUI layer self-contained (no cross-package
// dependency into internal/client just for a one-liner scan).
func extractMentionsInline(body string) []string {
	var mentions []string
	for i := 0; i < len(body); i++ {
		if body[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(body) {
			c := body[j]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
				j++
				continue
			}
			break
		}
		if j > i+1 {
			mentions = append(mentions, body[i+1:j])
		}
		i = j - 1
	}
	return mentions
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
	} else if r.Group != "" {
		// Group DM reaction — decrypt with per-message key
		dr, err := c.DecryptGroupReaction(r.WrappedKeys, r.Payload)
		if err == nil {
			emoji = dr.Emoji
			if dr.Target != r.ID {
				return
			}
		}
	} else if r.DM != "" {
		// 1:1 DM reaction — decrypt with per-message key
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

// SyncReactionsForMessage replaces a single message's in-memory reaction state
// with the canonical rows from the local DB. This is used after live
// reaction/reaction_removed frames so UI state matches persisted state even if
// a transient live-merge path misses.
func (m *MessagesModel) SyncReactionsForMessage(c *client.Client, msgID string) {
	if c == nil || msgID == "" {
		return
	}
	st := c.Store()
	if st == nil {
		return
	}

	msgIdx := -1
	for i, msg := range m.messages {
		if msg.ID == msgID {
			msgIdx = i
			break
		}
	}
	if msgIdx < 0 {
		return
	}

	// Clear tracker entries currently attached to this message before reloading.
	for _, byEmoji := range m.messages[msgIdx].ReactionsByUser {
		for _, ids := range byEmoji {
			for _, rid := range ids {
				delete(reactionTracker, rid)
			}
		}
	}
	m.messages[msgIdx].ReactionsByUser = nil

	reactions, err := st.GetReactionsForMessages([]string{msgID})
	if err != nil {
		return
	}
	for _, r := range reactions {
		m.addReactionRecord(r.MessageID, r.ReactionID, r.User, r.Emoji)
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

func (m *MessagesModel) SetTyping(user, room, group, dm string) {
	if (room != "" && room == m.room) || (group != "" && group == m.group) || (dm != "" && dm == m.dm) {
		m.typingUsers[user] = time.Now()
	}
}

// MessageAt returns the message at the given index, or nil if out of bounds.
func (m *MessagesModel) MessageAt(idx int) *DisplayMessage {
	if idx >= 0 && idx < len(m.messages) {
		return &m.messages[idx]
	}
	return nil
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
	Room     string
	Group    string
	DM       string
	BeforeID string
}

func (m MessagesModel) Update(msg tea.KeyMsg) (MessagesModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		// Cursor-driven up. Snap-then-move: if cursor is unset (-1)
		// or off the bottom of the viewport (e.g. user wheel-scrolled
		// up to read history), the first press snaps cursor to the
		// last message and that becomes the engaged browse position.
		// Subsequent presses walk up message-by-message; viewport
		// follows when cursor would go off the top edge.
		if m.cursor == -1 && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		} else if m.cursor > 0 {
			m.cursor--
			// At the top — request history. The hasMore guard inside
			// requestHistory keeps the call idempotent when there's
			// nothing left to fetch.
			if m.cursor == 0 && len(m.messages) > 0 && !m.loadingHistory {
				m.ensureCursorVisible()
				return m, m.requestHistory()
			}
		}
		m.ensureCursorVisible()
	case "pageup", "pgup":
		// Pure viewport scroll, cursor unchanged. Page = viewport height.
		m.viewport.ScrollUp(m.viewport.Height)
		// At the very top: opportunity to request history (server-side
		// catchup of older messages). Same hasMore + loadingHistory
		// gating as the up-arrow path.
		if m.viewport.AtTop() && len(m.messages) > 0 && !m.loadingHistory {
			return m, m.requestHistory()
		}
		return m, nil
	case "pagedown", "pgdown":
		// Pure viewport scroll, cursor unchanged.
		m.viewport.ScrollDown(m.viewport.Height)
		return m, nil
	case "home":
		// Pure viewport scroll to top, cursor unchanged.
		m.viewport.GotoTop()
		if len(m.messages) > 0 && !m.loadingHistory {
			return m, m.requestHistory()
		}
		return m, nil
	case "end":
		// Jump to the latest message + scroll to bottom. Cursor moves
		// to the last message because End semantically means "back to
		// live conversation."
		if len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		}
		m.viewport.GotoBottom()
		return m, nil
	case "down", "j":
		// Mirror of up/k. Snap-then-move on -1; advance cursor;
		// viewport follows down if cursor would go off-screen.
		if m.cursor == -1 && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
		} else if m.cursor < len(m.messages)-1 {
			m.cursor++
		}
		m.ensureCursorVisible()
	case "r": // reply
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				return MessageAction{Action: "reply", Msg: *sel}
			}
		}
	case "d": // delete
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				return MessageAction{Action: "delete", Msg: *sel}
			}
		}
	case "p": // pin
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted && m.room != "" {
			return m, func() tea.Msg {
				return MessageAction{Action: "pin", Msg: *sel}
			}
		}
	case "c": // copy
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				return MessageAction{Action: "copy", Msg: *sel}
			}
		}
	case "o": // open attachment
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted && len(sel.Attachments) > 0 {
			return m, func() tea.Msg {
				return MessageAction{Action: "open_attachment", Msg: *sel}
			}
		}
	case "s": // save attachment
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted && len(sel.Attachments) > 0 {
			return m, func() tea.Msg {
				return MessageAction{Action: "save_attachment", Msg: *sel}
			}
		}
	case "e": // react (emoji picker)
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				return MessageAction{Action: "react", Msg: *sel}
			}
		}
	case "u": // unreact — remove one of current user's reactions
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				// Empty Data means "pick first emoji user has reacted with";
				// app handler resolves to the specific reaction_id.
				return MessageAction{Action: "unreact", Msg: *sel}
			}
		}
	case "g": // go to parent (jump to message this is replying to)
		if sel := m.SelectedMessage(); sel != nil && sel.ReplyTo != "" {
			m.ScrollToMessage(sel.ReplyTo)
		}
	case "t": // open thread view
		if sel := m.SelectedMessage(); sel != nil {
			// Use the message itself as the root if it has replies,
			// or jump to the root if this is a reply.
			rootID := sel.ID
			if sel.ReplyTo != "" {
				rootID = sel.ReplyTo
			}
			return m, func() tea.Msg {
				return MessageAction{Action: "thread", Msg: *sel, Data: rootID}
			}
		}
	case "enter": // open context menu on selected message (keyboard path)
		if sel := m.SelectedMessage(); sel != nil && !sel.Deleted {
			return m, func() tea.Msg {
				return MessageAction{Action: "open_menu", Msg: *sel}
			}
		}
	}
	return m, nil
}

// renderHeader returns the always-pinned title + topic + blank
// separator block that sits above the scrollable message stream.
// Header lives outside the viewport per the post-2026-04-25 layout
// decision so the room title is always visible regardless of scroll
// position. Returns the rendered string and the line count (so the
// caller can subtract it from the panel height when sizing the
// viewport).
func (m MessagesModel) renderHeader() (string, int) {
	var b strings.Builder

	title := m.room
	if title != "" && m.resolveRoomName != nil {
		title = m.resolveRoomName(title)
	}
	if title == "" && m.group != "" {
		if m.resolveGroupName != nil {
			if resolved := strings.TrimSpace(m.resolveGroupName(m.group)); resolved != "" {
				title = resolved
			} else {
				title = m.group
			}
		} else {
			title = m.group
		}
	}
	if title == "" && m.dm != "" {
		if m.resolveName != nil {
			title = m.resolveName(m.dm)
		} else {
			title = m.dm
		}
	}
	if title == "" {
		title = "no room selected"
	}

	b.WriteString(searchHeaderStyle.Render(" " + title))
	b.WriteString("\n")
	headerLines := 2 // title line + blank separator
	if m.room != "" && m.roomTopic != "" {
		b.WriteString(helpDescStyle.Render(" " + m.roomTopic))
		b.WriteString("\n")
		headerLines = 3
	}
	b.WriteString("\n") // blank separator before the viewport

	return b.String(), headerLines
}

// buildContent renders the full scrollable content of the messages
// pane (everything below the header). No height bound, no padding —
// the viewport's job to clip and scroll. Includes:
//   - Loading-history indicator (top, when applicable)
//   - The full message stream (every message in m.messages, no slicing)
//   - Cursor highlight on the selected message
//   - Reply-to previews, reactions, attachments, inline images
//   - Typing indicator (bottom)
//   - Read-only / archived footer (bottom)
//
// Width is the viewport's content width (panel width minus borders).
// Used for line wrapping inside lipgloss styles (e.g. selectedMsgStyle
// .Width(width-2)) and for image-render bounds.
//
// imgMaxRows is the per-image height cap. With viewport adoption the
// image is rendered into the content stream, so its height counts
// against the viewport's total content height — picking a generous
// fixed cap (15 rows) means an image takes ~15 lines of scrollable
// content rather than blowing out the visible region as it did
// pre-refactor when imgMaxRows was visibleHeight*2/3.
// buildContent renders the full scrollable content + a row map.
//
// The returned rowMap has one entry per message: rowMap[i] is the
// 0-indexed content row at which message i begins. Used by mouse
// click handling to translate a viewport row back to a message
// index — see MessageAtViewportRow. Length == len(m.messages).
//
// Counting is done against the EMITTED line shape (count of '\n' in
// the rendered line + 1), not against the message struct, so any
// component that adds rows (reply preview, reactions, attachments,
// inline images) is automatically accounted for.
func (m MessagesModel) buildContent(width int) (string, []int) {
	const imgMaxRows = 15

	var b strings.Builder
	rowMap := make([]int, len(m.messages))
	currentRow := 0

	// Top: loading-history indicator. Replaces the previous
	// "shift start to show cursor at top" + "request history"
	// scroll mechanism — now the user scrolls to the top of
	// the viewport explicitly and sees this prompt.
	if m.loadingHistory {
		b.WriteString(systemMsgStyle.Render(" ── loading history ──"))
		b.WriteString("\n")
		currentRow++
	} else if m.hasMore && len(m.messages) > 0 {
		b.WriteString(systemMsgStyle.Render(" ── press up at top to load more history ──"))
		b.WriteString("\n")
		currentRow++
	}

	unreadShown := false
	prevSender := ""
	prevTS := int64(0)
	for i := 0; i < len(m.messages); i++ {
		msg := m.messages[i]

		// Unread divider
		if !unreadShown && m.unreadFromID != "" && msg.ID == m.unreadFromID {
			divider := systemMsgStyle.Render(" ── new messages ──────────────────────────")
			b.WriteString(divider + "\n")
			unreadShown = true
			currentRow++
		}

		// Record which content row this message starts on. The mouse
		// click handler uses this map to map a click row back to a
		// message index — see MessageAtViewportRow.
		rowMap[i] = currentRow

		var lineRows int

		if msg.IsSystem {
			line := systemMsgStyle.Render(" ── " + msg.SystemText + " ──")
			lineRows = strings.Count(line, "\n") + 1
			b.WriteString(line)
			prevSender = ""
			prevTS = 0
		} else if msg.Deleted {
			tombstone := "message deleted"
			if msg.DeletedBy != "" && msg.DeletedBy != msg.FromID {
				deleterName := msg.DeletedBy
				if m.resolveName != nil {
					deleterName = m.resolveName(msg.DeletedBy)
				}
				tombstone = "message removed by " + deleterName
			}
			line := systemMsgStyle.Render(" ── " + tombstone + " ──")
			lineRows = strings.Count(line, "\n") + 1
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

			// Check if this message mentions the current user (mentions are nanoids)
			isMentioned := false
			for _, mention := range msg.Mentions {
				if mention == m.currentUserID {
					isMentioned = true
					break
				}
			}

			var line string
			if showHeader {
				ts := time.Unix(msg.TS, 0).Format("3:04 PM")
				from := msg.From
				if m.resolveName != nil && msg.FromID != "" {
					from = m.resolveName(msg.FromID)
				}
				header := usernameStyle.Render(from)
				if m.retired[msg.FromID] {
					header += " " + helpDescStyle.Render("[retired]")
				}
				header += "  " + timestampStyle.Render(ts)
				// Phase 15: "(edited)" marker in dim style next to
				// the timestamp when the message has been edited.
				// EditedAt is 0 on unedited rows (default), non-zero
				// after the server echoed an `edited` event.
				if msg.EditedAt > 0 {
					header += " " + helpDescStyle.Render("(edited)")
				}
				line = " " + header + "\n" + body
			} else {
				// Consecutive-message grouping: header is hidden, but
				// if THIS message is edited we still want the marker
				// to show so the user can tell at a glance. Render
				// the marker as a trailing annotation on the body.
				if msg.EditedAt > 0 {
					line = body + " " + helpDescStyle.Render("(edited)")
				} else {
					line = body
				}
			}

			if isMentioned {
				line = mentionBorder.Render(line)
			}

			if msg.ReplyTo != "" {
				replyPreview := m.replyPreview(msg.ReplyTo)
				line += "\n " + replyRefStyle.Render("  ↳ "+replyPreview)
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
				localPath := m.attachmentLocalPath(att.FileID)
				if att.IsImage && localPath != "" && CanRenderImages() {
					// Inline image rendering — image takes up width minus
					// edge padding, fixed-height cap from the buildContent
					// outer-scope const (see imgMaxRows comment above).
					// Pre-viewport refactor this was visibleHeight*2/3 of
					// the visible panel; now images live in scrollable
					// content so an image is N rows of buffer instead of
					// "consumes 2/3 of the on-screen pane."
					imgStr := RenderImageInline(localPath, width-8, imgMaxRows)
					if imgStr != "" {
						line += "\n" + imgStr
						line += "\n " + fmt.Sprintf("%s (%s)", att.Name, formatSize(att.Size))
					} else {
						// Decoder returned empty (malformed image, decoder
						// panic recovered, or unsupported format). Fall
						// back to the placeholder so the user still sees
						// the attachment metadata.
						line += "\n " + fmt.Sprintf("🖼 %s (%s)", att.Name, formatSize(att.Size))
					}
				} else if att.IsImage {
					line += "\n " + fmt.Sprintf("🖼 %s (%s)", att.Name, formatSize(att.Size))
				} else {
					line += "\n " + fmt.Sprintf("📎 %s (%s)", att.Name, formatSize(att.Size))
				}
				if i == m.cursor {
					line += timestampStyle.Render("  o=open  s=save")
				}
			}

			if i == m.cursor {
				line = selectedMsgStyle.Width(width - 2).Render(line)
			}

			lineRows = strings.Count(line, "\n") + 1
			b.WriteString(line)
		}
		b.WriteString("\n")
		currentRow += lineRows
	}

	// Typing indicator
	var typingNames []string
	cutoff := time.Now().Add(-5 * time.Second)
	for user, t := range m.typingUsers {
		if t.After(cutoff) {
			name := user
			if m.resolveName != nil {
				name = m.resolveName(user)
			}
			typingNames = append(typingNames, name)
		}
	}
	if len(typingNames) > 0 {
		var typing string
		switch len(typingNames) {
		case 1:
			typing = typingNames[0] + " is typing..."
		case 2:
			typing = typingNames[0] + " and " + typingNames[1] + " are typing..."
		default:
			typing = fmt.Sprintf("%d people are typing...", len(typingNames))
		}
		b.WriteString(systemMsgStyle.Render(" ── " + typing + " ──"))
		b.WriteString("\n")
	}

	// Read-only / archived indicator. Shown when the user has left the
	// current context or the room has been retired by an admin. Messages
	// above remain readable, but the input bar is disabled. Use /delete
	// to remove the entry from your view.
	//
	// Retirement takes precedence over "left" because it's the cause:
	// if a room was retired, we want the banner to say so (and explain
	// that /delete is the only remaining action), even if the user also
	// happens to have left before it was retired.
	if m.roomRetired {
		b.WriteString(systemMsgStyle.Render(" ── this room was archived by an admin — read-only — type /delete to remove from your view ──"))
		b.WriteString("\n")
	} else if m.left {
		var label string
		switch {
		case m.room != "":
			label = "room"
		case m.group != "":
			label = "group"
		case m.dm != "":
			label = "DM"
		default:
			label = "context"
		}
		b.WriteString(systemMsgStyle.Render(" ── you left this " + label + " — read-only — type /delete to remove from your view ──"))
		b.WriteString("\n")
	}

	return b.String(), rowMap
}

// RefreshContent rebuilds the scrollable message-stream content and
// pushes it to the embedded viewport. Pointer receiver — mutates
// m.viewport in place — because this is the single site where
// auto-scroll-on-new-message has to make a state decision based on
// the viewport's current YOffset before the content changes.
//
// Auto-scroll rule: if the user was at the bottom of the viewport
// (or the viewport was empty), the new content scrolls to the new
// bottom — preserves the "always see latest" behaviour for an active
// chat. If the user was scrolled up reading history, position is
// preserved — they don't get yanked to the bottom every time a new
// message arrives.
//
// Called by the App on any state change that affects rendered content:
// LoadFromDB, message appends from server broadcasts, edit / delete
// dispatch, context switch (via SetContext), window resize, focus
// change. Cheap to call repeatedly — content rebuild is O(N messages)
// but N is typically small (room buffer) and viewport.SetContent is
// O(content size) for line wrapping.
func (m *MessagesModel) RefreshContent(width int) {
	wasAtBottom := m.viewport.AtBottom() || m.viewport.TotalLineCount() == 0
	content, rowMap := m.buildContent(width)
	m.rowMap = rowMap
	m.viewport.SetContent(content)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// MessageAtViewportRow translates a click row (0-indexed within the
// VIEWPORT, NOT the panel as a whole — caller subtracts header rows
// and the top border before passing in) to the message index
// underneath that row. Returns -1 if the row falls outside any
// message (e.g. on the loading-history banner, the unread divider,
// or the trailing typing indicator / left-banner).
//
// Accounts for viewport scroll position: contentRow is computed as
// viewportRow + viewport.YOffset, so a click on the same screen
// position resolves to a different message depending on how far the
// user has scrolled.
func (m MessagesModel) MessageAtViewportRow(viewportRow int) int {
	if viewportRow < 0 || len(m.rowMap) == 0 {
		return -1
	}
	contentRow := viewportRow + m.viewport.YOffset
	// rowMap is sorted ascending by construction (each subsequent
	// message starts at or after the previous one's start). Walk
	// linearly — len(rowMap) is bounded by the room buffer (small).
	last := -1
	for i, start := range m.rowMap {
		if start > contentRow {
			break
		}
		last = i
	}
	return last
}

// HeaderLines returns the number of rows the header takes ABOVE the
// viewport in View output. Mouse handlers in app.go subtract this
// (plus 1 for the top border) from a click's terminal-y to get the
// viewport row, which they then pass to MessageAtViewportRow.
func (m MessagesModel) HeaderLines() int {
	_, lines := m.renderHeader()
	return lines
}

// SetPinnedBar installs the pre-rendered pinned-messages bar string
// that View will splice in between the room header and the viewport.
// Pointer receiver because View is value-receiver — this needs to
// persist across renders. App.View calls this every render with the
// current PinnedBarModel.View output (or empty string when no pins).
func (m *MessagesModel) SetPinnedBar(s string) {
	m.pinnedBar = s
}

// PinnedBarRows reports how many rows the pinned bar will occupy in
// the rendered View. Mouse click handlers add this to the
// header/border offset when computing which viewport row a click
// landed on.
func (m MessagesModel) PinnedBarRows() int {
	if m.pinnedBar == "" {
		return 0
	}
	return strings.Count(m.pinnedBar, "\n") + 1
}

// ScrollUp moves the viewport up by n lines without touching the cursor.
// Used by mouse-wheel handlers — wheel scrolls without selecting a message
// (per the cursor-vs-scroll decoupling agreed with the user). Returns
// true if the viewport reached the top, so callers can request older
// history when the user scrolls past the loaded buffer.
func (m *MessagesModel) ScrollUp(n int) bool {
	m.viewport.ScrollUp(n)
	return m.viewport.AtTop()
}

// ScrollDown moves the viewport down by n lines without touching the cursor.
func (m *MessagesModel) ScrollDown(n int) {
	m.viewport.ScrollDown(n)
}

// AtTop reports whether the viewport is scrolled to the top — handy for
// callers deciding whether to issue a history fetch.
func (m MessagesModel) AtTop() bool {
	return m.viewport.AtTop()
}

// View renders the messages pane. Composition (top → bottom):
//   - Header (title + topic + blank separator) — always pinned at top,
//     outside the viewport so it stays visible regardless of scroll.
//   - Pinned-messages bar (when set via SetPinnedBar) — also outside the
//     viewport, sticks just below the header. Collapsed → 1 row;
//     expanded → header + per-pin rows + hint footer. Never scrolls.
//   - Viewport — renders the scrollable content. View calls RefreshContent
//     itself so the rendered output always reflects the current state of
//     m.messages / m.left / m.roomRetired / m.typingUsers / etc. without
//     callers having to remember an explicit refresh step.
//   - Outer style — rounded border + focused/unfocused colour. focused
//     parameter only chooses the border style; cursor highlight inside
//     content is independent of focus (always visible so the user can
//     see where their browse cursor sits even when typing in the
//     compose input).
//
// Pointer receiver: View calls RefreshContent which mutates m.viewport's
// stored content. The mutation only matters within the current render
// cycle — the next View call rebuilds anyway — but it must persist long
// enough that m.viewport.View() below sees the freshly-set content.
// Production callers go through the App's value-receiver View, so the
// mutation here lands on the throwaway App copy and doesn't propagate
// back; that's fine because the explicit a.refreshMessageContent()
// calls in app.go keep the persistent App state current for queries
// that happen between renders (e.g. mouse-wheel AtTop checks).
func (m *MessagesModel) View(width, height int, focused bool) string {
	headerStr, headerLines := m.renderHeader()

	contentWidth := width - 2 // panel width minus left+right borders
	if contentWidth < 1 {
		contentWidth = 1
	}
	// Rebuild content to reflect any mutations since last render. Cost
	// is O(N messages) — on typical room buffers (a few hundred
	// messages) this is sub-millisecond. RefreshContent preserves
	// scroll position when the user has scrolled up reading history.
	m.RefreshContent(contentWidth)

	pinnedRows := m.PinnedBarRows()
	pinnedSection := ""
	if pinnedRows > 0 {
		// Trailing newline so viewport.View() starts on its own row.
		pinnedSection = m.pinnedBar + "\n"
	}

	m.viewport.Width = contentWidth
	m.viewport.Height = height - 2 - headerLines - pinnedRows // borders + header + pinned bar
	if m.viewport.Height < 1 {
		m.viewport.Height = 1
	}

	style := messagesPanelStyle
	if focused {
		style = messagesFocusedStyle
	}
	return style.Width(width).Height(height).Render(headerStr + pinnedSection + m.viewport.View())
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
// replyPreview returns a short preview of the parent message for reply rendering.
// Looks up the message by ID in the current message list.
func (m *MessagesModel) replyPreview(msgID string) string {
	for _, msg := range m.messages {
		if msg.ID == msgID {
			if msg.Deleted {
				return "Deleted message"
			}
			from := msg.From
			if m.resolveName != nil && msg.FromID != "" {
				from = m.resolveName(msg.FromID)
			}
			preview := from + ": " + msg.Body
			if len(preview) > 60 {
				preview = preview[:57] + "..."
			}
			return preview
		}
	}
	// Not in current view — show truncated ID
	if len(msgID) > 12 {
		return msgID[:12] + "..."
	}
	return msgID
}

func highlightMentions(body, currentUser string) string {
	if currentUser == "" {
		return body
	}
	// Highlight current user's @mention in accent, respecting word boundaries.
	target := "@" + currentUser
	var result strings.Builder
	idx := 0
	for idx < len(body) {
		pos := strings.Index(body[idx:], target)
		if pos < 0 {
			result.WriteString(body[idx:])
			break
		}
		absPos := idx + pos
		end := absPos + len(target)

		// Word boundary: @ at start or after whitespace
		atBoundary := absPos == 0 || body[absPos-1] == ' ' || body[absPos-1] == '\n' || body[absPos-1] == '\t'
		// Trailing boundary: end of string or punctuation/whitespace
		atEnd := end >= len(body) || body[end] == ' ' || body[end] == '\n' || body[end] == '\t' || body[end] == ',' || body[end] == '.' || body[end] == '!' || body[end] == '?' || body[end] == ':' || body[end] == ';'

		if atBoundary && atEnd {
			result.WriteString(body[idx:absPos])
			result.WriteString(mentionStyle.Render(target))
			idx = end
		} else {
			result.WriteString(body[idx : absPos+1])
			idx = absPos + 1
		}
	}
	return result.String()
}
