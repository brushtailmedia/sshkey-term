// Package tui implements the Bubble Tea terminal UI.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
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

// passphraseNeededMsg signals that the SSH key needs a passphrase.
type passphraseNeededMsg struct{}

// ReconnectStatusMsg signals reconnection state changes.
type ReconnectStatusMsg struct {
	Status    string // "reconnecting", "connected", "failed"
	Attempt   int
	NextRetry time.Duration
}

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
	search      SearchModel
	newConv     NewConvModel
	emojiPicker EmojiPickerModel
	infoPanel   InfoPanelModel
	settings    SettingsModel
	addServer   AddServerModel
	memberPanel MemberPanelModel
	verify      VerifyModel
	keyWarning  KeyWarningModel
	quitConfirm QuitConfirmModel
	retireConfirm RetireConfirmModel
	deviceRevoked DeviceRevokedModel
	deviceMgr     DeviceMgrModel
	pinnedBar   PinnedBarModel

	// Config state
	appConfig   *config.Config
	configDir   string
	serverIdx   int // index of the active server in config
	bell              BellConfig
	muted             map[string]bool // room name or conv ID -> muted
	showHelpHint      bool
	reconnectAttempt  int

	width          int
	height         int
	focus          Focus
	layout         Layout
	contextMenu    ContextMenuModel
	memberMenu     MemberMenuModel
	passphrase         PassphraseModel
	passphraseCh       chan []byte
	passphraseCache    map[string][]byte // keyPath -> passphrase
}

// Focus tracks which panel has keyboard focus.
type Focus int

const (
	FocusInput Focus = iota
	FocusSidebar
	FocusMessages
	FocusMembers
)

// New creates the app model.
func New(cfg client.Config, appCfg *config.Config, configDir string, serverIdx int) App {
	return App{
		cfg:         cfg,
		sidebar:     NewSidebar(),
		messages:    NewMessages(),
		input:       NewInput(),
		statusBar:   NewStatusBar(),
		search:      NewSearch(),
		newConv:     NewNewConv(),
		emojiPicker: NewEmojiPicker(),
		memberPanel: NewMemberPanel(),
		settings:     NewSettings(),
		addServer:    NewAddServer(),
		retireConfirm: NewRetireConfirm(),
		deviceRevoked: NewDeviceRevoked(),
		deviceMgr:     NewDeviceMgr(),
		passphrase:      NewPassphrase(),
		passphraseCh:    make(chan []byte, 1),
		passphraseCache: make(map[string][]byte),
		appConfig:    appCfg,
		configDir:   configDir,
		serverIdx:   serverIdx,
		bell:         NewBellConfig(appCfg.Notifications),
		muted:        config.LoadMutedMap(appCfg),
		showHelpHint: !appCfg.Notifications.HelpShown,
		focus:        FocusInput,
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
		// Passphrase callback — return cached passphrase for this key if available,
		// otherwise signal the TUI to show the dialog.
		keyPath := cfg.KeyPath
		cached := a.passphraseCache[keyPath]
		passCh := a.passphraseCh
		cfg.OnPassphrase = func() ([]byte, error) {
			if len(cached) > 0 {
				return cached, nil
			}
			return <-passCh, nil
		}

		c := client.New(cfg)
		if err := c.Connect(); err != nil {
			// Check if it's a passphrase error
			errStr := err.Error()
			if strings.Contains(errStr, "passphrase") {
				return passphraseNeededMsg{}
			}
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
		// Dismiss first-time help hint on any keypress
		if a.showHelpHint {
			a.showHelpHint = false
			if a.appConfig != nil {
				config.MarkHelpShown(a.configDir, a.appConfig)
			}
		}

		// Passphrase dialog intercepts all keys
		if a.passphrase.IsVisible() {
			var cmd tea.Cmd
			a.passphrase, cmd = a.passphrase.Update(msg)
			return a, cmd
		}

		// Member menu intercepts all keys
		if a.memberMenu.IsVisible() {
			var cmd tea.Cmd
			a.memberMenu, cmd = a.memberMenu.Update(msg)
			return a, cmd
		}

		// Context menu intercepts all keys
		if a.contextMenu.IsVisible() {
			var cmd tea.Cmd
			a.contextMenu, cmd = a.contextMenu.Update(msg)
			return a, cmd
		}

		// Quit confirmation intercepts all keys
		if a.quitConfirm.IsVisible() {
			var cmd tea.Cmd
			a.quitConfirm, cmd = a.quitConfirm.Update(msg)
			if cmd != nil {
				if a.client != nil {
					a.client.Close()
				}
			}
			return a, cmd
		}

		// Retire confirmation intercepts all keys
		if a.retireConfirm.IsVisible() {
			var cmd tea.Cmd
			a.retireConfirm, cmd = a.retireConfirm.Update(msg)
			return a, cmd
		}

		// Device revoked dialog intercepts all keys
		if a.deviceRevoked.IsVisible() {
			var cmd tea.Cmd
			a.deviceRevoked, cmd = a.deviceRevoked.Update(msg)
			return a, cmd
		}

		// Device manager dialog intercepts all keys
		if a.deviceMgr.IsVisible() {
			var cmd tea.Cmd
			a.deviceMgr, cmd = a.deviceMgr.Update(msg)
			return a, cmd
		}

		// Key warning intercepts all keys
		if a.keyWarning.IsVisible() {
			var cmd tea.Cmd
			a.keyWarning, cmd = a.keyWarning.Update(msg)
			return a, cmd
		}

		// Verify dialog intercepts all keys
		if a.verify.IsVisible() {
			var cmd tea.Cmd
			a.verify, cmd = a.verify.Update(msg)
			return a, cmd
		}

		// Help screen intercepts all keys when visible
		if a.help.IsVisible() {
			if msg.String() == "esc" || msg.String() == "?" {
				a.help.Hide()
			}
			return a, nil
		}

		// Settings intercepts keys when visible
		if a.settings.IsVisible() {
			var cmd tea.Cmd
			a.settings, cmd = a.settings.Update(msg)
			return a, cmd
		}

		// Add server dialog intercepts keys when visible
		if a.addServer.IsVisible() {
			var cmd tea.Cmd
			a.addServer, cmd = a.addServer.Update(msg)
			return a, cmd
		}

		// Info panel intercepts keys when visible
		if a.infoPanel.IsVisible() {
			var cmd tea.Cmd
			a.infoPanel, cmd = a.infoPanel.Update(msg)
			return a, cmd
		}

		// Emoji picker intercepts keys when visible
		if a.emojiPicker.IsVisible() {
			var cmd tea.Cmd
			a.emojiPicker, cmd = a.emojiPicker.Update(msg)
			return a, cmd
		}

		// New conversation dialog intercepts keys when visible
		if a.newConv.IsVisible() {
			var cmd tea.Cmd
			a.newConv, cmd = a.newConv.Update(msg, a.client)
			return a, cmd
		}

		// Search screen intercepts keys when visible
		if a.search.IsVisible() {
			var cmd tea.Cmd
			a.search, cmd = a.search.Update(msg, a.client)
			return a, cmd
		}

		switch msg.String() {
		case "ctrl+c":
			if a.client != nil {
				a.client.Close()
			}
			return a, tea.Quit

		case "ctrl+q":
			serverName := "server"
			if a.appConfig != nil && a.serverIdx < len(a.appConfig.Servers) {
				serverName = a.appConfig.Servers[a.serverIdx].Name
			}
			a.quitConfirm.Show(serverName)
			return a, nil

		case "?":
			if a.focus != FocusInput {
				a.help.Toggle()
				return a, nil
			}

		case "ctrl+1", "ctrl+2", "ctrl+3", "ctrl+4", "ctrl+5", "ctrl+6", "ctrl+7", "ctrl+8", "ctrl+9":
			idx := int(msg.String()[len(msg.String())-1]-'0') - 1
			if a.appConfig != nil && idx < len(a.appConfig.Servers) && idx != a.serverIdx {
				// Disconnect current server
				if a.client != nil {
					a.client.Close()
				}

				// Switch to new server
				srv := a.appConfig.Servers[idx]
				a.serverIdx = idx
				a.connected = false
				a.reconnectAttempt = 0

				// Update config for new server
				a.cfg.Host = srv.Host
				a.cfg.Port = srv.Port
				a.cfg.KeyPath = srv.Key
				a.cfg.DataDir = filepath.Join(a.configDir, srv.Host)

				// Clear UI state
				a.messages.SetContext("", "")
				a.sidebar.SetRooms(nil)
				a.sidebar.SetConversations(nil)
				a.pinnedBar = PinnedBarModel{}
				a.statusBar.SetError("Switching to " + srv.Name + "...")
				a.statusBar.SetConnected(false)
				a.updateTitle()

				// Connect to new server
				return a, a.connect()
			}
			return a, nil

		case "ctrl+m":
			a.memberPanel.Toggle()
			if a.memberPanel.IsVisible() {
				a.memberPanel.Refresh(a.messages.room, a.messages.conversation, a.client, a.sidebar.online)
				// Also update input members for @completion
				a.input.SetMembers(a.activeMemberNames())
			}
			return a, nil

		case "ctrl+p":
			a.pinnedBar.Toggle()
			return a, nil

		case "ctrl+f":
			a.search.Show()
			return a, nil

		case "ctrl+,":
			username := ""
			if a.client != nil {
				username = a.client.Username()
			}
			a.settings.Show(a.appConfig, a.configDir, username, a.serverIdx)
			return a, nil

		case "ctrl+i":
			if a.client != nil {
				if a.messages.room != "" {
					a.infoPanel.ShowRoom(a.messages.room, a.client, a.sidebar.online)
				} else if a.messages.conversation != "" {
					a.infoPanel.ShowConversation(a.messages.conversation, a.client, a.sidebar.online)
				}
			}
			return a, nil

		case "ctrl+n":
			// Get all known user names from profiles
			if a.client != nil {
				var allMembers []string
				for _, room := range a.client.Rooms() {
					_ = room // profiles are global, not per-room
				}
				// Collect all known users except self, skipping retired accounts
				a.client.ForEachProfile(func(p *protocol.Profile) {
					if p.User == a.client.Username() {
						return
					}
					if retired, _ := a.client.IsRetired(p.User); retired {
						return
					}
					allMembers = append(allMembers, p.User)
				})
				a.newConv.Show(allMembers)
			}
			return a, nil

		case "tab":
			// Cycle focus: input -> sidebar -> messages -> members (if visible) -> input
			switch a.focus {
			case FocusInput:
				a.focus = FocusSidebar
			case FocusSidebar:
				a.focus = FocusMessages
			case FocusMessages:
				if a.memberPanel.IsVisible() {
					a.focus = FocusMembers
				} else {
					a.focus = FocusInput
				}
			case FocusMembers:
				a.focus = FocusInput
			}
			a.memberPanel.SetFocused(a.focus == FocusMembers)
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
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.conversation, a.client, a.sidebar.online)
					a.input.SetMembers(a.activeMemberNames())
				}
				// Send read receipt for the new context
				a.sendReadReceipt()
			}
		case FocusMessages:
			var cmd tea.Cmd
			a.messages, cmd = a.messages.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case FocusMembers:
			var cmd tea.Cmd
			a.memberPanel, cmd = a.memberPanel.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case FocusInput:
			// Drop sends to 1:1 DMs with a retired partner (banner replaces input)
			if retired, _ := a.currentDMRetiredPartner(); retired {
				// Allow navigation keys to move focus away, block everything else
				switch msg.String() {
				case "tab", "shift+tab", "up", "down", "left", "right":
					// fall through to normal handling below
				default:
					return a, nil
				}
			}
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg, a.client, a.messages.room, a.messages.conversation)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Check for pending slash commands
			if sc := a.input.PendingCommand(); sc != nil {
				a.handleSlashCommand(sc)
			}
		}

	case UnpinRequestMsg:
		if a.client != nil && a.messages.room != "" {
			a.client.Enc().Encode(protocol.Unpin{
				Type: "unpin",
				Room: a.messages.room,
				ID:   msg.MessageID,
			})
		}
		return a, nil

	case MuteToggleMsg:
		a.muted[msg.Target] = msg.Muted
		// Persist to config
		if a.appConfig != nil {
			config.SaveMutedMap(a.configDir, a.appConfig, a.muted)
		}
		if msg.Muted {
			a.statusBar.SetError("Muted: " + msg.Target)
		} else {
			a.statusBar.SetError("Unmuted: " + msg.Target)
		}
		return a, nil

	case MemberActionMsg:
		a.infoPanel.Hide()
		a.memberMenu.Hide()
		switch msg.Action {
		case "message":
			if a.client != nil {
				a.client.CreateDM([]string{msg.User}, "")
			}
		case "create_group":
			if a.client != nil {
				var allMembers []string
				a.client.ForEachProfile(func(p *protocol.Profile) {
					if p.User == a.client.Username() {
						return
					}
					if retired, _ := a.client.IsRetired(p.User); retired {
						return
					}
					allMembers = append(allMembers, p.User)
				})
				a.newConv.Show(allMembers, msg.User)
			}
		case "verify":
			if a.client != nil {
				a.verify.Show(msg.User, a.client)
			}
		case "profile":
			// Show info panel focused on this user's details
			if a.client != nil {
				p := a.client.Profile(msg.User)
				if p != nil {
					a.statusBar.SetError(fmt.Sprintf("%s — %s", p.DisplayName, p.KeyFingerprint))
				}
			}
		}
		return a, nil

	case ProfileUpdateMsg:
		if a.client != nil {
			a.client.Enc().Encode(protocol.SetProfile{
				Type:        "set_profile",
				DisplayName: msg.DisplayName,
			})
			a.statusBar.SetError("Display name updated")
		}
		return a, nil

	case StatusUpdateMsg:
		if a.client != nil {
			a.client.Enc().Encode(protocol.SetStatus{
				Type: "set_status",
				Text: msg.Text,
			})
			a.statusBar.SetError("Status updated")
		}
		return a, nil

	case SettingsActionMsg:
		switch msg.Action {
		case "clear_history":
			if a.client != nil && a.appConfig != nil && msg.ServerIdx < len(a.appConfig.Servers) {
				config.ClearServerData(a.configDir, a.appConfig.Servers[a.serverIdx])
				a.statusBar.SetError("Local history cleared")
			}
		case "remove_server":
			if a.appConfig != nil {
				config.RemoveServer(a.configDir, a.appConfig, msg.ServerIdx)
				a.statusBar.SetError("Server removed")
				// If we removed the active server, close
				if msg.ServerIdx == a.serverIdx {
					if a.client != nil {
						a.client.Close()
					}
					return a, tea.Quit
				}
			}
		case "add_server":
			a.settings.Hide()
			a.addServer.Show()
		case "retire_account":
			a.settings.Hide()
			a.retireConfirm.Show()
		case "manage_devices":
			a.settings.Hide()
			a.deviceMgr.Show()
			if a.client != nil {
				a.client.SendListDevices()
			}
		}
		return a, nil

	case RetireConfirmMsg:
		// User confirmed retirement — send retire_me, close session, quit.
		if a.client != nil {
			if err := a.client.SendRetireMe(msg.Reason); err != nil {
				a.statusBar.SetError("Retirement failed: " + err.Error())
				return a, nil
			}
			// Don't auto-reconnect — the server will close this session, and
			// the retired key won't authenticate on any subsequent attempt.
			a.client.Close()
		}
		return a, tea.Quit

	case DeviceMgrRevokeMsg:
		if a.client != nil {
			if err := a.client.SendRevokeDevice(msg.DeviceID); err != nil {
				a.deviceMgr.SetStatus("Send failed: " + err.Error())
			}
		}
		return a, nil

	case DeviceMgrRefreshMsg:
		if a.client != nil {
			a.client.SendListDevices()
		}
		return a, nil

	case DeviceRevokedQuitMsg:
		// User dismissed the device-revoked dialog — close client (to stop
		// the reconnect loop, which would otherwise keep hitting the same
		// revoked device_id) and quit.
		if a.client != nil {
			a.client.Close()
		}
		return a, tea.Quit

	case VerifyActionMsg:
		if a.client != nil && a.client.Store() != nil {
			a.client.Store().MarkVerified(msg.User)
			a.statusBar.SetError(msg.User + " marked as verified")
		}
		return a, nil

	case KeyWarningAcceptMsg:
		// Key was accepted — re-pin happened during StoreProfile
		a.statusBar.SetError("New key accepted for " + msg.User)
		return a, nil

	case KeyWarningDisconnectMsg:
		if a.client != nil {
			a.client.Close()
		}
		return a, tea.Quit

	case AddServerMsg:
		if a.appConfig != nil {
			srv := config.ServerConfig{
				Name: msg.Name,
				Host: msg.Host,
				Port: msg.Port,
				Key:  msg.Key,
			}
			config.AddServer(a.configDir, a.appConfig, srv)
			a.statusBar.SetError("Server added: " + msg.Name)
		}
		return a, nil

	case EmojiSelectedMsg:
		if a.client != nil {
			var err error
			if msg.Target.Room != "" {
				err = a.client.SendRoomReaction(msg.Target.Room, msg.Target.ID, msg.Emoji)
			} else if msg.Target.Conversation != "" {
				err = a.client.SendDMReaction(msg.Target.Conversation, msg.Target.ID, msg.Emoji)
			}
			if err != nil {
				a.statusBar.SetError("React failed: " + err.Error())
			}
		}
		return a, nil

	case CreateConvMsg:
		// DM created — the dm_created response will come via ServerMsg
		// and the sidebar will update
		return a, nil

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
				// Toggle: if already pinned, unpin
				isPinned := false
				for _, pin := range a.pinnedBar.pins {
					if pin.ID == msg.Msg.ID {
						isPinned = true
						break
					}
				}
				if isPinned {
					a.client.Enc().Encode(protocol.Unpin{
						Type: "unpin",
						Room: a.messages.room,
						ID:   msg.Msg.ID,
					})
				} else {
					a.client.Enc().Encode(protocol.Pin{
						Type: "pin",
						Room: a.messages.room,
						ID:   msg.Msg.ID,
					})
				}
			}
		case "copy":
			CopyToClipboard(msg.Msg.Body)
			a.statusBar.SetError("Copied to clipboard")
		case "open_attachment":
			if a.client != nil && len(msg.Msg.Attachments) > 0 {
				att := msg.Msg.Attachments[0]
				go func() {
					a.statusBar.SetError("Downloading " + att.Name + "...")
					// TODO: get decryption key based on room epoch or DM per-message key
					path, err := a.client.DownloadFile(att.FileID, nil)
					if err != nil {
						a.statusBar.SetError("Download failed: " + err.Error())
						return
					}
					client.OpenFile(path)
				}()
			}
		case "save_attachment":
			if a.client != nil && len(msg.Msg.Attachments) > 0 {
				att := msg.Msg.Attachments[0]
				go func() {
					a.statusBar.SetError("Downloading " + att.Name + "...")
					path, err := a.client.DownloadFile(att.FileID, nil)
					if err != nil {
						a.statusBar.SetError("Download failed: " + err.Error())
						return
					}
					home, _ := os.UserHomeDir()
					dst := filepath.Join(home, "Downloads", att.Name)
					client.SaveFileAs(path, dst)
					a.statusBar.SetError("Saved: " + dst)
				}()
			}
		case "react":
			a.emojiPicker.Show(msg.Msg)
		}
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height

	case tea.MouseMsg:
		return a.handleMouse(msg)

	case connectedWithClient:
		a.client = msg.client
		a.connected = true
		a.reconnectAttempt = 0

		// Populate sidebar and messages
		a.sidebar.SetRooms(a.client.Rooms())
		a.messages.currentUser = a.client.Username()
		if len(a.client.Rooms()) > 0 {
			a.messages.SetContext(a.client.Rooms()[0], "")
			a.messages.LoadFromDB(a.client)
			// Set up member list for @completion
			a.memberPanel.Refresh(a.client.Rooms()[0], "", a.client, a.sidebar.online)
			a.input.SetMembers(a.activeMemberNames())
		}

		a.statusBar.SetUser(a.client.Username(), a.client.IsAdmin())
		a.statusBar.SetConnected(true)
		a.updateTitle()

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

	case passphraseNeededMsg:
		a.passphrase.Show("")
		return a, nil

	case PassphraseResultMsg:
		if msg.Cancelled {
			return a, tea.Quit
		}
		// Cache passphrase by key path for reconnects and server switching
		a.passphraseCache[a.cfg.KeyPath] = msg.Passphrase
		a.passphraseCh <- msg.Passphrase
		return a, a.connect()

	case reconnectAttemptMsg:
		// Try to reconnect
		a.statusBar.SetReconnecting(msg.attempt, 0)
		return a, a.connect() // reuse the existing connect logic

	case ReconnectStatusMsg:
		switch msg.Status {
		case "reconnecting":
			a.statusBar.SetReconnecting(msg.Attempt, msg.NextRetry)
		case "connected":
			a.statusBar.SetConnected(true)
			a.connected = true
		case "failed":
			a.statusBar.SetError("Reconnection failed")
			a.statusBar.SetConnected(false)
		}
		return a, nil

	case ErrMsg:
		a.err = msg.Err
		a.statusBar.SetConnected(false)
		// Auto-reconnect if we were previously connected
		if a.connected || a.reconnectAttempt > 0 {
			a.connected = false
			a.reconnectAttempt++
			delay := time.Duration(a.reconnectAttempt) * time.Second
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			a.statusBar.SetReconnecting(a.reconnectAttempt, delay)
			cmds = append(cmds, a.reconnect(a.reconnectAttempt))
		}
	}

	return a, tea.Batch(cmds...)
}

// handleMouse processes mouse events — clicks and scroll wheel.
func (a App) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// AddServer supports mouse clicks (key list + field focus)
	if a.addServer.IsVisible() {
		var cmd tea.Cmd
		a.addServer, cmd = a.addServer.HandleMouse(msg)
		return a, cmd
	}

	// Other overlays are keyboard-only
	if a.help.IsVisible() || a.search.IsVisible() || a.newConv.IsVisible() ||
		a.emojiPicker.IsVisible() || a.infoPanel.IsVisible() || a.settings.IsVisible() ||
		a.verify.IsVisible() || a.keyWarning.IsVisible() ||
		a.quitConfirm.IsVisible() || a.retireConfirm.IsVisible() ||
		a.deviceRevoked.IsVisible() || a.deviceMgr.IsVisible() ||
		a.contextMenu.IsVisible() || a.memberMenu.IsVisible() {
		return a, nil
	}

	x := msg.X
	y := msg.Y

	switch msg.Button {
	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionRelease {
			return a.handleMouseClick(x, y)
		}

	case tea.MouseButtonWheelUp:
		panel := a.layout.HitTest(x, y)
		if panel == "messages" {
			// Scroll up in messages
			if a.messages.cursor == -1 && len(a.messages.messages) > 0 {
				a.messages.cursor = len(a.messages.messages) - 1
			}
			a.messages.cursor -= 3
			if a.messages.cursor < 0 {
				a.messages.cursor = 0
				// At top — request history
				if !a.messages.loadingHistory && len(a.messages.messages) > 0 {
					return a, a.messages.requestHistory()
				}
			}
		} else if panel == "sidebar" {
			if a.sidebar.cursor > 0 {
				a.sidebar.cursor--
			}
		}

	case tea.MouseButtonWheelDown:
		panel := a.layout.HitTest(x, y)
		if panel == "messages" {
			a.messages.cursor += 3
			if a.messages.cursor >= len(a.messages.messages) {
				a.messages.cursor = len(a.messages.messages) - 1
			}
		} else if panel == "sidebar" {
			if a.sidebar.cursor < a.sidebar.totalItems()-1 {
				a.sidebar.cursor++
			}
		}
	}

	return a, nil
}

// handleMouseClick processes a left click at the given coordinates.
func (a App) handleMouseClick(x, y int) (tea.Model, tea.Cmd) {
	panel := a.layout.HitTest(x, y)

	switch panel {
	case "sidebar":
		a.focus = FocusSidebar
		idx := a.layout.SidebarItemAt(y)
		if idx >= 0 && idx < a.sidebar.totalItems() {
			a.sidebar.cursor = idx
			a.sidebar.updateSelection()
			// Switch to selected room/conversation
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedConv() != a.messages.conversation {
				a.messages.SetContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedConv())
				a.messages.LoadFromDB(a.client)
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.conversation, a.client, a.sidebar.online)
					a.input.SetMembers(a.activeMemberNames())
				}
				a.sendReadReceipt()
			}
		}

	case "messages":
		a.focus = FocusMessages

		// Check if click is on the pinned bar (top 1-2 rows of messages panel)
		pinnedBarRows := 0
		if a.pinnedBar.HasPins() {
			pinnedBarRows = 1 // collapsed = 1 row
			if a.pinnedBar.expanded {
				pinnedBarRows = len(a.pinnedBar.pins) + 2 // header + pins + hint
			}
		}

		relY := y - a.layout.MessagesY0 - 1 // relative to panel content
		if a.pinnedBar.HasPins() && relY < pinnedBarRows {
			if !a.pinnedBar.expanded {
				// Click on collapsed bar — expand
				a.pinnedBar.Toggle()
			} else if relY > 0 && relY <= len(a.pinnedBar.pins) {
				// Click on a specific pin — jump to it
				pinIdx := relY - 1
				if pinIdx >= 0 && pinIdx < len(a.pinnedBar.pins) {
					a.pinnedBar.cursor = pinIdx
					// TODO: jump to pinned message in stream
				}
			}
			return a, nil
		}

		idx := a.layout.MessageItemAt(y)
		if idx >= 0 && idx < len(a.messages.messages) {
			a.messages.cursor = idx
			msg := a.messages.messages[idx]
			if !msg.IsSystem {
				isOwn := a.client != nil && msg.From == a.client.Username()
				isAdmin := a.client != nil && a.client.IsAdmin()
				isRoom := a.messages.room != ""
				a.contextMenu.Show(msg, x, y, isOwn, isAdmin, isRoom, a.pinnedBar.PinIDs())
			}
		}

	case "members":
		if a.memberPanel.IsVisible() {
			a.focus = FocusMembers
			a.memberPanel.SetFocused(true)
			idx := a.layout.MemberItemAt(y)
			if idx >= 0 && idx < len(a.memberPanel.members) {
				a.memberPanel.cursor = idx
				// Show member context menu
				user := a.memberPanel.members[idx].User
				a.memberMenu.Show(user, x, y)
			}
		}

	case "input":
		a.focus = FocusInput
		a.memberPanel.SetFocused(false)
	}

	return a, nil
}

// reconnect attempts to reconnect after a delay with exponential backoff.
func (a App) reconnect(attempt int) tea.Cmd {
	delay := time.Second * time.Duration(attempt)
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return reconnectAttemptMsg{attempt: attempt}
	})
}

type reconnectAttemptMsg struct {
	attempt int
}

// setUnreadDividerAfter finds the first message after lastReadID and sets the unread divider there.
func (a *App) setUnreadDividerAfter(lastReadID string) {
	found := false
	for _, msg := range a.messages.messages {
		if found && msg.ID != "" && !msg.IsSystem {
			a.messages.SetUnreadFrom(msg.ID)
			return
		}
		if msg.ID == lastReadID {
			found = true
		}
	}
}

// updateTitle updates the terminal title with the total unread count.
func (a *App) updateTitle() {
	total := 0
	for _, count := range a.sidebar.unread {
		total += count
	}
	serverName := ""
	if a.appConfig != nil && a.serverIdx < len(a.appConfig.Servers) {
		serverName = a.appConfig.Servers[a.serverIdx].Name
	}
	UpdateTitle(serverName, total)
}

// sendReadReceipt sends a read receipt for the latest message in the active room/conversation.
func (a *App) sendReadReceipt() {
	if a.client == nil {
		return
	}
	lastID := a.messages.LatestMessageID()
	if lastID == "" {
		return
	}
	a.client.SendRead(a.messages.room, a.messages.conversation, lastID)
	// Clear unread divider — user has now seen everything
	a.messages.SetUnreadFrom("")
}

// activeMemberNames returns the member list for @completion, excluding
// retired users. Retired users can't receive mentions (their session is
// gone and future messages can't be wrapped for them), so showing them in
// completion is misleading.
func (a *App) activeMemberNames() []string {
	names := a.memberPanel.MemberNames()
	if a.client == nil {
		return names
	}
	out := names[:0]
	for _, n := range names {
		if retired, _ := a.client.IsRetired(n); retired {
			continue
		}
		out = append(out, n)
	}
	return out
}

// currentDMRetiredPartner reports whether the active conversation is a 1:1
// DM whose other member has been retired. When true, sending should be
// disabled (the server rejects sends to retired members with user_retired
// error) and a notice banner should replace the input.
//
// Returns (false, "") when the conversation is a room, a group DM with 3+
// members, or a 1:1 DM with an active partner.
func (a *App) currentDMRetiredPartner() (bool, string) {
	if a.client == nil || a.messages.conversation == "" {
		return false, ""
	}
	members := a.client.ConvMembers(a.messages.conversation)
	if len(members) != 2 {
		return false, ""
	}
	me := a.client.Username()
	for _, m := range members {
		if m == me {
			continue
		}
		if retired, _ := a.client.IsRetired(m); retired {
			return true, m
		}
	}
	return false, ""
}

// handleSlashCommand processes slash commands that need app-level handling.
func (a *App) handleSlashCommand(sc *SlashCommandMsg) {
	switch sc.Command {
	case "/verify":
		if sc.Arg != "" && a.client != nil {
			a.verify.Show(sc.Arg, a.client)
		}
	case "/search":
		a.search.Show()
	case "/settings":
		username := ""
		if a.client != nil {
			username = a.client.Username()
		}
		a.settings.Show(a.appConfig, a.configDir, username, a.serverIdx)
	case "/help":
		a.help.Toggle()
	case "/upload":
		if sc.Arg == "" {
			a.statusBar.SetError("Usage: /upload <file path>")
			return
		}
		// Check if file exists
		path := sc.Arg
		if _, err := os.Stat(path); err != nil {
			// Check if running over SSH
			msg := "File not found: " + path
			if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
				msg += " (running remotely — copy the file first with scp)"
			}
			a.statusBar.SetError(msg)
			return
		}
		// Upload in background
		go func() {
			if a.client == nil {
				return
			}
			a.statusBar.SetError("Uploading " + filepath.Base(path) + "...")
			fileID, err := a.client.UploadFile(path, sc.Room, sc.Conv)
			if err != nil {
				a.statusBar.SetError("Upload failed: " + err.Error())
				return
			}
			a.statusBar.SetError("Uploaded: " + fileID)
			// TODO: send message referencing the file_id with attachment metadata
		}()
	}
}

// handleServerMessage processes incoming server messages for the UI.
func (a *App) handleServerMessage(msg ServerMsg) {
	switch msg.Type {
	case "message":
		var m protocol.Message
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddRoomMessage(m, a.client)
		// Auto-send read receipt if this is the active room
		if m.Room == a.messages.room {
			a.sendReadReceipt()
		}
		// Notifications for messages not from self
		if a.client != nil && m.From != a.client.Username() {
			payload, err := a.client.DecryptRoomMessage(m.Room, m.Epoch, m.Payload)
			body := "(encrypted)"
			isMention := false
			if err == nil {
				body = payload.Body
				for _, mention := range payload.Mentions {
					if mention == a.client.Username() {
						isMention = true
						break
					}
				}
			}
			if !a.muted[m.Room] {
				SendDesktopNotification(
					fmt.Sprintf("%s in #%s", m.From, m.Room),
					body,
				)
			}
			if a.bell.ShouldBell(m.Room, "", m.From, a.client.Username(), isMention, a.muted) {
				Ring()
			}
		}
	case "dm":
		var m protocol.DM
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddDMMessage(m, a.client)
		// Auto-send read receipt if this is the active conversation
		if m.Conversation == a.messages.conversation {
			a.sendReadReceipt()
		}
		if a.client != nil && m.From != a.client.Username() {
			payload, err := a.client.DecryptDMMessage(m.WrappedKeys, m.Payload)
			body := "(encrypted)"
			if err == nil {
				body = payload.Body
			}
			if !a.muted[m.Conversation] {
				SendDesktopNotification(m.From, body)
			}
			if a.bell.ShouldBell("", m.Conversation, m.From, a.client.Username(), false, a.muted) {
				Ring()
			}
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
			// Set unread divider for the active room
			if m.Room == a.messages.room && m.Count > 0 && m.LastRead != "" {
				a.setUnreadDividerAfter(m.LastRead)
			}
		} else if m.Conversation != "" {
			a.sidebar.SetUnreadConv(m.Conversation, m.Count)
			if m.Conversation == a.messages.conversation && m.Count > 0 && m.LastRead != "" {
				a.setUnreadDividerAfter(m.LastRead)
			}
		}
		a.updateTitle()
	case "deleted":
		var m protocol.Deleted
		json.Unmarshal(msg.Raw, &m)
		a.messages.RemoveMessage(m.ID)
	case "user_retired":
		var m protocol.UserRetired
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.messages.MarkRetired(m.User)
			a.sidebar.MarkRetired(m.User)
			// If the retired user is in the active conversation, show a notice
			if a.client != nil && a.messages.conversation != "" {
				for _, member := range a.client.ConvMembers(a.messages.conversation) {
					if member == m.User {
						a.messages.AddSystemMessage(m.User + "'s account was retired")
						break
					}
				}
			}
		}
	case "retired_users":
		var m protocol.RetiredUsers
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, u := range m.Users {
				a.messages.MarkRetired(u.User)
				a.sidebar.MarkRetired(u.User)
			}
		}
	case "profile":
		// Profiles for retired users include Retired=true — mirror that into
		// the message renderer so historical sender names get [retired] marker.
		var p protocol.Profile
		if err := json.Unmarshal(msg.Raw, &p); err == nil && p.Retired {
			a.messages.MarkRetired(p.User)
			a.sidebar.MarkRetired(p.User)
		}
	case "device_revoked":
		var m protocol.DeviceRevoked
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.deviceRevoked.Show(m.DeviceID, m.Reason)
		}
	case "device_list":
		var m protocol.DeviceList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.deviceMgr.SetDevices(m.Devices)
		}
	case "device_revoke_result":
		var m protocol.DeviceRevokeResult
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			if m.Success {
				a.deviceMgr.SetStatus("✓ revoked " + m.DeviceID + " — refreshing...")
				// Re-fetch the list so UI reflects the new state
				if a.client != nil {
					a.client.SendListDevices()
				}
			} else {
				a.deviceMgr.SetStatus("Error: " + m.Error)
			}
		}
	case "conversation_event":
		var m protocol.ConversationEvent
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			// Show a system message in the stream when it's the active conversation
			if m.Conversation == a.messages.conversation && m.Event == "leave" {
				if m.Reason == "retirement" {
					a.messages.AddSystemMessage(m.User + "'s account was retired")
				} else {
					a.messages.AddSystemMessage(m.User + " left the conversation")
				}
			}
		}
	case "reaction":
		var m protocol.Reaction
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddReactionDecrypted(m, a.client)
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
	case "pins":
		var m protocol.Pins
		json.Unmarshal(msg.Raw, &m)
		if m.Room == a.messages.room {
			// Decrypt bundled pinned messages and add to the display list for previews
			var pinnedDisplayMsgs []DisplayMessage
			pinnedDisplayMsgs = append(pinnedDisplayMsgs, a.messages.messages...)
			for _, raw := range m.MessageData {
				var pm protocol.Message
				if err := json.Unmarshal(raw, &pm); err != nil {
					continue
				}
				body := "(encrypted)"
				if a.client != nil {
					payload, err := a.client.DecryptRoomMessage(pm.Room, pm.Epoch, pm.Payload)
					if err == nil {
						body = payload.Body
					}
				}
				pinnedDisplayMsgs = append(pinnedDisplayMsgs, DisplayMessage{
					ID:   pm.ID,
					From: pm.From,
					Body: body,
					TS:   pm.TS,
					Room: pm.Room,
				})
			}
			a.pinnedBar.SetPins(m.Room, m.Messages, pinnedDisplayMsgs)
		}
	case "pinned":
		var m protocol.Pinned
		json.Unmarshal(msg.Raw, &m)
		if m.Room == a.messages.room {
			a.pinnedBar.AddPin(m.ID, a.messages.messages)
		}
	case "unpinned":
		var m protocol.Unpinned
		json.Unmarshal(msg.Raw, &m)
		if m.Room == a.messages.room {
			a.pinnedBar.RemovePin(m.ID)
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
	memberWidth := 0
	if a.memberPanel.IsVisible() {
		memberWidth = 18
	}
	statusBarHeight := 1
	inputHeight := 3
	mainWidth := a.width - sidebarWidth - memberWidth - 3 // borders
	if memberWidth > 0 {
		mainWidth -= 1 // extra gap
	}
	mainHeight := a.height - statusBarHeight - inputHeight - 2

	// Store layout for mouse hit testing
	a.layout = Layout{
		SidebarX0: 0, SidebarX1: sidebarWidth + 2,
		SidebarY0: 0, SidebarY1: a.height - statusBarHeight - 1,
		SidebarWidth: sidebarWidth,

		MessagesX0: sidebarWidth + 2, MessagesX1: sidebarWidth + 2 + mainWidth + 2,
		MessagesY0: 0, MessagesY1: mainHeight + 2,
		MessagesWidth: mainWidth,

		InputX0: sidebarWidth + 2, InputX1: sidebarWidth + 2 + mainWidth + 2,
		InputY0: mainHeight + 2, InputY1: a.height - statusBarHeight - 1,

		MemberX0: sidebarWidth + 2 + mainWidth + 3, MemberX1: a.width,
		MemberY0: 0, MemberY1: a.height - statusBarHeight - 1,
		MemberWidth: memberWidth,

		StatusY: a.height - 1,
		Height:  a.height,
	}

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
		msgHeight := mainHeight
		if a.showHelpHint {
			msgHeight-- // make room for the hint
		}
		messages := a.messages.View(mainWidth, msgHeight, a.focus == FocusMessages)
		hint := ""
		if a.showHelpHint {
			hint = helpDescStyle.Render("  Press ? for help or / for commands") + "\n"
		}
		var input string
		if retired, other := a.currentDMRetiredPartner(); retired {
			input = helpDescStyle.Render("  " + other + "'s account has been retired — this conversation is read-only. Verify their new account (if any) out of band before starting a new DM.")
		} else {
			input = a.input.View(mainWidth, a.focus == FocusInput)
		}
		mainPanel = messages + "\n" + hint + input
	}

	status := a.statusBar.View(a.width)

	var body string
	if a.memberPanel.IsVisible() {
		members := a.memberPanel.View(memberWidth, a.height-statusBarHeight-1)
		body = joinHorizontal(sidebar, joinHorizontal(mainPanel, members))
	} else {
		body = joinHorizontal(sidebar, mainPanel)
	}
	screen := body + "\n" + status

	// Overlays
	if a.help.IsVisible() {
		return a.help.View(a.width, a.height)
	}
	if a.newConv.IsVisible() {
		return a.newConv.View(a.width)
	}
	if a.emojiPicker.IsVisible() {
		return a.emojiPicker.View()
	}
	if a.infoPanel.IsVisible() {
		return a.infoPanel.View(a.width)
	}
	if a.settings.IsVisible() {
		return a.settings.View(a.width, a.height)
	}
	if a.addServer.IsVisible() {
		return a.addServer.View(a.width)
	}
	if a.verify.IsVisible() {
		return a.verify.View(a.width)
	}
	if a.keyWarning.IsVisible() {
		return a.keyWarning.View(a.width)
	}
	if a.quitConfirm.IsVisible() {
		return a.quitConfirm.View(a.width)
	}
	if a.retireConfirm.IsVisible() {
		return a.retireConfirm.View(a.width)
	}
	if a.deviceRevoked.IsVisible() {
		return a.deviceRevoked.View(a.width)
	}
	if a.deviceMgr.IsVisible() {
		return a.deviceMgr.View(a.width)
	}
	if a.contextMenu.IsVisible() {
		return screen + "\n" + a.contextMenu.View()
	}
	if a.memberMenu.IsVisible() {
		return screen + "\n" + a.memberMenu.View()
	}
	if a.passphrase.IsVisible() {
		return a.passphrase.View(a.width)
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
