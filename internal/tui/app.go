// Package tui implements the Bubble Tea terminal UI.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// undoWindowSeconds is the Phase 14 /undo window for reverting a
// kick. Per the plan: 30 seconds, no stack, only the last kick is
// trackable, any other admin action supersedes the undo target.
const undoWindowSeconds int64 = 30

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
	sidebar       SidebarModel
	messages      MessagesModel
	input         InputModel
	statusBar     StatusBarModel
	help          HelpModel
	search        SearchModel
	newConv       NewConvModel
	emojiPicker   EmojiPickerModel
	infoPanel     InfoPanelModel
	pendingPanel  PendingPanelModel
	connectFailed ConnectFailedModel
	settings      SettingsModel
	addServer     AddServerModel
	memberPanel   MemberPanelModel
	verify        VerifyModel
	keyWarning    KeyWarningModel
	quitConfirm   QuitConfirmModel
	// lastCtrlQAt (Phase 17c Step 5 polish) stamps the time of the
	// last Ctrl+Q keypress. A second Ctrl+Q within doubleQuitWindow
	// bypasses the confirm dialog — escape hatch for users who
	// genuinely want to abandon pending sends.
	lastCtrlQAt        time.Time
	retireConfirm      RetireConfirmModel
	leaveConfirm       LeaveConfirmModel
	leaveRoomConfirm   LeaveRoomConfirmModel
	deleteDMConfirm    DeleteDMConfirmModel
	deleteGroupConfirm DeleteGroupConfirmModel
	deleteRoomConfirm  DeleteRoomConfirmModel
	// Phase 14 in-group admin verb dialogs
	addConfirm      AddConfirmModel
	kickConfirm     KickConfirmModel
	promoteConfirm  PromoteConfirmModel
	demoteConfirm   DemoteConfirmModel
	transferConfirm TransferConfirmModel
	// Phase 14 read-only overlays (/audit, /members, /admins)
	auditOverlay   AuditOverlayModel
	membersOverlay MembersOverlayModel
	// Phase 14 last-admin inline promote picker — shown when the
	// server rejects /leave or /delete with ErrForbidden due to the
	// caller being the only admin of a group that has other members.
	lastAdminPicker LastAdminPickerModel
	// Tracks whether the pending /leave or /delete was actually a
	// /delete — set by the LeaveConfirmMsg and DeleteGroupConfirmMsg
	// handlers just before the wire send, cleared on next send or on
	// successful echo. If ErrForbidden (last-admin) arrives while
	// this is set, the picker is opened with TriggerDelete matching
	// the original request.
	pendingLastAdminGroup  string
	pendingLastAdminDelete bool
	// Phase 14 /undo state: last kick the local user performed.
	// If /undo runs within undoWindow seconds, the kicked user is
	// re-added via add_to_group. Cleared on any other admin action
	// or on expiry. Tracks exactly one kick — there's no undo stack.
	lastKickGroup  string
	lastKickUserID string
	lastKickTS     int64
	deviceRevoked  DeviceRevokedModel
	deviceMgr      DeviceMgrModel
	quickSwitch    QuickSwitchModel
	threadPanel    ThreadPanelModel
	pinnedBar      PinnedBarModel

	// Config state
	appConfig        *config.Config
	configDir        string
	serverIdx        int // index of the active server in config
	bell             BellConfig
	muted            map[string]bool // room name or conv ID -> muted
	showHelpHint     bool
	reconnectAttempt int

	width           int
	height          int
	focus           Focus
	layout          Layout
	contextMenu     ContextMenuModel
	memberMenu      MemberMenuModel
	passphrase      PassphraseModel
	passphraseCh    chan []byte
	passphraseCache map[string][]byte // keyPath -> passphrase

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

// doubleQuitWindow is the time window during which a second Ctrl+Q
// keypress bypasses the quit-confirmation dialog. Phase 17c Step 5
// polish: "bypass-able with repeated quit keypresses for users who
// genuinely want to abandon". 500ms — long enough for a deliberate
// double-tap, short enough that accidental double-presses from the
// existing dialog flow don't trigger it.
const doubleQuitWindow = 500 * time.Millisecond

// refreshingMinVisibleMs is the minimum duration the "refreshing…"
// keypress-ack indicator stays visible after a refresh keypress
// (Phase 17c Step 6). Server responses arriving faster than this
// still leave the indicator on screen for the floor window so the
// user gets unambiguous visual confirmation that their keypress
// registered. 200ms is the plan's specified value — long enough to
// read, short enough to feel instant.
const refreshingMinVisibleMs = 200

// refreshingTickMsg is emitted by tea.Tick after refreshingMinVisibleMs
// elapses to trigger a repaint — without this, the status bar
// wouldn't redraw to drop the indicator until the next keypress /
// server message arrived, which could be much later.
type refreshingTickMsg struct{}

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
		cfg:             cfg,
		sidebar:         NewSidebar(),
		messages:        NewMessages(),
		input:           NewInput(),
		statusBar:       NewStatusBar(),
		search:          NewSearch(),
		newConv:         NewNewConv(),
		emojiPicker:     NewEmojiPicker(),
		quickSwitch:     NewQuickSwitch(),
		memberPanel:     NewMemberPanel(),
		settings:        NewSettings(),
		addServer:       NewAddServer(),
		retireConfirm:   NewRetireConfirm(),
		deviceRevoked:   NewDeviceRevoked(),
		deviceMgr:       NewDeviceMgr(),
		passphrase:      NewPassphrase(),
		passphraseCh:    make(chan []byte, 1),
		passphraseCache: make(map[string][]byte),
		appConfig:       appCfg,
		configDir:       configDir,
		serverIdx:       serverIdx,
		bell:            NewBellConfig(appCfg.Notifications),
		muted:           config.LoadMutedMap(appCfg),
		showHelpHint:    !appCfg.Notifications.HelpShown,
		focus:           FocusInput,
	}
}

func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.input.Init(),
		a.connect(),
	)
}

// KeyChangeEvent is the tea.Msg form of a client-layer OnKeyWarning
// callback — signals that `StoreProfile` detected a fingerprint
// mismatch against the pinned value for an existing user ID. Under
// the no-rotation protocol invariant this is always anomalous (see
// PROTOCOL.md "Keys as Identities"); the App handler routes it to
// the `KeyWarningModel` blocking dialog. Phase 21 F3.a closure
// 2026-04-19.
type KeyChangeEvent struct {
	User           string
	OldFingerprint string
	NewFingerprint string
}

// connect starts the SSH connection in a goroutine.
func (a App) connect() tea.Cmd {
	return func() tea.Msg {
		msgCh := make(chan ServerMsg, 100)
		errCh := make(chan error, 1)
		// Buffered so a burst of profile broadcasts during catchup
		// can't block the client readLoop on a slow TUI. 10 events
		// is comfortably above any realistic burst (one key-change
		// event per user per session is the upper bound).
		keyWarnCh := make(chan KeyChangeEvent, 10)

		cfg := a.cfg
		cfg.OnMessage = func(msgType string, raw json.RawMessage) {
			msgCh <- ServerMsg{Type: msgType, Raw: raw}
		}
		cfg.OnError = func(err error) {
			errCh <- err
		}
		cfg.OnKeyWarning = func(user, oldFP, newFP string) {
			// Non-blocking send — if the buffer is full (shouldn't
			// happen in practice), drop rather than stall the
			// readLoop. The key change has already been logged +
			// ClearVerified has already run on the store side;
			// dropping the modal dispatch is a minor UX
			// degradation, not a correctness gap.
			select {
			case keyWarnCh <- KeyChangeEvent{User: user, OldFingerprint: oldFP, NewFingerprint: newFP}:
			default:
			}
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

		return connectedWithClient{client: c, msgCh: msgCh, errCh: errCh, keyWarnCh: keyWarnCh}
	}
}

type connectedWithClient struct {
	client    *client.Client
	msgCh     chan ServerMsg
	errCh     chan error
	keyWarnCh chan KeyChangeEvent
}

// waitForMsg returns a cmd that waits for the next server message.
// Selects across three channels: server-message events, errors, and
// client-layer key-change warnings (Phase 21 F3.a) which surface via
// their own channel rather than riding on the ServerMsg envelope
// because they are TUI-synthetic events, not protocol frames.
func waitForMsg(msgCh chan ServerMsg, errCh chan error, keyWarnCh chan KeyChangeEvent, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-msgCh:
			return msg
		case err := <-errCh:
			return ErrMsg{Err: err}
		case kw := <-keyWarnCh:
			return kw
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

		// Phase 14 read-only overlays intercept all keys
		if a.auditOverlay.IsVisible() {
			var cmd tea.Cmd
			a.auditOverlay, cmd = a.auditOverlay.Update(msg)
			return a, cmd
		}
		if a.membersOverlay.IsVisible() {
			var cmd tea.Cmd
			a.membersOverlay, cmd = a.membersOverlay.Update(msg)
			return a, cmd
		}
		// Phase 14 last-admin picker intercepts all keys when active
		if a.lastAdminPicker.IsVisible() {
			var cmd tea.Cmd
			a.lastAdminPicker, cmd = a.lastAdminPicker.Update(msg)
			return a, cmd
		}

		// Phase 14 in-group admin verb dialogs intercept all keys
		if a.addConfirm.IsVisible() {
			var cmd tea.Cmd
			a.addConfirm, cmd = a.addConfirm.Update(msg)
			return a, cmd
		}
		if a.kickConfirm.IsVisible() {
			var cmd tea.Cmd
			a.kickConfirm, cmd = a.kickConfirm.Update(msg)
			return a, cmd
		}
		if a.promoteConfirm.IsVisible() {
			var cmd tea.Cmd
			a.promoteConfirm, cmd = a.promoteConfirm.Update(msg)
			return a, cmd
		}
		if a.demoteConfirm.IsVisible() {
			var cmd tea.Cmd
			a.demoteConfirm, cmd = a.demoteConfirm.Update(msg)
			return a, cmd
		}
		if a.transferConfirm.IsVisible() {
			var cmd tea.Cmd
			a.transferConfirm, cmd = a.transferConfirm.Update(msg)
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
			// Phase 17c Step 5 polish: double-press Ctrl+Q within
			// doubleQuitWindow bypasses the confirm dialog — escape
			// hatch for users who genuinely want to abandon pending
			// sends.
			now := time.Now()
			if !a.lastCtrlQAt.IsZero() && now.Sub(a.lastCtrlQAt) < doubleQuitWindow {
				a.lastCtrlQAt = time.Time{}
				if a.client != nil {
					a.client.Close()
				}
				return a, tea.Quit
			}
			a.lastCtrlQAt = now

			serverName := "server"
			if a.appConfig != nil && a.serverIdx < len(a.appConfig.Servers) {
				serverName = a.appConfig.Servers[a.serverIdx].Name
			}
			// Phase 17c Step 5: surface pending-send count so the
			// user sees if they'll lose unflushed messages on quit.
			pendingSend := 0
			if a.client != nil {
				pendingSend = a.client.SendQueue().PendingCount()
			}
			a.quitConfirm.ShowWithPending(serverName, pendingSend)
			return a, nil

		case "?":
			if a.focus != FocusInput {
				// Phase 14: same context-aware help filter as the
				// /help slash command path.
				showAdmin := false
				if a.messages.group != "" {
					showAdmin = a.isLocalAdminOfGroup(a.messages.group)
				}
				a.help.SetContext(showAdmin)
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
				a.onContextSwitch()
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
				a.input.SetNonMembers(a.activeNonMemberEntries())
			}
			return a, nil

		case "ctrl+p":
			a.pinnedBar.Toggle()
			return a, nil

		case "ctrl+f":
			a.search.Show()
			return a, nil

		case "ctrl+shift+r":
			// Phase 17c Step 6: nuclear refresh — force full
			// reconnect handshake. Emits RefreshRequestMsg so the
			// central handler drives both the client.Close and the
			// keypress-ack indicator consistently.
			return a, func() tea.Msg { return RefreshRequestMsg{Kind: "reconnect"} }

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
			// Phase 15: Esc in input-focus edit mode cancels the in-progress
			// edit (clear buffer + exit edit state) rather than only changing
			// panel focus. This branch must run before the generic focus reset
			// to avoid leaving the input in zombie edit mode.
			if a.focus == FocusInput && a.input.IsEditing() {
				a.input.ExitEditMode()
				a.input.ClearInput()
				return a, nil
			}
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
				a.onContextSwitch()
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
					if a.messages.room != "" && a.client != nil {
						a.client.RequestRoomMembers(a.messages.room)
					}
					a.input.SetMembers(a.activeMemberEntries())
					a.input.SetNonMembers(a.activeNonMemberEntries())
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
			// Phase 15 edit mode entry: Up-arrow on an empty input in
			// an active (not left, not retired) context populates the
			// input with the user's most recent editable message body
			// and flips the input into edit mode. On Enter we dispatch
			// an edit envelope instead of a send. Esc cancels.
			//
			// Gated by:
			//   - input is empty (don't hijack normal editor cursor nav)
			//   - input is not already in edit mode
			//   - context is not archived (IsLeft || IsRoomRetired)
			//   - no DM-with-retired-partner banner
			//   - the user has at least one non-deleted message in the
			//     current context
			if msg.String() == "up" && a.input.IsEmpty() && !a.input.IsEditing() &&
				!a.messages.IsLeft() && !a.messages.IsRoomRetired() && a.client != nil {
				if a.tryEnterEditMode() {
					return a, nil
				}
				// Otherwise fall through to normal up-arrow handling.
			}
			// Phase 15 edit mode Esc handling: if we're in edit mode and
			// the user presses Esc, clear the buffer and exit edit mode
			// without dispatching anything.
			if msg.String() == "esc" && a.input.IsEditing() {
				a.input.ExitEditMode()
				a.input.ClearInput()
				return a, nil
			}
			// Phase 15 edit mode Enter dispatch: if we're in edit mode
			// and the user presses Enter with non-empty content, send
			// the edit envelope for the tracked target instead of a
			// normal send.
			if msg.String() == "enter" && a.input.IsEditing() {
				text := strings.TrimSpace(a.input.Value())
				if text == "" {
					// Empty body on edit is invalid — same as sending an
					// empty message. Exit edit mode silently.
					a.input.ExitEditMode()
					a.input.ClearInput()
					return a, nil
				}
				a.dispatchEdit(a.input.EditTarget(), text)
				a.input.ExitEditMode()
				a.input.ClearInput()
				return a, nil
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
		// Phase 14 admin actions keep the info panel open so the
		// user can chain multiple operations. All other actions
		// (message, menu, verify, profile) still close it as before.
		isAdminAction := msg.Action == "admin_kick" || msg.Action == "admin_promote" ||
			msg.Action == "admin_demote" || msg.Action == "admin_add"
		if !isAdminAction {
			a.infoPanel.Hide()
		}
		// Don't hide the member menu when the action IS to open it
		if msg.Action != "menu" {
			a.memberMenu.Hide()
		}
		switch msg.Action {
		case "admin_kick":
			// Route through the existing /kick path — same
			// pre-check, same dialog. The info panel stays open so
			// the user can make another selection after the dialog
			// closes.
			if msg.User != "" {
				a.handleGroupAdminCommand(&SlashCommandMsg{
					Command: "/kick",
					Group:   a.infoPanel.group,
					Arg:     msg.User, // raw userID, resolves via the fallback branch
				})
			}
		case "admin_promote":
			if msg.User != "" {
				a.handleGroupAdminCommand(&SlashCommandMsg{
					Command: "/promote",
					Group:   a.infoPanel.group,
					Arg:     msg.User,
				})
			}
		case "admin_demote":
			if msg.User != "" {
				a.handleGroupAdminCommand(&SlashCommandMsg{
					Command: "/demote",
					Group:   a.infoPanel.group,
					Arg:     msg.User,
				})
			}
		case "admin_add":
			// Add doesn't have a target in the info panel — the
			// user needs to type a username in response. Surface
			// a hint in the status bar and let them use /add from
			// the input. Future: could open an inline prompt.
			a.statusBar.SetError("Use /add @user to add a new member to this group")
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
		//
		// Phase 14: track the request so that a subsequent ErrForbidden
		// (last-admin rejection) can open the inline promote picker
		// with the right TriggerDelete flag.
		if a.client != nil && msg.Group != "" {
			a.pendingLastAdminGroup = msg.Group
			a.pendingLastAdminDelete = false
			if err := a.client.Enc().Encode(map[string]string{
				"type":  "leave_group",
				"group": msg.Group,
			}); err != nil {
				a.statusBar.SetError("Leave failed: " + err.Error())
			}
		}
		return a, nil

	case AddConfirmMsg:
		// Phase 14: user confirmed /add. Send add_to_group; the server
		// runs the admin gate and emits group_event{join} back to the
		// whole group plus group_added_to to the target's sessions.
		// State updates all flow through the existing dispatch pipeline.
		if a.client != nil && msg.Group != "" && msg.TargetID != "" {
			if err := a.client.AddToGroup(msg.Group, msg.TargetID, false); err != nil {
				a.statusBar.SetError("Add failed: " + err.Error())
			}
		}
		return a, nil

	case KickConfirmMsg:
		// Phase 14: user confirmed /kick. Send remove_from_group;
		// the server runs the admin gate + last-admin check and
		// emits group_event{leave, reason:"removed"} to remaining
		// members plus group_left to the kicked user's sessions.
		//
		// Also record the kick for /undo — within undoWindowSeconds
		// the user can run /undo to re-add the kicked user via
		// add_to_group. Only the MOST RECENT kick is tracked (no
		// undo stack); any subsequent admin action supersedes it.
		if a.client != nil && msg.Group != "" && msg.TargetID != "" {
			if err := a.client.RemoveFromGroup(msg.Group, msg.TargetID); err != nil {
				a.statusBar.SetError("Remove failed: " + err.Error())
			} else {
				a.lastKickGroup = msg.Group
				a.lastKickUserID = msg.TargetID
				a.lastKickTS = time.Now().Unix()
				targetName := a.resolveDisplayName(msg.TargetID)
				a.statusBar.SetError("Removed " + targetName + " — /undo within 30s to revert")
			}
		}
		return a, nil

	case PromoteConfirmMsg:
		// Phase 14: user confirmed /promote. Server runs the admin
		// gate and already-admin check, then broadcasts
		// group_event{promote}.
		if a.client != nil && msg.Group != "" && msg.TargetID != "" {
			if err := a.client.PromoteGroupAdmin(msg.Group, msg.TargetID, false); err != nil {
				a.statusBar.SetError("Promote failed: " + err.Error())
			}
		}
		return a, nil

	case DemoteConfirmMsg:
		// Phase 14: user confirmed /demote. Server runs the admin
		// gate + last-admin check, then broadcasts group_event{demote}.
		if a.client != nil && msg.Group != "" && msg.TargetID != "" {
			if err := a.client.DemoteGroupAdmin(msg.Group, msg.TargetID, false); err != nil {
				a.statusBar.SetError("Demote failed: " + err.Error())
			}
		}
		return a, nil

	case TransferConfirmMsg:
		// Phase 14: user confirmed /transfer. Client-side sugar:
		// promote target (if not already admin), then leave. The
		// server serializes writes so leave lands after promote and
		// the "at least one admin" invariant holds during the
		// transition. If the target is already admin, the promote
		// is skipped entirely — just send leave.
		if a.client == nil || msg.Group == "" || msg.TargetID == "" {
			return a, nil
		}
		if !msg.TargetAlreadyAdmin {
			if err := a.client.PromoteGroupAdmin(msg.Group, msg.TargetID, false); err != nil {
				a.statusBar.SetError("Transfer failed (promote): " + err.Error())
				return a, nil
			}
		}
		if err := a.client.Enc().Encode(map[string]string{
			"type":  "leave_group",
			"group": msg.Group,
		}); err != nil {
			a.statusBar.SetError("Transfer failed (leave): " + err.Error())
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
		//
		// Phase 14: track as pending last-admin candidate with
		// TriggerDelete=true so ErrForbidden opens the picker with
		// the right follow-up.
		if a.client != nil && msg.Group != "" {
			a.pendingLastAdminGroup = msg.Group
			a.pendingLastAdminDelete = true
			if err := a.client.DeleteGroup(msg.Group); err != nil {
				a.statusBar.SetError("Delete failed: " + err.Error())
			}
		}
		return a, nil

	case LastAdminPickerMsg:
		// Phase 14: user picked a successor in the last-admin
		// picker. Send promote_group_admin → then the original
		// leave_group or delete_group. Server serializes writes so
		// the leave/delete lands after the promote, satisfying the
		// invariant. Clear pending state.
		if a.client == nil || msg.Group == "" || msg.Successor == "" {
			a.pendingLastAdminGroup = ""
			a.pendingLastAdminDelete = false
			return a, nil
		}
		if err := a.client.PromoteGroupAdmin(msg.Group, msg.Successor, false); err != nil {
			a.statusBar.SetError("Promote failed: " + err.Error())
			a.pendingLastAdminGroup = ""
			a.pendingLastAdminDelete = false
			return a, nil
		}
		if msg.TriggerDelete {
			if err := a.client.DeleteGroup(msg.Group); err != nil {
				a.statusBar.SetError("Delete failed: " + err.Error())
			}
		} else {
			if err := a.client.Enc().Encode(map[string]string{
				"type":  "leave_group",
				"group": msg.Group,
			}); err != nil {
				a.statusBar.SetError("Leave failed: " + err.Error())
			}
		}
		a.pendingLastAdminGroup = ""
		a.pendingLastAdminDelete = false
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
			// Phase 17c Step 6: keypress-ack indicator + 200ms tea.Tick
			// to repaint when the timer elapses (even if the server
			// response hasn't cleared it first).
			a.statusBar.SetRefreshing(refreshingMinVisibleMs * time.Millisecond)
			return a, tea.Tick(refreshingMinVisibleMs*time.Millisecond, func(time.Time) tea.Msg {
				return refreshingTickMsg{}
			})
		}
		return a, nil

	case RefreshRequestMsg:
		// Phase 17c Step 6: central handler for refresh-key bindings
		// (info panel `r`, device manager `r`, global Ctrl+Shift+R).
		// Dispatches the right verb for each kind + fires the
		// keypress-ack indicator.
		if a.client == nil {
			return a, nil
		}
		switch msg.Kind {
		case "room_members":
			if a.messages.room != "" {
				a.client.RequestRoomMembers(a.messages.room)
			}
		case "device_list":
			a.client.SendListDevices()
		case "reconnect":
			// Force reconnect — closing the client triggers the
			// outer reconnect loop (see reconnect.go). The
			// refreshing indicator stays visible during the
			// transition; the SetConnected(true) on successful
			// re-handshake is what ultimately masks it.
			a.statusBar.SetReconnecting(0, 0)
			_ = a.client.Close()
		}
		a.statusBar.SetRefreshing(refreshingMinVisibleMs * time.Millisecond)
		return a, tea.Tick(refreshingMinVisibleMs*time.Millisecond, func(time.Time) tea.Msg {
			return refreshingTickMsg{}
		})

	case refreshingTickMsg:
		// Phase 17c Step 6: the 200ms minimum-visibility window
		// elapsed. Returning nil forces a repaint; statusBar View
		// notices refreshingUntil is past and drops the indicator.
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
		// Key was accepted — re-pin happened during StoreProfile.
		// Phase 21 F3.c closure 2026-04-19 — nudge toward verification
		// so users who want the trust work done have a clear next step.
		displayName := a.resolveDisplayName(msg.User)
		a.statusBar.SetError("New key accepted for " + displayName +
			". Run /verify " + displayName + " to compare safety numbers out-of-band.")
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
		a.onContextSwitch()
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
		// Phase 14: live callback into the client's in-memory admin
		// set so the sidebar ★ indicator updates immediately on
		// group_event{promote,demote} without waiting for a
		// group_list refetch.
		a.sidebar.resolveIsLocalAdmin = func(groupID string) bool {
			if a.client == nil {
				return false
			}
			return a.client.IsGroupAdmin(groupID, a.client.UserID())
		}
		a.newConv.resolveName = a.client.DisplayName
		a.infoPanel.resolveRoomName = a.client.DisplayRoomName
		if len(a.client.Rooms()) > 0 {
			a.messages.SetContext(a.client.Rooms()[0], "", "")
			a.onContextSwitch()
			a.syncMessagesLeftState()
			a.messages.LoadFromDB(a.client)
			// Set up member list for @completion
			a.memberPanel.Refresh(a.client.Rooms()[0], "", a.client, a.sidebar.online)
			a.input.SetMembers(a.activeMemberEntries())
			a.input.SetNonMembers(a.activeNonMemberEntries())
		}

		a.statusBar.SetUser(a.client.DisplayName(a.client.UserID()), a.client.IsAdmin())
		a.statusBar.SetConnected(true)
		a.updateTitle()

		// Start listening for server messages
		cmds = append(cmds, waitForMsg(msg.msgCh, msg.errCh, msg.keyWarnCh, a.client.Done()))
		// Store channels for future waits
		a.sidebar.msgCh = msg.msgCh
		a.sidebar.errCh = msg.errCh
		a.sidebar.keyWarnCh = msg.keyWarnCh

	case ServerMsg:
		if cmd := a.handleServerMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Continue listening
		if a.client != nil {
			if a.sidebar.msgCh != nil {
				cmds = append(cmds, waitForMsg(a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.client.Done()))
			}
		}

	case KeyChangeEvent:
		// Phase 21 F3.a closure 2026-04-19 — StoreProfile detected a
		// fingerprint mismatch against the pinned value. Under the
		// no-rotation protocol invariant, this is always an anomaly
		// (compromised server, server bug, or local DB tampering —
		// see PROTOCOL.md "Keys as Identities"). Show the blocking
		// modal unless another modal is already visible; if so, drop
		// this event (the user is busy with something; the detection
		// has already logged + ClearVerified; they'll see the missing
		// ✓ badge from F28 as a persistent indicator).
		if a.keyWarning.IsVisible() || a.verify.IsVisible() || a.quitConfirm.IsVisible() ||
			a.passphrase.IsVisible() {
			// Skip — a.keyWarning.Show would overwrite in-flight user
			// interaction. F28's badge carries the state until the
			// next profile receive (or next user action).
		} else {
			a.keyWarning.Show(msg.User, msg.OldFingerprint, msg.NewFingerprint)
		}
		// Continue listening for the next event.
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.client.Done()))
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
		a.addConfirm.IsVisible() || a.kickConfirm.IsVisible() || a.promoteConfirm.IsVisible() ||
		a.demoteConfirm.IsVisible() || a.transferConfirm.IsVisible() ||
		a.auditOverlay.IsVisible() || a.membersOverlay.IsVisible() ||
		a.lastAdminPicker.IsVisible() ||
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
				a.onContextSwitch()
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
					if a.messages.room != "" && a.client != nil {
						a.client.RequestRoomMembers(a.messages.room)
					}
					a.input.SetMembers(a.activeMemberEntries())
					a.input.SetNonMembers(a.activeNonMemberEntries())
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

// onContextSwitch runs every app-layer side effect that must fire
// when the messages context changes — sidebar navigation, quick
// switch, search jump, inline creation flows, or terminal context
// clears triggered by `room_deleted` / `group_deleted` / `dm_left` /
// `room_retired` broadcasts and by self-leave echoes. Called AFTER
// `a.messages.SetContext(...)` at every call site so the messages
// model already reflects the new context when the side effects run.
//
// Side effects in order:
//
//  1. **Exit edit mode** if the user had a half-finished edit in the
//     previous context. Dispatching that edit after a context switch
//     would either land in the wrong conversation (if the new context
//     is active) or silently no-op (if the new context is empty),
//     both of which are bad user experience. The buffer is cleared
//     along with the edit flag so the user doesn't ship an orphaned
//     body on their next keystroke. Phase 15.
//
//  2. **Apply the new room's topic** (or clear it in non-room
//     contexts). Reads from the local DB via
//     `Client.DisplayRoomTopic` and pushes the result into the
//     messages model so the two-line pane header renders the current
//     topic. Safe to call with group / DM / empty contexts — the
//     resolver returns empty and `SetRoomTopic("")` omits the topic
//     line cleanly. Also safe when the client is nil (first-run
//     wizard or pre-connect): the messages model renders the title
//     line only until the next context switch. Phase 18.
//
// This helper REPLACES the older `applyRoomTopic` — which only ran
// the topic-resolution step and had edit-mode cleanup piggybacked
// onto it. Piggybacking was a bug waiting to happen because the
// cleared-context call sites (`SetContext("", "", "")` for a deleted
// or retired room, left group, etc.) never called `applyRoomTopic`,
// so edit mode was left hanging with a stale target in a
// now-non-existent context.  Every SetContext site now calls this
// single helper, which closes the gap.
func (a *App) onContextSwitch() {
	// (1) Exit edit mode on every context switch.
	if a.input.IsEditing() {
		a.input.ExitEditMode()
		a.input.ClearInput()
	}
	// (2) Apply the new room's topic (or clear it).
	if a.client == nil || a.messages.room == "" {
		a.messages.SetRoomTopic("")
		return
	}
	a.messages.SetRoomTopic(a.client.DisplayRoomTopic(a.messages.room))
}

// tryEnterEditMode scans backwards through the loaded messages for
// the user's most recent non-deleted message in the current context.
// If found, populates the input buffer with that message's body and
// flips the input into edit mode, tracking the message ID so the
// subsequent Enter dispatches an edit envelope instead of a normal
// send. Phase 15. Returns true if edit mode was entered (handled the
// key press) or false if no editable message exists (key falls
// through to normal up-arrow handling).
//
// The scan uses the in-memory message list, not the store helper, so
// it respects whatever the user is currently viewing — if they've
// scrolled back through history and the local list has older rows,
// the "most recent" is still the highest TS row in the visible list.
func (a *App) tryEnterEditMode() bool {
	selfID := a.client.UserID()
	if selfID == "" {
		return false
	}
	// Scan backwards: most recent first. Skip deleted rows and system
	// messages (which have IsSystem == true). The first non-deleted
	// message from the local user is the edit target.
	for i := len(a.messages.messages) - 1; i >= 0; i-- {
		msg := a.messages.messages[i]
		if msg.IsSystem || msg.Deleted {
			continue
		}
		if msg.FromID != selfID {
			continue
		}
		// Found the user's most recent editable message.
		a.input.EnterEditMode(msg.ID, msg.Body)
		return true
	}
	return false
}

// dispatchEdit sends the appropriate edit verb for the current
// context. Called from the FocusInput Enter handler when the input
// is in edit mode. Dispatches to EditRoomMessage / EditGroupMessage
// / EditDMMessage based on which of messages.room / messages.group /
// messages.dm is non-empty. Client validates and sends; server
// validates and either broadcasts an `edited` echo (which the
// dispatch path applies to local state) or returns an error (which
// the error handler surfaces to the status bar).
func (a *App) dispatchEdit(msgID, newBody string) {
	if a.client == nil || msgID == "" {
		return
	}
	var err error
	switch {
	case a.messages.room != "":
		err = a.client.EditRoomMessage(msgID, a.messages.room, newBody)
	case a.messages.group != "":
		err = a.client.EditGroupMessage(msgID, a.messages.group, newBody)
	case a.messages.dm != "":
		err = a.client.EditDMMessage(msgID, a.messages.dm, newBody)
	default:
		return
	}
	if err != nil {
		a.statusBar.SetError("Edit failed: " + err.Error())
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
	a.onContextSwitch()
	a.syncMessagesLeftState()
	a.messages.LoadFromDB(a.client)
	if a.memberPanel.IsVisible() {
		a.memberPanel.Refresh(a.messages.room, a.messages.group, a.client, a.sidebar.online)
		if a.messages.room != "" && a.client != nil {
			a.client.RequestRoomMembers(a.messages.room)
		}
		a.input.SetMembers(a.activeMemberEntries())
		a.input.SetNonMembers(a.activeNonMemberEntries())
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

// buildLastAdminCandidates returns the non-admin, non-self, non-retired
// members of a group — the pool of valid successor candidates for the
// last-admin inline promote picker. Used by the error handler when
// ErrForbidden (last-admin) arrives.
func (a *App) buildLastAdminCandidates(groupID string) []pickerMember {
	if a.client == nil {
		return nil
	}
	self := a.client.UserID()
	var out []pickerMember
	for _, uid := range a.client.GroupMembers(groupID) {
		if uid == self {
			continue
		}
		if a.client.IsGroupAdmin(groupID, uid) {
			continue
		}
		if retired, _ := a.client.IsRetired(uid); retired {
			continue
		}
		out = append(out, pickerMember{
			UserID:      uid,
			DisplayName: a.client.DisplayName(uid),
		})
	}
	return out
}

// activeNonMemberEntries is Phase 14's companion to activeMemberEntries.
// Returns the users the client knows about (via the profile cache)
// who are NOT currently members of the active group. Used to feed
// /add completion — the target of /add is by definition not yet a
// member. Empty in non-group contexts. Filters out retired users
// and the local user themselves.
func (a *App) activeNonMemberEntries() []MemberEntry {
	if a.client == nil || a.messages.group == "" {
		return nil
	}
	inGroup := make(map[string]bool)
	for _, uid := range a.client.GroupMembers(a.messages.group) {
		inGroup[uid] = true
	}
	var out []MemberEntry
	a.client.ForEachProfile(func(p *protocol.Profile) {
		if p.User == a.client.UserID() {
			return
		}
		if inGroup[p.User] {
			return
		}
		if retired, _ := a.client.IsRetired(p.User); retired {
			return
		}
		out = append(out, MemberEntry{
			UserID:      p.User,
			DisplayName: p.DisplayName,
		})
	})
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
		// Phase 21 F29 closure (2026-04-19) — accept display names or
		// "@alice" syntax in addition to raw user IDs, matching the
		// completion affordance of /add. FindUserByName resolves both
		// shapes (display name and user ID); the raw user ID is also
		// accepted as a no-op passthrough so pre-F29 /verify usr_abc
		// invocations continue to work.
		if sc.Arg != "" && a.client != nil {
			targetID, ok := a.resolveUserByName(sc.Arg)
			if !ok {
				a.statusBar.SetError("unknown user: " + sc.Arg)
			} else {
				a.verify.Show(targetID, a.client)
			}
		}
	case "/unverify":
		// Phase 21 F29 closure — same resolution as /verify.
		if sc.Arg != "" && a.client != nil {
			targetID, ok := a.resolveUserByName(sc.Arg)
			if !ok {
				a.statusBar.SetError("unknown user: " + sc.Arg)
			} else if st := a.client.Store(); st != nil {
				st.ClearVerified(targetID)
				a.statusBar.SetError("Verification removed for " + a.resolveDisplayName(targetID))
			}
		}
	case "/whois":
		// Phase 21 F30 closure (2026-04-19) — display the full locally-
		// known identity info for a user on demand: display name, user
		// ID, SSH key fingerprint, verified state, first-seen timestamp
		// and last-key-updated timestamp. Analogous to ssh-keyscan's
		// output shape but using local TOFU state as the source of
		// truth. Also copies the fingerprint to the clipboard so it can
		// be pasted into a verification workflow.
		a.handleWhoisCommand(sc.Arg)
	case "/search":
		a.search.Show()
	case "/settings":
		username := ""
		if a.client != nil {
			username = a.client.UserID()
		}
		a.settings.Show(a.appConfig, a.configDir, username, a.serverIdx)
	case "/help":
		// Phase 14: context-aware /help — show admin commands only
		// when the local user is an admin of the currently-active
		// group. In room/DM contexts the admin verbs are hidden
		// regardless since they're group-only anyway.
		showAdmin := false
		if sc.Group != "" {
			showAdmin = a.isLocalAdminOfGroup(sc.Group)
		}
		a.help.SetContext(showAdmin)
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
	case "/rename":
		// Phase 14: client-side admin pre-check. Non-admins get a
		// friendly local rejection; admins pass through to the server
		// which also enforces the gate. Without this pre-check, the
		// server rejection surfaces as ErrUnknownGroup (byte-identical
		// privacy), which reads as a confusing "you are not a member".
		if sc.Group == "" || sc.Arg == "" {
			return
		}
		if a.client == nil {
			return
		}
		if !a.isLocalAdminOfGroup(sc.Group) {
			a.statusBar.SetError("You are not an admin of this group — only admins can /rename")
			return
		}
		if err := a.client.Enc().Encode(map[string]string{
			"type": "rename_group", "group": sc.Group, "name": sc.Arg,
		}); err != nil {
			a.statusBar.SetError("Rename failed: " + err.Error())
		}
	case "/add", "/kick", "/promote", "/demote", "/transfer":
		a.handleGroupAdminCommand(sc)
	case "/whoami":
		a.handleWhoamiCommand(sc)
	case "/groupinfo":
		if sc.Group == "" || a.client == nil {
			return
		}
		a.infoPanel.ShowGroup(sc.Group, a.client, a.sidebar.online)
	case "/audit":
		a.handleAuditCommand(sc)
	case "/members":
		a.handleMembersOverlayCommand(sc, false)
	case "/admins":
		a.handleMembersOverlayCommand(sc, true)
	case "/role":
		a.handleRoleCommand(sc)
	case "/undo":
		a.handleUndoCommand(sc)
	case "/groupcreate":
		a.handleGroupcreateCommand(sc)
	case "/dmcreate":
		a.handleDmcreateCommand(sc)
	case "/topic":
		a.handleTopicCommand(sc)
	}
}

// handleTopicCommand (Phase 18) surfaces the current room topic in the
// status bar. Read-only — changing a topic is deferred to Phase 16 with
// the CLI audit + room_updated broadcast work. In a room context with a
// topic set, shows "#name — topic text". In a room with no topic, shows
// "#name has no topic set". In a group or 1:1 DM context, shows
// "/topic is only available in rooms" — groups have no topics by design.
func (a *App) handleTopicCommand(sc *SlashCommandMsg) {
	if sc.Room == "" {
		a.statusBar.SetError("/topic is only available in rooms")
		return
	}
	if a.client == nil {
		a.statusBar.SetError("/topic unavailable — not connected")
		return
	}
	name := a.client.DisplayRoomName(sc.Room)
	topic := a.client.DisplayRoomTopic(sc.Room)
	if topic == "" {
		a.statusBar.SetError("#" + name + " has no topic set")
		return
	}
	a.statusBar.SetError("#" + name + " — " + topic)
}

// Phase 14 Chunk 6 command handlers ------------------------------------

// handleAuditCommand opens the /audit overlay with the last N events
// for the current group. Default N is 10; user can override with
// /audit 50 for a longer view.
func (a *App) handleAuditCommand(sc *SlashCommandMsg) {
	if sc.Group == "" || a.client == nil {
		return
	}
	limit := 10
	if sc.Arg != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(sc.Arg)); err == nil && n > 0 {
			if n > 500 {
				n = 500 // sanity cap
			}
			limit = n
		}
	}
	st := a.client.Store()
	if st == nil {
		a.statusBar.SetError("Audit unavailable — no local store")
		return
	}
	events, err := st.GetRecentGroupEvents(sc.Group, limit)
	if err != nil {
		a.statusBar.SetError("Audit read failed: " + err.Error())
		return
	}
	groupName := a.lookupGroupName(sc.Group)
	a.auditOverlay.Show(sc.Group, groupName, events, a.resolveDisplayName)
}

// handleMembersOverlayCommand opens the /members (all) or /admins
// (filtered) overlay for the current group. Reads from the client's
// in-memory groupMembers + groupAdmins maps at Show time; the
// overlay is one-shot and doesn't update live.
func (a *App) handleMembersOverlayCommand(sc *SlashCommandMsg, adminsOnly bool) {
	if sc.Group == "" || a.client == nil {
		return
	}
	members := a.client.GroupMembers(sc.Group)
	adminSet := make(map[string]bool)
	for _, uid := range a.client.GroupAdmins(sc.Group) {
		adminSet[uid] = true
	}
	groupName := a.lookupGroupName(sc.Group)
	a.membersOverlay.Show(sc.Group, groupName, members, adminSet, adminsOnly, a.resolveDisplayName)
}

// handleRoleCommand surfaces the target user's role in the current
// group via the status bar: "alice — admin" or "bob — member" or
// "carol is not a member of this group".
func (a *App) handleRoleCommand(sc *SlashCommandMsg) {
	if sc.Group == "" || sc.Arg == "" || a.client == nil {
		return
	}
	targetID, ok := a.resolveGroupMemberByName(sc.Group, sc.Arg)
	if !ok {
		a.statusBar.SetError(strings.TrimPrefix(sc.Arg, "@") + " is not a member of this group")
		return
	}
	name := a.client.DisplayName(targetID)
	role := "member"
	if a.client.IsGroupAdmin(sc.Group, targetID) {
		role = "admin"
	}
	a.statusBar.SetError(name + " — " + role)
}

// handleUndoCommand reverts the last kick the local user performed
// within the undo window. Sends add_to_group for the previously
// kicked user and clears the tracking state. Phase 14.
//
// Scope is deliberately narrow: exactly one kick, one group, 30s.
// Any subsequent admin action (kick, promote, demote, add) does NOT
// supersede this state on its own — only a new kick overwrites it,
// and an expired-and-cleared undo falls through to "nothing to undo".
func (a *App) handleUndoCommand(sc *SlashCommandMsg) {
	// Note: the client==nil check is NOT the first guard. We want
	// user-facing validation errors ("nothing to undo", "different
	// group") to surface even in test scenarios where the client
	// is nil — only the actual AddToGroup wire call needs the
	// client pointer.
	if a.lastKickGroup == "" || a.lastKickUserID == "" {
		a.statusBar.SetError("Nothing to undo")
		return
	}
	if time.Now().Unix()-a.lastKickTS > undoWindowSeconds {
		a.lastKickGroup = ""
		a.lastKickUserID = ""
		a.lastKickTS = 0
		a.statusBar.SetError("Undo window expired (30s)")
		return
	}
	if sc.Group != a.lastKickGroup {
		a.statusBar.SetError("Last kick was in a different group")
		return
	}
	if a.client == nil {
		return
	}
	// Pre-check: still an admin of this group?
	if !a.isLocalAdminOfGroup(sc.Group) {
		a.statusBar.SetError("You are no longer an admin of this group")
		return
	}
	targetID := a.lastKickUserID
	targetName := a.client.DisplayName(targetID)
	if err := a.client.AddToGroup(sc.Group, targetID, false); err != nil {
		a.statusBar.SetError("Undo failed: " + err.Error())
		return
	}
	// Clear state so /undo twice in a row is a no-op instead of
	// re-adding a user who just left voluntarily.
	a.lastKickGroup = ""
	a.lastKickUserID = ""
	a.lastKickTS = 0
	a.statusBar.SetError("Re-adding " + targetName + " to the group")
}

// handleGroupcreateCommand parses /groupcreate arguments and creates
// a new group DM directly via client.CreateGroup (bypassing the
// wizard). Accepted forms:
//
//	/groupcreate "Project X" @alice @bob @carol
//	/groupcreate @alice @bob @carol
//
// The quoted name is optional. Targets are resolved via the profile
// cache (same as /add). Min 1 target (plus the caller = 2-member
// group); max 149 (plus caller = 150, the server's hard cap).
func (a *App) handleGroupcreateCommand(sc *SlashCommandMsg) {
	if a.client == nil {
		return
	}
	name, tokens := parseGroupcreateArgs(sc.Arg)
	if len(tokens) == 0 {
		a.statusBar.SetError("Usage: /groupcreate [\"name\"] @user [@user ...]")
		return
	}
	var members []string
	var unresolved []string
	for _, tok := range tokens {
		uid, ok := a.resolveNonMemberByName(tok)
		if !ok {
			unresolved = append(unresolved, tok)
			continue
		}
		members = append(members, uid)
	}
	if len(unresolved) > 0 {
		a.statusBar.SetError("No user matching " + strings.Join(unresolved, ", "))
		return
	}
	if len(members) == 0 {
		a.statusBar.SetError("No valid members — nothing to create")
		return
	}
	if err := a.client.CreateGroup(members, name); err != nil {
		a.statusBar.SetError("Create failed: " + err.Error())
	}
}

// handleDmcreateCommand parses /dmcreate @user and creates (or opens)
// a 1:1 DM via client.CreateDM. The server dedups by pair so running
// twice for the same target returns the existing row.
func (a *App) handleDmcreateCommand(sc *SlashCommandMsg) {
	if a.client == nil || sc.Arg == "" {
		return
	}
	targetID, ok := a.resolveNonMemberByName(sc.Arg)
	if !ok {
		a.statusBar.SetError("No user matching " + sc.Arg)
		return
	}
	if targetID == a.client.UserID() {
		a.statusBar.SetError("Cannot create a DM with yourself")
		return
	}
	if err := a.client.CreateDM(targetID); err != nil {
		a.statusBar.SetError("Create DM failed: " + err.Error())
	}
}

// parseGroupcreateArgs splits a /groupcreate argument string into
// (optional quoted name, token list). Handles:
//
//	"Project X" @alice @bob        → ("Project X", ["@alice","@bob"])
//	@alice @bob @carol             → ("",           ["@alice","@bob","@carol"])
//	"Quoted Only"                  → ("Quoted Only",[])
//
// Only a leading double-quoted section is treated as the name;
// quotes mid-string are passed through as literal tokens.
func parseGroupcreateArgs(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	name := ""
	rest := raw
	if strings.HasPrefix(raw, "\"") {
		if end := strings.Index(raw[1:], "\""); end > 0 {
			name = raw[1 : 1+end]
			rest = strings.TrimSpace(raw[1+end+1:])
		}
	}
	if rest == "" {
		return name, nil
	}
	fields := strings.Fields(rest)
	return name, fields
}

// Phase 14 helpers ------------------------------------------------------

// isLocalAdminOfGroup returns true if the local user is currently
// recorded as an admin of the given group. Reads from the client's
// in-memory admin set (populated by group_list catchup and live
// group_event{promote,demote} broadcasts) so the answer reflects
// the most recent state without a server round-trip.
func (a *App) isLocalAdminOfGroup(groupID string) bool {
	if a.client == nil {
		return false
	}
	return a.client.IsGroupAdmin(groupID, a.client.UserID())
}

// resolveGroupMemberByName maps a typed @display-name string (with or
// without the leading @) to a member user ID in the given group.
// Returns ("", false) if no member matches. Case-insensitive.
func (a *App) resolveGroupMemberByName(groupID, name string) (string, bool) {
	if a.client == nil {
		return "", false
	}
	target := strings.TrimPrefix(name, "@")
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return "", false
	}
	for _, uid := range a.client.GroupMembers(groupID) {
		if strings.EqualFold(a.client.DisplayName(uid), target) {
			return uid, true
		}
		if strings.EqualFold(uid, target) {
			return uid, true
		}
	}
	return "", false
}

// resolveNonMemberByName searches the client's profile cache for a
// user matching the typed name. Used by /add where the target is
// (by definition) not yet in GroupMembers(). Delegates to the client
// layer which owns the profile cache + its lock.
func (a *App) resolveNonMemberByName(name string) (string, bool) {
	return a.resolveUserByName(name)
}

// resolveUserByName is the generic "display name or user ID" lookup.
// Unlike resolveNonMemberByName (which is scoped to the /add
// affordance), this variant is used by /verify and /unverify where
// the target can be any known user. Both wrappers delegate to the
// same `client.FindUserByName`; the distinct names document where
// each is used rather than expressing different lookup semantics.
//
// Phase 21 F29 closure (2026-04-19): /verify and /unverify previously
// passed raw args to their downstream handlers, breaking parity with
// /add's display-name completion. This helper landed to give both
// verbs the same affordance.
func (a *App) resolveUserByName(name string) (string, bool) {
	if a.client == nil {
		return "", false
	}
	// Trim both sides before AND after stripping the "@" prefix so
	// "  @Alice  " and "@Alice" both resolve. Users routinely paste
	// whitespace around mentions; strict ordering here would make
	// /verify finicky.
	target := strings.TrimSpace(name)
	target = strings.TrimPrefix(target, "@")
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	return a.client.FindUserByName(target)
}

// handleWhoisCommand implements the `/whois <name>` read-only slash
// command. Phase 21 F30 closure (2026-04-19) — operators investigating
// "did Alice's key actually rotate?" previously had no quick-lookup
// command; their only options were to wait for a KeyWarningModel
// (triggered only on change) or launch the full safety-number
// VerifyModel. /whois exposes all locally-known identity state at once:
//
//	Alice (usr_abc12345) SHA256:ab...ef [verified] first seen
//	  2026-03-15, key updated 2026-04-10 — fingerprint copied
//
// Data comes from two sources: the live profile cache (display name,
// admin/retired flags, current fingerprint) and the pinned_keys store
// (verified state, first-seen + last-updated timestamps, and the
// fingerprint fallback for users whose live profile isn't available —
// e.g., retired users whose profile broadcast we missed). When no
// pinned entry exists and no live profile is available, the command
// surfaces "unknown user" and does nothing else.
//
// The fingerprint is copied to the clipboard to match the `/mykey`
// ergonomic — operators routinely paste it into a verification-
// workflow document or chat.
func (a *App) handleWhoisCommand(rawName string) {
	if a.client == nil {
		a.statusBar.SetError("Usage: /whois <name>")
		return
	}
	targetID, ok := a.resolveUserByName(rawName)
	if !ok {
		// Retired-user fallback: the live profile cache
		// (`FindUserByName`) only holds users the server has
		// broadcast to us in this session, so retired accounts
		// whose profile broadcast we missed are invisible there.
		// If the raw argument matches a row in pinned_keys,
		// resolve directly against that — this is the scenario
		// the F30 recommendation explicitly cited ("did Alice's
		// key actually rotate?" where Alice has since been
		// retired). Lookup is by exact user ID; display-name
		// fallback to pinned_keys isn't implemented because the
		// pinned_keys row doesn't carry the display name.
		trimmed := strings.TrimSpace(rawName)
		trimmed = strings.TrimPrefix(trimmed, "@")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed != "" {
			if st := a.client.Store(); st != nil {
				if info, err := st.GetPinnedKeyInfo(trimmed); err == nil && info.Fingerprint != "" {
					targetID = trimmed
					ok = true
				}
			}
		}
		if !ok {
			a.statusBar.SetError("unknown user: " + strings.TrimSpace(rawName))
			return
		}
	}

	profile := a.client.Profile(targetID)
	displayName := a.resolveDisplayName(targetID) // falls back to target ID

	var info store.PinnedKeyInfo
	if st := a.client.Store(); st != nil {
		// Swallow sql errors — surface as no-pinned-data rather than
		// a noisy stack. Missing pinned entry is the norm for users
		// we've never received messages from.
		info, _ = st.GetPinnedKeyInfo(targetID)
	}

	// Prefer the live profile's fingerprint (authoritative, current-
	// broadcast) over the pinned fingerprint (last-cached). They should
	// match except during the window between a server push and
	// StoreProfile updating the pinned row; the live one wins either
	// way.
	fingerprint := info.Fingerprint
	if profile != nil && profile.KeyFingerprint != "" {
		fingerprint = profile.KeyFingerprint
	}
	if fingerprint == "" {
		a.statusBar.SetError("unknown user: " + strings.TrimSpace(rawName) + " (no profile or pinned key)")
		return
	}

	// Build the status-bar string. Kept compact so it fits on a
	// single line in typical terminal widths; full data goes to
	// clipboard and ergonomics follow /mykey.
	parts := []string{displayName + " (" + targetID + ")", fingerprint}

	verifyTag := "unverified"
	if info.Verified {
		verifyTag = "verified"
	}
	parts = append(parts, verifyTag)

	if profile != nil && profile.Admin {
		parts = append(parts, "admin")
	}
	if profile != nil && profile.Retired {
		parts = append(parts, "retired")
	}

	if info.FirstSeen > 0 {
		parts = append(parts, "first seen "+time.Unix(info.FirstSeen, 0).UTC().Format("2006-01-02"))
	}
	if info.UpdatedAt > 0 && info.UpdatedAt != info.FirstSeen {
		parts = append(parts, "key updated "+time.Unix(info.UpdatedAt, 0).UTC().Format("2006-01-02"))
	}

	CopyToClipboard(fingerprint)
	a.statusBar.SetError(strings.Join(parts, " — ") + " — fingerprint copied to clipboard")
}

// lookupGroupName returns the display name of a group, falling back
// to a comma-joined member list when no explicit name is set.
func (a *App) lookupGroupName(groupID string) string {
	for _, g := range a.sidebar.groups {
		if g.ID == groupID {
			if g.Name != "" {
				return g.Name
			}
			var names []string
			for _, m := range g.Members {
				name := m
				if a.client != nil {
					name = a.client.DisplayName(m)
				}
				names = append(names, name)
			}
			return strings.Join(names, ", ")
		}
	}
	return groupID
}

// handleGroupAdminCommand is the common entry point for the five
// Phase 14 in-group admin verbs. Pre-check + @resolve + dialog show.
// On dialog confirm a *ConfirmMsg flows back through the Update loop
// which calls the client.Send* function for the wire verb.
func (a *App) handleGroupAdminCommand(sc *SlashCommandMsg) {
	if sc.Group == "" {
		a.statusBar.SetError(sc.Command + " only works inside a group DM")
		return
	}
	if sc.Arg == "" {
		a.statusBar.SetError("Usage: " + sc.Command + " @user")
		return
	}
	if a.client == nil {
		return
	}

	// Pre-check: local is_admin flag. Non-admins get a friendly
	// client-side rejection. Without this, the server rejection
	// surfaces as ErrUnknownGroup (byte-identical privacy) which
	// reads as a confusing "you are not a member".
	if !a.isLocalAdminOfGroup(sc.Group) {
		a.statusBar.SetError("You are not an admin of this group — only admins can " + sc.Command + ". Type /admins to see who is.")
		return
	}

	groupName := a.lookupGroupName(sc.Group)

	// Resolve @user → userID. /add looks at the profile cache (target
	// is not yet a member); the other four verbs look at the current
	// group's member list.
	var targetID string
	var ok bool
	if sc.Command == "/add" {
		targetID, ok = a.resolveNonMemberByName(sc.Arg)
	} else {
		targetID, ok = a.resolveGroupMemberByName(sc.Group, sc.Arg)
	}
	if !ok {
		a.statusBar.SetError("No user matching " + sc.Arg)
		return
	}
	targetName := a.client.DisplayName(targetID)

	switch sc.Command {
	case "/add":
		for _, m := range a.client.GroupMembers(sc.Group) {
			if m == targetID {
				a.statusBar.SetError(targetName + " is already a member of this group")
				return
			}
		}
		a.addConfirm.Show(sc.Group, groupName, targetID, targetName)
	case "/kick":
		// Self-kick shortcut: route to /leave flow instead (applies
		// the last-admin gate and keeps the audit trail cleaner).
		if targetID == a.client.UserID() {
			a.leaveConfirm.Show(sc.Group, groupName)
			return
		}
		memberCount := len(a.client.GroupMembers(sc.Group))
		a.kickConfirm.Show(sc.Group, groupName, targetID, targetName, memberCount)
	case "/promote":
		if a.client.IsGroupAdmin(sc.Group, targetID) {
			a.statusBar.SetError(targetName + " is already an admin")
			return
		}
		a.promoteConfirm.Show(sc.Group, groupName, targetID, targetName)
	case "/demote":
		if !a.client.IsGroupAdmin(sc.Group, targetID) {
			a.statusBar.SetError(targetName + " is not an admin")
			return
		}
		adminCount := len(a.client.GroupAdmins(sc.Group))
		targetIsSelf := targetID == a.client.UserID()
		a.demoteConfirm.Show(sc.Group, groupName, targetID, targetName, adminCount, targetIsSelf)
	case "/transfer":
		alreadyAdmin := a.client.IsGroupAdmin(sc.Group, targetID)
		a.transferConfirm.Show(sc.Group, groupName, targetID, targetName, alreadyAdmin)
	}
}

// handleWhoamiCommand surfaces the local user's display name + role
// in the current context via the status bar.
func (a *App) handleWhoamiCommand(sc *SlashCommandMsg) {
	if a.client == nil {
		return
	}
	name := a.client.DisplayName(a.client.UserID())
	role := "member"
	if sc.Group != "" && a.isLocalAdminOfGroup(sc.Group) {
		role = "admin"
	}
	a.statusBar.SetError(name + " — " + role)
}

// handleServerMessage processes incoming server messages for the UI.
// Returns an optional tea.Cmd when the handler needs to schedule follow-up
// work (e.g. a delayed retry on server_busy).
//
// Phase 17c Step 5 classification walk (Activity 1) — reference for
// which cases correspond to which client-facing category per
// refactor_plan.md §Phase 17c. The send-queue Ack/Error dispatch is
// handled generically in client.readLoop via dispatchCorrID (see
// sendqueue_dispatch.go); the cases below need per-verb UI logic
// ON TOP of that generic dispatch.
//
//	SUCCESS ACKS (match client requests via corr_id — readLoop Acks):
//	  "message"            ← send              [ack via dispatch]
//	  "group_message"      ← send_group        [ack via dispatch]
//	  "dm"                 ← send_dm           [ack via dispatch]
//	  "edited"             ← edit              [ack via dispatch]
//	  "group_edited"       ← edit_group        [ack via dispatch]
//	  "dm_edited"          ← edit_dm           [ack via dispatch]
//	  "deleted"            ← delete            [ack via dispatch]
//	  "reaction"           ← react             [ack via dispatch]
//	  "reaction_removed"   ← unreact           [ack via dispatch]
//	  "pinned" / "unpinned"← pin / unpin       [ack via dispatch — no client verb today]
//	  "history_result"     ← history           [ack via dispatch]
//	  "room_members_list"  ← room_members      [ack via dispatch]
//	  "device_list"        ← list_devices      [ack via dispatch]
//	  "upload_ready"/"upload_complete" ← upload_start [ack via dispatch]
//	  "download_start"     ← download          [ack via dispatch]
//
//	ERROR CATEGORIES (routed through protocol.CategoryForCode):
//	  Category A-default: rate_limited on send/edit/react/admin →
//	                      retry w/ backoff (driver); surface on exhaust
//	  Category A-silent:  rate_limited on room_members / list_devices →
//	                      silent drop (see Gap 5 in case "error" below)
//	  Category B:         invalid_epoch / epoch_conflict /
//	                      stale_member_list → apply server-pushed
//	                      epoch_key (see case "epoch_key" which calls
//	                      Client.TriggerEpochRetry), then retry
//	  Category C:         permanent user-action (message_too_large,
//	                      edit_window_expired, etc.) → surface to
//	                      statusBar
//	  Category D:         privacy-identical (denied, unknown_room,
//	                      etc.) → surface generic message to statusBar
//
//	SERVER BROADCASTS / PUSHES (no ack, no category):
//	  typing, presence, read, profile, user_retired, room_retired,
//	  room_left, room_deleted, retired_rooms, retired_users,
//	  group_event, group_left, group_deleted, group_renamed,
//	  group_created, group_added_to, dm_created, dm_left, room_event,
//	  room_list, group_list, dm_list, sync_batch, unread, pins,
//	  device_revoked, admin_notify, pending_keys_list, epoch_key,
//	  epoch_trigger, epoch_confirmed, server_shutdown — these cases
//	  apply incoming state directly; no queue interaction needed.
//
// When a new message type is added, classify it here and either wire
// through the generic dispatch (if it's an ack) or apply-state inline
// (if it's a push).
func (a *App) handleServerMessage(msg ServerMsg) tea.Cmd {
	switch msg.Type {
	case "message":
		var m protocol.Message
		json.Unmarshal(msg.Raw, &m)
		// Phase 17c Step 5: if the server echoed back a corr_id that
		// matches an in-flight send-queue entry, ack it. Only the
		// originator's broadcast carries CorrID (server strips it for
		// other recipients).
		if m.CorrID != "" && a.client != nil {
			a.client.SendQueue().Ack(m.CorrID)
		}
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
		if m.CorrID != "" && a.client != nil {
			a.client.SendQueue().Ack(m.CorrID)
		}
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
	case "edited":
		// Phase 15: room message edit broadcast. The client layer has
		// already decrypted and persisted the new body + edited_at to
		// the store via storeEditedRoomMessage. Here we apply the
		// change to the in-memory DisplayMessage so the currently-open
		// message view updates without a LoadFromDB round-trip.
		var e protocol.Edited
		if err := json.Unmarshal(msg.Raw, &e); err == nil && a.client != nil {
			// Decrypt again in the TUI path so we have the plaintext
			// body for the in-memory update. Cheap — the payload is
			// already unwrapped in memory at this point.
			if payload, derr := a.client.DecryptRoomMessage(e.Room, e.Epoch, e.Payload); derr == nil {
				a.messages.ApplyEdit(e.ID, payload.Body, e.EditedAt)
			}
		}
	case "group_edited":
		var e protocol.GroupEdited
		if err := json.Unmarshal(msg.Raw, &e); err == nil && a.client != nil {
			if payload, derr := a.client.DecryptGroupMessage(e.WrappedKeys, e.Payload); derr == nil {
				a.messages.ApplyEdit(e.ID, payload.Body, e.EditedAt)
			}
		}
	case "dm_edited":
		var e protocol.DMEdited
		if err := json.Unmarshal(msg.Raw, &e); err == nil && a.client != nil {
			if payload, derr := a.client.DecryptDMMessage(e.WrappedKeys, e.Payload); derr == nil {
				a.messages.ApplyEdit(e.ID, payload.Body, e.EditedAt)
			}
		}
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
		// Response to room_members — update info panel and member panel.
		// Phase 17c Step 6: signal the status bar that the refresh
		// completed (200ms floor still applies via View check).
		a.statusBar.ClearRefreshing()
		if a.client != nil {
			room, members := a.client.RoomMembersList()
			a.infoPanel.SetRoomMembers(room, members, a.client, a.sidebar.online)
			if a.memberPanel.IsVisible() && a.messages.room == room {
				a.memberPanel.SetRoomMembers(members, a.client, a.sidebar.online)
				a.input.SetMembers(a.activeMemberEntries())
				a.input.SetNonMembers(a.activeNonMemberEntries())
			}
		}
	case "device_revoked":
		var m protocol.DeviceRevoked
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.deviceRevoked.Show(m.DeviceID, m.Reason)
		}
	case "device_list":
		// Phase 17c Step 6: signal refresh completion for the
		// "refreshing…" keypress-ack indicator.
		a.statusBar.ClearRefreshing()
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
		// Phase 14: unified dispatch for all five group event types.
		// The client-layer handler already updated in-memory state
		// (member list, admin set, persistent local admin flag) and
		// persisted the row to the local group_events table for
		// /audit replay. This branch is purely rendering: system
		// messages in the active group's message view, with the Quiet
		// flag honored (event still persisted, just not displayed).
		//
		// Note on coalescing: the plan calls for 10-second same-admin
		// same-verb coalescing ("alice added bob, carol, and dave").
		// That logic lives in Chunk 6 alongside the other render
		// polish; here we emit one system message per event and the
		// Chunk 6 coalescer wraps it.
		var m protocol.GroupEvent
		if err := json.Unmarshal(msg.Raw, &m); err != nil {
			break
		}
		if m.Quiet {
			// Suppressed: client-layer already updated state and
			// persisted the audit row; skip the inline system message.
			break
		}
		// Only render if the event is for the currently-active group.
		if m.Group != a.messages.group {
			break
		}
		userName := a.resolveDisplayName(m.User)
		byName := ""
		if m.By != "" {
			byName = a.resolveDisplayName(m.By)
		}
		switch m.Event {
		case "leave":
			switch m.Reason {
			case "retirement":
				// Retirement is an account-level event — never coalesced.
				a.messages.AddSystemMessage(userName + "'s account was retired")
			case "removed":
				if byName != "" {
					// Coalescing eligible: "alice removed bob" →
					// "alice removed bob, carol, and dave" for
					// same-admin rapid-fire kicks.
					a.messages.AddCoalescingSystemMessage(
						"removed", m.By, byName, userName, m.Group,
						byName+" removed "+userName+" from the group",
						func(joined string) string {
							return byName + " removed " + joined + " from the group"
						},
					)
				} else {
					// Defensive fallback — shouldn't happen post-migration.
					a.messages.AddSystemMessage(userName + " was removed from the group by an admin")
				}
			case "admin":
				// Deprecated legacy value — treat as removed-by-unknown-admin.
				a.messages.AddSystemMessage(userName + " was removed from the group by an admin")
			default:
				// Self-leave — user-initiated, never coalesced.
				a.messages.AddSystemMessage(userName + " left the group")
			}
		case "join":
			if byName != "" {
				a.messages.AddCoalescingSystemMessage(
					"join", m.By, byName, userName, m.Group,
					byName+" added "+userName+" to the group",
					func(joined string) string {
						return byName + " added " + joined + " to the group"
					},
				)
			} else {
				a.messages.AddSystemMessage(userName + " joined the group")
			}
		case "promote":
			if m.Reason == "retirement_succession" {
				// Server-initiated succession — distinct from
				// admin-initiated promote, never coalesced.
				a.messages.AddSystemMessage(userName + " was promoted to admin (previous admin retired)")
			} else if byName != "" {
				a.messages.AddCoalescingSystemMessage(
					"promote", m.By, byName, userName, m.Group,
					byName+" promoted "+userName+" to admin",
					func(joined string) string {
						return byName + " promoted " + joined + " to admin"
					},
				)
			} else {
				a.messages.AddSystemMessage(userName + " was promoted to admin")
			}
		case "demote":
			if byName != "" {
				a.messages.AddCoalescingSystemMessage(
					"demote", m.By, byName, userName, m.Group,
					byName+" demoted "+userName,
					func(joined string) string {
						return byName + " demoted " + joined
					},
				)
			} else {
				a.messages.AddSystemMessage(userName + " was demoted")
			}
		case "rename":
			// Rename is never coalesced — each rename is distinct and
			// the new name is content, not just target list extension.
			if m.Name != "" {
				a.messages.AddSystemMessage(userName + " renamed the group to \"" + m.Name + "\"")
			} else {
				a.messages.AddSystemMessage(userName + " cleared the group name")
			}
		}
	case "group_left":
		// Server confirmed a group leave. Phase 14 reason values:
		//   - ""           self-leave via /leave on this or another device
		//   - "removed"    an admin kicked us via handleRemoveFromGroup.
		//                  By carries the kicking admin's user ID so we
		//                  can render "You were removed from X by alice"
		//                  instead of the generic "by an admin" fallback.
		//   - "retirement" our own account was retired (should be rare
		//                  since retirement closes sessions, but handle
		//                  defensively for the short overlap window).
		//   - "admin"      deprecated Phase 11 value from the old CLI
		//                  escape hatch. Pre-Phase-14 clients may still
		//                  see this from persisted rows during an
		//                  upgrade window; treat as equivalent to
		//                  "removed" with unknown actor.
		//
		// In all cases the client layer has already marked the group
		// archived in the local DB and dropped it from the in-memory
		// member map. Here we update the sidebar greying, set the
		// active message view to read-only if the affected group is
		// currently focused, and surface a status message.
		var m protocol.GroupLeft
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.MarkGroupLeft(m.Group)
			if a.messages.group == m.Group {
				a.messages.SetLeft(true)
			}
			groupName := m.Group
			for _, g := range a.sidebar.groups {
				if g.ID == m.Group && g.Name != "" {
					groupName = g.Name
					break
				}
			}
			switch m.Reason {
			case "removed":
				if m.By != "" {
					a.statusBar.SetError("You were removed from " + groupName + " by " + a.resolveDisplayName(m.By))
				} else {
					a.statusBar.SetError("You were removed from " + groupName + " by an admin")
				}
			case "admin":
				// Deprecated legacy value — treat as removed-by-unknown-admin.
				a.statusBar.SetError("You were removed from " + groupName + " by an admin")
			case "retirement":
				a.statusBar.SetError("Your account was retired")
			default:
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
				a.onContextSwitch()
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
					a.onContextSwitch()
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
				a.onContextSwitch()
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
					a.onContextSwitch()
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
				// Phase 20: extended event vocabulary with inline
				// system messages matching the group-side UX.
				switch m.Event {
				case "join":
					if m.By != "" && m.By != m.User {
						a.messages.AddSystemMessage(
							a.resolveDisplayName(m.By) + " added " + a.resolveDisplayName(m.User) + " to the room")
					} else {
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " joined")
					}
				case "leave":
					switch m.Reason {
					case "removed":
						a.messages.AddSystemMessage(
							a.resolveDisplayName(m.User) + " was removed from the room by an admin")
					case "user_retired":
						a.messages.AddSystemMessage(
							a.resolveDisplayName(m.User) + "'s account was retired")
					default:
						a.messages.AddSystemMessage(a.resolveDisplayName(m.User) + " left")
					}
				case "topic":
					a.messages.AddSystemMessage(
						a.resolveDisplayName(m.By) + " changed the topic to \"" + m.Name + "\"")
				case "rename":
					a.messages.AddSystemMessage(
						a.resolveDisplayName(m.By) + " renamed the room to \"" + m.Name + "\"")
				case "retire":
					a.messages.AddSystemMessage("this room was retired by an admin")
				}
			}
		}
	case "group_created":
		var m protocol.GroupCreated
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			// Phase 14: pass Admins through so the sidebar can render
			// the ★ indicator for groups where the local user is an
			// admin. On a fresh /groupcreate the creator is always
			// the initial admin.
			a.sidebar.AddGroup(protocol.GroupInfo{
				ID:      m.Group,
				Members: m.Members,
				Admins:  m.Admins,
				Name:    m.Name,
			})
		}
	case "group_added_to":
		// Phase 14: a group admin added the local user to an existing
		// group. The client layer already inserted the row in the
		// local store, populated groupMembers / groupAdmins, and
		// called MarkGroupRejoined. Here we add the group to the
		// sidebar immediately and surface a toast-style status bar
		// message + OS-level desktop notification so the user knows
		// what happened even if the client isn't focused.
		var m protocol.GroupAddedTo
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.AddGroup(protocol.GroupInfo{
				ID:      m.Group,
				Members: m.Members,
				Admins:  m.Admins,
				Name:    m.Name,
			})
			groupName := m.Name
			if groupName == "" {
				groupName = m.Group
			}
			addedBy := a.resolveDisplayName(m.AddedBy)
			a.statusBar.SetError(addedBy + " added you to '" + groupName + "'")
			// OS-level notification — fires even when the terminal
			// isn't focused. Same helper used for room/DM message
			// notifications. Title = the actor, body = the action.
			SendDesktopNotification(addedBy, "added you to group '"+groupName+"'")
		}
	case "add_group_result", "remove_group_result", "promote_admin_result", "demote_admin_result":
		// Phase 14: server ACKs for the admin verb the local user
		// just sent. The meaningful state update already arrived via
		// the broadcast (group_event{join,promote,demote} or
		// group_left for kicks), so these echoes are informational —
		// we don't need to re-render anything. They exist so the
		// client can tell "my command succeeded" vs "my command was
		// rejected" (the error frame is distinct). Intentionally
		// no-op in the TUI layer today; Chunk 5 slash commands may
		// surface a "[action] succeeded" confirmation if the UX
		// warrants it.
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
		if m.CorrID != "" && a.client != nil {
			a.client.SendQueue().Ack(m.CorrID)
		}
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
				from := a.client.DisplayName(m.From)
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
				a.onContextSwitch()
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
		// Phase 14 / Phase 20: replay admin events that happened while
		// this client was offline. Each entry routes through the same
		// handleServerMessage path as live broadcasts, so persisted
		// replay and live delivery produce identical state. The server
		// guarantees ordering via ORDER BY ts ASC, id ASC.
		//
		// Events come from either group_events (Phase 14) or room_events
		// (Phase 20, bundled with leave catchup). We peek at the "type"
		// field to route correctly — the server packs group_event and
		// room_event raw JSON into the same Events slice.
		for _, raw := range batch.Events {
			eventType, _ := protocol.TypeOf(raw)
			if eventType == "room_event" {
				a.handleServerMessage(ServerMsg{Type: "room_event", Raw: raw})
			} else {
				// Default to group_event for backward compatibility
				// with servers that don't emit room_events yet.
				a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})
			}
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

		// Phase 17c Step 5: classify via corr_id + CategoryForCode.
		// The queue handles retry state for Category A/B (keeps the
		// entry pending) and removes the entry for Category C/D so
		// the UI surfaces the error once.
		var verb string
		if m.CorrID != "" && a.client != nil {
			if entry := a.client.SendQueue().Get(m.CorrID); entry != nil {
				verb = entry.Verb
			}
			a.client.SendQueue().Error(m.CorrID, &m)
		}
		category := protocol.CategoryForCode(m.Code)

		// Phase 17c Step 5 Gap 5: A-silent enforcement for refresh
		// verbs. When rate_limited comes back from room_members or
		// list_devices (A-silent), drop the error silently — cached
		// data on screen is still valid; surfacing an error for a
		// best-effort refresh alarms the user about a non-problem.
		if m.Code == "rate_limited" && (verb == "room_members" || verb == "list_devices") {
			return nil
		}

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

		// Category D: privacy-identical rejection — server already
		// sends the generic "operation rejected" text, so surfacing
		// m.Message is equivalent. Left as a hook for future
		// category-specific UX (e.g., soft toast vs hard banner).
		_ = category

		a.statusBar.SetError(m.Message)
		// If this is a username_taken error and settings is open, show it
		// prominently so the user knows their name change failed
		if m.Code == "username_taken" || m.Code == "invalid_profile" {
			a.statusBar.SetError("Name change failed: " + m.Message)
		}
		// Phase 15 edit errors: if an edit_window_expired or
		// edit_not_most_recent error arrives while the input is in
		// edit mode, surface a friendly status bar message and exit
		// edit mode so the user isn't left in a stuck state. The
		// buffer is preserved so they can paste it into a fresh
		// message if they want.
		if m.Code == protocol.ErrEditWindowExpired && a.input.IsEditing() {
			a.statusBar.SetError("Edit window expired — delete the message and send a new one instead")
			a.input.ExitEditMode()
			a.input.ClearInput()
		}
		if m.Code == protocol.ErrEditNotMostRecent && a.input.IsEditing() {
			a.statusBar.SetError("You can only edit your most recent message in this conversation")
			a.input.ExitEditMode()
			a.input.ClearInput()
		}
		// Phase 14 last-admin picker: when ErrForbidden arrives with
		// the "last admin" message AND we have a pending /leave or
		// /delete on this group, open the inline promote picker so
		// the user can select a successor. The picker then sends
		// promote + leave/delete in sequence.
		if m.Code == "forbidden" && a.pendingLastAdminGroup != "" &&
			strings.Contains(m.Message, "last admin") {
			group := a.pendingLastAdminGroup
			triggerDelete := a.pendingLastAdminDelete
			a.pendingLastAdminGroup = ""
			a.pendingLastAdminDelete = false
			if a.client != nil {
				candidates := a.buildLastAdminCandidates(group)
				a.lastAdminPicker.Show(group, a.lookupGroupName(group), triggerDelete, candidates)
			}
			return nil
		}
		// Phase 14 stale-cache heuristic: when the server returns
		// ErrUnknownGroup for the currently-active group, the most
		// likely cause is that the local user's admin status changed
		// (demoted on another device) between the last group_list and
		// the attempted admin action. The raw "you are not a member"
		// is misleading in that case — the user IS a member, just no
		// longer an admin. Show a hint pointing at the info panel so
		// they can refresh their view. Only fires when the group is
		// still in our local store as an active (non-left) group,
		// which rules out the legitimate "I was removed" case (that
		// would have marked the group as left via group_left echo).
		if m.Code == "unknown_group" && a.messages.group != "" {
			if st := a.client.Store(); st != nil {
				if !st.IsGroupLeft(a.messages.group) {
					a.statusBar.SetError("Your admin status may have changed — try /groupinfo to refresh")
				}
			}
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
	// Phase 14 read-only overlays
	if a.auditOverlay.IsVisible() {
		return a.auditOverlay.View(a.width)
	}
	if a.membersOverlay.IsVisible() {
		return a.membersOverlay.View(a.width)
	}
	if a.lastAdminPicker.IsVisible() {
		return a.lastAdminPicker.View(a.width)
	}
	// Phase 14 in-group admin verb dialogs
	if a.addConfirm.IsVisible() {
		return a.addConfirm.View(a.width)
	}
	if a.kickConfirm.IsVisible() {
		return a.kickConfirm.View(a.width)
	}
	if a.promoteConfirm.IsVisible() {
		return a.promoteConfirm.View(a.width)
	}
	if a.demoteConfirm.IsVisible() {
		return a.demoteConfirm.View(a.width)
	}
	if a.transferConfirm.IsVisible() {
		return a.transferConfirm.View(a.width)
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
