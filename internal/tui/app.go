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
	quitConfirm       QuitConfirmModel
	retireConfirm     RetireConfirmModel
	leaveConfirm      LeaveConfirmModel
	leaveRoomConfirm  LeaveRoomConfirmModel
	deleteDMConfirm   DeleteDMConfirmModel
	deleteGroupConfirm DeleteGroupConfirmModel
	deleteRoomConfirm  DeleteRoomConfirmModel
	deviceRevoked     DeviceRevokedModel
	deviceMgr     DeviceMgrModel
	quickSwitch QuickSwitchModel
	threadPanel ThreadPanelModel
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

	// pendingCreateDM tracks an in-flight create_dm so the client can
	// transparently retry if the server returns server_busy (a cleanup
	// mutex was held for a microsecond by a concurrent leave_dm). Single
	// field rather than a map because the user can only kick off one
	// create_dm at a time via the newconv dialog.
	pendingCreateDM pendingCreateDMState
}

// pendingCreateDMState records a create_dm we have sent and are waiting
// on a response for, along with how many automatic retries remain if
// the response is server_busy.
type pendingCreateDMState struct {
	other   string
	retries int
}

// maxCreateDMAutoRetries caps the number of silent retries on server_busy
// before the error is surfaced to the user. Cleanup holds the mutex for
// microseconds in the common case; 3 retries at ~80ms each should cover
// any realistic contention without the user noticing.
const maxCreateDMAutoRetries = 3

// createDMRetryDelay is how long to wait before re-sending create_dm
// after a server_busy response. Long enough that cleanup will finish,
// short enough to feel instant to the user.
const createDMRetryDelay = 80

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
		quickSwitch: NewQuickSwitch(),
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

		// Leave confirmation intercepts all keys
		if a.leaveConfirm.IsVisible() {
			var cmd tea.Cmd
			a.leaveConfirm, cmd = a.leaveConfirm.Update(msg)
			return a, cmd
		}

		// Leave room confirmation intercepts all keys
		if a.leaveRoomConfirm.IsVisible() {
			var cmd tea.Cmd
			a.leaveRoomConfirm, cmd = a.leaveRoomConfirm.Update(msg)
			return a, cmd
		}

		// Delete-DM confirmation intercepts all keys
		if a.deleteDMConfirm.IsVisible() {
			var cmd tea.Cmd
			a.deleteDMConfirm, cmd = a.deleteDMConfirm.Update(msg)
			return a, cmd
		}

		// Delete-group confirmation intercepts all keys
		if a.deleteGroupConfirm.IsVisible() {
			var cmd tea.Cmd
			a.deleteGroupConfirm, cmd = a.deleteGroupConfirm.Update(msg)
			return a, cmd
		}

		// Delete-room confirmation intercepts all keys (Phase 12)
		if a.deleteRoomConfirm.IsVisible() {
			var cmd tea.Cmd
			a.deleteRoomConfirm, cmd = a.deleteRoomConfirm.Update(msg)
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
				a.focus = FocusInput
			}
			return a, nil
		}

		// Settings intercepts keys when visible
		if a.settings.IsVisible() {
			var cmd tea.Cmd
			a.settings, cmd = a.settings.Update(msg)
			if !a.settings.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Add server dialog intercepts keys when visible
		if a.addServer.IsVisible() {
			var cmd tea.Cmd
			a.addServer, cmd = a.addServer.Update(msg)
			if !a.addServer.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Info panel intercepts keys when visible
		if a.infoPanel.IsVisible() {
			var cmd tea.Cmd
			a.infoPanel, cmd = a.infoPanel.Update(msg)
			if !a.infoPanel.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Pending panel intercepts keys when visible
		if a.pendingPanel.IsVisible() {
			var cmd tea.Cmd
			a.pendingPanel, cmd = a.pendingPanel.Update(msg)
			if !a.pendingPanel.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Emoji picker intercepts keys when visible
		if a.emojiPicker.IsVisible() {
			var cmd tea.Cmd
			a.emojiPicker, cmd = a.emojiPicker.Update(msg)
			if !a.emojiPicker.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// New conversation dialog intercepts keys when visible
		if a.newConv.IsVisible() {
			var cmd tea.Cmd
			a.newConv, cmd = a.newConv.Update(msg)
			if !a.newConv.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Thread panel intercepts keys when visible
		if a.threadPanel.IsVisible() {
			var cmd tea.Cmd
			a.threadPanel, cmd = a.threadPanel.Update(msg)
			if !a.threadPanel.IsVisible() {
				a.focus = FocusMessages
			}
			return a, cmd
		}

		// Quick switch intercepts keys when visible
		if a.quickSwitch.IsVisible() {
			var cmd tea.Cmd
			a.quickSwitch, cmd = a.quickSwitch.Update(msg)
			if !a.quickSwitch.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Search screen intercepts keys when visible
		if a.search.IsVisible() {
			var cmd tea.Cmd
			a.search, cmd = a.search.Update(msg, a.client)
			if !a.search.IsVisible() {
				a.focus = FocusInput
			}
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
				a.messages.SetContext("", "", "")
				a.sidebar.SetRooms(nil)
				a.sidebar.SetGroups(nil)
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
				a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
				// Request actual room membership from server (lazy)
				if a.messages.room != "" && a.client != nil {
					a.client.RequestRoomMembers(a.messages.room)
				}
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
				username = a.client.UserID()
			}
			a.settings.Show(a.appConfig, a.configDir, username, a.serverIdx)
			return a, nil

		case "ctrl+i":
			if a.client != nil {
				if a.messages.room != "" {
					a.infoPanel.ShowRoom(a.messages.room, a.client, a.sidebar.online)
					// Request actual room membership from server (lazy)
					a.client.RequestRoomMembers(a.messages.room)
				} else if a.messages.group != "" {
					a.infoPanel.ShowGroup(a.messages.group, a.client, a.sidebar.online)
				} else if a.messages.dm != "" {
					a.infoPanel.ShowDM(a.messages.dm, a.client, a.sidebar.online)
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
					if p.User == a.client.UserID() {
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

		case "alt+up":
			if a.sidebar.cursor > 0 {
				a.sidebar.cursor--
				a.sidebar.updateSelection()
				a.switchToSidebarSelection()
			}
			return a, nil

		case "alt+down":
			if a.sidebar.cursor < a.sidebar.totalItems()-1 {
				a.sidebar.cursor++
				a.sidebar.updateSelection()
				a.switchToSidebarSelection()
			}
			return a, nil

		case "ctrl+k":
			a.quickSwitch.Show(a.sidebar.rooms, a.sidebar.groups, a.sidebar.resolveName, a.sidebar.resolveRoomName)
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
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedGroup() != a.messages.group {
				a.messages.SetContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), "")
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
					if a.messages.room != "" && a.client != nil {
						a.client.RequestRoomMembers(a.messages.room)
					}
					a.input.SetMembers(a.activeMemberEntries())
				}
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
			// Block input on archived contexts (user has /leave'd a room or
			// group, OR the room has been retired by an admin). Allow
			// navigation keys to move focus away. Slash commands like
			// /delete still work because we explicitly let "enter" through
			// and the input filters slash commands separately.
			if a.messages.IsLeft() || a.messages.IsRoomRetired() {
				switch msg.String() {
				case "tab", "shift+tab", "up", "down", "left", "right":
					// fall through
				case "enter":
					// Only allow slash commands (start with /), block normal sends
					text := strings.TrimSpace(a.input.Value())
					if !strings.HasPrefix(text, "/") {
						if a.messages.IsRoomRetired() {
							a.statusBar.SetError("This room was archived by an admin — type /delete to remove from your view")
						} else {
							label := "context"
							switch {
							case a.messages.room != "":
								label = "room"
							case a.messages.group != "":
								label = "group"
							}
							a.statusBar.SetError("You left this " + label + " — type /delete to remove from your view")
						}
						return a, nil
					}
					// fall through for slash commands
				default:
					// Allow typing so the user can compose /delete
					// fall through
				}
			}
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg, a.client, a.messages.room, a.messages.group, a.messages.dm)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Clear status bar error on send (not on typing)
			if a.input.DidSend() {
				a.statusBar.ClearError()
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
				a.client.CreateGroup([]string{msg.User}, "")
			}
		case "create_group":
			if a.client != nil {
				var allMembers []string
				a.client.ForEachProfile(func(p *protocol.Profile) {
					if p.User == a.client.UserID() {
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

	case LeaveConfirmMsg:
		// User confirmed /leave — send leave_group and wait for the server's
		// "group_left" echo before touching local state. The confirmation
		// handler (case "group_left" below) does the DB write, sidebar
		// refresh, messages-view flip, and status bar update.
		if a.client != nil && msg.Group != "" {
			if err := a.client.Enc().Encode(map[string]string{
				"type":  "leave_group",
				"group": msg.Group,
			}); err != nil {
				a.statusBar.SetError("Leave failed: " + err.Error())
			}
		}
		return a, nil

	case LeaveRoomConfirmMsg:
		// User confirmed /leave for a room — send leave_room and wait for
		// the server's "room_left" echo (or "forbidden" error) before
		// touching local state. Server is authoritative on the policy
		// gate; if allow_self_leave_rooms is disabled the error path
		// surfaces a status-bar message and nothing else changes.
		if a.client != nil && msg.Room != "" {
			if err := a.client.Enc().Encode(map[string]string{
				"type": "leave_room",
				"room": msg.Room,
			}); err != nil {
				a.statusBar.SetError("Leave failed: " + err.Error())
			}
		}
		return a, nil

	case DeleteDMConfirmMsg:
		// User confirmed /delete on a 1:1 DM. /delete is silent (the
		// other party is never notified) and atomic in the sense that
		// we wait for the server's dm_left echo before touching local
		// state. The echo arrives via the dm_left case below, which
		// calls into client.go to purge local messages and then drops
		// the sidebar entry here.
		//
		// Server-busy retries are NOT auto-handled — if the server
		// returns server_busy because a cleanup is in progress, the
		// status bar will surface the error and the user can re-issue.
		if a.client != nil && msg.DM != "" {
			if err := a.client.Enc().Encode(map[string]string{
				"type": "leave_dm",
				"dm":   msg.DM,
			}); err != nil {
				a.statusBar.SetError("Delete failed: " + err.Error())
			}
		}
		return a, nil

	case DeleteGroupConfirmMsg:
		// User confirmed /delete on a group DM. Send delete_group and
		// wait for the server's group_deleted echo before touching local
		// state. The echo arrives via the group_deleted case below,
		// which is handled by both client.go (purge messages, mark left)
		// and here (drop sidebar entry, reset active context).
		//
		// Idempotent: if the user has already left the group via /leave,
		// the server still records the deletion intent and echoes back,
		// so the same purge path runs.
		if a.client != nil && msg.Group != "" {
			if err := a.client.DeleteGroup(msg.Group); err != nil {
				a.statusBar.SetError("Delete failed: " + err.Error())
			}
		}
		return a, nil

	case DeleteRoomConfirmMsg:
		// User confirmed /delete on a room. Sends delete_room and waits
		// for the server's room_deleted echo before touching local state.
		// The echo arrives via the room_deleted case, which is handled
		// by both client.go (purge messages, epoch keys, reactions; set
		// left_at) and here (drop sidebar entry, reset active context).
		//
		// Idempotent: works on both active and retired rooms. For active
		// rooms the server performs the leave side-effects first (remove
		// from room_members, broadcast room_event leave, rotate epoch);
		// for retired rooms the leave steps are skipped since the room
		// is already archived and the epoch is already frozen.
		if a.client != nil && msg.Room != "" {
			if err := a.client.DeleteRoom(msg.Room); err != nil {
				a.statusBar.SetError("Delete failed: " + err.Error())
			}
		}
		return a, nil

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
			if msg.Target.UserHasReacted(a.client.UserID(), msg.Emoji) {
				a.statusBar.SetError("You already reacted with " + msg.Emoji)
				return a, nil
			}
			var err error
			if msg.Target.Room != "" {
				err = a.client.SendRoomReaction(msg.Target.Room, msg.Target.ID, msg.Emoji)
			} else if msg.Target.Group != "" {
				err = a.client.SendGroupReaction(msg.Target.Group, msg.Target.ID, msg.Emoji)
			}
			if err != nil {
				a.statusBar.SetError("React failed: " + err.Error())
			}
		}
		return a, nil

	case CreateConvMsg:
		// User picked a new DM or group target from the newconv dialog.
		// We send the appropriate create message here (rather than in the
		// dialog closure) so we can track pending create_dm state for the
		// auto-retry path on server_busy.
		if a.client != nil {
			if len(msg.Members) == 1 && msg.Name == "" {
				a.pendingCreateDM = pendingCreateDMState{
					other:   msg.Members[0],
					retries: maxCreateDMAutoRetries,
				}
				a.client.CreateDM(msg.Members[0])
			} else if len(msg.Members) > 0 {
				// Soft warning at 50+ total members (49+ others + caller).
				// Per-message wrapped keys scale linearly with member count;
				// rooms use a shared epoch key and are more efficient for
				// high-traffic large groups. The server hard-caps at 150.
				if len(msg.Members) >= 49 {
					a.statusBar.SetError(fmt.Sprintf("Large group (%d members) — consider using a room for better performance", len(msg.Members)+1))
				}
				a.client.CreateGroup(msg.Members, msg.Name)
			}
		}
		return a, nil

	case retryCreateDMMsg:
		// Fired by a delayed tea.Cmd after we received server_busy for a
		// create_dm. Retry only if the pending target matches and the
		// user hasn't started a different create in the meantime.
		if a.client != nil && a.pendingCreateDM.other == msg.other && a.pendingCreateDM.retries > 0 {
			a.client.CreateDM(msg.other)
		}
		return a, nil

	case QuickSwitchMsg:
		// Switch to the selected room or group DM
		if msg.Room != "" {
			for i, r := range a.sidebar.rooms {
				if r == msg.Room {
					a.sidebar.cursor = i
					break
				}
			}
		} else if msg.Group != "" {
			for i, g := range a.sidebar.groups {
				if g.ID == msg.Group {
					a.sidebar.cursor = len(a.sidebar.rooms) + i
					break
				}
			}
		}
		a.sidebar.updateSelection()
		a.switchToSidebarSelection()
		return a, nil

	case SearchJumpMsg:
		// Jump to the message in context
		a.search.Hide()
		a.focus = FocusInput
		if msg.Room != "" {
			a.messages.SetContext(msg.Room, "", "")
		} else if msg.Group != "" {
			a.messages.SetContext("", msg.Group, "")
		} else if msg.DM != "" {
			a.messages.SetContext("", "", msg.DM)
		}
		a.syncMessagesLeftState()
		a.messages.LoadFromDB(a.client)
		a.messages.ScrollToMessage(msg.MessageID)
		return a, nil

	case HistoryRequestMsg:
		if a.client == nil {
			return a, nil
		}
		// Try local DB first — avoids a server round-trip when messages
		// are already cached from previous sync/history fetches
		if st := a.client.Store(); st != nil {
			localMsgs, err := st.GetMessagesBefore(msg.Room, msg.Group, "", msg.BeforeID, 100)
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
						ID:       m.ID,
						FromID:   m.Sender,
						From:     from,
						Body:     m.Body,
						TS:       m.TS,
						Room:     m.Room,
						Group:    m.Group,
						ReplyTo:  m.ReplyTo,
						Mentions: m.Mentions,
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
					a.client.RequestHistory(msg.Room, msg.Group, msg.BeforeID, 100)
				}
				return a, nil
			}
		}
		// No local data — fall through to server
		a.client.RequestHistory(msg.Room, msg.Group, msg.BeforeID, 100)
		return a, nil

	case MessageAction:
		a.statusBar.ClearError()
		switch msg.Action {
		case "reply":
			preview := msg.Msg.Body
			if len(preview) > 50 {
				preview = preview[:47] + "..."
			}
			a.input.SetReply(msg.Msg.ID, msg.Msg.From+": "+preview)
			a.focus = FocusInput
		case "delete":
			if a.client != nil && (msg.Msg.FromID == a.client.UserID() || a.client.IsAdmin()) {
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
			isOwn := a.client != nil && msg.Msg.FromID == a.client.UserID()
			isAdmin := a.client != nil && a.client.IsAdmin()
			isRoom := a.messages.room != ""
			var myEmojis []string
			if a.client != nil {
				myEmojis = msg.Msg.UserEmojis(a.client.UserID())
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
			user := a.client.UserID()
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
		case "thread":
			a.threadPanel.Show(msg.Data, a.messages.messages)
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
		a.messages.currentUser = a.client.DisplayName(a.client.UserID())
		a.messages.currentUserID = a.client.UserID()
		a.messages.resolveName = a.client.DisplayName
		a.messages.resolveRoomName = a.client.DisplayRoomName
		a.search.resolveName = a.client.DisplayName
		if st := a.client.Store(); st != nil {
			a.search.SetFTS(st.HasFTS())
		}
		a.sidebar.resolveName = a.client.DisplayName
		a.sidebar.resolveRoomName = a.client.DisplayRoomName
		a.sidebar.resolveVerified = func(user string) bool {
			if a.client == nil {
				return false
			}
			st := a.client.Store()
			if st == nil {
				return false
			}
			_, verified, err := st.GetPinnedKey(user)
			return err == nil && verified
		}
		a.newConv.resolveName = a.client.DisplayName
		a.infoPanel.resolveRoomName = a.client.DisplayRoomName
		if len(a.client.Rooms()) > 0 {
			a.messages.SetContext(a.client.Rooms()[0], "", "")
			a.syncMessagesLeftState()
			a.messages.LoadFromDB(a.client)
			// Set up member list for @completion
			a.memberPanel.Refresh(a.client.Rooms()[0], "", a.client, a.sidebar.online)
			a.input.SetMembers(a.activeMemberEntries())
		}

		a.statusBar.SetUser(a.client.DisplayName(a.client.UserID()), a.client.IsAdmin())
		a.statusBar.SetConnected(true)
		a.updateTitle()

		// Start listening for server messages
		cmds = append(cmds, waitForMsg(msg.msgCh, msg.errCh, a.client.Done()))
		// Store channels for future waits
		a.sidebar.msgCh = msg.msgCh
		a.sidebar.errCh = msg.errCh

	case ServerMsg:
		if cmd := a.handleServerMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
			// Was previously connected — auto-reconnect with exponential backoff
			a.connected = false
			a.reconnectAttempt++
			delay := reconnectDelay(a.reconnectAttempt)
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
	if a.help.IsVisible() || a.search.IsVisible() || a.quickSwitch.IsVisible() || a.threadPanel.IsVisible() || a.newConv.IsVisible() ||
		a.emojiPicker.IsVisible() || a.infoPanel.IsVisible() || a.pendingPanel.IsVisible() ||
		a.verify.IsVisible() || a.keyWarning.IsVisible() ||
		a.quitConfirm.IsVisible() || a.leaveConfirm.IsVisible() || a.leaveRoomConfirm.IsVisible() ||
		a.deleteDMConfirm.IsVisible() || a.deleteGroupConfirm.IsVisible() || a.deleteRoomConfirm.IsVisible() ||
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
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedGroup() != a.messages.group {
				a.messages.SetContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), "")
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
					if a.messages.room != "" && a.client != nil {
						a.client.RequestRoomMembers(a.messages.room)
					}
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
					a.messages.ScrollToMessage(a.pinnedBar.pins[pinIdx].ID)
				}
			}
			return a, nil
		}

		idx := a.layout.MessageItemAt(y)
		if idx >= 0 && idx < len(a.messages.messages) {
			a.messages.cursor = idx
			msg := a.messages.messages[idx]
			if !msg.IsSystem {
				isOwn := a.client != nil && msg.FromID == a.client.UserID()
				isAdmin := a.client != nil && a.client.IsAdmin()
				isRoom := a.messages.room != ""
				var myEmojis []string
				if a.client != nil {
					myEmojis = msg.UserEmojis(a.client.UserID())
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

// reconnectDelay returns the backoff duration for a given attempt number.
// Exponential: 1s, 2s, 4s, 8s, 16s, 30s, 30s... (capped at 30s).
func reconnectDelay(attempt int) time.Duration {
	delay := time.Second
	for i := 1; i < attempt && delay < 30*time.Second; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

// reconnect attempts to reconnect after the backoff delay.
func (a App) reconnect(attempt int) tea.Cmd {
	delay := reconnectDelay(attempt)
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

// syncMessagesLeftState updates the messages model's "left" and
// "roomRetired" flags based on the current context (room or group DM).
// Called after any SetContext to keep the read-only indicator in sync
// with whether the user has left OR the room was retired by an admin.
func (a *App) syncMessagesLeftState() {
	if a.client == nil {
		a.messages.SetLeft(false)
		a.messages.SetRoomRetired(false)
		return
	}
	st := a.client.Store()
	if st == nil {
		a.messages.SetLeft(false)
		a.messages.SetRoomRetired(false)
		return
	}
	if a.messages.group != "" {
		a.messages.SetLeft(st.IsGroupLeft(a.messages.group))
		a.messages.SetRoomRetired(false)
		return
	}
	if a.messages.room != "" {
		a.messages.SetLeft(st.IsRoomLeft(a.messages.room))
		a.messages.SetRoomRetired(st.IsRoomRetired(a.messages.room))
		return
	}
	a.messages.SetLeft(false)
	a.messages.SetRoomRetired(false)
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

func (a *App) resolveRoomDisplayName(roomID string) string {
	if a.client != nil {
		return a.client.DisplayRoomName(roomID)
	}
	return roomID
}

func (a *App) sendReadReceipt() {
	if a.client == nil {
		return
	}
	lastID := a.messages.LatestMessageID()
	if lastID == "" {
		return
	}
	a.client.SendRead(a.messages.room, a.messages.group, a.messages.dm, lastID)
	// Persist locally so the unread divider survives restarts. Rooms and
	// group DMs use the client-side read_positions table; 1:1 DMs do not
	// (the server is authoritative for DM read state, multi-device sync
	// comes via read broadcasts — see handleRead in sshkey-chat/session.go).
	if st := a.client.Store(); st != nil {
		target := a.messages.room
		if target == "" {
			target = a.messages.group
		}
		if target != "" {
			st.StoreReadPosition(target, lastID)
		}
	}
	// Clear unread divider and sidebar badge — user has now seen everything.
	a.messages.SetUnreadFrom("")
	target := a.messages.room
	if target == "" {
		target = a.messages.group
	}
	if target == "" {
		target = a.messages.dm
	}
	if target != "" {
		a.sidebar.SetUnread(target, 0)
	}
}

// switchToSidebarSelection switches the messages context to whatever the
// sidebar currently has selected. Used by Alt+Up/Down and quick switch.
func (a *App) switchToSidebarSelection() {
	if a.sidebar.SelectedRoom() == a.messages.room && a.sidebar.SelectedGroup() == a.messages.group {
		return
	}
	a.messages.SetContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), "")
	a.syncMessagesLeftState()
	a.messages.LoadFromDB(a.client)
	if a.memberPanel.IsVisible() {
		a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
		if a.messages.room != "" && a.client != nil {
			a.client.RequestRoomMembers(a.messages.room)
		}
		a.input.SetMembers(a.activeMemberEntries())
	}
	a.sendReadReceipt()
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
		if retired, _ := a.client.IsRetired(m.UserID); retired {
			continue
		}
		out = append(out, m)
	}
	return out
}

// currentDMRetiredPartner reports whether the active context is a 1:1 DM
// whose other member has been retired. When true, sending should be
// disabled (the server rejects sends to retired members with user_retired
// error) and a notice banner should replace the input.
//
// Returns (false, "") when the context is a room, a group DM, or a 1:1 DM
// with an active partner.
func (a *App) currentDMRetiredPartner() (bool, string) {
	if a.client == nil || a.messages.dm == "" {
		return false, ""
	}
	other := a.client.DMOther(a.messages.dm)
	if other == "" {
		return false, ""
	}
	if retired, _ := a.client.IsRetired(other); retired {
		return true, other
	}
	return false, ""
}

// handleSlashCommand processes slash commands that need app-level handling.
func (a *App) handleSlashCommand(sc *SlashCommandMsg) {
	switch sc.Command {
	case "/leave":
		// Branches on context. Group DM and room each get their own
		// confirmation dialog. 1:1 DMs don't have a /leave path — they
		// only expose /delete, since "leave but keep in sidebar" has
		// no useful semantic on a 1:1. Server enforces policy and
		// returns errors; the client always opens the dialog and
		// waits for the echo (or the error) before touching local state.
		if sc.Group != "" {
			// Group DM /leave — look up display name for the dialog
			groupName := ""
			for _, g := range a.sidebar.groups {
				if g.ID == sc.Group {
					groupName = g.Name
					if groupName == "" {
						// Build a fallback from member display names
						var names []string
						for _, m := range g.Members {
							name := m
							if a.client != nil {
								name = a.client.DisplayName(m)
							}
							names = append(names, name)
						}
						groupName = strings.Join(names, ", ")
					}
					break
				}
			}
			a.leaveConfirm.Show(sc.Group, groupName)
			return
		}
		if sc.Room != "" {
			// Room /leave — display name comes from the resolver
			roomName := sc.Room
			if a.client != nil {
				roomName = a.client.DisplayRoomName(sc.Room)
			}
			a.leaveRoomConfirm.Show(sc.Room, roomName)
			return
		}
	case "/leave_dm_rejected":
		// 1:1 DMs don't expose /leave — the only client surface is
		// /delete, which is fully wired for DMs. Surface a clear
		// redirect so the user isn't confused about which command to use.
		a.statusBar.SetError("/leave is not available for 1:1 DMs — use /delete")
	case "/delete":
		// /delete is context-aware. All three contexts (1:1 DM, group DM,
		// room) are wired end-to-end with confirmation dialogs and
		// wait-for-echo state management.
		if sc.DM != "" {
			// Resolve the other party's display name for the dialog.
			other := ""
			for _, dm := range a.sidebar.dms {
				if dm.ID == sc.DM {
					for _, m := range dm.Members {
						if a.client != nil && m == a.client.UserID() {
							continue
						}
						other = m
						break
					}
					break
				}
			}
			otherName := other
			if a.client != nil && other != "" {
				otherName = a.client.DisplayName(other)
			}
			a.deleteDMConfirm.Show(sc.DM, otherName)
			return
		}
		if sc.Group != "" {
			// Group DM /delete — look up display name for the dialog,
			// fall back to a comma-joined member list when there's no
			// explicit name.
			groupName := ""
			for _, g := range a.sidebar.groups {
				if g.ID == sc.Group {
					groupName = g.Name
					if groupName == "" {
						var names []string
						for _, m := range g.Members {
							name := m
							if a.client != nil {
								name = a.client.DisplayName(m)
							}
							names = append(names, name)
						}
						groupName = strings.Join(names, ", ")
					}
					break
				}
			}
			a.deleteGroupConfirm.Show(sc.Group, groupName)
			return
		}
		if sc.Room != "" {
			// Room /delete — Phase 12. Dialog wording depends on whether
			// the room has been retired by an admin. Active rooms get
			// the "you'll need an admin to re-add you" hint; retired
			// rooms get "this action cannot be undone" since there's no
			// un-retirement path. We resolve the retired flag from the
			// local client store (populated by room_retired / retired_rooms
			// catchup events).
			roomName := sc.Room
			if a.client != nil {
				roomName = a.client.DisplayRoomName(sc.Room)
			}
			retired := false
			if a.client != nil {
				if st := a.client.Store(); st != nil {
					retired = st.IsRoomRetired(sc.Room)
				}
			}
			a.deleteRoomConfirm.Show(sc.Room, roomName, retired)
			return
		}
		a.statusBar.SetError("/delete must be run inside a conversation")
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
			username = a.client.UserID()
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
		// Upload + send as a message with attachment. Routes to room,
		// group DM, or 1:1 DM sender based on the current context.
		go func() {
			if a.client == nil {
				return
			}
			a.statusBar.SetError("Uploading " + filepath.Base(path) + "...")
			body := filepath.Base(path)
			var err error
			if sc.Room != "" {
				err = a.client.SendRoomMessageFile(sc.Room, body, path, "", nil)
			} else if sc.Group != "" {
				err = a.client.SendGroupMessageFile(sc.Group, body, path, "", nil)
			} else if sc.DM != "" {
				err = a.client.SendDMMessageFile(sc.DM, body, path, "", nil)
			} else {
				a.statusBar.SetError("No active room or group")
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
// Returns an optional tea.Cmd when the handler needs to schedule follow-up
// work (e.g. a delayed retry on server_busy).
func (a *App) handleServerMessage(msg ServerMsg) tea.Cmd {
	switch msg.Type {
	case "message":
		var m protocol.Message
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddRoomMessage(m, a.client)
		if m.Room == a.messages.room {
			a.sendReadReceipt()
		} else {
			a.sidebar.IncrementUnread(m.Room)
		}
		// Notifications for messages not from self
		if a.client != nil && m.From != a.client.UserID() {
			payload, err := a.client.DecryptRoomMessage(m.Room, m.Epoch, m.Payload)
			body := "(encrypted)"
			isMention := false
			if err == nil {
				body = payload.Body
				for _, mention := range payload.Mentions {
					if mention == a.client.UserID() {
						isMention = true
						break
					}
				}
			}
			if !a.muted[m.Room] {
				SendDesktopNotification(
					fmt.Sprintf("%s in #%s", a.resolveDisplayName(m.From), a.resolveRoomDisplayName(m.Room)),
					body,
				)
			}
			if a.bell.ShouldBell(m.Room, "", m.From, a.client.UserID(), isMention, a.muted) {
				Ring()
			}
		}
	case "group_message":
		var m protocol.GroupMessage
		json.Unmarshal(msg.Raw, &m)
		a.messages.AddGroupMessage(m, a.client)
		if m.Group == a.messages.group {
			a.sendReadReceipt()
		} else {
			a.sidebar.IncrementUnread(m.Group)
		}
		if a.client != nil && m.From != a.client.UserID() {
			payload, err := a.client.DecryptGroupMessage(m.WrappedKeys, m.Payload)
			body := "(encrypted)"
			if err == nil {
				body = payload.Body
			}
			if !a.muted[m.Group] {
				SendDesktopNotification(a.resolveDisplayName(m.From), body)
			}
			if a.bell.ShouldBell("", m.Group, m.From, a.client.UserID(), false, a.muted) {
				Ring()
			}
		}
	case "typing":
		var m protocol.Typing
		json.Unmarshal(msg.Raw, &m)
		a.messages.SetTyping(m.User, m.Room, m.Group, m.DM)
	case "room_list":
		// Reconcile the server's active-room list with any locally-archived
		// rooms. The server drops a room from room_list as soon as the user
		// leaves it, but the local DB keeps a row with left_at > 0 so the
		// sidebar can render the archived entry as greyed/read-only until
		// the user explicitly purges it. Same reconciliation pattern as
		// group_list — server truth wins for active rooms, local DB fills
		// in the archived ones the server no longer knows about.
		//
		// Room metadata for active rooms is persisted at the client layer
		// (client.go handles room_list and calls UpsertRoom). For archived
		// rooms the metadata in the local rooms table is whatever the last
		// server sync had — fine for an entry the user has left.
		var m protocol.RoomList
		json.Unmarshal(msg.Raw, &m)

		var ids []string
		for _, r := range m.Rooms {
			ids = append(ids, r.ID)
		}

		if a.client != nil {
			if st := a.client.Store(); st != nil {
				// Re-add case: if the server sends a room we had marked
				// archived, the user must have been re-added to it via
				// admin CLI. Clear the archive flag so the sidebar renders
				// it as active.
				for _, r := range m.Rooms {
					if st.IsRoomLeft(r.ID) {
						if err := st.MarkRoomRejoined(r.ID); err != nil {
							a.statusBar.SetError("Failed to clear archived room flag: " + err.Error())
						}
					}
				}

				// Merge in any locally-archived rooms the server no longer
				// sends. These persist (greyed, read-only) until /delete.
				seen := make(map[string]bool, len(ids))
				for _, id := range ids {
					seen[id] = true
				}
				if archived, err := st.GetLeftRooms(); err == nil {
					for _, ar := range archived {
						if seen[ar.ID] {
							continue
						}
						ids = append(ids, ar.ID)
					}
				}
			}
		}

		a.sidebar.SetRooms(ids)

		// Apply archived markers to the merged sidebar list based on the
		// (post-rejoin-clear) local DB state.
		if a.client != nil {
			if st := a.client.Store(); st != nil {
				for _, id := range ids {
					if st.IsRoomLeft(id) {
						a.sidebar.MarkRoomLeft(id)
					}
				}
			}
		}
	case "group_list":
		// Reconcile the server's active-group list with any locally-archived
		// groups. The server drops groups from group_list as soon as the user
		// leaves, but the local DB keeps a row with left_at > 0 so the sidebar
		// can render the archived entry as greyed/read-only until the user
		// explicitly purges it. This handler is the single reconciliation
		// point: server truth wins for active groups, local DB fills in the
		// archived ones the server no longer knows about.
		var m protocol.GroupList
		json.Unmarshal(msg.Raw, &m)

		groups := m.Groups

		if a.client != nil {
			if st := a.client.Store(); st != nil {
				// Re-add case: if the server sends a group we had marked
				// archived, the user must have been re-added to it. Clear
				// the archive flag so the sidebar renders it as active.
				for _, g := range m.Groups {
					if st.IsGroupLeft(g.ID) {
						if err := st.MarkGroupRejoined(g.ID); err != nil {
							a.statusBar.SetError("Failed to clear archived flag: " + err.Error())
						}
					}
				}

				// Merge in any locally-archived groups the server no longer
				// sends. These persist (greyed, read-only) until /delete.
				seen := make(map[string]bool, len(groups))
				for _, g := range groups {
					seen[g.ID] = true
				}
				if archived, err := st.GetArchivedGroups(); err == nil {
					for _, ag := range archived {
						if seen[ag.ID] {
							continue
						}
						var members []string
						if ag.Members != "" {
							members = strings.Split(ag.Members, ",")
						}
						groups = append(groups, protocol.GroupInfo{
							ID:      ag.ID,
							Name:    ag.Name,
							Members: members,
						})
					}
				}
			}
		}

		a.sidebar.SetGroups(groups)

		// Apply archived markers to the merged sidebar list based on the
		// (post-rejoin-clear) local DB state.
		if a.client != nil {
			if st := a.client.Store(); st != nil {
				for _, g := range groups {
					if st.IsGroupLeft(g.ID) {
						a.sidebar.MarkGroupLeft(g.ID)
					}
				}
			}
		}
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
		} else if m.Group != "" {
			a.sidebar.SetUnreadGroup(m.Group, m.Count)
			if m.Group == a.messages.group && m.Count > 0 && m.LastRead != "" {
				a.setUnreadDividerAfter(m.LastRead)
			}
		} else if m.DM != "" {
			a.sidebar.SetUnreadDM(m.DM, m.Count)
			if m.DM == a.messages.dm && m.Count > 0 && m.LastRead != "" {
				a.setUnreadDividerAfter(m.LastRead)
			}
		}
		a.updateTitle()
	case "deleted":
		var m protocol.Deleted
		json.Unmarshal(msg.Raw, &m)
		a.messages.MarkDeleted(m.ID, m.DeletedBy)
	case "user_retired":
		var m protocol.UserRetired
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.messages.MarkRetired(m.User)
			a.sidebar.MarkRetired(m.User)
			// If the retired user is in the active group, show a notice
			if a.client != nil && a.messages.group != "" {
				for _, member := range a.client.GroupMembers(a.messages.group) {
					if member == m.User {
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + "'s account was retired")
						break
					}
				}
			}
			// If the retired user is the other party in the active 1:1 DM
			if a.client != nil && a.messages.dm != "" {
				other := a.client.DMOther(a.messages.dm)
				if other == m.User {
					a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + "'s account was retired")
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
	case "room_members_list":
		// Response to room_members — update info panel and member panel
		if a.client != nil {
			room, members := a.client.RoomMembersList()
			a.infoPanel.SetRoomMembers(room, members, a.client, a.sidebar.online)
			if a.memberPanel.IsVisible() && a.messages.room == room {
				a.memberPanel.SetRoomMembers(members, a.client, a.sidebar.online)
				a.input.SetMembers(a.activeMemberEntries())
			}
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
	case "group_event":
		var m protocol.GroupEvent
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			if m.Event == "leave" {
				// Show system message in the active group stream when
				// SOMEONE ELSE leaves. The leaver themselves is not in the
				// broadcast set (they were removed from members already), so
				// this branch only fires for other users — no self check needed.
				//
				// Reason distinguishes the trigger so the system message
				// matches what actually happened:
				//   - "retirement": the leaver's account was retired
				//   - "admin": an admin removed the leaver via sshkey-ctl
				//   - "" (empty): the leaver ran /leave themselves
				if m.Group == a.messages.group {
					switch m.Reason {
					case "retirement":
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + "'s account was retired")
					case "admin":
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " was removed from the group by an admin")
					default:
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " left the group")
					}
				}
			}
		}
	case "group_left":
		// Server confirmed a group leave. Reason distinguishes the
		// trigger:
		//   - "" (empty): self-leave via /leave command on this or
		//     another device
		//   - "admin": an admin removed us via sshkey-ctl
		//     remove-from-group (the moderation escape hatch)
		//
		// In both cases the client layer has already marked the group
		// archived in the local DB and dropped it from the in-memory
		// member map. Here we just update the sidebar greying, set the
		// active message view to read-only if the affected group is
		// currently focused, and surface a status message that tells
		// the user what happened.
		var m protocol.GroupLeft
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.MarkGroupLeft(m.Group)
			if a.messages.group == m.Group {
				a.messages.SetLeft(true)
			}
			if m.Reason == "admin" {
				groupName := m.Group
				for _, g := range a.sidebar.groups {
					if g.ID == m.Group && g.Name != "" {
						groupName = g.Name
						break
					}
				}
				a.statusBar.SetError("You were removed from " + groupName + " by an admin")
			} else {
				a.statusBar.SetError("Left group")
			}
		}
	case "group_deleted":
		// Server confirmed /delete (this device or another). The client
		// layer has already purged local messages and marked left; here
		// we drop the sidebar entry entirely and reset the active
		// message context if the deleted group was being viewed. Setting
		// an empty context also clears the message buffer.
		var m protocol.GroupDeleted
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.RemoveGroup(m.Group)
			if a.messages.group == m.Group {
				a.messages.SetContext("", "", "")
			}
		}
	case "deleted_groups":
		// Sync catchup. Each entry was /delete'd from another device
		// while this one was offline. The client layer has already
		// applied MarkGroupLeft + PurgeGroupMessages for each. Here we
		// drop them from the sidebar and reset the active context if
		// any of them was the currently-viewed group.
		var m protocol.DeletedGroupsList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, groupID := range m.Groups {
				a.sidebar.RemoveGroup(groupID)
				if a.messages.group == groupID {
					a.messages.SetContext("", "", "")
				}
			}
		}
	case "room_left":
		// Server confirmed our leave_room. The client layer has already
		// marked the room archived in the local DB; flip the sidebar
		// + messages view + status bar to match.
		var m protocol.RoomLeft
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.MarkRoomLeft(m.Room)
			if a.messages.room == m.Room {
				a.messages.SetLeft(true)
			}
			a.statusBar.SetError("Left room")
		}
	case "room_retired":
		// An admin retired a room via sshkey-ctl. The client layer has
		// already updated the local DB (new display name + retired_at
		// flag). Here we refresh the sidebar entry to the new name +
		// retired marker, flip the messages view to read-only if the
		// retired room was currently focused, and surface a status
		// message. Retirement is a broadcast event — all connected
		// members of the room see this at the same time.
		var m protocol.RoomRetired
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.MarkRoomRetired(m.Room)
			if a.messages.room == m.Room {
				a.messages.SetRoomRetired(true)
				// Inline system message so the user sees the event in
				// the message stream, not just the banner.
				a.messages.AddSystemMessage("this room was archived by an admin")
			}
			a.statusBar.SetError("Room archived by admin")
		}
	case "retired_rooms":
		// Sync catchup. Each entry was retired by an admin while this
		// device was offline. The client layer has already applied
		// MarkRoomRetired for each. Here we flag them in the sidebar
		// and update the active context's read-only state if one of
		// them is currently focused.
		var m protocol.RetiredRoomsList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, r := range m.Rooms {
				a.sidebar.MarkRoomRetired(r.Room)
				if a.messages.room == r.Room {
					a.messages.SetRoomRetired(true)
				}
			}
		}
	case "room_deleted":
		// Server confirmed /delete (this device or another). The
		// client layer has already purged local messages, epoch keys,
		// and reactions, and flipped left_at. Here we drop the
		// sidebar entry entirely and reset the active message
		// context if the deleted room was being viewed. Setting an
		// empty context also clears the message buffer.
		var m protocol.RoomDeleted
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.RemoveRoom(m.Room)
			if a.messages.room == m.Room {
				a.messages.SetContext("", "", "")
			}
		}
	case "deleted_rooms":
		// Sync catchup. Each entry was /delete'd from another device
		// while this one was offline. The client layer has already
		// applied MarkRoomLeft + PurgeRoomMessages for each. Here we
		// drop them from the sidebar and reset the active context if
		// any of them was the currently-viewed room.
		var m protocol.DeletedRoomsList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, roomID := range m.Rooms {
				a.sidebar.RemoveRoom(roomID)
				if a.messages.room == roomID {
					a.messages.SetContext("", "", "")
				}
			}
		}
	case "group_renamed":
		var m protocol.GroupRenamed
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.RenameGroup(m.Group, m.Name)
			if m.Group == a.messages.group {
				a.messages.AddSystemMessage(a.resolveDisplayName(m.RenamedBy) + " renamed the group to " + m.Name)
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
	case "group_created":
		var m protocol.GroupCreated
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.AddGroup(protocol.GroupInfo{
				ID:      m.Group,
				Members: m.Members,
				Name:    m.Name,
			})
		}
	case "dm_list":
		var m protocol.DMList
		json.Unmarshal(msg.Raw, &m)
		// Filter out DMs the caller has already left — those are tombstones
		// from /delete on another device. The client layer has already
		// purged any local messages and flipped the local left_at flag in
		// the dm_list handler in client.go; here we just refuse to surface
		// them in the sidebar.
		active := make([]protocol.DMInfo, 0, len(m.DMs))
		for _, dm := range m.DMs {
			if dm.LeftAtForCaller == 0 {
				active = append(active, dm)
			}
		}
		a.sidebar.SetDMs(active)
		// Set the sidebar's selfUserID so it knows which party is "other" in each DM
		if a.client != nil {
			a.sidebar.selfUserID = a.client.UserID()
		}
	case "dm_created":
		var m protocol.DMCreated
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.AddDM(protocol.DMInfo{
				ID:      m.DM,
				Members: m.Members,
			})
			// Successful create — clear any pending auto-retry state for
			// this target so stale retries scheduled before the success
			// are ignored when they fire.
			for _, member := range m.Members {
				if a.client != nil && member == a.client.UserID() {
					continue
				}
				if member == a.pendingCreateDM.other {
					a.pendingCreateDM = pendingCreateDMState{}
				}
			}
		}
	case "dm":
		var m protocol.DM
		json.Unmarshal(msg.Raw, &m)
		// Add to messages view if the active context is this DM
		if m.DM == a.messages.dm {
			// DM messages are decrypted + displayed inline (same as group_message)
			// For now, messages view needs an AddDMMessage — we'll use the same
			// DisplayMessage path as groups since DMs use the same wrapped-key model.
			if a.client != nil {
				payload, err := a.client.DecryptDMMessage(m.WrappedKeys, m.Payload)
				body := "(encrypted)"
				replyTo := ""
				var mentions []string
				if err == nil {
					body = payload.Body
					replyTo = payload.ReplyTo
					mentions = payload.Mentions
				}
				from := m.From
				from = a.client.DisplayName(m.From)
				a.messages.messages = append(a.messages.messages, DisplayMessage{
					ID:       m.ID,
					FromID:   m.From,
					From:     from,
					Body:     body,
					TS:       m.TS,
					DM:       m.DM,
					ReplyTo:  replyTo,
					Mentions: mentions,
				})
			}
			a.sendReadReceipt()
		} else {
			a.sidebar.IncrementUnread(m.DM)
		}
		// Desktop notification
		if a.client != nil && m.From != a.client.UserID() {
			payload, err := a.client.DecryptDMMessage(m.WrappedKeys, m.Payload)
			body := "(encrypted)"
			if err == nil {
				body = payload.Body
			}
			SendDesktopNotification(a.resolveDisplayName(m.From), body)
			if a.bell.ShouldBell("", "", m.From, a.client.UserID(), false, a.muted) {
				Ring()
			}
		}
	case "dm_left":
		// Server confirmed /delete (this device or another). The client
		// layer has already purged local messages and flipped left_at;
		// here we just need to drop the sidebar entry and reset the
		// message view if the deleted DM was currently active. Setting
		// an empty context also clears the message buffer.
		var dl protocol.DMLeft
		if err := json.Unmarshal(msg.Raw, &dl); err == nil {
			a.sidebar.RemoveDM(dl.DM)
			if a.messages.dm == dl.DM {
				a.messages.SetContext("", "", "")
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
		// Apply reactions on the synced messages (reactions are encrypted
		// and go through the same AddReactionDecrypted path as real-time)
		for _, raw := range batch.Reactions {
			a.handleServerMessage(ServerMsg{Type: "reaction", Raw: raw})
		}
	case "history_result":
		var result protocol.HistoryResult
		json.Unmarshal(msg.Raw, &result)

		// Build display messages and prepend (history arrives oldest-first).
		// Epoch keys are already unwrapped by the client layer (handleHistoryKeys),
		// and messages are persisted there too (storeRoomMessage/storeGroupMessage).
		var histMsgs []DisplayMessage
		for _, raw := range result.Messages {
			histType, _ := protocol.TypeOf(raw)
			switch histType {
			case "message":
				var pm protocol.Message
				if json.Unmarshal(raw, &pm) == nil {
					histMsgs = append(histMsgs, a.messages.buildDisplayMsg(pm, a.client))
				}
			case "group_message":
				var gm protocol.GroupMessage
				if json.Unmarshal(raw, &gm) == nil {
					histMsgs = append(histMsgs, a.messages.buildDisplayGroup(gm, a.client))
				}
			case "dm":
				var dm protocol.DM
				if json.Unmarshal(raw, &dm) == nil {
					// Reuse buildDisplayGroup-style logic for DM history
					body := "(encrypted)"
					replyTo := ""
					var mentions []string
					if a.client != nil {
						payload, err := a.client.DecryptDMMessage(dm.WrappedKeys, dm.Payload)
						if err == nil {
							body = payload.Body
							replyTo = payload.ReplyTo
							mentions = payload.Mentions
						}
					}
					from := dm.From
					if a.client != nil {
						from = a.client.DisplayName(dm.From)
					}
					histMsgs = append(histMsgs, DisplayMessage{
						ID:       dm.ID,
						FromID:   dm.From,
						From:     from,
						Body:     body,
						TS:       dm.TS,
						DM:       dm.DM,
						ReplyTo:  replyTo,
						Mentions: mentions,
					})
				}
			}
		}
		if len(histMsgs) > 0 {
			a.messages.PrependMessages(histMsgs, result.HasMore)
		} else {
			a.messages.loadingHistory = false
			a.messages.hasMore = result.HasMore
		}

		// Apply reactions from the history batch
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

		// server_busy on an in-flight create_dm → transparently retry
		// with a short backoff. The user never sees the server's busy
		// message; either the retry succeeds and a dm_created arrives,
		// or all retries are exhausted and we surface a generic "please
		// try again" in the status bar without explaining the reason.
		if m.Code == "server_busy" && a.pendingCreateDM.other != "" {
			if a.pendingCreateDM.retries > 0 {
				a.pendingCreateDM.retries--
				other := a.pendingCreateDM.other
				return tea.Tick(createDMRetryDelay*time.Millisecond, func(time.Time) tea.Msg {
					return retryCreateDMMsg{other: other}
				})
			}
			a.pendingCreateDM = pendingCreateDMState{}
			a.statusBar.SetError("Could not start conversation — please try again.")
			return nil
		}

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
	return nil
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
			input = helpDescStyle.Render("  " + a.resolveDisplayName(other) + "'s account has been retired — this DM is read-only. Verify their new account (if any) out of band before starting a new DM.")
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
	if a.threadPanel.IsVisible() {
		return a.threadPanel.View(a.width, a.height)
	}
	if a.quickSwitch.IsVisible() {
		return a.quickSwitch.View(a.width)
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
	if a.leaveConfirm.IsVisible() {
		return a.leaveConfirm.View(a.width)
	}
	if a.leaveRoomConfirm.IsVisible() {
		return a.leaveRoomConfirm.View(a.width)
	}
	if a.deleteDMConfirm.IsVisible() {
		return a.deleteDMConfirm.View(a.width)
	}
	if a.deleteGroupConfirm.IsVisible() {
		return a.deleteGroupConfirm.View(a.width)
	}
	if a.deleteRoomConfirm.IsVisible() {
		return a.deleteRoomConfirm.View(a.width)
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
