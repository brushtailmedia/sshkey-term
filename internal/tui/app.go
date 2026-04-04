// Package tui implements the Bubble Tea terminal UI.
package tui

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// ServerMsg wraps a protocol message received from the server.
type ServerMsg struct {
	Type string
	Raw  json.RawMessage
}

// ErrMsg wraps a connection error.
type ErrMsg struct{ Err error }

// ConnectedMsg signals successful connection.
type ConnectedMsg struct{}

// App is the top-level Bubble Tea model.
type App struct {
	client    *client.Client
	cfg       client.Config
	connected bool
	err       error

	// UI state
	sidebar   SidebarModel
	messages  MessagesModel
	input     InputModel
	statusBar StatusBarModel
	help      HelpModel
	search    SearchModel

	width  int
	height int
	focus  Focus
}

// Focus tracks which panel has keyboard focus.
type Focus int

const (
	FocusInput Focus = iota
	FocusSidebar
	FocusMessages
)

// New creates the app model.
func New(cfg client.Config) App {
	return App{
		cfg:       cfg,
		sidebar:   NewSidebar(),
		messages:  NewMessages(),
		input:     NewInput(),
		statusBar: NewStatusBar(),
		search:    NewSearch(),
		focus:     FocusInput,
	}
}

func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.input.Init(),
		a.connect(),
	)
}

// connect starts the SSH connection in a goroutine.
func (a App) connect() tea.Cmd {
	return func() tea.Msg {
		msgCh := make(chan ServerMsg, 100)
		errCh := make(chan error, 1)

		cfg := a.cfg
		cfg.OnMessage = func(msgType string, raw json.RawMessage) {
			msgCh <- ServerMsg{Type: msgType, Raw: raw}
		}
		cfg.OnError = func(err error) {
			errCh <- err
		}

		c := client.New(cfg)
		if err := c.Connect(); err != nil {
			return ErrMsg{Err: err}
		}

		// Store the client reference via a message
		go func() {
			for {
				select {
				case msg := <-msgCh:
					// Forward to tea program (set externally)
					_ = msg
				case err := <-errCh:
					_ = err
				case <-c.Done():
					return
				}
			}
		}()

		return connectedWithClient{client: c, msgCh: msgCh, errCh: errCh}
	}
}

type connectedWithClient struct {
	client *client.Client
	msgCh  chan ServerMsg
	errCh  chan error
}

// waitForMsg returns a cmd that waits for the next server message.
func waitForMsg(msgCh chan ServerMsg, errCh chan error, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-msgCh:
			return msg
		case err := <-errCh:
			return ErrMsg{Err: err}
		case <-done:
			return ErrMsg{Err: fmt.Errorf("disconnected")}
		}
	}
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Help screen intercepts all keys when visible
		if a.help.IsVisible() {
			if msg.String() == "esc" || msg.String() == "?" {
				a.help.Hide()
			}
			return a, nil
		}

		// Search screen intercepts keys when visible
		if a.search.IsVisible() {
			var cmd tea.Cmd
			a.search, cmd = a.search.Update(msg, a.client)
			return a, cmd
		}

		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			if a.client != nil {
				a.client.Close()
			}
			return a, tea.Quit

		case "?":
			if a.focus != FocusInput {
				a.help.Toggle()
				return a, nil
			}

		case "ctrl+f":
			a.search.Show()
			return a, nil

		case "tab":
			// Cycle focus: input -> sidebar -> messages -> input
			switch a.focus {
			case FocusInput:
				a.focus = FocusSidebar
			case FocusSidebar:
				a.focus = FocusMessages
			case FocusMessages:
				a.focus = FocusInput
			}
			return a, nil

		case "esc":
			a.focus = FocusInput
			return a, nil
		}

		// Route key to focused panel
		switch a.focus {
		case FocusSidebar:
			var cmd tea.Cmd
			a.sidebar, cmd = a.sidebar.Update(msg, a.client)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Check if sidebar selected a new room/conversation
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedConv() != a.messages.conversation {
				a.messages.SetContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedConv())
				a.messages.LoadFromDB(a.client)
			}
		case FocusMessages:
			var cmd tea.Cmd
			a.messages, cmd = a.messages.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case FocusInput:
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg, a.client, a.messages.room, a.messages.conversation)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case SearchJumpMsg:
		// Jump to the message in context
		a.search.Hide()
		if msg.Room != "" {
			a.messages.SetContext(msg.Room, "")
		} else {
			a.messages.SetContext("", msg.Conversation)
		}
		a.messages.LoadFromDB(a.client)
		// TODO: scroll to the specific message ID
		return a, nil

	case HistoryRequestMsg:
		if a.client != nil {
			a.client.RequestHistory(msg.Room, msg.Conversation, msg.BeforeID, 100)
		}
		return a, nil

	case MessageAction:
		switch msg.Action {
		case "reply":
			preview := msg.Msg.Body
			if len(preview) > 50 {
				preview = preview[:47] + "..."
			}
			a.input.SetReply(msg.Msg.ID, msg.Msg.From+": "+preview)
			a.focus = FocusInput
		case "delete":
			if a.client != nil && (msg.Msg.From == a.client.Username() || a.client.IsAdmin()) {
				a.client.SendDelete(msg.Msg.ID)
			}
		case "pin":
			if a.client != nil && a.messages.room != "" {
				a.client.Enc().Encode(protocol.Pin{
					Type: "pin",
					Room: a.messages.room,
					ID:   msg.Msg.ID,
				})
			}
		case "copy":
			// TODO: copy to clipboard via OSC 52 or atotto/clipboard
		case "react":
			// TODO: open emoji picker overlay
		}
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height

	case connectedWithClient:
		a.client = msg.client
		a.connected = true

		// Populate sidebar and messages
		a.sidebar.SetRooms(a.client.Rooms())
		a.messages.currentUser = a.client.Username()
		if len(a.client.Rooms()) > 0 {
			a.messages.SetContext(a.client.Rooms()[0], "")
			a.messages.LoadFromDB(a.client)
		}

		a.statusBar.SetUser(a.client.Username(), a.client.IsAdmin())
		a.statusBar.SetConnected(true)

		// Start listening for server messages
		cmds = append(cmds, waitForMsg(msg.msgCh, msg.errCh, a.client.Done()))
		// Store channels for future waits
		a.sidebar.msgCh = msg.msgCh
		a.sidebar.errCh = msg.errCh

	case ServerMsg:
		a.handleServerMessage(msg)
		// Continue listening
		if a.client != nil {
			if a.sidebar.msgCh != nil {
				cmds = append(cmds, waitForMsg(a.sidebar.msgCh, a.sidebar.errCh, a.client.Done()))
			}
		}

	case ErrMsg:
		a.err = msg.Err
		a.statusBar.SetConnected(false)
	}

	return a, tea.Batch(cmds...)
}

// handleServerMessage processes incoming server messages for the UI.
func (a *App) handleServerMessage(msg ServerMsg) {
	switch msg.Type {
	case "message":
		var m protocol.Message
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddRoomMessage(m, a.client)
		// Desktop notification for messages not from self
		if a.client != nil && m.From != a.client.Username() {
			payload, err := a.client.DecryptRoomMessage(m.Room, m.Epoch, m.Payload)
			body := "(encrypted)"
			if err == nil {
				body = payload.Body
			}
			SendDesktopNotification(
				fmt.Sprintf("%s in #%s", m.From, m.Room),
				body,
			)
		}
	case "dm":
		var m protocol.DM
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddDMMessage(m, a.client)
		if a.client != nil && m.From != a.client.Username() {
			payload, err := a.client.DecryptDMMessage(m.WrappedKeys, m.Payload)
			body := "(encrypted)"
			if err == nil {
				body = payload.Body
			}
			SendDesktopNotification(m.From, body)
		}
	case "typing":
		var m protocol.Typing
		json.Unmarshal(msg.Raw, &m)
		a.messages.SetTyping(m.User, m.Room, m.Conversation)
	case "room_list":
		var m protocol.RoomList
		json.Unmarshal(msg.Raw, &m)
		var names []string
		for _, r := range m.Rooms {
			names = append(names, r.Name)
		}
		a.sidebar.SetRooms(names)
	case "conversation_list":
		var m protocol.ConversationList
		json.Unmarshal(msg.Raw, &m)
		a.sidebar.SetConversations(m.Conversations)
	case "presence":
		var m protocol.Presence
		json.Unmarshal(msg.Raw, &m)
		a.sidebar.SetOnline(m.User, m.Status == "online")
	case "unread":
		var m protocol.Unread
		json.Unmarshal(msg.Raw, &m)
		if m.Room != "" {
			a.sidebar.SetUnread(m.Room, m.Count)
		} else if m.Conversation != "" {
			a.sidebar.SetUnreadConv(m.Conversation, m.Count)
		}
	case "deleted":
		var m protocol.Deleted
		json.Unmarshal(msg.Raw, &m)
		a.messages.RemoveMessage(m.ID)
	case "reaction":
		var m protocol.Reaction
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddReaction(m)
	case "reaction_removed":
		var m protocol.ReactionRemoved
		json.Unmarshal(msg.Raw, &m)
		a.messages.RemoveReaction(m.ReactionID)
	case "sync_batch":
		var batch protocol.SyncBatch
		json.Unmarshal(msg.Raw, &batch)
		for _, raw := range batch.Messages {
			batchType, _ := protocol.TypeOf(raw)
			a.handleServerMessage(ServerMsg{Type: batchType, Raw: raw})
		}
	case "history_result":
		var result protocol.HistoryResult
		json.Unmarshal(msg.Raw, &result)
		for _, raw := range result.Messages {
			histType, _ := protocol.TypeOf(raw)
			a.handleServerMessage(ServerMsg{Type: histType, Raw: raw})
		}
	case "error":
		var m protocol.Error
		json.Unmarshal(msg.Raw, &m)
		a.statusBar.SetError(m.Message)
	case "server_shutdown":
		var m protocol.ServerShutdown
		json.Unmarshal(msg.Raw, &m)
		a.statusBar.SetError(fmt.Sprintf("Server shutting down: %s", m.Message))
		a.statusBar.SetConnected(false)
	}
}

func (a App) View() string {
	if a.width == 0 || a.height == 0 {
		return "Loading..."
	}

	if a.err != nil && !a.connected {
		return fmt.Sprintf("\n  Connection error: %v\n\n  Press Ctrl+C to quit.\n", a.err)
	}

	if !a.connected {
		return "\n  Connecting...\n"
	}

	// Layout dimensions
	sidebarWidth := 20
	statusBarHeight := 1
	inputHeight := 3
	mainWidth := a.width - sidebarWidth - 3 // borders
	mainHeight := a.height - statusBarHeight - inputHeight - 2

	if mainWidth < 20 {
		mainWidth = 20
	}
	if mainHeight < 5 {
		mainHeight = 5
	}

	// Render panels
	sidebar := a.sidebar.View(sidebarWidth, a.height-statusBarHeight-1, a.focus == FocusSidebar)

	var mainPanel string
	if a.search.IsVisible() {
		searchView := a.search.View(mainWidth, mainHeight+inputHeight)
		mainPanel = searchView
	} else {
		messages := a.messages.View(mainWidth, mainHeight, a.focus == FocusMessages)
		input := a.input.View(mainWidth, a.focus == FocusInput)
		mainPanel = messages + "\n" + input
	}

	status := a.statusBar.View(a.width)

	body := joinHorizontal(sidebar, mainPanel)
	screen := body + "\n" + status

	// Help overlay
	if a.help.IsVisible() {
		helpView := a.help.View(a.width, a.height)
		// Center the help overlay
		return helpView
	}

	return screen
}

// joinHorizontal places two strings side by side.
func joinHorizontal(left, right string) string {
	leftLines := splitLines(left)
	rightLines := splitLines(right)

	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}

	result := ""
	for i := 0; i < maxLines; i++ {
		l := ""
		r := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		if i > 0 {
			result += "\n"
		}
		result += l + " " + r
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}
