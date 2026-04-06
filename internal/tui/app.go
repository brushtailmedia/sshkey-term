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
	infoPanel    InfoPanelModel
	pendingPanel  PendingPanelModel
	connectFailed ConnectFailedModel
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

		// Connection failed overlay (first-run)
		if a.connectFailed.IsVisible() {
			var cmd tea.Cmd
			a.connectFailed, cmd = a.connectFailed.Update(msg)
			return a, cmd
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

		// Pending panel intercepts keys when visible
		if a.pendingPanel.IsVisible() {
			var cmd tea.Cmd
			a.pendingPanel, cmd = a.pendingPanel.Update(msg)
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
				a.input.SetMembers(a.activeMemberEntries())
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
					a.input.SetMembers(a.activeMemberEntries())
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
		// Don't hide the member menu when the action IS to open it
		if msg.Action != "menu" {
			a.memberMenu.Hide()
		}
		switch msg.Action {
		case "menu":
			// Open MemberMenu via keyboard (Enter on a member in the panel).
			// Same options as right-click: message, create_group, verify,
			// profile. Screen position (0, 0) is fine — menu renders as
			// a centered dialog regardless.
			a.memberMenu.Show(msg.User, a.resolveDisplayName(msg.User), 0, 0)
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
			// Don't show success here — wait for the server's profile
			// broadcast (success) or error response (username_taken).
			// Errors display automatically via the "error" handler.
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
		case "copy_pubkey":
			if a.client != nil {
				pubKey := a.client.PublicKeyAuthorized()
				if pubKey != "" {
					CopyToClipboard(pubKey)
					a.statusBar.SetError("Public key copied to clipboard")
				}
			}
		case "copy_fingerprint":
			if a.client != nil {
				fp := a.client.KeyFingerprint()
				if fp != "" {
					CopyToClipboard(fp)
					a.statusBar.SetError(fp + " — copied to clipboard")
				}
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
			a.statusBar.SetError(a.resolveDisplayName(msg.User) + " marked as verified")
		}
		return a, nil

	case KeyWarningAcceptMsg:
		// Key was accepted — re-pin happened during StoreProfile
		a.statusBar.SetError("New key accepted for " + a.resolveDisplayName(msg.User))
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
			// Skip if the current user already has a reaction with this
			// emoji on the target message (client-enforced de-dup; see
			// PROTOCOL.md Reactions section — "Picking the same emoji
			// twice in the emoji picker should be a no-op").
			if msg.Target.UserHasReacted(a.client.Username(), msg.Emoji) {
				a.statusBar.SetError("You already reacted with " + msg.Emoji)
				return a, nil
			}
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
		if a.client == nil {
			return a, nil
		}
		// Try local DB first — avoids a server round-trip when messages
		// are already cached from previous sync/history fetches
		if st := a.client.Store(); st != nil {
			localMsgs, err := st.GetMessagesBefore(msg.Room, msg.Conversation, msg.BeforeID, 100)
			if err == nil && len(localMsgs) > 0 {
				// Load epoch keys for these messages so they can be decrypted
				if msg.Room != "" {
					epochs := make(map[int64]bool)
					for _, m := range localMsgs {
						if m.Epoch > 0 {
							epochs[m.Epoch] = true
						}
					}
					epochList := make([]int64, 0, len(epochs))
					for e := range epochs {
						epochList = append(epochList, e)
					}
					a.client.LoadEpochKeysFromDB(msg.Room, epochList)
				}

				// Convert to display messages and prepend
				var display []DisplayMessage
				for _, m := range localMsgs {
					from := m.Sender
					if a.client != nil {
						from = a.client.DisplayName(m.Sender)
					}
					display = append(display, DisplayMessage{
						ID:           m.ID,
						FromID:       m.Sender,
						From:         from,
						Body:         m.Body,
						TS:           m.TS,
						Room:         m.Room,
						Conversation: m.Conversation,
						ReplyTo:      m.ReplyTo,
						Mentions:     m.Mentions,
					})
				}
				hasMore := len(localMsgs) >= 100
				a.messages.PrependMessages(display, hasMore)
				a.messages.loadingHistory = false

				// Load reactions for the prepended messages
				msgIDs := make([]string, 0, len(localMsgs))
				for _, m := range localMsgs {
					if m.ID != "" {
						msgIDs = append(msgIDs, m.ID)
					}
				}
				if len(msgIDs) > 0 {
					if reactions, err := st.GetReactionsForMessages(msgIDs); err == nil {
						for _, r := range reactions {
							a.messages.addReactionRecord(r.MessageID, r.ReactionID, r.User, r.Emoji)
						}
					}
				}

				// If local DB had fewer than a full page, also hit server
				// for any remaining that haven't been synced yet
				if !hasMore {
					a.client.RequestHistory(msg.Room, msg.Conversation, msg.BeforeID, 100)
				}
				return a, nil
			}
		}
		// No local data — fall through to server
		a.client.RequestHistory(msg.Room, msg.Conversation, msg.BeforeID, 100)
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
			if a.client != nil && (msg.Msg.FromID == a.client.Username() || a.client.IsAdmin()) {
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
					path, err := a.client.DownloadFile(att.FileID, att.DecryptKey)
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
					path, err := a.client.DownloadFile(att.FileID, att.DecryptKey)
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
		case "open_menu":
			// Keyboard-triggered context menu opener (Enter on selected message).
			// Shows the same menu the mouse right-click produces, at screen origin.
			isOwn := a.client != nil && msg.Msg.FromID == a.client.Username()
			isAdmin := a.client != nil && a.client.IsAdmin()
			isRoom := a.messages.room != ""
			var myEmojis []string
			if a.client != nil {
				myEmojis = msg.Msg.UserEmojis(a.client.Username())
			}
			a.contextMenu.Show(msg.Msg, 0, 0, isOwn, isAdmin, isRoom, a.pinnedBar.PinIDs(), myEmojis)
		case "react":
			a.emojiPicker.Show(msg.Msg)
		case "unreact":
			// Remove one of the current user's reactions on this message.
			//   Data == emoji  → remove that specific emoji (context menu path)
			//   Data == ""     → remove first reaction user has (keyboard 'u' path;
			//                    repeatable presses peel off more)
			if a.client == nil {
				return a, nil
			}
			user := a.client.Username()
			emoji := msg.Data
			if emoji == "" {
				emojis := msg.Msg.UserEmojis(user)
				if len(emojis) == 0 {
					a.statusBar.SetError("No reactions to remove")
					return a, nil
				}
				emoji = emojis[0]
			}
			ids := msg.Msg.UserReactionIDs(user, emoji)
			if len(ids) == 0 {
				a.statusBar.SetError("Reaction already removed")
				return a, nil
			}
			if err := a.client.SendUnreact(ids[0]); err != nil {
				a.statusBar.SetError("Unreact failed: " + err.Error())
			}
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
		a.messages.currentUser = a.client.DisplayName(a.client.Username())
		a.messages.currentUserID = a.client.Username()
		a.messages.resolveName = a.client.DisplayName
		a.search.resolveName = a.client.DisplayName
		a.sidebar.resolveName = a.client.DisplayName
		a.newConv.resolveName = a.client.DisplayName
		if len(a.client.Rooms()) > 0 {
			a.messages.SetContext(a.client.Rooms()[0], "")
			a.messages.LoadFromDB(a.client)
			// Set up member list for @completion
			a.memberPanel.Refresh(a.client.Rooms()[0], "", a.client, a.sidebar.online)
			a.input.SetMembers(a.activeMemberEntries())
		}

		a.statusBar.SetUser(a.client.DisplayName(a.client.Username()), a.client.IsAdmin())
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
		if a.connected || a.reconnectAttempt > 0 {
			// Was previously connected — auto-reconnect
			a.connected = false
			a.reconnectAttempt++
			delay := time.Duration(a.reconnectAttempt) * time.Second
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			a.statusBar.SetReconnecting(a.reconnectAttempt, delay)
			cmds = append(cmds, a.reconnect(a.reconnectAttempt))
		} else {
			// First-ever connection failed — show guidance overlay
			// with key info so the user can share with admin
			fp := ""
			pubKey := ""
			if a.client != nil {
				fp = a.client.KeyFingerprint()
				pubKey = a.client.PublicKeyAuthorized()
			}
			if fp == "" {
				// Client didn't initialize — read key directly
				fp = "unknown"
			}
			a.connectFailed.Show(msg.Err.Error(), fp, pubKey)
		}

	case ConnectFailedRetryMsg:
		// User pressed [r] from the connection failed overlay
		cmds = append(cmds, a.connect())
	}

	return a, tea.Batch(cmds...)
}

// handleMouse processes mouse events — clicks and scroll wheel.
func (a App) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Dialogs with mouse support — route clicks to the dialog.
	if a.connectFailed.IsVisible() {
		var cmd tea.Cmd
		a.connectFailed, cmd = a.connectFailed.HandleMouse(msg)
		return a, cmd
	}
	if a.addServer.IsVisible() {
		var cmd tea.Cmd
		a.addServer, cmd = a.addServer.HandleMouse(msg)
		return a, cmd
	}
	if a.deviceMgr.IsVisible() {
		var cmd tea.Cmd
		a.deviceMgr, cmd = a.deviceMgr.HandleMouse(msg)
		return a, cmd
	}
	if a.retireConfirm.IsVisible() {
		var cmd tea.Cmd
		a.retireConfirm, cmd = a.retireConfirm.HandleMouse(msg)
		return a, cmd
	}
	if a.deviceRevoked.IsVisible() {
		var cmd tea.Cmd
		a.deviceRevoked, cmd = a.deviceRevoked.HandleMouse(msg)
		return a, cmd
	}
	if a.settings.IsVisible() {
		var cmd tea.Cmd
		a.settings, cmd = a.settings.HandleMouse(msg)
		return a, cmd
	}

	// Other overlays are keyboard-only
	if a.help.IsVisible() || a.search.IsVisible() || a.newConv.IsVisible() ||
		a.emojiPicker.IsVisible() || a.infoPanel.IsVisible() || a.pendingPanel.IsVisible() ||
		a.verify.IsVisible() || a.keyWarning.IsVisible() ||
		a.quitConfirm.IsVisible() ||
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
					a.input.SetMembers(a.activeMemberEntries())
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
				isOwn := a.client != nil && msg.FromID == a.client.Username()
				isAdmin := a.client != nil && a.client.IsAdmin()
				isRoom := a.messages.room != ""
				var myEmojis []string
				if a.client != nil {
					myEmojis = msg.UserEmojis(a.client.Username())
				}
				a.contextMenu.Show(msg, x, y, isOwn, isAdmin, isRoom, a.pinnedBar.PinIDs(), myEmojis)
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
				a.memberMenu.Show(user, a.resolveDisplayName(user), x, y)
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
// resolveDisplayName maps a username (nanoid) to its display name for system
// messages. Falls back to the raw username if no profile is available.
func (a *App) resolveDisplayName(username string) string {
	if a.client != nil {
		return a.client.DisplayName(username)
	}
	return username
}

func (a *App) sendReadReceipt() {
	if a.client == nil {
		return
	}
	lastID := a.messages.LatestMessageID()
	if lastID == "" {
		return
	}
	a.client.SendRead(a.messages.room, a.messages.conversation, lastID)
	// Persist locally so the unread divider survives restarts
	if st := a.client.Store(); st != nil {
		target := a.messages.room
		if target == "" {
			target = a.messages.conversation
		}
		if target != "" {
			st.StoreReadPosition(target, lastID)
		}
	}
	// Clear unread divider — user has now seen everything
	a.messages.SetUnreadFrom("")
}

// activeMemberNames returns the member list for @completion, excluding
// retired users. Retired users can't receive mentions (their session is
// gone and future messages can't be wrapped for them), so showing them in
// completion is misleading.
func (a *App) activeMemberEntries() []MemberEntry {
	members := a.memberPanel.MemberEntries()
	if a.client == nil {
		return members
	}
	out := members[:0]
	for _, m := range members {
		if retired, _ := a.client.IsRetired(m.Username); retired {
			continue
		}
		out = append(out, m)
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
	case "/unverify":
		if sc.Arg != "" && a.client != nil {
			if st := a.client.Store(); st != nil {
				st.ClearVerified(sc.Arg)
				a.statusBar.SetError("Verification removed for " + sc.Arg)
			}
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
	case "/pending":
		if a.client == nil {
			return
		}
		if !a.client.IsAdmin() {
			a.statusBar.SetError("Admin required")
			return
		}
		a.client.SendListPendingKeys()
	case "/mykey":
		if a.client == nil {
			return
		}
		pubKey := a.client.PublicKeyAuthorized()
		fingerprint := a.client.KeyFingerprint()
		if pubKey == "" {
			a.statusBar.SetError("No key available")
			return
		}
		CopyToClipboard(pubKey)
		a.statusBar.SetError(fingerprint + " — public key copied to clipboard")
		// Panel will open when pending_keys_list response arrives
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
		// Upload + send as a message with attachment. Routes to room or DM
		// sender based on the current conversation context.
		go func() {
			if a.client == nil {
				return
			}
			a.statusBar.SetError("Uploading " + filepath.Base(path) + "...")
			body := filepath.Base(path)
			var err error
			if sc.Room != "" {
				err = a.client.SendRoomMessageFile(sc.Room, body, path, "", nil)
			} else if sc.Conv != "" {
				err = a.client.SendDMMessageFile(sc.Conv, body, path, "", nil)
			} else {
				a.statusBar.SetError("No active room or conversation")
				return
			}
			if err != nil {
				a.statusBar.SetError("Upload failed: " + err.Error())
				return
			}
			a.statusBar.SetError("Uploaded: " + filepath.Base(path))
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
					fmt.Sprintf("%s in #%s", a.resolveDisplayName(m.From), m.Room),
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
				SendDesktopNotification(a.resolveDisplayName(m.From), body)
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
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + "'s account was retired")
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
	case "admin_notify":
		// Update status bar pending indicator for admins
		if a.client != nil && a.client.IsAdmin() {
			a.statusBar.SetPending(true)
		}
	case "pending_keys_list":
		// Response to /pending — show the panel
		if a.client != nil {
			keys := a.client.PendingKeys()
			a.pendingPanel.Show(keys)
			a.statusBar.SetPending(len(keys) > 0)
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
			if m.Event == "leave" {
				// Show system message in the active conversation stream
				if m.Conversation == a.messages.conversation {
					if m.Reason == "retirement" {
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + "'s account was retired")
					} else {
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " left the conversation")
					}
				}
				// If WE left, remove from sidebar and switch away
				if a.client != nil && m.User == a.client.Username() {
					a.sidebar.RemoveConversation(m.Conversation)
					if a.messages.conversation == m.Conversation {
						a.messages.SetContext("", "")
						if len(a.sidebar.rooms) > 0 {
							a.sidebar.selectedRoom = a.sidebar.rooms[0]
							a.messages.SetContext(a.sidebar.rooms[0], "")
						}
					}
				}
			}
		}
	case "conversation_renamed":
		var m protocol.ConversationRenamed
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.RenameConversation(m.Conversation, m.Name)
			if m.Conversation == a.messages.conversation {
				a.messages.AddSystemMessage(a.resolveDisplayName(m.RenamedBy) + " renamed the conversation to " + m.Name)
			}
		}
	case "room_event":
		var m protocol.RoomEvent
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			if m.Room == a.messages.room {
				switch m.Event {
				case "join":
					a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " joined")
				case "leave":
					a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " left")
				}
			}
		}
	case "dm_created":
		var m protocol.DMCreated
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.AddConversation(protocol.ConversationInfo{
				ID:      m.Conversation,
				Members: m.Members,
				Name:    m.Name,
			})
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
		// Apply reactions on the synced messages (reactions are encrypted
		// and go through the same AddReactionDecrypted path as real-time)
		for _, raw := range batch.Reactions {
			a.handleServerMessage(ServerMsg{Type: "reaction", Raw: raw})
		}
	case "history_result":
		var result protocol.HistoryResult
		json.Unmarshal(msg.Raw, &result)
		for _, raw := range result.Messages {
			histType, _ := protocol.TypeOf(raw)
			a.handleServerMessage(ServerMsg{Type: histType, Raw: raw})
		}
		for _, raw := range result.Reactions {
			a.handleServerMessage(ServerMsg{Type: "reaction", Raw: raw})
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
		// If this is a username_taken error and settings is open, show it
		// prominently so the user knows their name change failed
		if m.Code == "username_taken" || m.Code == "invalid_profile" {
			a.statusBar.SetError("Name change failed: " + m.Message)
		}
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
	if a.connectFailed.IsVisible() {
		return a.connectFailed.View(a.width)
	}
	if a.pendingPanel.IsVisible() {
		return a.pendingPanel.View(a.width)
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
