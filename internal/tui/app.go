// Package tui implements the Bubble Tea terminal UI.
package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// undoWindowSeconds is the Phase 14 /undo window for reverting a
// kick. Per the plan: 30 seconds, no stack, only the last kick is
// trackable, any other admin action supersedes the undo target.
const undoWindowSeconds int64 = 30

// ServerMsg wraps a protocol message received from the server.
// gen carries the connection generation that produced this message, so
// updateInner can drop stale ServerMsg deliveries from a superseded
// connect attempt (server switch, reconnect race). See
// fix-cross-server-db-isolation.md.
type ServerMsg struct {
	Type string
	Raw  json.RawMessage
	gen  uint64
}

// ErrMsg wraps a connection error. gen carries the connection
// generation that produced this error so a stale connect-failed for a
// superseded attempt cannot show the connect-failed modal or schedule
// reconnect for the current attempt.
type ErrMsg struct {
	Err error
	gen uint64
}

// passphraseNeededMsg signals that the SSH key needs a passphrase.
// gen + keyPath bind the request to a specific connection attempt and
// its target key, so a stale request after a server switch cannot open
// the passphrase modal for a key the user is no longer connecting to.
type passphraseNeededMsg struct {
	gen     uint64
	keyPath string
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

	// renameInFlight + renameAttempted implement Option B confirm-then-apply
	// for the Settings display-name rename (rename-collision-ux.md). On submit
	// we set the marker and show "Saving…" instead of optimistic success; the
	// self-`profile` broadcast whose DisplayName matches renameAttempted is the
	// durable confirmation, and any empty-correlation server error while the
	// marker is set surfaces the failure in-panel. No previous-name stash —
	// the confirmed value is always DisplayName(self).
	renameInFlight  bool
	renameAttempted string

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
	unverifyConfirm UnverifyConfirmModel // §9 step 4 — the one net-new confirm in the picker effort (#8)
	// Attachment save-as dialog (post-v0.2.0 image fix follow-up)
	saveAttachment SaveAttachmentModel
	// Phase 14 read-only overlays (/audit, /admins)
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
	lastKickGroup   string
	lastKickUserID  string
	lastKickTS      int64
	deviceRevoked   DeviceRevokedModel
	newDeviceAlert  NewDeviceAlertModel
	roomAttestation RoomAttestationAlertModel
	deviceMgr       DeviceMgrModel
	quickSwitch     QuickSwitchModel
	threadPanel     ThreadPanelModel
	pinnedBar       PinnedBarModel
	roomPins        map[string][]string // room_id -> pinned message IDs (context-scoped cache)

	// Config state
	appConfig        *config.Config
	configDir        string
	serverIdx        int // index of the active server in config
	bell             BellConfig
	muted            map[string]bool // room name or conv ID -> muted
	showHelpHint     bool
	reconnectAttempt int

	width  int
	height int
	focus  Focus
	// navMode gates the Ctrl+g prefix flow. When true, the next key is
	// interpreted as a navigation verb. Nav mode no longer auto-exits — it
	// ends on a mapped key, g/esc/ctrl+g, or any other key / mouse click.
	navMode bool
	// navModePopupDelay is how long after Ctrl+g the which-key popup
	// reveals. Zero = reveal instantly.
	navModePopupDelay time.Duration
	// navModeTickGen increments on each enter; the reveal tick carries a
	// generation so a key consumed during the delay cancels a stale reveal.
	navModeTickGen int
	// navPopupVisible gates the which-key popup render. Set by the reveal
	// tick (only when no modal is up), cleared on exit.
	navPopupVisible bool
	// navPopupEnabled mirrors the nav_mode_popup config kill switch.
	navPopupEnabled bool
	// Layout is no longer cached on the App. It's a derived value of
	// (width, height, memberPanel.IsVisible) and is computed on
	// demand by mouse handlers and View via computeLayout(). The
	// previous cached `layout Layout` field was a stale-state bug —
	// View() has a value receiver so any `a.layout = Layout{...}`
	// assignment landed on a throwaway copy, leaving Update's mouse
	// handlers reading zero-valued rectangles. See computeLayout's
	// doc-comment for the full history.
	contextMenu     ContextMenuModel
	memberMenu      MemberMenuModel
	statusPicker    StatusPickerModel
	picker          PickerModel // shared single-select picker (bare-form verbs)
	passphrase      PassphraseModel
	passphraseCh    chan []byte
	passphraseCache map[string][]byte // keyPath -> passphrase

	// pendingCreateDM tracks an in-flight create_dm so the client can
	// transparently retry if the server returns server_busy (a cleanup
	// mutex was held for a microsecond by a concurrent leave_dm). Single
	// field rather than a map because the user can only kick off one
	// create_dm at a time via the newconv dialog.
	pendingCreateDM pendingCreateDMState

	// membersPanelCreateSource tracks whether the currently-open NewConv
	// dialog was launched from the member panel/menu. Used to scope the
	// auto-focus-on-created behavior to that UX path.
	membersPanelCreateSource bool

	// pendingFocusCreated* stores the expected create result (from the
	// member panel flow). When the matching dm_created/group_created
	// arrives, the app switches context to that conversation.
	pendingFocusCreatedDMOther   string
	pendingFocusCreatedGroupName string
	pendingFocusCreatedGroupMems []string
	pendingFocusCreatedSetAt     time.Time

	// replayingSyncBatch is true while the sync_batch case is replaying
	// catch-up payloads through handleServerMessage. Live-only side effects
	// (unread increments, read receipts, notifications, bell) must be
	// suppressed during replay so reconnect hydration does not look like
	// fresh incoming traffic.
	replayingSyncBatch bool

	// connGen is the monotonically-increasing connection generation.
	// Every fresh connect attempt (initial, server switch, reconnect
	// timer, retry from connect-failed, passphrase retry, refresh
	// reconnect) bumps this and captures the new value. Events emitted
	// by that attempt carry the captured gen; updateInner drops events
	// whose gen no longer matches a.connGen as the first meaningful
	// operation in each gen-scoped case.
	//
	// Seeded to 1 in New() so the initial connection's events are
	// stamped with a non-zero generation — never use 0 in production.
	// See fix-cross-server-db-isolation.md for full rationale; the
	// previous design relied on no-arg connect() and had no event
	// origin identity, letting slow stale events overwrite a.client or
	// mutate UI state for a server the user had already left.
	connGen uint64
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

// pendingFocusCreatedTTL bounds how long a members-panel create intent
// can wait for its dm_created/group_created before being discarded as
// stale, preventing late unrelated creates from stealing focus.
const pendingFocusCreatedTTL = 30 * time.Second

// doubleQuitWindow is the time window during which a second Ctrl+Q
// keypress bypasses the quit-confirmation dialog. Phase 17c Step 5
// polish: "bypass-able with repeated quit keypresses for users who
// genuinely want to abandon". 500ms — long enough for a deliberate
// double-tap, short enough that accidental double-presses from the
// existing dialog flow don't trigger it.
const doubleQuitWindow = 500 * time.Millisecond

const topicUpdatePendingStatus = "Topic update sent — pending server confirmation"

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

// navPopupRevealMsg is emitted by the tea.Tick armed in enterNavMode;
// when it fires (nav mode still active, gen matches, no modal up) the
// which-key popup becomes visible.
type navPopupRevealMsg struct {
	Gen int
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
	kb := config.LoadKeybindings(configDir)
	popupDelayMs := kb.Navigation.NavModePopupDelayMs
	if popupDelayMs < 0 {
		popupDelayMs = 0
	}

	a := App{
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
		settings:    NewSettings(),
		// AddServer's scanDirsFn captures appCfg + configDir so the
		// "Existing Ed25519 keys" list reflects the LIVE list of
		// configured servers at scan time — every dialog open / rescan
		// reads the current `appCfg.Servers`, so servers added or
		// removed during the session show up correctly without
		// rebuilding the model.
		//
		// Phase 4 hardening: skip entries where ValidateHost rejects
		// the Host. A hand-edited bad Host in config.toml (path
		// separator, traversal segment, control byte) would otherwise
		// join into a bogus keys dir. ServerKeysDir trusts its input
		// per the §"Server data dir" contract, so the validation has
		// to happen here at the closure boundary before derivation.
		addServer: NewAddServerWithConfigDir(configDir, func() []string {
			if appCfg == nil {
				return nil
			}
			dirs := make([]string, 0, len(appCfg.Servers))
			for _, srv := range appCfg.Servers {
				if config.ValidateHost(srv.Host) != nil {
					continue
				}
				dirs = append(dirs, config.ServerKeysDir(configDir, srv.Host))
			}
			return dirs
		}),
		retireConfirm:     NewRetireConfirm(),
		deviceRevoked:     NewDeviceRevoked(),
		newDeviceAlert:    NewNewDeviceAlert(),
		roomAttestation:   NewRoomAttestationAlert(),
		deviceMgr:         NewDeviceMgr(),
		saveAttachment:    NewSaveAttachment(),
		passphrase:        NewPassphrase(),
		passphraseCh:      make(chan []byte, 1),
		passphraseCache:   make(map[string][]byte),
		roomPins:          make(map[string][]string),
		appConfig:         appCfg,
		configDir:         configDir,
		serverIdx:         serverIdx,
		bell:              NewBellConfig(appCfg.Notifications),
		muted:             config.LoadMutedMap(appCfg),
		showHelpHint:      !appCfg.Notifications.HelpShown,
		focus:             FocusInput,
		navModePopupDelay: time.Duration(popupDelayMs) * time.Millisecond,
		navPopupEnabled:   kb.Navigation.NavModePopup,
		// Seed connGen to 1, not 0. Production code never uses gen 0;
		// keeping zero out of the valid range means a missing-stamp bug
		// (struct constructed without setting gen) shows up immediately
		// as a stale-drop, instead of accidentally looking current. See
		// fix-cross-server-db-isolation.md §Implementation Risks.
		connGen: 1,
	}
	// Seed the input model's typing-suppression flag from persisted
	// config so `/typing off` survives a restart. Zero value = false
	// = enabled, so a fresh or older config with no `typing_disabled`
	// key defaults to typing-on with no migration. appCfg is
	// guaranteed non-nil here (already dereferenced above for bell /
	// muted / showHelpHint).
	a.input.SetTypingDisabled(appCfg.Notifications.TypingDisabled)
	// Seed the message model's connection-generation stamp from the App's
	// seeded gen (1) so first-run history requests stamp the current gen
	// rather than the zero value and get stale-dropped against connGen == 1.
	a.messages.connGen = a.connGen
	return a
}

func (a App) Init() tea.Cmd {
	// Use the gen seeded in New() — Init() has a value receiver, so any
	// mutation here would be lost (Bubble Tea uses the returned cmd, not
	// the model). The initial connection therefore reuses the seeded
	// gen 1 rather than calling nextConnGen.
	return tea.Batch(
		a.input.Init(),
		a.connect(a.connGen),
	)
}

// nextConnGen bumps the connection generation and returns the new
// value. Call this exactly when a fresh non-initial connection attempt
// is starting (server switch, reconnect timer fire, connect-failed
// retry, passphrase retry, explicit refresh reconnect).
func (a *App) nextConnGen() uint64 {
	a.connGen++
	// Keep the message model's stamp in sync so history requests created
	// after this bump carry the current generation (Outgoing Request Guard).
	a.messages.connGen = a.connGen
	return a.connGen
}

// sendServerHistory generates a fresh corr_id, records it as the active visible
// history request, and sends the server history request under it. The corr_id
// lets the inbound history_result guard pin the result to this exact request
// (Incoming Result Guard, history-state-model.md).
func (a *App) sendServerHistory(room, group, dm, before string) {
	corrID := protocol.GenerateCorrID()
	a.messages.activeHistoryCorrID = corrID
	if err := a.client.RequestHistoryWithID(corrID, room, group, dm, before, historyPageLimit); err != nil {
		// Send failed (encode error / closed conn): the queue entry was
		// dropped internally; clear the visible load so the pane doesn't stay
		// on "loading history" forever.
		a.messages.abortHistoryRequest()
	}
}

// startConnect bumps the generation and returns a connect command
// stamped with the new generation. Every non-initial connection
// attempt should go through this helper; direct `connect(gen)` calls
// from production code are reserved for the initial Init() path. The
// compiler-enforced gen argument on connect makes it hard to bypass:
// future call sites either reach for startConnect or have to pass an
// explicit gen, which is a readable signal that something connection-
// scoped is happening.
func (a *App) startConnect() tea.Cmd {
	gen := a.nextConnGen()
	return a.connect(gen)
}

func (a *App) openQuickSwitch() {
	a.quickSwitch.Show(a.sidebar.rooms, a.sidebar.groups, a.sidebar.dms, a.sidebar.resolveName, a.sidebar.resolveRoomName, a.sidebar.resolveDMName)
}

func (a *App) openNewConversation() {
	if a.client == nil {
		return
	}
	var allMembers []string
	// Collect all known users except self, skipping retired accounts.
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
	a.membersPanelCreateSource = false
}

// commandAllowedInReadOnlyRoom reports whether the input text is one of the
// commands permitted in a retired/left room. Allow-list: /delete, /search,
// /help, /?. Normalization: trim leading/trailing whitespace, match only the
// first token (the verb), case-insensitively. So "/DELETE", "  /delete", and
// "/delete confirm" all pass (arg parsing is the command handler's job);
// "/deletex" and "/foo" do not. Normal (non-slash) text is never allowed.
func commandAllowedInReadOnlyRoom(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	verb := strings.ToLower(strings.SplitN(trimmed, " ", 2)[0])
	switch verb {
	case "/delete", "/search", "/help", "/?":
		return true
	default:
		return false
	}
}

func (a *App) toggleMemberPanel() {
	a.memberPanel.Toggle()
	if a.memberPanel.IsVisible() {
		// V8: Refresh renders room members from the local cache; no fetch.
		a.memberPanel.Refresh(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
		a.input.SetMembers(a.activeMemberEntries())
		a.input.SetNonMembers(a.activeNonMemberEntries())
	}
	// Toggling the member panel changes the messages-pane width
	// (member column takes 18 columns when shown). The viewport
	// content is wrapped to that width — re-wrap so it doesn't
	// overflow or under-fill the new pane.
	a.refreshMessageContent()
}

func (a *App) openSettingsPanel() {
	displayName := ""
	if a.client != nil {
		// Resolve nanoid → human display name. Settings shows
		// this in the "Display name" row; rendering the raw
		// nanoid was a bug (it's the internal user ID, not
		// what the user typed as their display name).
		displayName = a.client.DisplayName(a.client.UserID())
	}
	a.settings.Show(a.appConfig, a.configDir, displayName, a.serverIdx)
	a.settings.SetDisplayNameRenamePending(a.renameInFlight)
}

func (a *App) switchServerByIndex(idx int) tea.Cmd {
	return a.switchServerByIndexInternal(idx, false)
}

func (a *App) forceSwitchServerByIndex(idx int) tea.Cmd {
	return a.switchServerByIndexInternal(idx, true)
}

func (a *App) switchServerByIndexInternal(idx int, force bool) tea.Cmd {
	// Dismiss the Add Server overlay if a switch was initiated from the
	// ring slot. This sits ABOVE the early-return (unlike connectFailed.Hide
	// / passphrase.Hide below, which are after it) on purpose: re-selecting
	// the current server from the Add Server slot — single-server `Ctrl+g l`,
	// or `Ctrl+g <current digit>` — is a no-op switch that must still reveal
	// the server underneath. No-op when the wizard isn't open.
	a.addServer.Hide()
	if a.appConfig == nil || idx < 0 || idx >= len(a.appConfig.Servers) || (!force && idx == a.serverIdx) {
		return nil
	}

	srv := a.appConfig.Servers[idx]

	// Validate host BEFORE closing the current client — defense against
	// hand-edited config.toml. Previously the close ran first, so a
	// hand-edited invalid host would disconnect the user from the
	// working server, then refuse the switch, stranding them with no
	// connection at all. Validate-then-close means an invalid target
	// surfaces a status-bar error and leaves the current session
	// untouched.
	if err := config.ValidateHost(srv.Host); err != nil {
		a.statusBar.SetError("Cannot switch to " + srv.Name + ": " + err.Error())
		return nil
	}

	// Target is valid — disconnect the current server.
	if a.client != nil {
		a.abandonActiveHistoryRequest()
		a.client.Close()
	}

	a.serverIdx = idx
	a.connected = false
	a.reconnectAttempt = 0

	// Update config for new server.
	a.cfg.Host = srv.Host
	a.cfg.Port = srv.Port
	// Derive KeyPath from the per-server canonical location.
	// ServerConfig no longer carries a Key field (Phase 3e
	// deletion) — every server's key lives at
	// <configDir>/<host>/keys/id_ed25519, populated by the wizard
	// or Add Server flow before this code runs.
	a.cfg.KeyPath = config.ServerKeyPath(a.configDir, srv.Host)
	a.cfg.DataDir = config.ServerDataDirForHost(a.configDir, srv.Host)
	// Carry the per-server requested display-name hint as the SSH username
	// so a runtime switch (incl. switching to a freshly Added server) dials
	// with the right pre-approval name. Empty = no hint.
	a.cfg.User = srv.RequestedDisplayName

	// Clear UI state.
	a.switchMessageContext("", "", "")
	a.sidebar.SetRooms(nil)
	a.sidebar.SetGroups(nil)
	a.pinnedBar = PinnedBarModel{}
	// Abandon any in-flight display-name rename: it targeted the prior server,
	// and its confirmation/error will never arrive on the new connection. Clear
	// the marker so it can't be falsely confirmed or leave a stuck "Saving…".
	a.renameInFlight = false
	a.renameAttempted = ""
	if a.settings.IsVisible() {
		a.settings.SetDisplayNameRenamePending(false)
	}
	// Dismiss the connection-failed overlay if it was up: a switch
	// initiated FROM that modal (the escape hatch — see
	// fix-server-switching.md) must not leave the old modal painted
	// over the new server's connect. No-op when not visible, so the
	// normal connected→switch path is unaffected.
	a.connectFailed.Hide()
	// Dismiss the passphrase modal if it was up — it was bound to
	// the prior server's gen+keyPath, and even if the user typed in
	// it now the result would be stale-dropped. Hiding ensures the
	// new server's potential passphraseNeededMsg can re-Show cleanly
	// without an obviously-wrong dialog from the prior server
	// flashing through. See fix-cross-server-db-isolation.md
	// §"Passphrase result ownership".
	a.passphrase.Hide()
	a.statusBar.SetError("Switching to " + srv.Name + "...")
	a.statusBar.SetConnected(false)
	a.updateTitle()

	// Connect to new server. startConnect bumps connGen so any
	// late-arriving events from the prior server's connection are
	// dropped as stale by updateInner's per-case gen-checks. See
	// fix-cross-server-db-isolation.md.
	return a.startConnect()
}

func (a *App) enterNavMode() tea.Cmd {
	a.navMode = true
	a.navModeTickGen++
	a.statusBar.SetNavigationMode(true)
	// No popup when the kill switch is off, or when a modal owns the screen
	// (the popup is a bare-chat affordance — decision 6). Nav keys still
	// dispatch in both cases; there's just nothing to reveal.
	if !a.navPopupEnabled || a.anyModalVisible() {
		return nil
	}
	if a.navModePopupDelay <= 0 {
		a.navPopupVisible = true
		return nil
	}
	gen := a.navModeTickGen
	return tea.Tick(a.navModePopupDelay, func(time.Time) tea.Msg {
		return navPopupRevealMsg{Gen: gen}
	})
}

func (a *App) exitNavMode() {
	a.navMode = false
	a.navPopupVisible = false
	a.statusBar.SetNavigationMode(false)
}

// openDeviceManager shows the registered-devices panel and requests a fresh
// device list. Shared by the Settings "manage devices" action and Ctrl+g d.
// The list fetch is gated on a live connection: SendListDevices writes to the
// client's encoder, which is nil while disconnected, so fetching without the
// connected guard would panic (e.g. Ctrl+g d pressed mid-reconnect). When
// offline the panel still opens, just without a refresh.
func (a *App) openDeviceManager() {
	a.deviceMgr.Show()
	if a.client != nil && a.connected {
		a.client.SendListDevices()
	}
}

// handleNavModeKey handles the second key in Ctrl+g nav mode.
// Returns handled=true when the key was consumed by nav-mode logic.
// Unrecognized keys return handled=false after exiting nav mode so
// the key can fall through to normal panel handlers.
func (a *App) handleNavModeKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "g", "esc", "ctrl+g":
		a.exitNavMode()
		return nil, true
	case "k":
		a.exitNavMode()
		a.openQuickSwitch()
		return nil, true
	case "n":
		a.exitNavMode()
		a.openNewConversation()
		return nil, true
	case "m":
		a.exitNavMode()
		a.toggleMemberPanel()
		return nil, true
	case "i":
		a.exitNavMode()
		a.showInfoPanelForContext(a.messages.room, a.messages.group, a.messages.dm)
		return nil, true
	case "s":
		a.exitNavMode()
		a.openSettingsPanel()
		return nil, true
	case "d":
		a.exitNavMode()
		a.openDeviceManager()
		return nil, true
	case "p":
		a.exitNavMode()
		if a.client != nil {
			a.whoisReadout(a.client.UserID())
		}
		return nil, true
	case "/":
		a.exitNavMode()
		a.search.Show()
		return nil, true
	case "h":
		a.exitNavMode()
		return a.cycleServer(-1), true
	case "l":
		a.exitNavMode()
		return a.cycleServer(1), true
	case "j":
		a.exitNavMode()
		a.openServerSwitcher()
		return nil, true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		a.exitNavMode()
		idx := int(key[0]-'0') - 1
		// A digit past the configured server count is the explicit
		// "I tried to reach a server slot that does not exist" path:
		// open the add-server wizard instead of the old silent no-op.
		if a.appConfig == nil || idx >= len(a.appConfig.Servers) {
			a.openAddServerFromNav()
			return nil, true
		}
		return a.switchServerByIndex(idx), true
	default:
		// Unrecognized key in nav mode: dismiss the popup and swallow the
		// key (strict which-key — it does NOT fall through to typing).
		a.exitNavMode()
		return nil, true
	}
}

// openAddServerFromNav opens the add-server wizard from any nav-origin
// path. It hides the connect-failed overlay first so the wizard is not
// painted behind the failure screen (View renders connectFailed before
// normal overlays). No-op hide when the overlay isn't visible.
func (a *App) openAddServerFromNav() {
	a.connectFailed.Hide()
	a.addServer.Show()
}

// cycleServer moves through the server navigation ring by delta (+1 next,
// -1 previous). The ring is: Server 1 → … → Server N → Add Server → Server 1.
// Returns the connect tea.Cmd when the move lands on a real server, or nil
// when it opens the Add Server slot.
func (a *App) cycleServer(delta int) tea.Cmd {
	if a.appConfig == nil || len(a.appConfig.Servers) == 0 {
		// No configured targets: any ring move opens the wizard.
		a.openAddServerFromNav()
		return nil
	}
	n := len(a.appConfig.Servers)

	// Add Server slot: when the wizard is the active ring slot, h/l are
	// slot-relative — forward wraps to the first server, backward to the
	// last — NOT serverIdx±1, because serverIdx still points at the
	// underlying server. switchServerByIndex hides the wizard (top-hide),
	// so a no-op re-selection (single server, or the current index) still
	// reveals the server underneath.
	if a.addServer.IsVisible() {
		if delta < 0 {
			return a.switchServerByIndex(n - 1)
		}
		return a.switchServerByIndex(0)
	}

	// Ephemeral CLI mode: the current server is not in the configured list,
	// but there are real configured targets — use the list edges rather than
	// opening the wizard.
	if a.serverIdx < 0 || a.serverIdx >= n {
		if delta < 0 {
			return a.switchServerByIndex(n - 1)
		}
		return a.switchServerByIndex(0)
	}

	// Add Server is the extra ring slot after the last configured server
	// and before the first configured server.
	if delta > 0 && a.serverIdx == n-1 {
		a.openAddServerFromNav()
		return nil
	}
	if delta < 0 && a.serverIdx == 0 {
		a.openAddServerFromNav()
		return nil
	}

	return a.switchServerByIndex(a.serverIdx + delta)
}

// serverPickerAddID is the sentinel PickerItem ID for the "[Add server]"
// row in the server quick-switch picker. The \x00 prefix guarantees it can
// never collide with a strconv.Itoa(serverIndex) value, and strconv.Atoi
// on it fails (handled by the explicit equality check in the verb branch).
const serverPickerAddID = "\x00add"

// openServerSwitcher opens the shared picker as a server quick-switch
// overlay: one row per configured server (the current one marked) plus a
// trailing [Add server] row. Falls back to the wizard when no servers are
// configured. Builds items and calls picker.Show directly (NOT via
// openPicker/pickerCandidates, which are user/group candidate builders).
func (a *App) openServerSwitcher() {
	if a.appConfig == nil || len(a.appConfig.Servers) == 0 {
		a.openAddServerFromNav()
		return
	}
	// Clear overlays that must not paint over the picker: connectFailed
	// (escape-hatch origin) and addServer (the `j`-from-Add-Server path,
	// which doesn't switch, so it can't rely on switchServerByIndex's hide).
	a.connectFailed.Hide()
	a.addServer.Hide()
	items := make([]PickerItem, 0, len(a.appConfig.Servers)+1)
	for i, srv := range a.appConfig.Servers {
		secondary := fmt.Sprintf("%s:%d", srv.Host, srv.Port)
		if i == a.serverIdx {
			secondary += "  (current)"
		}
		items = append(items, PickerItem{
			ID:        strconv.Itoa(i),
			Primary:   srv.Name,
			Secondary: secondary,
			Search:    []string{srv.Host},
		})
	}
	items = append(items, PickerItem{ID: serverPickerAddID, Primary: "[Add server]"})
	a.picker.Show(PickerRequest{Verb: "switch_server", ShowFilter: true}, items)
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
	gen            uint64
}

// connect starts the SSH connection in a goroutine. gen is the
// connection generation that the resulting events should be stamped
// with — callers pass the value returned by startConnect (or, for
// the initial connect, the seeded a.connGen from Init). Every
// possible return (connectedWithClient, ErrMsg, passphraseNeededMsg)
// and every callback-driven event (ServerMsg, KeyChangeEvent,
// AttachmentReadyEvent, RoomUpdatedEvent) is stamped with this gen
// so updateInner can drop stale arrivals after a server switch or
// reconnect race.
//
// The signature deliberately requires gen explicitly: no no-arg
// connect() exists post-fix, so a future call site either uses
// startConnect or has to pass a known generation. That removes the
// implicit "bump-then-call" foot-gun where the bump happens after
// connect captures its argument and the new connection's events
// carry the prior (now-stale) generation. See
// fix-cross-server-db-isolation.md §"Connect command" and §"Bump-
// order tests".
func (a App) connect(gen uint64) tea.Cmd {
	return func() tea.Msg {
		msgCh := make(chan ServerMsg, 100)
		errCh := make(chan error, 1)
		// Buffered so a burst of profile broadcasts during catchup
		// can't block the client readLoop on a slow TUI. 10 events
		// is comfortably above any realistic burst (one key-change
		// event per user per session is the upper bound).
		keyWarnCh := make(chan KeyChangeEvent, 10)
		// Buffered to cover image-attachment bursts during catchup —
		// a chatty room with many recent images could fire a dozen
		// auto-preview completions in quick succession. 100 matches
		// msgCh's buffer.
		attachReadyCh := make(chan AttachmentReadyEvent, 100)
		// Buffered for upload-result events. A handful at most in flight
		// (rare concurrent uploads); 16 is comfortably ample.
		uploadResultCh := make(chan UploadResultEvent, 16)
		// Buffered for o / p download-action result events. Same low
		// in-flight ceiling as uploads.
		downloadResultCh := make(chan DownloadResultEvent, 16)
		// Buffered for save-as copy result events. Single-digit
		// in-flight ceiling expected — user clicks "Save" then
		// generally waits; 16 is more than ample headroom.
		saveResultCh := make(chan SaveResultEvent, 16)
		// Buffered for room_updated callbacks from the client layer.
		// Mirrors msgCh's buffer size so bursts of room updates (for
		// example reconnect catchup) don't block readLoop.
		roomUpdatedCh := make(chan RoomUpdatedEvent, 100)

		cfg := a.cfg
		cfg.OnMessage = func(msgType string, raw json.RawMessage) {
			msgCh <- ServerMsg{Type: msgType, Raw: raw, gen: gen}
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
			case keyWarnCh <- KeyChangeEvent{User: user, OldFingerprint: oldFP, NewFingerprint: newFP, gen: gen}:
			default:
			}
		}
		cfg.OnAttachmentReady = func(fileID string) {
			// Non-blocking send — a burst of images arriving during
			// catchup shouldn't stall the auto-preview goroutine on a
			// slow TUI. Buffer is large enough to cover typical bursts;
			// if full, drop the nudge (worst case: the user sees the
			// 🖼 placeholder until the next keypress triggers a repaint,
			// at which point the render path's os.Stat picks up the
			// cache file anyway).
			select {
			case attachReadyCh <- AttachmentReadyEvent{FileID: fileID, gen: gen}:
			default:
			}
		}
		cfg.OnRoomUpdated = func(room string) {
			select {
			case roomUpdatedCh <- RoomUpdatedEvent{Room: room, gen: gen}:
			default:
			}
		}
		// Passphrase callback — return cached passphrase for this key if available,
		// otherwise signal the TUI to show the dialog.
		keyPath := cfg.KeyPath
		cached := a.passphraseCache[keyPath]
		passCh := a.passphraseCh

		// Pre-flight encryption check. If the key is passphrase-encrypted
		// AND we have no cached passphrase, dispatch passphraseNeededMsg
		// BEFORE attempting Connect(). Without this, loadSSHKey blocks
		// inside cfg.OnPassphrase's channel receive (parked on <-passCh)
		// with no one to send a passphrase — because the dialog trigger
		// (the strings.Contains check below) is only reachable AFTER
		// Connect() returns, and Connect() never returns because it's
		// parked in OnPassphrase. Deadlock. See passphrase-prompt-fix.md
		// for the full trace.
		//
		// On the user-typed-passphrase retry, cached is populated, the
		// pre-flight is skipped, and OnPassphrase returns the cached
		// bytes synchronously (no channel block).
		if len(cached) == 0 {
			needs, err := client.KeyNeedsPassphrase(keyPath)
			if err != nil {
				return ErrMsg{Err: err, gen: gen}
			}
			if needs {
				return passphraseNeededMsg{gen: gen, keyPath: keyPath}
			}
		}

		cfg.OnPassphrase = func() ([]byte, error) {
			if len(cached) > 0 {
				return cached, nil
			}
			return <-passCh, nil
		}

		c := client.New(cfg)
		if err := c.Connect(); err != nil {
			// Defensive fallback. Under the current loadSSHKey contract
			// this branch is unreachable (the pre-flight above catches
			// the encrypted-key case first; loadSSHKey then either
			// succeeds via cached bytes or fails with a non-passphrase
			// error). Retained as defence-in-depth so a regression in
			// the pre-flight detection wouldn't silently re-introduce
			// the original hang — Connect() failing with a passphrase
			// error here would still surface the dialog.
			errStr := err.Error()
			if strings.Contains(errStr, "passphrase") {
				return passphraseNeededMsg{gen: gen, keyPath: keyPath}
			}
			return ErrMsg{Err: err, gen: gen}
		}

		// The connected event is what drives the App's first
		// waitForMsg cmd; once Update processes it, waitForMsg becomes
		// the sole consumer of msgCh / errCh / keyWarnCh / attachReadyCh.
		// A secondary discarder goroutine used to live here — its
		// comment read "Forward to tea program (set externally)",
		// marking unfinished plumbing that was never completed.
		// With both it and waitForMsg blocked on receive from the same
		// buffered channels, Go's scheduler picked arbitrarily: ~50%
		// of incoming protocol events were silently eaten by the
		// discarder instead of reaching Update. Deleted 2026-04-25
		// after the audit caught the race (reproduction in
		// discarder_race_test.go); see CHANGELOG for the full
		// user-visible impact.
		return connectedWithClient{
			client:           c,
			msgCh:            msgCh,
			errCh:            errCh,
			keyWarnCh:        keyWarnCh,
			attachReadyCh:    attachReadyCh,
			uploadResultCh:   uploadResultCh,
			downloadResultCh: downloadResultCh,
			saveResultCh:     saveResultCh,
			roomUpdatedCh:    roomUpdatedCh,
			gen:              gen,
		}
	}
}

type connectedWithClient struct {
	client           *client.Client
	msgCh            chan ServerMsg
	errCh            chan error
	keyWarnCh        chan KeyChangeEvent
	attachReadyCh    chan AttachmentReadyEvent
	uploadResultCh   chan UploadResultEvent
	downloadResultCh chan DownloadResultEvent
	saveResultCh     chan SaveResultEvent
	roomUpdatedCh    chan RoomUpdatedEvent
	// gen identifies which connect attempt produced this client. The
	// `case connectedWithClient` in updateInner drops stale arrivals
	// AND closes the stale client first — leaving a slow old connect
	// to silently win against a newer one would leave a live read
	// loop, SSH connection, store handle, and keepalive goroutine
	// behind. See fix-cross-server-db-isolation.md §"Connect command".
	gen uint64
}

// saveAttachmentOpenMsg flips the save-as modal open after a
// download completes on a tea.Cmd goroutine. Carries the bits the
// modal needs: the local plaintext cache path (copy source), the
// sender-supplied filename (sanitized, for display + rename
// suggestion), and the pre-filled default destination.
type saveAttachmentOpenMsg struct {
	SourcePath     string
	AttachmentName string
	DefaultPath    string
}

// saveAttachmentDownloadFailedMsg surfaces a download error when the
// tea.Cmd goroutine failed to fetch the attachment. The modal never
// opens in this path.
type saveAttachmentDownloadFailedMsg struct {
	Err error
}

// AttachmentReadyEvent is the tea.Msg form of a client-layer
// OnAttachmentReady callback — signals that an auto-preview image
// download has completed and the file is now cached on disk. The TUI
// uses this purely as a re-render trigger; the render path derives
// LocalPath from the cache on every paint, so no model state has to
// mutate — returning from Update with no cmd is sufficient to make
// View pick up the new file.
type AttachmentReadyEvent struct {
	FileID string
	gen    uint64
}

// RoomUpdatedEvent is the tea.Msg form of a client-layer
// OnRoomUpdated callback. Carries the room nanoid whose local
// name/topic row was just updated by a room_updated envelope.
type RoomUpdatedEvent struct {
	Room string
	gen  uint64
}

// UploadResultEvent is the tea.Msg form of an /upload completion
// (or failure). Pushed by the upload goroutine via uploadResultCh
// and consumed in updateInner to surface user-visible status-bar
// feedback.
//
// Why a channel instead of mutating statusBar directly from the
// goroutine: handleSlashCommand has pointer-receiver semantics, so
// the goroutine captures `a *App` — a pointer to updateInner's
// local App copy. Once Update returns, that local copy is no longer
// the canonical model (bubbletea takes the returned value), and
// goroutine writes through the stale pointer don't reach the
// rendered state. Pre-fix symptom: oversized-file uploads (and any
// other server-side rejection that returns upload_error) failed
// silently with no status-bar feedback — the goroutine's
// "Upload failed: ..." SetError call landed on an orphaned App
// copy. Routing through tea.Msg + updateInner mutates the canonical
// model in the normal way.
type UploadResultEvent struct {
	Name string
	Err  error
	gen  uint64
}

// DownloadResultEvent is the tea.Msg form of an attachment-download
// action completion (or failure). Covers both `o = open` and `p =
// preview` paths — Action discriminates between them so updateInner
// can format the status bar appropriately. Same goroutine-vs-
// stale-pointer rationale as UploadResultEvent above; pre-fix path
// mutated statusBar directly from the MessageAction goroutine and
// the writes landed on the orphaned local App copy.
//
// Action values:
//   - "open":    `o` press → status only on failure (success opens
//     the file externally; the OS app surfaces success visibly).
//   - "preview": `p` press → status on both outcomes ("Preview
//     ready: name" on success, "Preview failed: err" on failure).
type DownloadResultEvent struct {
	Action string
	Name   string
	Err    error
	gen    uint64
}

// SaveResultEvent is the tea.Msg form of the save-as copy step
// (SaveFileAs) completing. Pushed by the SaveAttachmentDoMsg
// goroutine after the file copy from local cache to user-chosen
// destination finishes (or fails). Same goroutine-vs-stale-pointer
// rationale as the events above — pre-fix path mutated statusBar
// directly from the goroutine and the writes landed on the
// orphaned local App copy.
//
// Separate from DownloadResultEvent because the work is a local
// file copy (not a download), the success-string semantics differ
// ("Saved: <path>" rather than "Preview ready: <name>"), and the
// timing windows differ enough that mixing them under one Action
// discriminator would muddy the doc.
type SaveResultEvent struct {
	Dest string
	Err  error
	gen  uint64
}

// previewRenderReadyMsg is delivered by renderPreviewCmd when an
// off-thread preview-pane render completes. updateInner consumes
// it and, if the carried key still matches the sidebar's current
// previewRenderKey, stores the rendered escape on the model for
// the next View to pick up. Stale results (key mismatch — the
// user navigated away before the Cmd completed) are dropped:
// single-slot semantics, no queue, no cancellation primitives.
type previewRenderReadyMsg struct {
	key   previewRenderKey
	value string
}

// renderPreviewCmd dispatches RenderImageInline on a goroutine so
// View() never blocks on decode/encode work. The Cmd's body does
// the cache lookup + decode + encode + lazy-thumbnail-goroutine
// dance that RenderImageInline already encapsulates; we just move
// that work off the View thread.
//
// Result delivered as a previewRenderReadyMsg with the requesting
// key, so updateInner can verify relevance before landing the
// value on the model.
func renderPreviewCmd(key previewRenderKey) tea.Cmd {
	return func() tea.Msg {
		return previewRenderReadyMsg{
			key:   key,
			value: RenderImageInline(key.path, key.maxCols, key.maxRows),
		}
	}
}

// waitForMsg returns a cmd that waits for the next server message.
// Selects across eight channels: server-message events, errors,
// client-layer key-change warnings (Phase 21 F3.a), attachment
// auto-preview completions, upload-result feedback, download-action
// result feedback (o / p), save-as copy result feedback, and
// room-updated callbacks. All eight surface via separate channels rather than riding on the
// ServerMsg envelope because they are TUI-synthetic events, not
// protocol frames.
//
// gen is the connection generation that armed this wait. Events
// produced via the channels are already stamped with their producing
// generation by connect's callbacks (or by the goroutine that wrote
// to the result channel). Synthetic events created HERE — the error
// wrapper for errCh and the disconnected wrapper for done — are
// stamped with gen so updateInner can drop them if the user has
// since switched servers or kicked off a new connect.
func waitForMsg(gen uint64, msgCh chan ServerMsg, errCh chan error, keyWarnCh chan KeyChangeEvent, attachReadyCh chan AttachmentReadyEvent, uploadResultCh chan UploadResultEvent, downloadResultCh chan DownloadResultEvent, saveResultCh chan SaveResultEvent, roomUpdatedCh chan RoomUpdatedEvent, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-msgCh:
			return msg
		case err := <-errCh:
			return ErrMsg{Err: err, gen: gen}
		case kw := <-keyWarnCh:
			return kw
		case ar := <-attachReadyCh:
			return ar
		case ur := <-uploadResultCh:
			return ur
		case dr := <-downloadResultCh:
			return dr
		case sr := <-saveResultCh:
			return sr
		case ru := <-roomUpdatedCh:
			return ru
		case <-done:
			return ErrMsg{Err: fmt.Errorf("disconnected"), gen: gen}
		}
	}
}

// Update is the bubbletea entry point. It runs the actual message
// dispatch in updateInner, then applies post-update derived state
// (currently: the sidebar preview path) on the way out so mutations
// persist into the next frame.
//
// Why the wrapper exists: pre-2026-05-08 the sidebar preview path was
// derived inside View() each frame. View is value-receiver, so the
// SetPreviewImagePath mutation only persisted within the single
// View call — `sidebar.previewImagePath` on the App struct itself
// was never updated. Update IS value-receiver too, but bubbletea
// takes its return value as the new model — so mutations inside
// updateInner DO persist via the returned `a`. Centralizing the
// SetPreviewImagePath call here (after updateInner has finished
// modifying focus / cursor / modal state) catches every event that
// could change the derived path, without having to thread the call
// through every individual `return a, cmd` site inside the dispatch
// switch.
//
// Rasterm clear flag handling lived here in an earlier design but
// has been removed — the kitty delete escape is emitted stateless-ly
// inside buildPreviewContent / App.View based on whether the
// rendered output carries a kitty placement. See those functions'
// comments for the rationale.
//
// All returns from updateInner that yield an App model flow through
// this wrapper; non-App returns (rare; if a future refactor swaps in
// a different model type) pass through untouched.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Keep persistent message-viewport geometry in sync for Update-time
	// scroll logic. View is value-receiver, so render-time size writes do
	// not persist between events.
	a.syncMessageViewportState()

	model, cmd := a.updateInner(msg)
	if app, ok := model.(App); ok {
		app.syncMessageViewportState()
		app.sidebar.SetPreviewImagePath(app.computePreviewPath())
		// Async preview render: dispatch a tea.Cmd if the desired
		// render key changed and the cache doesn't already have it.
		// View() never decodes — it reads the pre-rendered escape
		// from sidebar.previewRenderValue, populated either
		// synchronously (cache hit) inside RequestPreviewRender or
		// asynchronously via the Cmd's previewRenderReadyMsg.
		if w, r := app.sidebarPreviewDims(); w > 0 && r > 0 {
			if pcmd := app.sidebar.RequestPreviewRender(w, r); pcmd != nil {
				cmd = tea.Batch(cmd, pcmd)
			}
		}
		return app, cmd
	}
	return model, cmd
}

// sidebarPreviewDims returns the (width, rows) the sidebar preview
// pane will be rendered at on this frame. Mirrors the dimension
// math in viewBody → sidebar.View → sidebarSectionHeights so
// Update-side render-dispatch keys align with the dims the View
// path actually uses. Pure: same width / height / memberPanel
// visibility produces the same answer.
//
// Layout chain: outer height minus statusBar (1) and sidebar
// border (2) gives the sidebar's inner content height. Split via
// sidebarSectionHeights into list + preview sections; preview gets
// `previewSection - 1` after the divider row. Sidebar inner width
// is sidebarWidth - 2 (left + right borders).
//
// Returns (0, 0) before initial WindowSizeMsg lands — App.Update
// guards against this with `w > 0 && r > 0` so no Cmd is dispatched
// for a zero-sized pane.
func (a App) sidebarPreviewDims() (width, rows int) {
	if a.width == 0 || a.height == 0 {
		return 0, 0
	}
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	sidebarWidth := layout.SidebarWidth
	const statusBarHeight = 1
	sidebarInnerHeight := a.height - statusBarHeight - 2
	if sidebarInnerHeight < 1 {
		return 0, 0
	}
	contentWidth := sidebarWidth - 2
	if contentWidth < 1 {
		return 0, 0
	}
	_, previewSection := sidebarSectionHeights(sidebarInnerHeight)
	previewRows := previewSection - 1 // -1 for the divider row
	if previewRows < 1 {
		return 0, 0
	}
	return contentWidth, previewRows
}

// computePreviewPath returns the image path that should be rendered
// in the sidebar's preview pane this frame.
//
// Two clearing triggers, both about avoiding visual conflict between
// the rasterm graphics layer and other UI:
//
//   - Modal visible: a full-screen modal would render text cells
//     where the rasterm placement sits. Clearing keeps the modal
//     legible (kitty graphics don't get overwritten by text repaints
//     on their own).
//
//   - Nav popup visible: the Ctrl+g which-key popup is intentionally
//     not a modal, but it still overlays text cells near the preview
//     pane. Suppress rasterm while the popup is on-screen so the
//     graphics layer cannot bleed through/around the hint.
//
//   - Cursor not on an image attachment in the messages pane: the
//     "selection" is gone, no image to preview.
//
// Notably absent: a focus check. Earlier iterations also cleared
// when focus moved off `FocusMessages` to the input bar — but that
// punished the common flow of "see an image attachment, click the
// input to type a reply about it." The image stays visible until
// the user explicitly moves the cursor away from it (in messages
// pane) or opens a modal that would overlap the preview.
func (a App) computePreviewPath() string {
	if a.anyModalVisible() || a.navPopupVisible {
		return ""
	}
	return a.messages.SelectedImagePath()
}

// syncMessageViewportState mirrors the messages-panel geometry math used in
// View() and applies it to the persistent model so keyboard/mouse scrolling
// in Update() uses accurate viewport dimensions.
func (a *App) syncMessageViewportState() {
	if a.width == 0 || a.height == 0 {
		return
	}

	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	mainWidth := layout.MessagesWidth
	mainHeight := layout.MessagesY1 - 2
	if mainWidth < 20 {
		mainWidth = 20
	}
	if mainHeight < 5 {
		mainHeight = 5
	}

	bannerRows := a.input.BannerRows()
	msgHeight := mainHeight - bannerRows
	if a.showHelpHint {
		msgHeight--
	}
	if msgHeight < 1 {
		msgHeight = 1
	}

	// Keep pinned-bar rows current for hit-testing and viewport-height math.
	a.messages.SetPinnedBar(a.pinnedBar.View(mainWidth - 2))
	a.messages.SyncViewportLayoutForPanel(mainWidth, msgHeight)
}

func (a App) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
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

		// Ctrl+C is the universal panic-button: ALWAYS quit, regardless
		// of which modal/overlay has focus. Pre-2026-05-05 the Ctrl+C
		// handler lived inside the bottom-of-Update key switch, AFTER
		// every modal-IsVisible() intercept. Modals that didn't include
		// a "ctrl+c" branch in their own Update (e.g. ConnectFailedModel
		// — only handles r/c/q/esc) silently dropped the keypress, and
		// the user reported the app couldn't be quit on connection-error.
		// Lifted to the very top of the dispatcher so it can never be
		// swallowed.
		if msg.Type == tea.KeyCtrlC {
			if a.client != nil {
				a.abandonActiveHistoryRequest()
				a.client.Close()
			}
			return a, tea.Quit
		}

		// Connection failed overlay (first-run)
		if a.connectFailed.IsVisible() {
			// Escape hatch: a failed/unreachable server must not be
			// a dead end. Let the Ctrl+g nav prefix and a
			// server-switch digit (or the nav-cancel keys) through
			// so the user can switch to another configured server
			// instead of being stuck on retry-or-quit. Mirrors the
			// Ctrl+C-above-modal escape hatch above. The other nav
			// actions (k/n/m/i/s//) operate on a connected
			// session's state and are intentionally NOT honored
			// here. See fix-server-switching.md.
			if msg.String() == "ctrl+g" {
				return a, a.enterNavMode()
			}
			if a.navMode {
				switch msg.String() {
				case "h", "l", "j",
					"1", "2", "3", "4", "5", "6", "7", "8", "9",
					"g", "esc", "ctrl+g":
					if cmd, handled := a.handleNavModeKey(msg); handled {
						return a, cmd
					}
				default:
					// Non-server nav key from the failure screen:
					// cancel the prefix and stay on the modal (no
					// empty/irrelevant panels).
					a.exitNavMode()
				}
			}
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

		// Member menu intercepts all keys. Esc is handled here at the
		// App level (not delegated to the menu's Update) — defensive
		// against any subtle routing or value-receiver issue that
		// would otherwise leave the menu stuck open. Hide is a
		// pointer-receiver method on an addressable field, so the
		// mutation propagates back via the App.Update return value.
		if a.memberMenu.IsVisible() {
			if msg.Type == tea.KeyEsc {
				a.memberMenu.Hide()
				return a, nil
			}
			var cmd tea.Cmd
			a.memberMenu, cmd = a.memberMenu.Update(msg)
			return a, cmd
		}

		// Status picker intercepts all keys when visible. Esc-at-App-
		// level for the same reason as memberMenu (terminal/key-routing
		// quirks made an inner-only Esc handler unreliable).
		if a.statusPicker.IsVisible() {
			if msg.Type == tea.KeyEsc {
				a.statusPicker.Hide()
				return a, nil
			}
			var cmd tea.Cmd
			a.statusPicker, cmd = a.statusPicker.Update(msg)
			return a, cmd
		}

		// Shared picker intercepts all keys when visible. Same
		// Esc-at-App-level pattern as statusPicker above (#6 modal
		// lifecycle: one modal focused at a time; Esc → bare).
		if a.picker.IsVisible() {
			if msg.Type == tea.KeyEsc {
				a.picker.Hide()
				return a, nil
			}
			var cmd tea.Cmd
			a.picker, cmd = a.picker.Update(msg)
			return a, cmd
		}

		// Context menu intercepts all keys. Same Esc-at-App-level
		// pattern as memberMenu above. The user reported (2026-05-05)
		// that pressing Esc with the menu open did NOT dismiss the
		// menu — they could only close it by selecting an item. The
		// inner ContextMenuModel.Update has a `case "esc": c.Hide()`
		// branch that passed unit tests, so the bug must be in the
		// real-world routing somewhere; rather than chase it through
		// Bubble Tea internals or terminal-specific key delivery,
		// guarantee the dismissal here.
		if a.contextMenu.IsVisible() {
			if msg.Type == tea.KeyEsc {
				a.contextMenu.Hide()
				return a, nil
			}
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
					a.abandonActiveHistoryRequest()
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

		// Save-attachment dialog intercepts all keys: text-input for
		// the destination path in phaseEdit, y/n/e-style shortcuts in
		// phaseExists. Placed alongside the other modal intercepts so
		// the whole background (compose, sidebar, message actions) is
		// inert while the dialog is up.
		if a.saveAttachment.IsVisible() {
			var cmd tea.Cmd
			a.saveAttachment, cmd = a.saveAttachment.Update(msg)
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
		if a.unverifyConfirm.IsVisible() {
			var cmd tea.Cmd
			a.unverifyConfirm, cmd = a.unverifyConfirm.Update(msg)
			return a, cmd
		}

		// Device revoked dialog intercepts all keys
		if a.deviceRevoked.IsVisible() {
			var cmd tea.Cmd
			a.deviceRevoked, cmd = a.deviceRevoked.Update(msg)
			return a, cmd
		}

		// New-device alert (non-fatal) intercepts all keys while shown
		if a.newDeviceAlert.IsVisible() {
			var cmd tea.Cmd
			a.newDeviceAlert, cmd = a.newDeviceAlert.Update(msg)
			return a, cmd
		}

		// Room-attestation alert (F7, non-fatal) intercepts all keys while shown
		if a.roomAttestation.IsVisible() {
			var cmd tea.Cmd
			a.roomAttestation, cmd = a.roomAttestation.Update(msg)
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
			a.help.Update(msg, a.width, a.height)
			if !a.help.IsVisible() {
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

		// Add server dialog intercepts keys when visible. Add Server is a
		// first-class slot in the server navigation ring (not a dead-end
		// modal), so Ctrl+g stays the global nav prefix here — key
		// generation moved off Ctrl+g to Alt+g + the [Generate new key]
		// row so the prefix is free. Mirrors the connect-failed escape
		// hatch above; the a.navMode gate is what keeps plain h/l/j typed
		// in the form fields as ordinary text.
		if a.addServer.IsVisible() {
			if msg.String() == "ctrl+g" {
				return a, a.enterNavMode()
			}
			if a.navMode {
				switch msg.String() {
				case "h", "l", "j",
					"1", "2", "3", "4", "5", "6", "7", "8", "9",
					"g", "esc", "ctrl+g":
					if cmd, handled := a.handleNavModeKey(msg); handled {
						return a, cmd
					}
				default:
					// Non-nav key after the prefix: cancel nav mode and let
					// the key fall through to the form as normal input.
					a.exitNavMode()
				}
			}
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
			a.infoPanel.SetViewport(a.width, a.height)
			// Finding 1: refresh the live member set + display state before
			// Update reads i.members, so cursor nav and Enter/r/p/x act on the
			// current membership (persistent — updateInner returns the model).
			a.refreshInfoPanelLiveRows()
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
			layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
			a.emojiPicker.SetViewport(layout.MessagesWidth, layout.InputY0-layout.MessagesY0)
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
				// Dialog closed without emitting a create intent (Esc/cancel).
				// Reset source marker so stale member-panel context can't leak
				// into a future create flow.
				if cmd == nil {
					a.membersPanelCreateSource = false
				}
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
			// Hard close path for terminals that emit escape variants that
			// don't route cleanly through the search input's key map.
			if msg.Type == tea.KeyEsc || msg.String() == "ctrl+[" {
				a.search.Hide()
				a.focus = FocusInput
				return a, nil
			}
			var cmd tea.Cmd
			a.search, cmd = a.search.Update(msg, a.client)
			if !a.search.IsVisible() {
				a.focus = FocusInput
			}
			return a, cmd
		}

		// Expanded pinned-bar takes keyboard focus: up/down navigates
		// the pin list, Enter jumps to the selected pin, u unpins, and
		// Ctrl+P or Esc collapses. Without this intercept the keys
		// fell through to the input/messages handlers (since
		// PinnedBarModel.Update was never called from anywhere) — the
		// user reported all the listed shortcuts were dead.
		if a.pinnedBar.expanded && msg.String() != "ctrl+q" {
			var cmd tea.Cmd
			a.pinnedBar, cmd = a.pinnedBar.Update(msg)
			return a, cmd
		}

		// Global navigation prefix mode (Ctrl+g). Modal overlays above
		// short-circuit before this block, so modal-local handlers win.
		// When nav mode is active, one key dispatches an action and exits.
		// Unrecognized keys exit nav mode and fall through to normal
		// handling (so input-focus users can still type the key).
		if a.navMode {
			if cmd, handled := a.handleNavModeKey(msg); handled {
				return a, cmd
			}
		}

		switch msg.String() {
		// ctrl+c is handled at the top of the dispatcher (above every
		// modal IsVisible check) so it can't be swallowed by any
		// overlay that lacks its own ctrl+c branch. See the early
		// return above.

		case "ctrl+q":
			// Phase 17c Step 5 polish: double-press Ctrl+Q within
			// doubleQuitWindow bypasses the confirm dialog — escape
			// hatch for users who genuinely want to abandon pending
			// sends.
			now := time.Now()
			if !a.lastCtrlQAt.IsZero() && now.Sub(a.lastCtrlQAt) < doubleQuitWindow {
				a.lastCtrlQAt = time.Time{}
				if a.client != nil {
					a.abandonActiveHistoryRequest()
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

		case "ctrl+g":
			return a, a.enterNavMode()

		case "?":
			if a.focus != FocusInput {
				a.help.Toggle()
				return a, nil
			}

		case "ctrl+p":
			a.pinnedBar.Toggle()
			return a, nil

		case "ctrl+shift+r":
			// Phase 17c Step 6: nuclear refresh — force full
			// reconnect handshake. Emits RefreshRequestMsg so the
			// central handler drives both the client.Close and the
			// keypress-ack indicator consistently.
			return a, func() tea.Msg { return RefreshRequestMsg{Kind: "reconnect"} }

		case "i":
			// Info-panel shortcut scoped to the messages pane so normal
			// typing in the input field is unaffected.
			if a.focus == FocusMessages {
				a.showInfoPanelForContext(a.messages.room, a.messages.group, a.messages.dm)
				return a, nil
			}
			// Not in messages focus: let the key continue to normal handlers
			// (notably the input field) instead of swallowing typed "i".

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
			// Esc behavior, in priority order:
			//
			//   1. If a reply is active (regardless of which panel
			//      currently has focus), cancel the compose state
			//      and refocus the input. This makes Esc-from-the-
			//      messages-pane-while-replying actually cancel the
			//      reply instead of just refocusing — the previous
			//      handler only cancelled when focus was already on
			//      input, leaving the user replying-but-not-focused
			//      stuck pressing Esc twice.
			//
			//   2. Else if input focus + (editing | non-empty draft),
			//      ResetComposeState (clears edit + text + completion).
			//
			//   3. Else, refocus the input panel — the existing "Esc
			//      returns focus to input from anywhere" behavior.
			if a.input.IsReplying() {
				a.input.ResetComposeState()
				if a.focus != FocusInput {
					a.focus = FocusInput
				}
				return a, nil
			}
			if a.focus == FocusInput {
				if a.input.IsEditing() || !a.input.IsEmpty() {
					a.input.ResetComposeState()
				}
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
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedGroup() != a.messages.group || a.sidebar.SelectedDM() != a.messages.dm {
				a.switchMessageContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), a.sidebar.SelectedDM())
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				a.syncPinnedBarForContext()
				a.refreshMessageContent()
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
					// V8: Refresh renders from the local cache; no fetch.
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
			// Finding 1: refresh live membership (preserving the selected user)
			// + @-completion sources before Update reads a.memberPanel.members,
			// so nav / Enter / m act on the current set. Persistent path
			// (updateInner returns the model).
			a.refreshMemberPanelLiveRowsAndCompletion()
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
				text := strings.TrimSpace(a.input.ValueForSend())
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
					text := strings.TrimSpace(a.input.Value())
					// V8 scope-lock: the narrow read-only allow-list applies
					// to ROOMS only (retired or left). Left groups keep their
					// prior behavior (block normal sends, allow all slash
					// commands) so this change can't regress group UX.
					readOnlyRoom := a.messages.room != "" &&
						(a.messages.IsRoomRetired() || a.messages.IsLeft())
					if readOnlyRoom {
						// Only /delete, /search, /help, /? work in a read-only
						// room. Everything else (normal text + other slash
						// commands) is rejected with the exact status message.
						if !commandAllowedInReadOnlyRoom(text) {
							a.statusBar.SetError(`"/delete", "/search", and "/help" are available`)
							return a, nil
						}
						// fall through for allow-listed commands
					} else {
						// Left group (or other archived non-room context):
						// block normal sends, allow all slash commands.
						if !strings.HasPrefix(text, "/") {
							label := "context"
							switch {
							case a.messages.group != "":
								label = "group"
							case a.messages.room != "":
								label = "room"
							}
							a.statusBar.SetError("You left this " + label + " — type /delete to remove from your view")
							return a, nil
						}
						// fall through for slash commands
					}
				default:
					// Allow typing so the user can compose a command
					// fall through
				}
			}
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg, a.client, a.messages.room, a.messages.group, a.messages.dm)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// On send: clear status bar error, AND snap the messages
			// pane to "user just sent" state — viewport scrolls to
			// bottom + cursor advances to the last message + a flag
			// is raised so when the server echoes the user's message
			// back, the cursor follows to that new index. Net effect:
			// switch back to messages pane and find the cursor on
			// the message you just sent, even if you were reading
			// history when you decided to compose. See SnapToOwnSend
			// for the full rationale + edge cases.
			if a.input.DidSend() {
				a.statusBar.ClearError()
				a.messages.SnapToOwnSend()
			}
			// Check for pending slash commands
			if sc := a.input.PendingCommand(); sc != nil {
				a.handleSlashCommand(sc)
			}
		}

	case UnpinRequestMsg:
		a.sendUnpin(msg.MessageID)
		return a, nil

	case PinnedJumpMsg:
		// Pressed Enter (or clicked) on a pin in the expanded bar.
		// ScrollToMessage moves the cursor to the message AND scrolls
		// the viewport so the row is visible. Focus shifts to the
		// messages pane so the cursor highlight is rendered (cursor
		// highlight is style-independent of focus, but the user's
		// next up/down/r/e/etc. key naturally targets messages once
		// focus is there). The pinned bar's own Update has already
		// set p.expanded = false before emitting this message, so
		// the bar is collapsed by the time the App renders.
		a.messages.ScrollToMessage(msg.MessageID)
		a.focus = FocusMessages
		return a, nil

	case MuteToggleMsg:
		// Info-panel `m` key. Same downstream as the typed `/mute`
		// slash command (handleSlashCommand case "/mute") — both
		// route into applyMuteState.
		a.applyMuteState(msg.Target, msg.Kind, msg.Muted)
		return a, nil

	case InfoPanelAdminKeyMsg:
		// §9 step 6: group info-panel admin keys (a/r/p/x) hand off to
		// the right downstream. Panel is already Hide()d by the time
		// this fires (#6 modal lifecycle — Hide-before-Show). `a` is
		// the only verb that opens the shared picker (target is not
		// in the panel's member list); the other three call the
		// matching `<verb>ConfirmForTarget` for the highlighted
		// member, reusing the exact post-resolution step the typed
		// path uses (#3) — so already-admin / not-an-admin / self
		// pre-checks and last-admin guards behave identically.
		switch msg.Verb {
		case "/add":
			a.openPicker(PickerRequest{
				Verb:       "/add",
				Source:     PickerSourceInfoPanel,
				Group:      msg.Group,
				ShowFilter: true,
			})
		case "/kick":
			a.kickConfirmForTarget(msg.Group, msg.TargetID)
		case "/promote":
			a.promoteConfirmForTarget(msg.Group, msg.TargetID)
		case "/demote":
			a.demoteConfirmForTarget(msg.Group, msg.TargetID)
		}
		return a, nil

	case MemberActionMsg:
		fromMembersPanel := a.memberPanel.IsVisible() && (a.focus == FocusMembers || a.memberMenu.IsVisible())
		fromContextInfoPanel := a.infoPanel.IsVisible() && !a.infoPanel.isDM && (a.infoPanel.room != "" || a.infoPanel.group != "")
		// The user identity panel (ShowUser — /whois, member-panel "view
		// profile", Ctrl+g p) hides itself BEFORE emitting MemberActionMsg, so
		// IsVisible() is already false here; its isUser/userID state survives
		// Hide(), so match on that. "Message this user" from it should land in
		// the new DM's input, same as the member panel.
		fromUserInfoPanel := a.infoPanel.isUser && a.infoPanel.userID == msg.User
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
			// Open MemberMenu for the currently selected member. Mouse
			// clicks in the member pane only move selection; Enter is the
			// explicit action that opens this menu.
			// Anchor at the member panel's top-left so the overlay pops up
			// over the panel rather than down at screen origin.
			//
			// §9 step 7: items are built by App (the menu widget is
			// dumb). `buildMemberMenuItems` conditionally appends
			// "Add to group..." when there is at least one eligible
			// target group — never advertises a dead path.
			layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
			displayName := a.resolveDisplayName(msg.User)
			items := a.buildMemberMenuItems(msg.User, displayName)
			a.memberMenu.Show(msg.User, items, layout.MemberX0+1, layout.MemberY0+2)
		case "add_to_existing_group":
			// §9 step 7: open the shared picker over eligible target
			// groups for the subject user. SubjectUserID is carried
			// through selection so the post-resolution step
			// (PickerSelectedMsg case "add_to_group") knows which
			// user to add. Source=PickerSourceMemberPanel makes the
			// origin explicit in observability/debug traces.
			a.openPicker(PickerRequest{
				Verb:            "add_to_group",
				Source:          PickerSourceMemberPanel,
				SubjectUserID:   msg.User,
				SubjectUserName: a.resolveDisplayName(msg.User),
				ShowFilter:      true,
			})
		case "message":
			if a.client != nil {
				if msg.User == a.client.UserID() {
					a.statusBar.SetError("Cannot create a DM with yourself")
					break
				}
				if fromMembersPanel || fromContextInfoPanel || fromUserInfoPanel {
					a.setPendingFocusCreatedDM(msg.User)
				}
				if err := a.client.CreateDM(msg.User); err != nil {
					a.clearPendingFocusCreated()
					a.statusBar.SetError("Create DM failed: " + err.Error())
				}
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
				a.membersPanelCreateSource = fromMembersPanel
			}
		case "verify":
			if a.client != nil {
				a.verify.Show(msg.User, a.client)
			}
		case "profile":
			// Open the per-user profile panel — full detail (display
			// name, ID, fingerprint, public key, presence, verified
			// state, first-seen, role, retirement). Auto-copies the
			// public key to clipboard. Same panel as /whois opens —
			// single source of truth for "show me this user."
			if a.client != nil {
				a.infoPanel.ShowUser(msg.User, a.client, a.sidebar.online, a.sidebar.status, a.messages.dm)
			}
		}
		return a, nil

	case PickerSelectedMsg:
		// The shared picker echoed a selection back (#5). App owns
		// ALL verb routing — the widget stayed dumb. Each branch
		// invokes that verb's POST-RESOLUTION step with the already-
		// resolved ID, never re-entering name resolution (#3). More
		// verbs are added here per the §9 sequence; `/whois` is the
		// proving milestone (#12).
		var cmd tea.Cmd
		switch msg.Request.Verb {
		case "switch_server":
			// Server quick-switch (server-nav-ux §9). IDs are opaque:
			// either the add sentinel or a strconv server index. Unlike
			// every other verb here, switchServerByIndex returns a connect
			// tea.Cmd that MUST propagate via `return a, cmd` below — drop
			// it and the picker switches config + UI but never dials.
			if msg.SelectedID == serverPickerAddID {
				a.openAddServerFromNav()
			} else if idx, err := strconv.Atoi(msg.SelectedID); err == nil {
				cmd = a.switchServerByIndex(idx)
			}
		case "/whois":
			a.whoisReadout(msg.SelectedID)
		case "/verify":
			// `/verify` post-resolution = the existing VerifyModel
			// (already a clean ID seam, #3) — no new dialog.
			if a.client != nil {
				a.verify.Show(msg.SelectedID, a.client)
			}
		case "/unverify":
			// §9 step 4 / #8: the one net-new confirm in the picker
			// effort. Same dialog as the typed path uses now —
			// behavior consistent across both entry paths.
			a.unverifyConfirm.Show(msg.SelectedID, a.resolveDisplayName(msg.SelectedID))
		case "/role":
			// `/role` post-resolution: shared with the typed path
			// via roleReadout (#3). The picker only offers members
			// of msg.Request.Group, so the caller invariants of
			// roleReadout hold by construction.
			a.roleReadout(msg.Request.Group, msg.SelectedID)
		case "/add":
			a.addConfirmForTarget(msg.Request.Group, msg.SelectedID)
		case "/kick":
			a.kickConfirmForTarget(msg.Request.Group, msg.SelectedID)
		case "/promote":
			a.promoteConfirmForTarget(msg.Request.Group, msg.SelectedID)
		case "/demote":
			a.demoteConfirmForTarget(msg.Request.Group, msg.SelectedID)
		case "/transfer":
			a.transferConfirmForTarget(msg.Request.Group, msg.SelectedID)
		case "add_to_group":
			// §9 step 7: member-panel add-to-existing-group. ID-shape
			// is OPPOSITE to slash `/add`: SelectedID is the picked
			// GROUP ID; the user being added is the subject user
			// carried in Request.SubjectUserID. The post-resolution
			// step is the same addConfirmForTarget(groupID, userID)
			// shared by step 5 — already-member etc. checks fire
			// identically. Source==PickerSourceMemberPanel by
			// construction; assert nothing changes on slash /add.
			a.addConfirmForTarget(msg.SelectedID, msg.Request.SubjectUserID)
		}
		return a, cmd

	case StatusSelectMsg:
		// User picked a status from the StatusPicker modal. Same
		// downstream flow as a typed `/setstatus <name>`: send to
		// server + optimistic local sidebar update so the dot
		// color flips immediately. Picker already validated the
		// status against the locked set (it only emits the three
		// valid values), so no validation needed here.
		if a.client != nil && msg.Status != "" {
			a.client.Enc().Encode(protocol.SetStatus{
				Type: "set_status",
				Text: msg.Status,
			})
			self := a.client.UserID()
			a.sidebar.SetStatus(self, msg.Status)
			online, ok := a.sidebar.online[self]
			if !ok {
				online = true
			}
			a.memberPanel.SetPresence(self, online, msg.Status)
			a.statusBar.SetError("Status set to " + msg.Status)
		}
		return a, nil

	case ProfileUpdateMsg:
		if a.client != nil {
			// Option B confirm-then-apply (rename-collision-ux.md): do NOT
			// optimistically show success. The displayed name stays the
			// server-confirmed value (DisplayName(self)); we set an in-flight
			// marker, send set_profile, and show a tentative "Saving…" notice.
			// The self-`profile` broadcast confirms (case "profile"), and any
			// empty-correlation server error while the marker is set surfaces
			// the failure in the panel. Ignore a re-submit while one is already
			// in flight so we never send a second set_profile for the same edit.
			if a.renameInFlight {
				return a, nil
			}
			if err := a.client.Enc().Encode(protocol.SetProfile{
				Type:        "set_profile",
				DisplayName: msg.DisplayName,
			}); err != nil {
				// Local encode/write failed — the request never left the client, so
				// no self-`profile` (success) or server `error` will ever arrive.
				// Do NOT set the in-flight marker; setting it would stick Settings on
				// "Saving…" forever. Surface the failure where the user is looking.
				if a.settings.IsVisible() {
					a.settings.SetDisplayNameRenamePending(false)
					a.settings.SetErrorNotice("Name change failed: could not send (" + err.Error() + ")")
				} else {
					a.statusBar.SetError("Name change failed: could not send")
				}
				return a, nil
			}
			a.renameInFlight = true
			a.renameAttempted = msg.DisplayName
			if a.settings.IsVisible() {
				a.settings.SetDisplayNameRenamePending(true)
				a.settings.SetNotice("Saving \"" + msg.DisplayName + "\"…")
			}
		}
		return a, nil

	case SettingsActionMsg:
		switch msg.Action {
		case "clear_history":
			// Ephemeral-sentinel guard: a.serverIdx < 0 means we're
			// running against an unconfigured ephemeral host (CLI
			// bypass with existing cfg.Servers). Refuse destructive
			// actions — there's no genuine "active configured
			// server" to clear data for; without this guard the
			// fallback would target cfg.Servers[0] which the user
			// did not intend.
			if a.serverIdx < 0 {
				a.statusBar.SetError("Cannot clear history in ephemeral mode (no configured server)")
				break
			}
			if a.client != nil && a.appConfig != nil && a.serverIdx < len(a.appConfig.Servers) {
				if err := config.ClearServerData(a.configDir, a.appConfig.Servers[a.serverIdx]); err != nil {
					a.statusBar.SetError("Failed to clear history: " + err.Error())
				} else {
					a.statusBar.SetError("Local history cleared")
				}
			}
		case "remove_server":
			// Ephemeral-sentinel guard: see clear_history above.
			if a.serverIdx < 0 {
				a.statusBar.SetError("Cannot remove server in ephemeral mode (no configured server)")
				break
			}
			if a.appConfig != nil {
				if err := config.RemoveServer(a.configDir, a.appConfig, msg.ServerIdx); err != nil {
					a.statusBar.SetError("Failed to remove server: " + err.Error())
					// Side-effects (close + quit) intentionally
					// NOT run on error — server removal aborted,
					// leave the app + client running so the user
					// can recover.
					break
				}
				a.statusBar.SetError("Server removed")
				// If we removed the active server, close. Only
				// runs on the success path (after the nil-error
				// branch above) so an aborted RemoveServer never
				// quits the app behind the user's back.
				if msg.ServerIdx == a.serverIdx {
					if a.client != nil {
						a.abandonActiveHistoryRequest()
						a.client.Close()
					}
					return a, tea.Quit
				}
				// Reindex fix: removing an entry at an index BELOW
				// the active one shifts cfg.Servers down by one, so
				// a.serverIdx must decrement to keep pointing at the
				// SAME logical server. Without this, a.serverIdx
				// silently slides forward by one (now references a
				// different server entry) and subsequent settings
				// actions target the wrong row. Caught in Phase 4
				// audit.
				if msg.ServerIdx < a.serverIdx {
					a.serverIdx--
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
			a.openDeviceManager()
		case "copy_pubkey":
			if a.client != nil {
				pubKey := a.client.PublicKeyAuthorized()
				if pubKey != "" {
					CopyToClipboard(pubKey)
					// Settings overlays the status bar so the
					// confirmation has to live in the panel itself,
					// not just in statusBar.SetError.
					if a.settings.IsVisible() {
						a.settings.SetNotice("Public key copied to clipboard")
					} else {
						a.statusBar.SetError("Public key copied to clipboard")
					}
				}
			}
		case "copy_fingerprint":
			if a.client != nil {
				fp := a.client.KeyFingerprint()
				if fp != "" {
					CopyToClipboard(fp)
					if a.settings.IsVisible() {
						a.settings.SetNotice("Fingerprint copied to clipboard")
					} else {
						a.statusBar.SetError(fp + " — copied to clipboard")
					}
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
			a.abandonActiveHistoryRequest()
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
				return a, nil
			}
			// §9 step 7 UX: focus the target group after a
			// successful add IF the local user wasn't already viewing
			// it. Self-correcting across all add paths:
			//   - typed /add @user in active group        → current == target → no-op
			//   - bare /add picker (slash + footer `a`)   → current == target → no-op
			//   - member-panel "Add to existing group..." → current is the source
			//     room/DM/other-group → switches to the target group, landing the
			//     admin where the just-added user now is (the natural next action).
			if a.messages.group != msg.Group {
				a.focusSidebarGroup(msg.Group)
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

	case UnverifyConfirmMsg:
		// §9 step 4 / #8: user confirmed /unverify (typed or picker
		// path — same dialog, same handler). Clears the verified=1
		// flag in pinned_keys; the user's profile/messages are
		// unaffected, only the trust marker.
		if a.client == nil || msg.TargetID == "" {
			return a, nil
		}
		if st := a.client.Store(); st != nil {
			if err := st.ClearVerified(msg.TargetID); err != nil {
				a.statusBar.SetError("Unverify failed: " + err.Error())
				return a, nil
			}
		}
		a.statusBar.SetError("Verification removed for " + a.resolveDisplayName(msg.TargetID))
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

	case saveAttachmentOpenMsg:
		// Download goroutine completed; open the save-as modal on the
		// Update goroutine. Status bar's "Downloading..." message is
		// cleared by a subsequent status update (or by the modal
		// covering the status bar until the user finishes).
		a.saveAttachment.Show(msg.SourcePath, msg.AttachmentName, msg.DefaultPath)
		return a, nil

	case saveAttachmentDownloadFailedMsg:
		// Download goroutine failed before we ever reached the modal.
		// Show the error in the status bar and do nothing else — no
		// modal opened, no state to unwind.
		a.statusBar.SetError("Download failed: " + msg.Err.Error())
		return a, nil

	case SaveAttachmentDoMsg:
		// User confirmed a destination (possibly via overwrite or
		// rename). Perform the copy in a goroutine so the TUI stays
		// responsive, and report the outcome via the status bar
		// through SaveResultEvent.
		//
		// Capture by value: SaveFileAs is a pure os-level copy that
		// doesn't need to touch `a`, and the saveResultCh reference
		// is captured directly. Pre-fix path mutated statusBar
		// directly from the goroutine on the about-to-go-stale `a`
		// pointer, so save success/failure feedback was silently
		// dropped.
		src := msg.SourcePath
		dst := msg.DestPath
		resultCh := a.sidebar.saveResultCh
		// Capture gen at launch time so the resulting event can be
		// matched back to this connection generation. A save kicked
		// off before a server switch should not surface its
		// completion status against the new server.
		gen := a.connGen
		go func() {
			err := client.SaveFileAs(src, dst)
			if resultCh != nil {
				select {
				case resultCh <- SaveResultEvent{Dest: dst, Err: err, gen: gen}:
				default:
				}
			}
		}()
		return a, nil

	case SaveAttachmentCancelledMsg:
		// User bailed out of the save flow. Surface a subtle
		// acknowledgement so they know the Esc registered; don't spam
		// the status bar if they're about to do something else.
		a.statusBar.SetError("Save cancelled")
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
		// by both client.go (purge messages, epoch keys, reactions, local
		// room row) and here (drop sidebar entry, reset active context).
		//
		// For a current member, the server performs the normal leave
		// side-effects first (remove from room_members, broadcast
		// room_event leave, echo room_left). Active rooms rotate epoch;
		// retired rooms are read-only, so only epoch rotation is skipped.
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
			// V8: the explicit `r` refresh — the single remaining
			// RequestRoomMembers call site. Read-only rooms (retired/left)
			// have no member list, so refresh is a render-layer no-op: do
			// not fetch, just surface the read-only status.
			if a.messages.room != "" {
				if a.messages.IsRoomRetired() {
					a.statusBar.SetError("room retired")
				} else if a.messages.IsLeft() {
					a.statusBar.SetError("you are not a member of this room")
				} else {
					a.client.RequestRoomMembers(a.messages.room)
				}
			}
		case "device_list":
			a.client.SendListDevices()
		case "reconnect":
			// Explicit reconnect: close the old client and bump the
			// connection generation immediately by calling
			// startConnect. With gen-gating in effect, the old
			// client's `done` event (delivered via waitForMsg as
			// ErrMsg{disconnected}) is now stale and will be
			// dropped by updateInner — we can't rely on it to
			// schedule reconnect anymore. The refreshing indicator
			// stays visible during the transition; the
			// SetConnected(true) on successful re-handshake is what
			// ultimately masks it. See
			// fix-cross-server-db-isolation.md §"Generation bumps".
			a.statusBar.SetReconnecting(0, 0)
			a.abandonActiveHistoryRequest()
			_ = a.client.Close()
			a.statusBar.SetRefreshing(refreshingMinVisibleMs * time.Millisecond)
			return a, tea.Batch(
				a.startConnect(),
				tea.Tick(refreshingMinVisibleMs*time.Millisecond, func(time.Time) tea.Msg {
					return refreshingTickMsg{}
				}),
			)
		}
		a.statusBar.SetRefreshing(refreshingMinVisibleMs * time.Millisecond)
		return a, tea.Tick(refreshingMinVisibleMs*time.Millisecond, func(time.Time) tea.Msg {
			return refreshingTickMsg{}
		})

	case navPopupRevealMsg:
		// Reveal the which-key popup only if nav mode is still active (a
		// consumed key bumps the gen / clears navMode) and no modal opened
		// during the delay — gating where the bool is *set* keeps the popup
		// out of modal contexts entirely (decision 6).
		if a.navMode && msg.Gen == a.navModeTickGen && !a.anyModalVisible() {
			a.navPopupVisible = true
		}
		return a, nil

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
			a.abandonActiveHistoryRequest()
			a.client.Close()
		}
		return a, tea.Quit

	case VerifyActionMsg:
		if a.client != nil && a.client.Store() != nil {
			a.client.Store().MarkVerified(msg.User)
			a.statusBar.SetError(a.resolveDisplayName(msg.User) + " marked as verified")
		}
		return a, nil

	case KeyWarningDisconnectMsg:
		if a.client != nil {
			a.abandonActiveHistoryRequest()
			a.client.Close()
		}
		return a, tea.Quit

	case AddServerMsg:
		// Defensive: the nav ring can open Add Server from a no-config
		// state (e.g. an ephemeral CLI session). Initialize an empty
		// config so submit adds the first server instead of silently
		// no-oping — a silent no-op would be worse than the old digit
		// dead-end this feature removes.
		if a.appConfig == nil {
			a.appConfig = &config.Config{}
		}
		var cmd tea.Cmd
		srv := config.ServerConfig{
			Name:                 msg.Name,
			Host:                 msg.Host,
			Port:                 msg.Port,
			RequestedDisplayName: msg.RequestedDisplayName,
		}
		// AddServer validates srv.Host and dedups on host:port; the error
		// path surfaces a clear status-bar message instead of the prior
		// unconditional "Server added" false-success.
		if err := config.AddServer(a.configDir, a.appConfig, srv); err != nil {
			a.statusBar.SetError("Failed to add server: " + err.Error())
		} else {
			// Ring UX: switch to (and connect) the just-added server.
			// AddServer appended in place, so it is the last entry. The
			// connect cmd from switchServerByIndex MUST be returned
			// (below) or the new server is selected but never dialed.
			a.statusBar.SetError("Server added: " + msg.Name)
			cmd = a.forceSwitchServerByIndex(len(a.appConfig.Servers) - 1)
		}
		return a, cmd

	case EmojiSelectedMsg:
		if a.client != nil {
			if msg.Remove {
				a.sendUnreactForMessage(msg.Target, msg.Emoji)
				return a, nil
			}
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
			} else if msg.Target.DM != "" {
				err = a.client.SendDMReaction(msg.Target.DM, msg.Target.ID, msg.Emoji)
			} else {
				err = fmt.Errorf("unknown message context")
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
				// Focus the just-created DM regardless of where the
				// NewConv dialog was opened from (members panel OR
				// Ctrl+g n). Explicitly creating a conversation and
				// then landing on it is the expected UX everywhere;
				// the intent is TTL-bounded and matched against the
				// dm_created result, so a failed / never-arriving
				// create harmlessly expires.
				a.setPendingFocusCreatedDM(msg.Members[0])
				a.pendingCreateDM = pendingCreateDMState{
					other:   msg.Members[0],
					retries: maxCreateDMAutoRetries,
				}
				a.client.CreateDM(msg.Members[0])
			} else if len(msg.Members) > 0 {
				// Focus the just-created group regardless of where the
				// NewConv dialog was opened from (members panel OR
				// Ctrl+g n) — same rationale as the DM branch above.
				a.setPendingFocusCreatedGroup(msg.Members, msg.Name)
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
		a.membersPanelCreateSource = false
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
		} else if msg.DM != "" {
			for i, d := range a.sidebar.dms {
				if d.ID == msg.DM {
					a.sidebar.cursor = len(a.sidebar.rooms) + len(a.sidebar.groups) + i
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
			a.switchMessageContext(msg.Room, "", "")
		} else if msg.Group != "" {
			a.switchMessageContext("", msg.Group, "")
		} else if msg.DM != "" {
			a.switchMessageContext("", "", msg.DM)
		}
		a.syncMessagesLeftState()
		a.messages.LoadFromDB(a.client)
		a.syncPinnedBarForContext()
		a.refreshMessageContent()
		a.messages.ScrollToMessage(msg.MessageID)
		return a, nil

	case HistoryRequestMsg:
		// Outgoing Request Guard first (needs no client): drop a request from a
		// superseded connection generation or a context the user has since
		// left. A stale request belongs to a previous context and must not
		// mutate — or abort — a newer context's Loading/probe state.
		if msg.Gen != a.connGen || !validHistoryContext(msg.Room, msg.Group, msg.DM) || !a.messages.historyRequestMatches(msg.Room, msg.Group, msg.DM) {
			return a, nil
		}
		if a.client == nil {
			// Current request but the client is gone (disconnected before
			// send): clear the visible load so the pane doesn't stay stuck on
			// "loading history".
			a.messages.abortHistoryRequest()
			return a, nil
		}
		// Try local DB first — avoids a server round-trip when messages
		// are already cached from previous sync/history fetches.
		//
		// Pre-2026-05-07 this passed an empty string for the DM
		// parameter, so 1:1 DM history scrollback always missed the
		// local cache (every row had a real DM but we queried for ""),
		// fell through to the server, and the server-side request
		// also dropped DM (RequestHistory took no dm param), so the
		// envelope arrived with all three context fields empty and
		// the server never replied. The TUI's loadingHistory flag is
		// only cleared inside the local-success branch or by a
		// history_result event — neither fired — so the spinner
		// stuck forever.
		if st := a.client.Store(); st != nil {
			localMsgs, err := st.GetMessagesBefore(msg.Room, msg.Group, msg.DM, msg.BeforeID, 100)
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

				// Convert to display messages and prepend.
				// DM context + attachments are propagated from the store
				// so DM history scrollback shows attachments and the
				// resulting DisplayMessages carry the right context tag
				// (mattered when other view code paths filter by m.DM).
				var display []DisplayMessage
				for _, m := range localMsgs {
					from := m.Sender
					if a.client != nil {
						from = a.client.DisplayName(m.Sender)
					}
					var attachments []DisplayAttachment
					for _, a := range m.Attachments {
						key, _ := base64.StdEncoding.DecodeString(a.DecryptKey)
						attachments = append(attachments, DisplayAttachment{
							FileID:      a.FileID,
							Name:        a.Name,
							Size:        a.Size,
							Mime:        a.Mime,
							IsImage:     isImageMime(a.Mime),
							DecryptKey:  key,
							ContentHash: a.ContentHash, // F11: verified on download
						})
					}
					display = append(display, DisplayMessage{
						ID:          m.ID,
						FromID:      m.Sender,
						From:        from,
						Body:        m.Body,
						TS:          m.TS,
						EditedAt:    m.EditedAt,
						Room:        m.Room,
						Group:       m.Group,
						DM:          m.DM,
						ReplyTo:     m.ReplyTo,
						Mentions:    m.Mentions,
						Deleted:     m.Deleted,
						DeletedBy:   m.DeletedBy,
						Attachments: attachments,
					})
				}
				a.messages.PrependMessages(display)
				full := len(localMsgs) >= historyPageLimit
				// Full local page: stop here (Loading cleared, hint shown).
				// Short page: Loading stays set so the server continuation
				// below is one logical load and a second top-scroll can't
				// double-fire.
				a.messages.markLocalHistoryPage(len(localMsgs), historyPageLimit)

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

				// If local DB had fewer than a full page, continue to the
				// server for older rows not yet synced. Loading stays set
				// (markLocalHistoryPage left it on for a short page), so this
				// is one logical load; markServerHistoryResult clears it.
				if !full {
					a.sendServerHistory(msg.Room, msg.Group, msg.DM, msg.BeforeID)
				}
				return a, nil
			}
		}
		// No local data — fall through to server. Loading stays set;
		// markServerHistoryResult clears it when history_result arrives.
		a.sendServerHistory(msg.Room, msg.Group, msg.DM, msg.BeforeID)
		return a, nil

	case MessageAction:
		a.statusBar.ClearError()
		switch msg.Action {
		case "reply":
			preview := msg.Msg.Body
			if len(preview) > 50 {
				preview = preview[:47] + "..."
			}
			from := msg.Msg.From
			if msg.Msg.FromID != "" {
				from = a.resolveDisplayName(msg.Msg.FromID)
			}
			a.input.SetReplyContext(msg.Msg.ID, from+": "+preview, msg.Msg.Room, msg.Msg.Group, msg.Msg.DM)
			a.focus = FocusInput
		case "delete":
			if a.client != nil && (msg.Msg.FromID == a.client.UserID() || a.client.IsAdmin()) {
				a.client.SendDelete(msg.Msg.ID, msg.Msg.Room, msg.Msg.Group, msg.Msg.DM)
			}
		case "pin":
			if a.client != nil && a.messages.room != "" {
				if a.isMessagePinned(msg.Msg.ID) {
					a.statusBar.SetError("Message is already pinned")
					return a, nil
				} else {
					a.client.Enc().Encode(protocol.Pin{
						Type: "pin",
						Room: a.messages.room,
						ID:   msg.Msg.ID,
					})
				}
			}
		case "unpin":
			if a.client != nil && a.messages.room != "" {
				if !a.isMessagePinned(msg.Msg.ID) {
					a.statusBar.SetError("Message is not pinned")
					return a, nil
				}
				a.sendUnpin(msg.Msg.ID)
			}
		case "copy":
			CopyToClipboard(msg.Msg.Body)
			a.statusBar.SetError("Copied to clipboard")
		case "open_attachment":
			// Goroutine-driven download then external open. Capture
			// reference-typed locals by value so the goroutine never
			// dereferences the about-to-go-stale `a` pointer (see
			// UploadResultEvent doc comment for why direct goroutine
			// writes to a.statusBar from this case were silently
			// dropped pre-fix). Synchronous "Downloading..." status
			// works because updateInner is value-receiver and the
			// returned `a` becomes the new canonical model.
			if a.client != nil && len(msg.Msg.Attachments) > 0 {
				att := msg.Msg.Attachments[0]
				a.statusBar.SetError("Downloading " + att.Name + "...")
				c := a.client
				fileID := att.FileID
				decryptKey := att.DecryptKey
				attName := att.Name
				resultCh := a.sidebar.downloadResultCh
				// Capture gen at goroutine launch — the status event
				// is dropped if the user has switched servers by the
				// time the download finishes. NOTE: the OS-level
				// OpenFile side effect inside the goroutine fires
				// even on stale gen (file is downloaded for the
				// prior server's data, OS handler is invoked). This
				// is known and accepted, NOT a bug: the user clicked
				// `o` to open the file — they get the file they
				// asked for. No data corruption (download lands in
				// the originating server's files dir), no security
				// boundary crossed, no UI confusion. See
				// fix-cross-server-db-isolation.md §"`open_attachment`
				// OS-level OpenFile — known and accepted" for the
				// full decision and cost-benefit rationale.
				gen := a.connGen
				go func() {
					path, err := c.DownloadFile(fileID, decryptKey, att.ContentHash)
					if err == nil {
						// Pass attName as the extension hint — the
						// cached file lives at .../files/<fileID>
						// with no extension, which makes macOS `open`
						// and xdg-open fall back to text editors.
						// OpenFile uses the hint's extension to
						// create a sibling symlink (or copy) the OS
						// can identify by file type.
						client.OpenFile(path, attName)
					}
					if resultCh != nil {
						select {
						case resultCh <- DownloadResultEvent{Action: "open", Name: attName, Err: err, gen: gen}:
						default:
						}
					}
				}()
			}
		case "preview_attachment":
			// `p` keypress on an image attachment that isn't yet
			// cached locally. Triggers DownloadFile (which populates
			// the local plaintext cache + generates both block-char
			// and rasterm thumbnails via its eager goroutines). The
			// existing OnAttachmentReady → AttachmentReadyEvent →
			// invalidateImageRenderCacheForPath → wrapper-level
			// RequestPreviewRender re-dispatch chain handles the
			// preview-pane update; this case just kicks off the
			// download and surfaces success/failure feedback via the
			// status bar.
			//
			// Picks the FIRST image attachment on the message
			// (mirrors SelectedImagePath's behavior). The keypress
			// gate in messages.go already verified at least one
			// image attachment exists and isn't cached.
			if a.client != nil && len(msg.Msg.Attachments) > 0 {
				for _, att := range msg.Msg.Attachments {
					if !att.IsImage {
						continue
					}
					a.statusBar.SetError("Loading preview of " + att.Name + "...")
					c := a.client
					fileID := att.FileID
					decryptKey := att.DecryptKey
					attName := att.Name
					resultCh := a.sidebar.downloadResultCh
					gen := a.connGen
					go func() {
						_, err := c.DownloadFile(fileID, decryptKey, att.ContentHash)
						if resultCh != nil {
							select {
							case resultCh <- DownloadResultEvent{Action: "preview", Name: attName, Err: err, gen: gen}:
							default:
							}
						}
					}()
					break
				}
			}
		case "save_attachment":
			if a.client != nil && len(msg.Msg.Attachments) > 0 {
				att := msg.Msg.Attachments[0]
				// Strip any path components from the sender-supplied
				// filename before we let it near filepath.Join — a
				// hostile sender with `att.Name = "../../etc/passwd"`
				// would otherwise be able to steer the save outside
				// the chosen destination directory.
				safeName := sanitizeAttachmentName(att.Name)
				defaultPath := filepath.Join(defaultSaveDir(), safeName)
				cachePath := config.AttachmentPath(a.cfg.DataDir, att.FileID)

				// If auto-preview already cached the file (small image)
				// or the user previously opened it, skip the network
				// round-trip and open the modal immediately. Otherwise
				// fire a tea.Cmd that downloads then emits the
				// open-modal message so Show() lands on the Update
				// goroutine, not the download goroutine.
				//
				// NB: the enclosing `case MessageAction:` block ends
				// with `return a, nil` (see line below the switch), so
				// we can't use the `cmds = append(cmds, ...)` pattern
				// here — that accumulator is discarded. Return the cmd
				// directly instead. Caught by staticcheck SA4006
				// after an early attempt used the append pattern; the
				// symptom was a permanent "Downloading..." status-bar
				// message because the download goroutine never ran.
				// F12: att.FileID is sender-supplied — only consult the
				// on-disk cache when it's a safe single path component, so a
				// traversal id is never os.Stat'd or opened in the save modal
				// here. An unsafe id takes the download branch, where
				// DownloadFile re-validates and rejects it cleanly.
				cached := false
				if config.ValidFileID(att.FileID) {
					if _, err := os.Stat(cachePath); err == nil {
						cached = true
					}
				}
				if cached {
					a.saveAttachment.Show(cachePath, safeName, defaultPath)
				} else {
					a.statusBar.SetError("Downloading " + att.Name + "...")
					fileID := att.FileID
					decryptKey := att.DecryptKey
					c := a.client
					return a, func() tea.Msg {
						path, err := c.DownloadFile(fileID, decryptKey, att.ContentHash)
						if err != nil {
							return saveAttachmentDownloadFailedMsg{Err: err}
						}
						return saveAttachmentOpenMsg{
							SourcePath:     path,
							AttachmentName: safeName,
							DefaultPath:    defaultPath,
						}
					}
				}
			}
		case "open_menu":
			// Keyboard-triggered context menu opener (Enter on selected message).
			// Shows the same menu the mouse right-click produces. Anchor at
			// the messages-pane left edge, near the bottom so the overlay
			// sits above the input box — there's no precise per-message
			// row tracking for the keyboard cursor (messages can wrap to
			// multi-line) so the bottom-of-pane anchor is the closest
			// equivalent to "near the user's focus."
			isOwn := a.client != nil && msg.Msg.FromID == a.client.UserID()
			isAdmin := a.client != nil && a.client.IsAdmin()
			isRoom := a.messages.room != ""
			var myEmojis []string
			if a.client != nil {
				myEmojis = msg.Msg.UserEmojis(a.client.UserID())
			}
			layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
			anchorX := layout.MessagesX0 + 2
			anchorY := layout.MessagesY1 - 8 // overlay() clamps if too low
			a.contextMenu.Show(msg.Msg, anchorX, anchorY, isOwn, isAdmin, isRoom, a.pinnedBar.PinIDs(), myEmojis)
		case "react":
			userID := ""
			if a.client != nil {
				userID = a.client.UserID()
			}
			a.emojiPicker.Show(msg.Msg, userID)
		case "unreact":
			a.sendUnreactForMessage(msg.Msg, msg.Data)
		case "thread":
			a.threadPanel.Show(msg.Data, a.messages.messages)
		}
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		// Refresh messages content because the line-wrapping width
		// changed — without this the viewport's content would still
		// be wrapped at the old width and would render off the right
		// edge or with stale soft-wraps after a resize.
		a.refreshMessageContent()
		// Resize the textinput's pan window. If we set Width only in
		// input.View (value-receiver render path), the assignment
		// lands on a throwaway copy and the next input.Update sees
		// Width=0 — handleOverflow then doesn't clip, the value
		// renders in full, and lipgloss wraps to a 2nd inner row,
		// blowing the input box up to 4 rows. Setting it here on
		// the persistent App state makes the panning actually engage
		// across keystrokes.
		a.input.SetTextInputWidth(layoutInputContentWidth(a.width, a.memberPanel.IsVisible()))

	case tea.MouseMsg:
		return a.handleMouse(msg)

	case connectedWithClient:
		// Stale-drop FIRST — before any state mutation. A slow old
		// connect attempt finishing after a server switch or after a
		// newer connect already won would otherwise overwrite the
		// active client, leak a live readLoop + SSH connection +
		// store handle, and surface the wrong server's data. Close
		// the stale client here as cleanup of the discarded
		// connection, not as a current-state mutation. See
		// fix-cross-server-db-isolation.md §"Connect command".
		if msg.gen != a.connGen {
			if msg.client != nil {
				_ = msg.client.Close()
			}
			return a, nil
		}
		a.client = msg.client
		a.connected = true
		a.reconnectAttempt = 0

		// Seed our own online state so the green dot shows up next
		// to our entries in the sidebar (DMs/groups containing us)
		// and in the member panel. The server's `presence` push
		// updates other users but doesn't always echo our self-
		// presence at session start, so without this seed the local
		// user appears offline in their own UI. Re-fires on every
		// reconnect — fine, the map is idempotent.
		if uid := a.client.UserID(); uid != "" {
			a.sidebar.SetOnline(uid, true)
			// Sidebar self-identity for group-dot self-exclusion.
			// groupPresenceDot() filters self out of the presence
			// aggregation so the dot reflects "is someone ELSE
			// here." Pre-fix, selfUserID was only set by the
			// dm_list handler, leaving a startup window where
			// group_list rendered dots with selfUserID="" and self
			// leaked into the color (persisting for solo-self
			// groups and when dm_list never arrives). Setting it
			// here — same non-empty uid, same connect-time point
			// as the online seed — closes the window. The dm_list
			// setter stays as defensive backup this phase. See
			// presence-dot-self-leak-fix.md.
			a.sidebar.SetSelfUserID(uid)
		}

		// Populate sidebar and messages
		a.sidebar.SetRooms(a.client.Rooms())
		a.messages.resolveName = a.client.DisplayName
		a.pinnedBar.SetResolveName(a.client.DisplayName)
		a.messages.resolveRoomName = a.client.DisplayRoomName
		a.messages.resolveGroupName = a.client.DisplayGroupName
		a.messages.resolveDMName = a.client.DisplayDMName
		a.search.resolveName = a.client.DisplayName
		a.search.resolveRoomName = a.client.DisplayRoomName
		a.search.resolveGroupName = a.client.DisplayGroupName
		a.search.resolveDMName = a.client.DisplayDMName
		if st := a.client.Store(); st != nil {
			a.search.SetFTS(st.HasFTS())
		}
		a.sidebar.resolveName = a.client.DisplayName
		a.sidebar.resolveDMName = a.client.DisplayDMName
		a.sidebar.resolveDMOther = a.client.DMOther
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
			a.switchMessageContext(a.client.Rooms()[0], "", "")
			a.syncMessagesLeftState()
			a.messages.LoadFromDB(a.client)
			a.syncPinnedBarForContext()
			a.refreshMessageContent()
			// Set up member list for @completion
			a.memberPanel.Refresh(a.client.Rooms()[0], "", "", a.client, a.sidebar.online, a.sidebar.status)
			a.input.SetMembers(a.activeMemberEntries())
			a.input.SetNonMembers(a.activeNonMemberEntries())
		}

		a.syncLocalIdentityUI()
		a.statusBar.SetConnected(true)
		a.updateTitle()

		// Start listening for server messages
		cmds = append(cmds, waitForMsg(a.connGen, msg.msgCh, msg.errCh, msg.keyWarnCh, msg.attachReadyCh, msg.uploadResultCh, msg.downloadResultCh, msg.saveResultCh, msg.roomUpdatedCh, a.client.Done()))
		// Store channels for future waits
		a.sidebar.msgCh = msg.msgCh
		a.sidebar.errCh = msg.errCh
		a.sidebar.keyWarnCh = msg.keyWarnCh
		a.sidebar.attachReadyCh = msg.attachReadyCh
		a.sidebar.uploadResultCh = msg.uploadResultCh
		a.sidebar.downloadResultCh = msg.downloadResultCh
		a.sidebar.saveResultCh = msg.saveResultCh
		a.sidebar.roomUpdatedCh = msg.roomUpdatedCh

		// Wire the per-server file cache directory into the messages
		// model so the inline-image render path can resolve downloaded
		// attachments by file_id at paint time. See MessagesModel.SetFilesDir.
		a.messages.SetFilesDir(config.FilesDir(a.cfg.DataDir))

	case ServerMsg:
		// Stale-drop must precede every side effect — including the
		// recursive handleServerMessage dispatch, the identity-sync
		// refresh, the message-content rebuild, and the waitForMsg
		// re-arm. Stale events are silently discarded with no UI
		// mutation, no status update, and no wait re-arm.
		if msg.gen != a.connGen {
			return a, nil
		}
		if cmd := a.handleServerMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Profile events can arrive after connectedWithClient. Refresh local
		// identity sinks so status bar + mention highlighting stop showing raw
		// nanoid fallbacks once display-name cache is hydrated.
		a.syncLocalIdentityUI()
		// Server messages can mutate any of: messages list, edits/deletes,
		// reactions, system events, typing indicator, topic, retired-set,
		// left state. Rather than instrument every call site inside
		// handleServerMessage, refresh once here — buildContent is cheap
		// (O(messages)) and idempotent, and most server messages do
		// touch render-affecting state.
		a.refreshMessageContent()
		// Continue listening
		if a.client != nil {
			if a.sidebar.msgCh != nil {
				cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
			}
		}

	case RoomUpdatedEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
		if a.client != nil && msg.Room != "" && msg.Room == a.messages.room {
			a.messages.SetRoomTopic(a.client.DisplayRoomTopic(msg.Room))
			if a.statusBar.errorMsg == topicUpdatePendingStatus {
				a.statusBar.SetError("Topic updated")
			}
		}
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case AttachmentReadyEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
		// Auto-preview image download completed OR a thumbnail-
		// generation goroutine just finished. Invalidate cached
		// render entries for this attachment's path and reset the
		// sidebar's render key if it matches — the wrapper's
		// RequestPreviewRender call this Update will then dispatch
		// a fresh Cmd that picks up whichever encoder's thumbnail
		// is newly available (typically rasterm replacing a
		// transient block-char fallback). Without this, the
		// lookupCachedRenderForKey fast-path keeps returning the
		// stale render forever — visible as "block-char preview
		// stuck until app restart" on rasterm-capable terminals
		// when the rasterm thumbnail finishes after first render.
		// F12: msg.FileID is sender-supplied; gate the join so a traversal
		// id can't produce a path we invalidate or compare against.
		if a.messages.filesDir != "" && config.ValidFileID(msg.FileID) {
			localPath := filepath.Join(a.messages.filesDir, msg.FileID)
			invalidateImageRenderCacheForPath(localPath)
			if a.sidebar.previewRenderKey.path == localPath {
				a.sidebar.previewRenderKey = previewRenderKey{}
				a.sidebar.previewRenderValue = ""
			}
		}
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case previewRenderReadyMsg:
		// Off-thread preview render completed. Land the value on the
		// model only if the carried key still matches sidebar's
		// current desired key — stale results (user navigated away
		// while the Cmd was running) are dropped.
		if msg.key == a.sidebar.previewRenderKey {
			a.sidebar.previewRenderValue = msg.value
			a.sidebar.previewRendering = false
		}

	case UploadResultEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
		// `/upload` goroutine finished. Surface user-visible feedback
		// via the canonical statusBar (mutating it here works because
		// updateInner is value-receiver and the returned `a` becomes
		// the new model). On failure, the wrapped error from
		// SendRoomMessageFile / SendGroupMessageFile / SendDMMessageFile
		// carries the server-side rejection code+message (e.g.
		// "upload: file_too_large: File exceeds maximum size
		// (52428800 bytes)" for oversized uploads).
		if msg.Err != nil {
			a.statusBar.SetError("Upload failed: " + humanizeByteCountsInError(msg.Err.Error()))
		} else {
			a.statusBar.SetError("Uploaded: " + msg.Name)
		}
		// Continue listening for the next event.
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case DownloadResultEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
		// `o` or `p` goroutine finished. Status messages differ by
		// action because the success-visibility differs: `o` opens
		// the file in the OS app on success so the user sees the
		// result there (no status needed for success), while `p`
		// fires the rasterm-render chain async after download
		// completion — a status line confirms the work landed.
		switch msg.Action {
		case "open":
			if msg.Err != nil {
				a.statusBar.SetError("Open failed: " + humanizeByteCountsInError(msg.Err.Error()))
			}
		case "preview":
			if msg.Err != nil {
				a.statusBar.SetError("Preview failed: " + humanizeByteCountsInError(msg.Err.Error()))
			} else {
				a.statusBar.SetError("Preview ready: " + msg.Name)
			}
		}
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case SaveResultEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
		// SaveAttachmentDoMsg's copy goroutine finished. Both
		// outcomes get explicit feedback because the visible
		// artifact (the destination file) lives outside the TUI —
		// users need an in-app confirmation to know the copy
		// landed.
		if msg.Err != nil {
			a.statusBar.SetError("Save failed: " + humanizeByteCountsInError(msg.Err.Error()))
		} else {
			a.statusBar.SetError("Saved: " + msg.Dest)
		}
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case KeyChangeEvent:
		if msg.gen != a.connGen {
			return a, nil
		}
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
			a.keyWarning.Show(a.resolveDisplayName(msg.User), msg.OldFingerprint, msg.NewFingerprint)
		}
		// Continue listening for the next event.
		if a.client != nil && a.sidebar.msgCh != nil {
			cmds = append(cmds, waitForMsg(a.connGen, a.sidebar.msgCh, a.sidebar.errCh, a.sidebar.keyWarnCh, a.sidebar.attachReadyCh, a.sidebar.uploadResultCh, a.sidebar.downloadResultCh, a.sidebar.saveResultCh, a.sidebar.roomUpdatedCh, a.client.Done()))
		}

	case passphraseNeededMsg:
		if msg.gen != a.connGen {
			return a, nil
		}
		// Bind the dialog to this gen + keyPath so the emitted
		// PassphraseResultMsg can be matched back to the correct
		// connection + key. Without this, a switch-then-submit can
		// land the result on the wrong server's cache.
		a.passphrase.Show("", msg.gen, msg.keyPath)
		return a, nil

	case PassphraseResultMsg:
		if msg.gen != a.connGen {
			// Stale submission — user switched servers (or otherwise
			// invalidated the original gen) before finishing the
			// dialog. Drop without caching, without starting a
			// connect, and without quitting on Cancelled. The
			// current-gen flow owns its own modal lifecycle.
			return a, nil
		}
		if msg.Cancelled {
			return a, tea.Quit
		}
		// Cache passphrase by the key path the request was issued
		// for, not by a.cfg.KeyPath. Under the original implementation
		// a user who opened the dialog for server A's key, switched
		// to server B, then submitted, would cache A's passphrase
		// against B's key path. The keyPath carried on the result is
		// stable across the switch.
		keyPath := msg.keyPath
		if keyPath == "" {
			// Belt-and-braces — current path goes through Show with
			// non-empty keyPath, but tests can build a result by
			// hand. Fall back to a.cfg.KeyPath in that case rather
			// than caching against an empty string.
			keyPath = a.cfg.KeyPath
		}
		a.passphraseCache[keyPath] = msg.Passphrase
		// Drain any stale buffered value before sending fresh, so a
		// future OnPassphrase consumer (e.g. a reconnect that cleared
		// the cache) doesn't pick up a leftover byte slice from a
		// prior cycle. Buffer capacity is 1; without the drain a full
		// buffer would BLOCK the send and hang Update. See
		// passphrase-prompt-fix.md §"Note on passphraseCh buffering".
		select {
		case <-a.passphraseCh:
		default:
		}
		a.passphraseCh <- msg.Passphrase
		return a, a.startConnect()

	case reconnectAttemptMsg:
		if msg.gen != a.connGen {
			// Stale timer from a prior connect attempt — without
			// this drop, a stale reconnect tick can fire after a
			// server switch and start a connect against the current
			// server's state for the wrong reason.
			return a, nil
		}
		// Try to reconnect
		a.statusBar.SetReconnecting(msg.attempt, 0)
		return a, a.startConnect() // bump gen + connect

	case ErrMsg:
		if msg.gen != a.connGen {
			// Stale error from a superseded connect attempt — drop
			// without mutating connected/reconnectAttempt state,
			// without showing the connect-failed overlay, and
			// without scheduling a reconnect. The current generation
			// owns those decisions.
			return a, nil
		}
		a.err = msg.Err
		a.statusBar.SetConnected(false)
		// A current-server disconnect ends any in-flight display-name rename with
		// an unknown outcome: the set_profile may or may not have been written, and
		// no self-`profile`/error will arrive on this dead connection. Clear the
		// pending marker so Settings doesn't stick on "Saving…"; the reconnect
		// welcome self-`profile` re-renders the authoritative name (and still fires
		// the success toast via case "profile" if it matches renameAttempted).
		if a.renameInFlight {
			a.renameInFlight = false
			a.renameAttempted = ""
			if a.settings.IsVisible() {
				a.settings.SetDisplayNameRenamePending(false)
				a.settings.SetErrorNotice("Connection lost — display-name change status unknown")
			}
		}
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
			fp, pubKey := a.connectFailedKeyInfo()
			if fp == "" {
				// Client didn't initialize — read key directly
				fp = "unknown"
			}
			a.connectFailed.Show(msg.Err.Error(), fp, pubKey)
		}

	case ConnectFailedRetryMsg:
		// User pressed [r] from the connection failed overlay —
		// startConnect bumps the gen so stale events from the
		// previously-failed attempt are dropped.
		cmds = append(cmds, a.startConnect())
	}

	return a, tea.Batch(cmds...)
}

// connectFailedKeyInfo returns fingerprint + authorized key for the
// pending-approval overlay. On first connect failure, a.client is still nil
// (Connect() failed before connectedWithClient was dispatched), so we
// fall back to reading KeyPath+".pub" directly.
func (a App) connectFailedKeyInfo() (string, string) {
	if a.client != nil {
		fp := strings.TrimSpace(a.client.KeyFingerprint())
		pub := strings.TrimSpace(a.client.PublicKeyAuthorized())
		if fp != "" || pub != "" {
			return fp, pub
		}
	}
	return keyInfoFromPubPath(a.cfg.KeyPath)
}

func keyInfoFromPubPath(keyPath string) (string, string) {
	path := strings.TrimSpace(keyPath)
	if path == "" {
		return "", ""
	}
	path = config.ExpandUserPath(path)
	pubPath := path + ".pub"
	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		return "", ""
	}
	pubLine := strings.TrimSpace(string(pubData))
	if pubLine == "" {
		return "", ""
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		// Keep the full key text for copy/share even if parse fails.
		return "", pubLine
	}
	return ssh.FingerprintSHA256(pubKey), pubLine
}

// handleMouse processes mouse events — clicks and scroll wheel.
func (a App) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// ConnectFailed deliberately swallows mouse events without
	// routing them to any handler. The screen's keyboard shortcuts
	// ([r]/[c]/[q]) are the only valid action paths; absorbing
	// clicks lets the user mouse-drag-select the public-key text
	// as a clipboard fallback when OSC 52 isn't available (the
	// design intent documented at connectfailed.go View()).
	if a.connectFailed.IsVisible() {
		return a, nil
	}
	// Dialogs with mouse support — route clicks to the dialog.
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
	if a.newDeviceAlert.IsVisible() {
		var cmd tea.Cmd
		a.newDeviceAlert, cmd = a.newDeviceAlert.HandleMouse(msg)
		return a, cmd
	}
	if a.roomAttestation.IsVisible() {
		var cmd tea.Cmd
		a.roomAttestation, cmd = a.roomAttestation.HandleMouse(msg)
		return a, cmd
	}
	if a.settings.IsVisible() {
		var cmd tea.Cmd
		a.settings, cmd = a.settings.HandleMouse(msg)
		return a, cmd
	}
	if a.help.IsVisible() {
		a.help.Update(msg, a.width, a.height)
		return a, nil
	}
	if a.picker.IsVisible() {
		var cmd tea.Cmd
		a.picker, cmd = a.picker.HandleMouse(msg)
		return a, cmd
	}

	// Other overlays are keyboard-only. Listing them here consumes any
	// mouse event (click / wheel / drag) that arrives while the overlay
	// is up, so clicks landing "outside" the visible dialog footprint
	// don't bleed through to the sidebar / messages / compose input
	// underneath. Any new modal with the same behaviour must be added
	// to this list or it will silently pass clicks through.
	if a.search.IsVisible() || a.quickSwitch.IsVisible() || a.threadPanel.IsVisible() || a.newConv.IsVisible() ||
		a.emojiPicker.IsVisible() || a.infoPanel.IsVisible() || a.pendingPanel.IsVisible() ||
		a.verify.IsVisible() || a.keyWarning.IsVisible() ||
		a.quitConfirm.IsVisible() || a.leaveConfirm.IsVisible() || a.leaveRoomConfirm.IsVisible() ||
		a.deleteDMConfirm.IsVisible() || a.deleteGroupConfirm.IsVisible() || a.deleteRoomConfirm.IsVisible() ||
		a.addConfirm.IsVisible() || a.kickConfirm.IsVisible() || a.promoteConfirm.IsVisible() ||
		a.demoteConfirm.IsVisible() || a.transferConfirm.IsVisible() || a.unverifyConfirm.IsVisible() ||
		a.auditOverlay.IsVisible() || a.membersOverlay.IsVisible() ||
		a.lastAdminPicker.IsVisible() ||
		a.saveAttachment.IsVisible() ||
		a.contextMenu.IsVisible() || a.memberMenu.IsVisible() ||
		a.statusPicker.IsVisible() || a.picker.IsVisible() {
		return a, nil
	}

	// Compute layout fresh from current window dimensions + panel
	// visibility. See computeLayout's doc-comment for the bug history
	// — pre-fix the layout was assigned in View() with a value
	// receiver, so a.layout was always zero-valued and HitTest
	// returned "" for every coordinate.
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())

	x := msg.X
	y := msg.Y

	switch msg.Button {
	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionRelease {
			return a.handleMouseClick(x, y)
		}

	case tea.MouseButtonWheelUp:
		panel := layout.HitTest(x, y)
		if panel == "messages" {
			// Wheel scrolls the viewport WITHOUT moving the cursor — this
			// decoupling matches Slack/Discord behaviour and was the
			// explicit user request: "when using the mouse i may not
			// necessarily have a message highlighted before i scroll".
			// Cursor movement remains tied to up/down arrow keys.
			atTop := a.messages.ScrollUp(3)
			if atTop && !a.messages.loadingHistory && len(a.messages.messages) > 0 {
				return a, a.messages.requestHistory()
			}
		} else if panel == "sidebar" {
			if a.sidebar.cursor > 0 {
				a.sidebar.cursor--
			}
		}

	case tea.MouseButtonWheelDown:
		panel := layout.HitTest(x, y)
		if panel == "messages" {
			a.messages.ScrollDown(3)
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
	// A click anywhere dismisses an active nav-mode popup and is swallowed
	// (click-outside-to-close, decision 2). This is the click-only path
	// (left release), so mere cursor motion never dismisses the popup.
	if a.navMode {
		a.exitNavMode()
		return a, nil
	}
	// Compute layout fresh — see computeLayout's doc-comment for why
	// we don't read from a.layout (it was a stale-state bug). All
	// HitTest / SidebarItemAt / MessageItemAt / MemberItemAt /
	// MessagesY0 reads in this function use this local layout.
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())

	panel := layout.HitTest(x, y)

	switch panel {
	case "sidebar":
		a.focus = FocusSidebar
		// Walk the section layout to map click row → cursor index.
		// Pre-2026-05-05 used layout.SidebarItemAt which returned
		// `y - 1`, ignoring the "Rooms"/"Messages"/"DMs" headers and
		// the blank separators between sections — so clicks selected
		// the wrong item, and clicks on header/blank rows still
		// returned a (wrong) index.
		visualRow := y - layout.SidebarY0 - 1
		sidebarInnerHeight := layout.SidebarY1 - layout.SidebarY0 - 2
		idx := a.sidebar.CursorAtRow(visualRow, sidebarInnerHeight)
		if idx >= 0 && idx < a.sidebar.totalItems() {
			a.sidebar.cursor = idx
			a.sidebar.updateSelection()
			// Switch to selected room/conversation
			if a.sidebar.SelectedRoom() != a.messages.room || a.sidebar.SelectedGroup() != a.messages.group || a.sidebar.SelectedDM() != a.messages.dm {
				a.switchMessageContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), a.sidebar.SelectedDM())
				a.syncMessagesLeftState()
				a.messages.LoadFromDB(a.client)
				a.syncPinnedBarForContext()
				a.refreshMessageContent()
				if a.memberPanel.IsVisible() {
					a.memberPanel.Refresh(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
					// V8: Refresh renders from the local cache; no fetch.
					a.input.SetMembers(a.activeMemberEntries())
					a.input.SetNonMembers(a.activeNonMemberEntries())
				}
				a.sendReadReceipt()
			}
		}

	case "messages":
		a.focus = FocusMessages

		// Layout inside the messages panel (top → bottom):
		//   row 0                         top border
		//   rows 1..headerLines           room header (title / topic / blank)
		//   rows ..pinnedBarRows          pinned-bar (only if HasPins)
		//   rows ..viewport.Height        scrollable viewport
		//   last row                      bottom border
		//
		// `relY` is the y coord relative to the panel's first content
		// row (= y - MessagesY0 - 1 for the top border). Pinned bar
		// sits AFTER the header — pre-2026-05-05 click handler treated
		// pinnedBarRows as starting at relY=0 which is now the room
		// header.
		headerLines := a.messages.HeaderRowsForHitTest(layout.MessagesWidth)
		pinnedBarRows := a.messages.PinnedBarRowsForHitTest(layout.MessagesWidth)
		relY := y - layout.MessagesY0 - 1 // 0-indexed inside panel content area

		// Click on the pinned bar.
		if a.pinnedBar.HasPins() && relY >= headerLines && relY < headerLines+pinnedBarRows {
			pinRel := relY - headerLines // 0-indexed within pinned bar
			if !a.pinnedBar.expanded {
				// Click on collapsed bar — expand
				a.pinnedBar.Toggle()
			} else {
				// Expanded: map visual row -> pin index using the same
				// rendered line shape (including soft-wrap) as the pinned
				// bar view. Header/footer rows are non-selectable.
				pinIdx := a.pinnedBar.PinIndexAtVisualRow(pinRel, layout.MessagesWidth-2)
				if pinIdx >= 0 && pinIdx < len(a.pinnedBar.pins) {
					id := a.pinnedBar.pins[pinIdx].ID
					a.pinnedBar.cursor = pinIdx
					a.pinnedBar.expanded = false
					a.messages.ScrollToMessage(id)
					a.focus = FocusMessages
				}
			}
			return a, nil
		}

		// Click maps to a message via the rowMap built during the
		// most recent RefreshContent. Subtract panel top border (1)
		// + header rows + pinned-bar rows to get the viewport row.
		// MessageAtViewportRow then folds in viewport.YOffset to
		// handle scrolled-content correctly.
		//
		// Click only SELECTS the message (highlights via cursor) and
		// switches focus — Enter is the trigger that opens the
		// context menu, via the existing keyboard `enter` path in
		// MessagesModel.Update which emits MessageAction{open_menu}.
		viewportRow := relY - headerLines - pinnedBarRows
		idx := a.messages.MessageAtViewportRow(viewportRow)
		if idx >= 0 && idx < len(a.messages.messages) {
			a.messages.cursor = idx
			a.focus = FocusMessages
			// Collapse the pinned bar so its expanded-state key grab (top of the
			// KeyMsg handler) releases — otherwise the just-selected message
			// couldn't be replied to / edited / actioned by keyboard.
			a.pinnedBar.expanded = false
		}

	case "members":
		if a.memberPanel.IsVisible() {
			a.focus = FocusMembers
			a.memberPanel.SetFocused(true)
			// Finding 1: refresh live membership before mapping the click to a
			// row, so a click can't select a stale row when the rendered set
			// changed. Persistent path (handleMouseClick returns the model).
			a.refreshMemberPanelLiveRowsAndCompletion()
			idx := layout.MemberItemAt(y)
			if idx >= 0 && idx < len(a.memberPanel.members) {
				a.memberPanel.cursor = idx
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
//
// gen is captured at schedule time, NOT looked up when the timer
// fires. When the resulting reconnectAttemptMsg arrives, updateInner
// drops it if a.connGen has moved on (server switch, manual reconnect,
// retry-from-overlay). Without this, a stale timer from a prior
// connect could fire after a switch and start a connect against the
// new server's state for the wrong reason.
func (a App) reconnect(attempt int) tea.Cmd {
	delay := reconnectDelay(attempt)
	gen := a.connGen
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return reconnectAttemptMsg{attempt: attempt, gen: gen}
	})
}

type reconnectAttemptMsg struct {
	attempt int
	// gen is the connection generation at the time the reconnect
	// timer was scheduled. When the timer fires we drop the message
	// if the generation has moved on — without this, a stale
	// reconnect timer from a prior connection could fire after a
	// server switch and start a connection against the new server's
	// state for the wrong reason.
	gen uint64
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

// abandonActiveHistoryRequest drops the active visible history request from the
// current client's send queue before context/server ownership changes clear the
// corr_id. SetContext still clears the corr_id defensively, but this helper
// prevents the background retry driver from resending an abandoned scroll-back.
func (a *App) abandonActiveHistoryRequest() {
	corrID := a.messages.activeHistoryCorrID
	if corrID != "" && a.client != nil {
		if entry := a.client.SendQueue().Get(corrID); entry != nil && entry.Verb == "history" {
			a.client.SendQueue().Drop(corrID)
		}
	}
	if corrID != "" || a.messages.loadingHistory {
		a.messages.abortHistoryRequest()
	}
}

func (a *App) switchMessageContext(room, group, dm string) {
	a.abandonActiveHistoryRequest()
	a.messages.SetContext(room, group, dm)
	a.onContextSwitch()
}

// onContextSwitch runs every app-layer side effect that must fire
// when the messages context changes — sidebar navigation, quick
// switch, search jump, inline creation flows, or terminal context
// clears triggered by `room_deleted` / `group_deleted` / `dm_left` /
// `room_retired` broadcasts and by self-leave echoes. Called by
// switchMessageContext AFTER `a.messages.SetContext(...)` so the messages
// model already reflects the new context when the side effects run.
//
// Side effects in order:
//
//  1. **Clear compose state** from the previous context: draft text,
//     reply target, and edit mode. Without this, reply/edit metadata
//     can leak across context boundaries (room/group/DM), causing sends
//     to carry stale intent into the wrong conversation.
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
// the topic-resolution step and had partial compose cleanup piggybacked
// onto it. That was a bug waiting to happen because the
// cleared-context call sites (`SetContext("", "", "")` for a deleted
// or retired room, left group, etc.) never called `applyRoomTopic`,
// so compose state could be left hanging with stale targets in a
// now-non-existent context. Every production context switch now goes through
// switchMessageContext, which closes the gap.
func (a *App) onContextSwitch() {
	// (1) Clear compose state on every context switch. This prevents
	// draft/reply/edit carry-over across rooms/groups/DMs.
	a.input.ResetComposeState()
	// Pinned-bar expansion is context-local UX. Collapsing on switch
	// avoids stealing keyboard focus in the new room/DM/group.
	a.pinnedBar.expanded = false
	// (2) Apply the new room's topic (or clear it).
	if a.client == nil || a.messages.room == "" {
		a.messages.SetRoomTopic("")
	} else {
		a.messages.SetRoomTopic(a.client.DisplayRoomTopic(a.messages.room))
	}
	// NB: messages-content refresh is NOT done here — onContextSwitch
	// is typically called BEFORE LoadFromDB in the existing flow
	// (SetContext clears messages, onContextSwitch sets topic,
	// LoadFromDB populates from store). A refresh here would push an
	// empty slice into the viewport. Each caller refreshes itself
	// after LoadFromDB via a.refreshMessageContent().
	a.syncPinnedBarForContext()
}

// refreshMessageContent rebuilds the messages-pane scrollable content
// from the current messages slice and pushes it to the viewport. Called
// from anywhere that mutates a.messages.messages (or changes width
// affecting line wrapping). Width is computed from the same
// computeLayout source-of-truth used for mouse hit testing, so render
// dimensions stay coherent with click coordinates.
func (a *App) refreshMessageContent() {
	if a.width == 0 || a.height == 0 {
		return // not yet sized
	}
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	contentWidth := layout.MessagesWidth
	if contentWidth < 1 {
		contentWidth = 1
	}
	a.messages.RefreshContent(contentWidth)
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
//
// Side effect: scrolls the messages pane so the message being edited
// is visible, matching the affordance of "you can see what you're
// editing." The user pressed Up from the input bar — they expect
// the input to populate AND the messages pane to surface the target.
// Without the scroll, edit mode could activate against a message
// that's currently scrolled off-screen and the user has no visual
// confirmation of which message is being edited (the input just
// fills with text). Focus stays on the input — only the cursor and
// viewport position move.
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
		// Found the user's most recent editable message. Populate the
		// input AND scroll the messages pane to show the target. The
		// duplicate ID lookup inside ScrollToMessage is cheap (linear
		// scan over the same in-memory list) and keeps the side-effect
		// localized to one method call.
		a.input.EnterEditMode(msg.ID, msg.Body)
		a.messages.ScrollToMessage(msg.ID)
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

// refreshInfoPanelLiveRows re-reads the current member-ID set + live display
// state into the open info panel for its active context (Finding 1). App owns
// the client + sidebar presence/status + context, so the refresh lives here —
// the panel does not reach outward into global state. Called immediately
// before the panel's Update and View; SetLiveMemberIDs preserves
// cursor/scroll and the selected user. On the View path (viewBody is a value
// receiver) this only refreshes the rendered copy, which is fine for paint —
// the Update + mouse paths handle persistent action state.
func (a *App) refreshInfoPanelLiveRows() {
	if a.client == nil || !a.infoPanel.IsVisible() {
		return
	}
	a.infoPanel.SetLiveMemberIDs(a.client, a.sidebar.online, a.sidebar.status)
}

// refreshMemberPanelLiveRows re-reads live membership into the visible member
// panel for the current context, preserving the selected user (Finding 1).
// Mirrors refreshInfoPanelLiveRows. View-path use only: it does NOT refresh the
// @-completion sources, because viewBody is a value receiver and must stay
// side-effect free for persistent App state. Persistent paths (keyboard Update,
// mouse row hit) use refreshMemberPanelLiveRowsAndCompletion instead.
func (a *App) refreshMemberPanelLiveRows() {
	if a.client == nil || !a.memberPanel.IsVisible() {
		return
	}
	a.memberPanel.RefreshPreservingSelection(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
}

// refreshMemberPanelLiveRowsAndCompletion is the persistent-path variant: it
// refreshes the member rows AND the @-completion sources (which read from the
// now-refreshed member panel, so order matters). Use only on paths that return
// the model (Update, mouse hit) — never the View path.
func (a *App) refreshMemberPanelLiveRowsAndCompletion() {
	if a.client == nil || !a.memberPanel.IsVisible() {
		return
	}
	a.memberPanel.RefreshPreservingSelection(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
	a.input.SetMembers(a.activeMemberEntries())
	a.input.SetNonMembers(a.activeNonMemberEntries())
}

// syncPinnedBarForContext applies the cached pin IDs for the active room to
// the pinned bar. Non-room contexts (group / DM / empty) clear the pinned bar.
func (a *App) syncPinnedBarForContext() {
	a.ensureRoomPinsMap()
	if a.messages.room == "" {
		a.pinnedBar.SetPins("", nil, nil)
		return
	}
	ids := a.roomPins[a.messages.room]
	a.pinnedBar.SetPins(a.messages.room, ids, a.messages.messages)
}

func (a *App) ensureRoomPinsMap() {
	if a.roomPins == nil {
		a.roomPins = make(map[string][]string)
	}
}

func appendUniquePinID(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removePinID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// syncLocalIdentityUI refreshes all UI fields that present the local user's
// identity. At first connect, DisplayName may briefly fall back to raw userID
// until profile frames arrive; calling this after server messages updates the
// status bar and mention-highlighting identity once the profile cache is warm.
func (a *App) syncLocalIdentityUI() {
	if a.client == nil {
		return
	}
	userID := a.client.UserID()
	if userID == "" {
		return
	}
	name := a.client.DisplayName(userID)
	a.messages.currentUser = name
	a.messages.currentUserID = userID
	a.statusBar.SetUser(name, a.client.IsAdmin())
}

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

// sendReadReceipt sends a read receipt for the latest message in the active room/conversation.
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

func (a *App) sendUnreactForMessage(msg DisplayMessage, emoji string) {
	if a.client == nil {
		return
	}
	user := a.client.UserID()
	if emoji == "" {
		emojis := msg.UserEmojis(user)
		if len(emojis) == 0 {
			a.statusBar.SetError("No reactions to remove")
			return
		}
		emoji = emojis[0]
	}
	ids := msg.UserReactionIDs(user, emoji)
	if len(ids) == 0 {
		a.statusBar.SetError("Reaction already removed")
		return
	}
	if err := a.client.SendUnreact(ids[0], msg.Room, msg.Group, msg.DM); err != nil {
		a.statusBar.SetError("Unreact failed: " + err.Error())
	}
}

func (a *App) isMessagePinned(messageID string) bool {
	for _, pin := range a.pinnedBar.pins {
		if pin.ID == messageID {
			return true
		}
	}
	return false
}

func (a *App) sendUnpin(messageID string) {
	if a.client == nil || a.messages.room == "" {
		return
	}
	a.client.Enc().Encode(protocol.Unpin{
		Type: "unpin",
		Room: a.messages.room,
		ID:   messageID,
	})
}

// switchToSidebarSelection switches the messages context to whatever the
// sidebar currently has selected. Used by Alt+Up/Down and quick switch.
func (a *App) switchToSidebarSelection() {
	if a.sidebar.SelectedRoom() == a.messages.room && a.sidebar.SelectedGroup() == a.messages.group && a.sidebar.SelectedDM() == a.messages.dm {
		return
	}
	a.switchMessageContext(a.sidebar.SelectedRoom(), a.sidebar.SelectedGroup(), a.sidebar.SelectedDM())
	a.syncMessagesLeftState()
	a.messages.LoadFromDB(a.client)
	a.syncPinnedBarForContext()
	a.refreshMessageContent()
	if a.memberPanel.IsVisible() {
		a.memberPanel.Refresh(a.messages.room, a.messages.group, a.messages.dm, a.client, a.sidebar.online, a.sidebar.status)
		// V8: Refresh renders from the local cache; no fetch.
		a.input.SetMembers(a.activeMemberEntries())
		a.input.SetNonMembers(a.activeNonMemberEntries())
	}
	a.sendReadReceipt()
}

func (a *App) setPendingFocusCreatedDM(other string) {
	a.pendingFocusCreatedDMOther = other
	a.pendingFocusCreatedGroupName = ""
	a.pendingFocusCreatedGroupMems = nil
	a.pendingFocusCreatedSetAt = time.Now()
}

func (a *App) setPendingFocusCreatedGroup(members []string, name string) {
	a.pendingFocusCreatedDMOther = ""
	a.pendingFocusCreatedGroupName = strings.TrimSpace(name)
	a.pendingFocusCreatedGroupMems = append(a.pendingFocusCreatedGroupMems[:0], members...)
	a.pendingFocusCreatedSetAt = time.Now()
}

func (a *App) clearPendingFocusCreated() {
	a.pendingFocusCreatedDMOther = ""
	a.pendingFocusCreatedGroupName = ""
	a.pendingFocusCreatedGroupMems = nil
	a.pendingFocusCreatedSetAt = time.Time{}
}

func (a *App) pendingFocusCreatedExpired() bool {
	if a.pendingFocusCreatedSetAt.IsZero() {
		return false
	}
	return time.Since(a.pendingFocusCreatedSetAt) > pendingFocusCreatedTTL
}

func (a *App) shouldFocusCreatedDM(m protocol.DMCreated) bool {
	if a.pendingFocusCreatedDMOther == "" {
		return false
	}
	if a.pendingFocusCreatedExpired() {
		a.clearPendingFocusCreated()
		return false
	}
	if a.client == nil {
		a.clearPendingFocusCreated()
		return false
	}
	self := a.client.UserID()
	other := a.pendingFocusCreatedDMOther
	hasSelf := false
	hasOther := false
	for _, member := range m.Members {
		if member == self {
			hasSelf = true
		}
		if member == other {
			hasOther = true
		}
	}
	if hasSelf && hasOther {
		a.clearPendingFocusCreated()
		return true
	}
	return false
}

func (a *App) shouldFocusCreatedGroup(m protocol.GroupCreated) bool {
	if len(a.pendingFocusCreatedGroupMems) == 0 {
		return false
	}
	if a.pendingFocusCreatedExpired() {
		a.clearPendingFocusCreated()
		return false
	}
	if a.client == nil {
		a.clearPendingFocusCreated()
		return false
	}
	if strings.TrimSpace(m.Name) != a.pendingFocusCreatedGroupName {
		return false
	}
	expected := make(map[string]bool, len(a.pendingFocusCreatedGroupMems)+1)
	expected[a.client.UserID()] = true
	for _, member := range a.pendingFocusCreatedGroupMems {
		expected[member] = true
	}
	if len(m.Members) != len(expected) {
		return false
	}
	for _, member := range m.Members {
		if !expected[member] {
			return false
		}
	}
	a.clearPendingFocusCreated()
	return true
}

func (a *App) focusSidebarGroup(groupID string) {
	for i, g := range a.sidebar.groups {
		if g.ID != groupID {
			continue
		}
		a.sidebar.cursor = len(a.sidebar.rooms) + i
		a.sidebar.updateSelection()
		a.switchToSidebarSelection()
		return
	}
}

func (a *App) focusSidebarDM(dmID string) {
	for i, dm := range a.sidebar.dms {
		if dm.ID != dmID {
			continue
		}
		a.sidebar.cursor = len(a.sidebar.rooms) + len(a.sidebar.groups) + i
		a.sidebar.updateSelection()
		a.switchToSidebarSelection()
		return
	}
}

// focusSidebarDMForCompose / focusSidebarGroupForCompose switch to a
// just-created (or reused) conversation AND land the cursor in the compose
// input, so the natural next action after "message this user" / creating a group
// is to type — no Tab/Esc/click needed. These are deliberately separate from the
// plain focusSidebar* helpers (and from the generic switchMessageContext /
// switchToSidebarSelection) so ordinary sidebar navigation, quick switch, and
// search jump keep their existing focus semantics and are never force-focused
// into compose. Only the matched local-create result branches use these (see
// created-conversation-input-focus.md). The member panel, if still visible, is
// unfocused so it does not keep a highlighted cursor behind the input.
func (a *App) focusSidebarDMForCompose(dmID string) {
	a.focusSidebarDM(dmID)
	a.focus = FocusInput
	a.memberPanel.SetFocused(false)
}

func (a *App) focusSidebarGroupForCompose(groupID string) {
	a.focusSidebarGroup(groupID)
	a.focus = FocusInput
	a.memberPanel.SetFocused(false)
}

func (a *App) showInfoPanelForContext(room, group, dm string) {
	if a.client == nil {
		return
	}
	if room != "" {
		// V8: ShowRoom populates from the local member cache; no fetch.
		a.infoPanel.ShowRoom(room, a.client, a.sidebar.online, a.sidebar.status)
		return
	}
	if group != "" {
		a.infoPanel.ShowGroup(group, a.client, a.sidebar.online, a.sidebar.status)
		return
	}
	if dm != "" {
		a.infoPanel.ShowDM(dm, a.client, a.sidebar.online, a.sidebar.status)
		return
	}
	a.statusBar.SetError("No active room, group, or DM")
}

func (a *App) showMemberPanelForContext(room, group, dm string) {
	if a.client == nil {
		a.statusBar.SetError("/members unavailable - not connected")
		return
	}
	if room == "" && group == "" && dm == "" {
		a.statusBar.SetError("No active room, group, or DM")
		return
	}
	// /members is a toggle: close when already open.
	if a.memberPanel.IsVisible() {
		a.memberPanel.Toggle()
		if a.focus == FocusMembers {
			a.focus = FocusMessages
			a.memberPanel.SetFocused(false)
		}
		a.refreshMessageContent()
		return
	}
	a.memberPanel.Toggle()
	// V8: Refresh renders room members from the local cache; no fetch.
	a.memberPanel.Refresh(room, group, dm, a.client, a.sidebar.online, a.sidebar.status)
	a.input.SetMembers(a.activeMemberEntries())
	a.input.SetNonMembers(a.activeNonMemberEntries())
	// Member panel visibility changes pane widths; re-wrap content.
	a.refreshMessageContent()
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
	// V8 dispatch-layer read-only gate — the authoritative single source of
	// truth. The keypress path gates too (for immediate feedback), but any
	// SlashCommandMsg that reaches dispatch via another emitter (picker,
	// menu, future programmatic path) is still held to the allow-list in a
	// read-only ROOM. Scope-locked to rooms (a.messages.room != "") so left
	// groups keep their own behavior. sc.Command is already lowercased by
	// handleCommand, so commandAllowedInReadOnlyRoom matches case-insensitively.
	readOnlyRoom := a.messages.room != "" &&
		(a.messages.IsRoomRetired() || a.messages.IsLeft())
	if readOnlyRoom && !commandAllowedInReadOnlyRoom(sc.Command) {
		a.statusBar.SetError(`"/delete", "/search", and "/help" are available`)
		return
	}

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
	case "/unrecognized":
		// Mode-A router robustness (Verb sentinel synthesized by
		// InputModel.handleCommand's `default:`). sc.Arg carries the
		// raw verb the user typed; surface it so the message is
		// actionable. The input box is intentionally NOT reset in
		// the `enter` branch on this path, so the user can fix the
		// typo without retyping. /? is the discoverable reference.
		bad := strings.TrimSpace(sc.Arg)
		if bad == "" {
			bad = "(empty)"
		}
		a.statusBar.SetError("Unknown command: " + bad + " — type /? for the list")
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
		//
		// #1c/§9 step 3: bare `/verify` opens the shared picker (known
		// users not yet verified, any context, retired excluded, #9a);
		// typed `/verify @user` keeps its existing direct path
		// (typed-bypass coexists) and never opens the picker. The
		// post-resolution step for both paths is `a.verify.Show(ID,
		// client)` — already a clean ID seam (#3, no refactor needed).
		if strings.TrimSpace(sc.Arg) == "" {
			a.openPicker(PickerRequest{
				Verb:       "/verify",
				Source:     PickerSourceSlash,
				Room:       sc.Room,
				Group:      sc.Group,
				DM:         sc.DM,
				ShowFilter: true, // #7: large all-users set
			})
			return
		}
		if a.client != nil {
			targetID, ok := a.resolveUserByName(sc.Arg)
			if !ok {
				a.statusBar.SetError("unknown user: " + sc.Arg)
			} else {
				// State guard (matches the picker candidate filter):
				// /verify on an already-verified user surfaces a
				// friendly status instead of re-opening the safety-
				// number flow. The picker hides these from the bare
				// path; this is the typed-path equivalent.
				if st := a.client.Store(); st != nil {
					if info, _ := st.GetPinnedKeyInfo(targetID); info.Verified {
						a.statusBar.SetError(a.resolveDisplayName(targetID) + " is already verified")
						return
					}
				}
				a.verify.Show(targetID, a.client)
			}
		}
	case "/unverify":
		// Phase 21 F29 closure — same resolution as /verify.
		//
		// §9 step 4 / #8: bare `/unverify` opens the picker (currently
		// verified users, exclude retired/self); typed `/unverify
		// @user` keeps its existing direct path (typed-bypass coexists,
		// #1c). The NEW UnverifyConfirmModel is applied to BOTH paths
		// — typed previously cleared verification silently in one
		// keystroke ("Verification removed for X"); now both forms
		// open the confirm first and only clear on y/enter. This is
		// the one net-new dialog in the entire picker effort (#8).
		if strings.TrimSpace(sc.Arg) == "" {
			a.openPicker(PickerRequest{
				Verb:       "/unverify",
				Source:     PickerSourceSlash,
				Room:       sc.Room,
				Group:      sc.Group,
				DM:         sc.DM,
				ShowFilter: true, // #7
			})
			return
		}
		if a.client != nil {
			targetID, ok := a.resolveUserByName(sc.Arg)
			if !ok {
				a.statusBar.SetError("unknown user: " + sc.Arg)
			} else {
				// State guard (matches the picker candidate filter):
				// /unverify on someone who isn't verified surfaces a
				// friendly status instead of opening a confirm for a
				// no-op. Covers both "pinned but verified=0" and "no
				// pinned key at all" — neither is meaningfully
				// "unverifiable." The picker hides these from the
				// bare path; this is the typed-path equivalent.
				verified := false
				if st := a.client.Store(); st != nil {
					if info, _ := st.GetPinnedKeyInfo(targetID); info.Verified {
						verified = true
					}
				}
				if !verified {
					a.statusBar.SetError(a.resolveDisplayName(targetID) + " is not verified")
					return
				}
				a.unverifyConfirm.Show(targetID, a.resolveDisplayName(targetID))
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
		//
		// #1c/#12: bare `/whois` opens the shared picker (all users,
		// cross-context, retired included + marked, #9a); typed
		// `/whois @user` keeps its existing direct path (typed-bypass
		// coexists) and never opens the picker.
		if strings.TrimSpace(sc.Arg) == "" {
			a.openPicker(PickerRequest{
				Verb:       "/whois",
				Source:     PickerSourceSlash,
				Room:       sc.Room,
				Group:      sc.Group,
				DM:         sc.DM,
				ShowFilter: true, // #7: large all-users set
			})
			return
		}
		a.handleWhoisCommand(sc.Arg)
	case "/search":
		a.search.Show()
	case "/settings":
		displayName := ""
		if a.client != nil {
			// Resolve nanoid → human display name. See openSettingsPanel
			// for the full rationale (settings shows this in the
			// "Display name" row, raw nanoid was wrong).
			displayName = a.client.DisplayName(a.client.UserID())
		}
		a.settings.Show(a.appConfig, a.configDir, displayName, a.serverIdx)
		a.settings.SetDisplayNameRenamePending(a.renameInFlight)
	case "/setstatus":
		// Locked-set status — the only valid arguments are the
		// constants in sidebar.go (available / away / busy). Bare
		// /setstatus opens the picker for discoverability + arrow-
		// key selection; the typed-arg form bypasses the picker and
		// still works for power users / scripted sessions.
		//
		// Sends SetStatus to the server which echoes it back via
		// the Presence broadcast that other clients (including ours)
		// pick up; we also optimistically update local state so
		// the dot color flips immediately rather than waiting for
		// the server round-trip.
		if a.client == nil {
			return
		}
		arg := strings.ToLower(strings.TrimSpace(sc.Arg))
		if arg == "" {
			// No arg → open the picker. Pre-position cursor on the
			// current status so a quick Enter is a no-op rather
			// than a silent reset to Available.
			a.statusPicker.Show(a.sidebar.status[a.client.UserID()])
			return
		}
		switch arg {
		case StatusAvailable, StatusAway, StatusBusy:
			// valid — fall through to send
		default:
			a.statusBar.SetError("Unknown status: " + arg + " (try: available, away, busy)")
			return
		}
		a.client.Enc().Encode(protocol.SetStatus{
			Type: "set_status",
			Text: arg,
		})
		self := a.client.UserID()
		a.sidebar.SetStatus(self, arg)
		online, ok := a.sidebar.online[self]
		if !ok {
			online = true
		}
		a.memberPanel.SetPresence(self, online, arg)
		a.statusBar.SetError("Status set to " + arg)
	case "/typing":
		// Local UX toggle for typing indicators. THIS IS NOT A
		// SECURITY CONTROL — the server is authoritative and
		// independently authorizes every typing relay (sshkey-chat
		// handleTyping; see typing-relay-authz-hardening). Any client
		// can speak the protocol, so this flag only governs whether
		// *our* client emits typing pings and renders received ones.
		// Persisted to config.toml so the preference survives a
		// restart, consistent with the other local TUI prefs
		// (HelpShown, muted, bell).
		//
		// Bare `/typing` toggles; `/typing on` / `/typing off` set
		// explicitly. Anything else is a usage error.
		if a.appConfig == nil {
			a.statusBar.SetError("Typing toggle unavailable (no config)")
			return
		}
		arg := strings.ToLower(strings.TrimSpace(sc.Arg))
		var disabled bool
		switch arg {
		case "":
			disabled = !a.appConfig.Notifications.TypingDisabled
		case "on":
			disabled = false
		case "off":
			disabled = true
		default:
			a.statusBar.SetError("Usage: /typing [on|off]")
			return
		}
		a.appConfig.Notifications.TypingDisabled = disabled
		a.input.SetTypingDisabled(disabled)
		if err := config.Save(a.configDir, a.appConfig); err != nil {
			a.statusBar.SetError("Typing pref not saved: " + err.Error())
			return
		}
		if disabled {
			a.statusBar.SetError("Typing indicators off")
		} else {
			a.statusBar.SetError("Typing indicators on")
		}
	case "/help", "/?":
		// The help panel lists ALL slash commands unconditionally;
		// admin/context restrictions are labelled in each command's
		// description, not hidden behind a role/context gate (the
		// old context-aware showAdminCommands path was removed — see
		// help.go — so the reference stays complete regardless of
		// role or active context). /? is an advertised alias
		// (rendered in the panel as "/help or /?").
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
	case "/mute":
		// Toggle mute on the active context (room/group/dm). Same
		// downstream as the info-panel `m` key (MuteToggleMsg →
		// applyMuteState). Fixes the prior empty router case
		// (Mode-A silent no-op: typed `/mute` did nothing).
		var target, kind string
		switch {
		case sc.Room != "":
			target, kind = sc.Room, "room"
		case sc.Group != "":
			target, kind = sc.Group, "group"
		case sc.DM != "":
			target, kind = sc.DM, "dm"
		default:
			a.statusBar.SetError("/mute needs an active room, group, or DM")
			return
		}
		a.applyMuteState(target, kind, !a.muted[target])
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
		if a.client == nil {
			a.statusBar.SetError("Not connected")
			return
		}
		if sc.Room == "" && sc.Group == "" && sc.DM == "" {
			a.statusBar.SetError("No active room or group")
			return
		}
		body := filepath.Base(path)
		// Synchronous status update — this mutation propagates because
		// handleSlashCommand is pointer-receiver, so the change persists
		// via Update's returned model.
		a.statusBar.SetError("Uploading " + body + "...")
		// Capture by value: client and the result channel are reference
		// types, so the goroutine doesn't need to dereference the
		// (about-to-go-stale) `a *App` pointer after Update returns.
		// Goroutine pushes a UploadResultEvent through the channel;
		// waitForMsg picks it up and delivers it as a tea.Msg, which
		// updateInner's `case UploadResultEvent` handler turns into a
		// canonical-state SetError call. Pre-fix path mutated statusBar
		// directly from the goroutine — those writes landed on the
		// orphaned local-to-updateInner App copy and never reached
		// the rendered model, so server-side rejections (oversized,
		// missing_hash, etc.) failed completely silently.
		c := a.client
		room := sc.Room
		group := sc.Group
		dm := sc.DM
		resultCh := a.sidebar.uploadResultCh
		// Capture gen at launch time so an upload kicked off
		// against server A doesn't surface its completion status
		// against server B after a switch.
		gen := a.connGen
		go func() {
			var err error
			switch {
			case room != "":
				err = c.SendRoomMessageFile(room, body, path, "", nil)
			case group != "":
				err = c.SendGroupMessageFile(group, body, path, "", nil)
			case dm != "":
				err = c.SendDMMessageFile(dm, body, path, "", nil)
			}
			// Non-blocking send — buffer of 16 plus the rarity of
			// concurrent uploads makes drops effectively impossible,
			// but `default` keeps us from stalling the goroutine on
			// a wedged channel.
			if resultCh != nil {
				select {
				case resultCh <- UploadResultEvent{Name: body, Err: err, gen: gen}:
				default:
				}
			}
		}()
	case "/rename":
		// Phase 14: client-side admin pre-check. Non-admins get a
		// friendly local rejection; admins pass through to the server
		// which also enforces the gate. Without this pre-check, the
		// server rejection surfaces as ErrUnknownGroup (byte-identical
		// privacy), which reads as a confusing "you are not a member".
		//
		// (2026-05-20) bare-no-arg and non-group cases now surface
		// friendly status messages instead of the previous silent
		// drop (Mode-B class fix — matches the /role + group-admin
		// verbs treatment from the picker work).
		if sc.Group == "" {
			a.statusBar.SetError("/rename only works inside a group")
			return
		}
		if strings.TrimSpace(sc.Arg) == "" {
			a.statusBar.SetError("Usage: /rename <name>")
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
		// §9 step 5: bare verb → shared picker; `verb @user` → existing
		// direct path via handleGroupAdminCommand (typed-bypass
		// coexists, #1c). Bare-branch gates (group context + local
		// admin) mirror handleGroupAdminCommand's own messages so the
		// two branches read identically; bare → invalid-context /
		// non-admin surfaces a status, never opens an empty picker
		// (§6 / #10).
		if strings.TrimSpace(sc.Arg) == "" {
			if sc.Group == "" {
				a.statusBar.SetError(sc.Command + " only works inside a group")
				return
			}
			if a.client == nil {
				return
			}
			if !a.isLocalAdminOfGroup(sc.Group) {
				a.statusBar.SetError("You are not an admin of this group — only admins can " + sc.Command + ". Type /admins to see who is.")
				return
			}
			a.openPicker(PickerRequest{
				Verb:       sc.Command,
				Source:     PickerSourceSlash,
				Room:       sc.Room,
				Group:      sc.Group,
				DM:         sc.DM,
				ShowFilter: true, // #7
			})
			return
		}
		a.handleGroupAdminCommand(sc)
	case "/whoami":
		a.handleWhoamiCommand(sc)
	case "/groupinfo", "/info":
		a.showInfoPanelForContext(sc.Room, sc.Group, sc.DM)
	case "/audit":
		a.handleAuditCommand(sc)
	case "/members":
		a.showMemberPanelForContext(sc.Room, sc.Group, sc.DM)
	case "/admins":
		a.handleMembersOverlayCommand(sc, true)
	case "/role":
		// #1c/§9 step 3: bare `/role` opens the shared picker;
		// `/role @user` keeps its existing direct path. `/role` is
		// group-scoped (#9b) and NOT admin-gated. Per the §6
		// invalid-context rule, BOTH the bare form (handled here)
		// and the typed form (handled in handleRoleCommand) surface
		// the same "/role only works inside a group" status when
		// `sc.Group == ""` — never an empty picker, never a silent
		// drop. The two branches use the same wording.
		if strings.TrimSpace(sc.Arg) == "" {
			if sc.Group == "" {
				a.statusBar.SetError("/role only works inside a group")
				return
			}
			a.openPicker(PickerRequest{
				Verb:       "/role",
				Source:     PickerSourceSlash,
				Room:       sc.Room,
				Group:      sc.Group,
				DM:         sc.DM,
				ShowFilter: true, // #7: groups can be long
			})
			return
		}
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

// handleTopicCommand handles the /topic slash command. Two modes:
//
//   - `/topic` (no arg) — read-only, shows the current room topic in
//     the status bar. "#name — topic text" if a topic is set, or
//     "#name has no topic set" otherwise.
//
//   - `/topic <text>` (with arg) — admin-only, sets a new topic for
//     the current room. Sends a RoomUpdate request to the server.
//     The status bar first shows an optimistic pending message, then
//     flips to "Topic updated" when the room_updated callback arrives
//     for the current room.
//
// In a group or 1:1 DM context, both modes show
// "/topic is only available in rooms" — groups have no topics by
// design.
func (a *App) handleTopicCommand(sc *SlashCommandMsg) {
	if sc.Room == "" {
		a.statusBar.SetError("/topic is only available in rooms")
		return
	}
	if a.client == nil {
		a.statusBar.SetError("/topic unavailable — not connected")
		return
	}

	newTopic := strings.TrimSpace(sc.Arg)
	if newTopic == "" {
		// Read mode — display current topic
		name := a.client.DisplayRoomName(sc.Room)
		topic := a.client.DisplayRoomTopic(sc.Room)
		if topic == "" {
			a.statusBar.SetError("#" + name + " has no topic set")
			return
		}
		a.statusBar.SetError("#" + name + " — " + topic)
		return
	}

	// Write mode — admin only. The server enforces this too, but
	// catching it client-side gives a faster + clearer error than
	// "request silently dropped."
	if !a.client.IsAdmin() {
		a.statusBar.SetError("/topic <text> is admin-only")
		return
	}
	if err := a.client.SendRoomUpdate(sc.Room, newTopic); err != nil {
		a.statusBar.SetError("Failed to set topic: " + err.Error())
		return
	}
	// Optimistic confirmation. The actual topic update lands when the
	// server's RoomUpdated broadcast arrives — at that point the
	// in-memory cache + the messages-pane header refresh
	// automatically. Until then, the user knows the request was sent.
	a.statusBar.SetError(topicUpdatePendingStatus)
}

// Phase 14 Chunk 6 command handlers ------------------------------------

// handleAuditCommand opens the /audit overlay with the last N events
// for the current group. Default N is 10; user can override with
// /audit 50 for a longer view.
func (a *App) handleAuditCommand(sc *SlashCommandMsg) {
	if sc.Group == "" {
		a.statusBar.SetError("/audit only works inside a group")
		return
	}
	if a.client == nil {
		a.statusBar.SetError("/audit unavailable - not connected")
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

// handleMembersOverlayCommand opens the /admins (filtered) overlay for the
// current group. Reads from the client's
// in-memory groupMembers + groupAdmins maps at Show time; the
// overlay is one-shot and doesn't update live.
func (a *App) handleMembersOverlayCommand(sc *SlashCommandMsg, adminsOnly bool) {
	if sc.Group == "" {
		a.statusBar.SetError(sc.Command + " only works inside a group")
		return
	}
	if a.client == nil {
		a.statusBar.SetError(sc.Command + " unavailable - not connected")
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
	if a.client == nil || sc.Arg == "" {
		return
	}
	// Surface the same invalid-context message as the bare-picker
	// branch when /role is typed outside a group (e.g. `/role @bob`
	// from a room or 1:1 DM). The previous silent return here meant
	// typed /role outside a group did nothing at all — the user
	// asked us to mirror the bare branch's status here.
	if sc.Group == "" {
		a.statusBar.SetError("/role only works inside a group")
		return
	}
	targetID, ok := a.resolveGroupMemberByName(sc.Group, sc.Arg)
	if !ok {
		a.statusBar.SetError(strings.TrimPrefix(sc.Arg, "@") + " is not a member of this group")
		return
	}
	a.roleReadout(sc.Group, targetID)
}

// roleReadout is the post-resolution step for `/role` — shared by the
// typed path (after name→ID via resolveGroupMemberByName) and the
// picker selection path (which already has the picked ID). #3
// entry-vs-post-resolution: neither path re-enters name resolution,
// so behavior is identical on both. Caller guarantees groupID and
// targetID are non-empty and that targetID is a member of groupID
// (the typed path enforces this; the picker only offers members).
func (a *App) roleReadout(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	name := a.client.DisplayName(targetID)
	role := "member"
	if a.client.IsGroupAdmin(groupID, targetID) {
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
		// Bare /groupcreate (no member tokens) → open the in-place
		// New-Conversation panel (the guided group/DM creator) instead
		// of erroring. The args form below still acts directly.
		// missing.md §6.
		a.openNewConversation()
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
	if a.client == nil {
		return
	}
	if sc.Arg == "" {
		// Bare /dmcreate → open the in-place New-Conversation panel
		// (same guided creator; selecting one user = DM). missing.md §6.
		a.openNewConversation()
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

	a.whoisReadout(targetID)
}

// whoisReadout opens the identity panel for an ALREADY-RESOLVED user
// ID. It is the post-resolution step (shared-picker-widget.md §3
// entry-vs-post-resolution): the typed `/whois <name>` path calls it
// after name→ID resolution, and the picker selection path calls it
// with the already-resolved ID — neither re-enters name resolution,
// so behavior is identical on both paths.
func (a *App) whoisReadout(targetID string) {
	if a.client == nil {
		return
	}
	// Sanity check: we need a fingerprint from somewhere — live
	// profile OR pinned-keys row — for the panel to be meaningful.
	// A profile with an empty KeyFingerprint is effectively the
	// same as no profile here, so check both sources for a non-
	// empty fingerprint before opening the panel.
	profile := a.client.Profile(targetID)
	hasFingerprint := profile != nil && profile.KeyFingerprint != ""
	if !hasFingerprint {
		if st := a.client.Store(); st != nil {
			if info, err := st.GetPinnedKeyInfo(targetID); err == nil && info.Fingerprint != "" {
				hasFingerprint = true
			}
		}
	}
	if !hasFingerprint {
		a.statusBar.SetError("unknown user: " + a.resolveDisplayName(targetID) + " (no profile or pinned key)")
		return
	}

	// Open the per-user profile panel. Single source of truth for
	// "show this user's identity" — same panel used by the member-
	// panel "view profile" action. ShowUser handles auto-copying
	// the public key to clipboard (matching /mykey ergonomics) and
	// renders fingerprint, pubkey, presence, verified state,
	// first-seen, role, retirement.
	a.infoPanel.ShowUser(targetID, a.client, a.sidebar.online, a.sidebar.status, a.messages.dm)
}

// openPicker is the SINGLE entry for the shared picker (#6/§6). Both
// the bare slash verb and (later) the group-info-panel footer `a`
// converge here. It builds the verb's candidate list, applies #10
// (empty / invalid-context → contextual status message and NO modal),
// otherwise Shows the picker. The widget stays dumb — all verb and
// context knowledge lives here.
func (a *App) openPicker(req PickerRequest) {
	items := a.pickerCandidates(req)
	if len(items) == 0 {
		// #10: never a dead modal — surface a message, stay in chat.
		a.statusBar.SetError("No one to pick for " + req.Verb)
		return
	}
	a.picker.Show(req, items)
}

// pickerCandidates builds the verb-specific candidate set. App owns
// this; the widget never queries client/store. Only `/whois` is wired
// for the proving milestone (#12); the remaining verbs are added here
// per the §9 sequence (each with its own gate/scope from §6).
func (a *App) pickerCandidates(req PickerRequest) []PickerItem {
	switch req.Verb {
	case "/whois":
		return a.whoisCandidates()
	case "/verify":
		return a.verifyCandidates()
	case "/unverify":
		return a.unverifyCandidates()
	case "/role":
		return a.roleCandidates(req.Group)
	case "/add":
		return a.addCandidates(req.Group)
	case "/kick":
		return a.kickCandidates(req.Group)
	case "/promote":
		return a.promoteCandidates(req.Group)
	case "/demote":
		return a.demoteCandidates(req.Group)
	case "/transfer":
		return a.kickCandidates(req.Group) // same shape: members excl. self
	case "add_to_group":
		// §9 step 7: member-panel "Add to existing group" — items are
		// GROUPS, subject user is req.SubjectUserID. Not a slash verb;
		// see addToGroupCandidates doc for the ID-shape note.
		return a.addToGroupCandidates(req.SubjectUserID)
	default:
		return nil
	}
}

// whoisCandidates returns all locally-known users (#2: all-users,
// cross-context — no group/admin gate). Per #9a `/whois` deliberately
// INCLUDES retired accounts, marked + selectable, because it is an
// identity-investigation tool. Self is excluded. Sorted by display
// name so the list is stable (ForEachProfile iterates a map).
func (a *App) whoisCandidates() []PickerItem {
	if a.client == nil {
		return nil
	}
	self := a.client.UserID()
	seen := make(map[string]bool)
	var items []PickerItem
	a.client.ForEachProfile(func(p *protocol.Profile) {
		if p == nil || p.User == "" || p.User == self || seen[p.User] {
			return
		}
		seen[p.User] = true
		items = append(items, PickerItem{
			ID:      p.User,
			Primary: a.resolveDisplayName(p.User),
		})
	})
	for uid := range a.client.RetiredUsers() {
		if uid == self || seen[uid] {
			continue
		}
		seen[uid] = true
		items = append(items, PickerItem{
			ID:        uid,
			Primary:   a.resolveDisplayName(uid),
			Secondary: "retired",
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// verifyCandidates returns known users not yet verified (#6: any
// context, exclude retired, exclude self, exclude users without a
// pinned key — you can't verify someone whose fingerprint isn't
// pinned yet). Sorted by display name. Per #9a retired accounts are
// excluded from action verbs.
func (a *App) verifyCandidates() []PickerItem {
	if a.client == nil {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	st := a.client.Store()
	seen := make(map[string]bool)
	var items []PickerItem
	a.client.ForEachProfile(func(p *protocol.Profile) {
		if p == nil || p.User == "" || p.User == self || seen[p.User] {
			return
		}
		if _, isRetired := retired[p.User]; isRetired {
			return
		}
		if st != nil {
			info, _ := st.GetPinnedKeyInfo(p.User)
			if info.Fingerprint == "" || info.Verified {
				return
			}
		}
		seen[p.User] = true
		items = append(items, PickerItem{
			ID:      p.User,
			Primary: a.resolveDisplayName(p.User),
		})
	})
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// unverifyCandidates returns currently verified users (#6 — backed
// by the new Store.ListVerifiedPinnedKeys helper; scanning live UI
// state would miss users whose profile broadcast hasn't arrived this
// session). Exclude retired (#9a) and self. Sorted by display name.
func (a *App) unverifyCandidates() []PickerItem {
	if a.client == nil {
		return nil
	}
	st := a.client.Store()
	if st == nil {
		return nil
	}
	users, err := st.ListVerifiedPinnedKeys()
	if err != nil {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	var items []PickerItem
	for _, uid := range users {
		if uid == "" || uid == self {
			continue
		}
		if _, isRetired := retired[uid]; isRetired {
			continue
		}
		items = append(items, PickerItem{
			ID:      uid,
			Primary: a.resolveDisplayName(uid),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// addCandidates returns users who are NOT in the active group (the
// /add candidate set: § §6). Excludes self (admin implies membership
// so self is filtered indirectly), retired (#9a), and current
// members. Sorted by display name.
func (a *App) addCandidates(groupID string) []PickerItem {
	if a.client == nil || groupID == "" {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	members := make(map[string]bool)
	for _, m := range a.client.GroupMembers(groupID) {
		members[m] = true
	}
	seen := make(map[string]bool)
	var items []PickerItem
	a.client.ForEachProfile(func(p *protocol.Profile) {
		if p == nil || p.User == "" || p.User == self || seen[p.User] {
			return
		}
		if members[p.User] {
			return // already in the group
		}
		if _, isRetired := retired[p.User]; isRetired {
			return // #9a
		}
		seen[p.User] = true
		items = append(items, PickerItem{
			ID:      p.User,
			Primary: a.resolveDisplayName(p.User),
		})
	})
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// kickCandidates returns the active group's members excluding self
// (you can't /kick yourself — `/leave` is the right verb; the typed
// path silently redirects, and the picker just hides self) and
// excluding retired (#9a). Also used as-is by `/transfer` (same
// candidate shape — members excl. self).
func (a *App) kickCandidates(groupID string) []PickerItem {
	if a.client == nil || groupID == "" {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	var items []PickerItem
	for _, uid := range a.client.GroupMembers(groupID) {
		if uid == "" || uid == self {
			continue
		}
		if _, isRetired := retired[uid]; isRetired {
			continue
		}
		items = append(items, PickerItem{
			ID:      uid,
			Primary: a.resolveDisplayName(uid),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// promoteCandidates returns active-group members who are NOT already
// admins, excluding self and retired (#9a, #6).
func (a *App) promoteCandidates(groupID string) []PickerItem {
	if a.client == nil || groupID == "" {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	var items []PickerItem
	for _, uid := range a.client.GroupMembers(groupID) {
		if uid == "" || uid == self {
			continue
		}
		if a.client.IsGroupAdmin(groupID, uid) {
			continue // already an admin
		}
		if _, isRetired := retired[uid]; isRetired {
			continue
		}
		items = append(items, PickerItem{
			ID:      uid,
			Primary: a.resolveDisplayName(uid),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// demoteCandidates returns the active group's current admins,
// retired excluded (#9a). Self is INTENTIONALLY included — self-
// demote is a valid action (the existing DemoteConfirm carries
// targetIsSelf and the last-admin guard applies) and matches the
// typed path's behavior. No explicit self exclusion (unlike the
// other mutating verbs).
func (a *App) demoteCandidates(groupID string) []PickerItem {
	if a.client == nil || groupID == "" {
		return nil
	}
	retired := a.client.RetiredUsers()
	var items []PickerItem
	for _, uid := range a.client.GroupAdmins(groupID) {
		if uid == "" {
			continue
		}
		if _, isRetired := retired[uid]; isRetired {
			continue
		}
		items = append(items, PickerItem{
			ID:      uid,
			Primary: a.resolveDisplayName(uid),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// defaultMemberMenuItems returns the standard member-context menu
// actions for the given user. App callers start from this list and
// conditionally append context-dependent extras (§9 step 7 appends
// "Add to group..." when `addToGroupCandidates` is non-empty). Kept
// as a free helper so it's trivially testable and the menu widget
// stays dumb (it does not query any state).
func defaultMemberMenuItems(displayName string) []ContextMenuItem {
	return []ContextMenuItem{
		{Label: "Create group with...", Action: "create_group"},
		{Label: "Message " + displayName, Action: "message"},
		{Label: "Verify " + displayName, Action: "verify"},
		{Label: "View profile", Action: "profile"},
	}
}

// buildMemberMenuItems composes the default member-menu items and
// PREPENDS "Add to group..." at position 0 when the local user is an
// admin of at least one group the subject user could be added to (§9
// step 7 / member-panel-add-to-group.md). Top-of-list placement so
// the action is the first thing under the cursor when the menu opens
// — the typical reason an admin opens this menu on someone else is
// to take an admin action on them, not to chat. If the action would
// land in an empty picker, it is HIDDEN here — no dead path.
func (a *App) buildMemberMenuItems(targetID, displayName string) []ContextMenuItem {
	defaults := defaultMemberMenuItems(displayName)
	if len(a.addToGroupCandidates(targetID)) == 0 {
		return defaults
	}
	return append(
		[]ContextMenuItem{{Label: "Add to group...", Action: "add_to_existing_group"}},
		defaults...,
	)
}

// addToGroupCandidates returns the list of GROUPS the member-panel
// "Add to existing group" picker should offer, for the given subject
// user (the one whose menu was opened). §9 step 7 / member-panel-
// add-to-group.md. ID-shape note: returned PickerItem.ID is a GROUP
// ID — opposite to slash `/add` where SelectedID is a user ID. The
// caller routes by Request.Verb ("add_to_group") + Source
// (PickerSourceMemberPanel) to interpret correctly. Eligibility (all
// four must hold):
//
//  1. Local user is an admin of the group.
//  2. Group is active (not archived / left locally).
//  3. Subject user is not already a member of the group.
//  4. Subject user is not retired AND not self (admin ⇒ member, so
//     self-add is a dead path).
//
// Empty result → caller (member-menu) HIDES the action entirely; do
// not surface a dead path. Sorted by display name for stability.
func (a *App) addToGroupCandidates(subjectUserID string) []PickerItem {
	if a.client == nil || subjectUserID == "" {
		return nil
	}
	self := a.client.UserID()
	if subjectUserID == self {
		return nil // rule 4: self
	}
	if retired := a.client.RetiredUsers(); retired != nil {
		if _, isRetired := retired[subjectUserID]; isRetired {
			return nil // rule 4: retired
		}
	}
	var items []PickerItem
	for _, g := range a.sidebar.groups {
		if g.ID == "" {
			continue
		}
		// rule 1: local admin of the group
		if !a.client.IsGroupAdmin(g.ID, self) {
			continue
		}
		// rule 2: active. sidebar.groups is the LIVE active list
		// (the sidebar reconciles archived/left at append time —
		// using sidebar.groups instead of raw Store.GetAllGroups is
		// the §6 rule because GetAllGroups has no left_at).
		//
		// rule 3: subject user not already a member
		alreadyMember := false
		for _, m := range a.client.GroupMembers(g.ID) {
			if m == subjectUserID {
				alreadyMember = true
				break
			}
		}
		if alreadyMember {
			continue
		}
		name := strings.TrimSpace(a.client.DisplayGroupName(g.ID))
		if name == "" {
			name = g.ID
		}
		items = append(items, PickerItem{
			ID:      g.ID,
			Primary: name,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// roleCandidates returns the current group's members for the `/role`
// picker (#9b: group-scoped, NOT admin-gated; per #9a mark retired
// members with Secondary="retired" but keep them in the list — /role
// is a readout, not an action). Self is excluded for consistency
// with the other user-target pickers (`/whoami` is the self-readout
// equivalent). Sorted by display name.
func (a *App) roleCandidates(groupID string) []PickerItem {
	if a.client == nil || groupID == "" {
		return nil
	}
	self := a.client.UserID()
	retired := a.client.RetiredUsers()
	var items []PickerItem
	for _, uid := range a.client.GroupMembers(groupID) {
		if uid == "" || uid == self {
			continue
		}
		item := PickerItem{
			ID:      uid,
			Primary: a.resolveDisplayName(uid),
		}
		if _, isRetired := retired[uid]; isRetired {
			item.Secondary = "retired"
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Primary) < strings.ToLower(items[j].Primary)
	})
	return items
}

// applyMuteState writes the new mute state for `target` into the
// canonical `a.muted` map, persists, and surfaces a "Muted: X" /
// "Unmuted: X" status confirmation (resolving a human-readable label
// via the kind-appropriate display helper). Shared by:
//   - the info-panel `m` key (MuteToggleMsg handler)
//   - the typed `/mute` slash command
//
// Both ultimately do the same thing; factoring this out removes the
// duplication that existed when MuteToggleMsg held the only copy.
func (a *App) applyMuteState(target, kind string, muted bool) {
	if target == "" {
		return
	}
	a.muted[target] = muted
	if a.appConfig != nil {
		config.SaveMutedMap(a.configDir, a.appConfig, a.muted)
	}
	label := target
	if a.client != nil {
		switch kind {
		case "room":
			if name := strings.TrimSpace(a.client.DisplayRoomName(target)); name != "" {
				label = name
			}
		case "group":
			if name := strings.TrimSpace(a.client.DisplayGroupName(target)); name != "" {
				label = name
			}
		case "dm":
			if name := strings.TrimSpace(a.client.DisplayDMName(target)); name != "" {
				label = name
			}
		}
	}
	if muted {
		a.statusBar.SetError("Muted: " + label)
	} else {
		a.statusBar.SetError("Unmuted: " + label)
	}
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
		a.statusBar.SetError(sc.Command + " only works inside a group")
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

	// §9 step 5 / §3 entry-vs-post-resolution: dispatch to the per-
	// verb POST-RESOLUTION step (ID → action). The picker selection
	// path calls these same functions directly with its picked ID —
	// neither path re-enters name resolution, so behavior is identical
	// on both.
	switch sc.Command {
	case "/add":
		a.addConfirmForTarget(sc.Group, targetID)
	case "/kick":
		a.kickConfirmForTarget(sc.Group, targetID)
	case "/promote":
		a.promoteConfirmForTarget(sc.Group, targetID)
	case "/demote":
		a.demoteConfirmForTarget(sc.Group, targetID)
	case "/transfer":
		a.transferConfirmForTarget(sc.Group, targetID)
	}
}

// addConfirmForTarget is the post-resolution step for `/add` — opens
// the existing AddConfirmModel for an already-resolved target. Shared
// by the typed path (after name→ID) and the picker selection path.
// Caller invariant: groupID is non-empty and the local user is an
// admin of groupID (the typed path's gates enforce this; the picker
// only opens when those gates pass).
func (a *App) addConfirmForTarget(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	targetName := a.client.DisplayName(targetID)
	for _, m := range a.client.GroupMembers(groupID) {
		if m == targetID {
			a.statusBar.SetError(targetName + " is already a member of this group")
			return
		}
	}
	a.addConfirm.Show(groupID, a.lookupGroupName(groupID), targetID, targetName)
}

// kickConfirmForTarget is the post-resolution step for `/kick`.
// Self-kick is redirected to the /leave flow (applies the last-admin
// gate and keeps the audit trail cleaner).
func (a *App) kickConfirmForTarget(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	groupName := a.lookupGroupName(groupID)
	if targetID == a.client.UserID() {
		a.leaveConfirm.Show(groupID, groupName)
		return
	}
	targetName := a.client.DisplayName(targetID)
	memberCount := len(a.client.GroupMembers(groupID))
	a.kickConfirm.Show(groupID, groupName, targetID, targetName, memberCount)
}

// promoteConfirmForTarget is the post-resolution step for `/promote`.
// Already-admin → status, no dialog (matches the picker candidate
// filter which already hides admins from the /promote list).
func (a *App) promoteConfirmForTarget(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	targetName := a.client.DisplayName(targetID)
	if a.client.IsGroupAdmin(groupID, targetID) {
		a.statusBar.SetError(targetName + " is already an admin")
		return
	}
	a.promoteConfirm.Show(groupID, a.lookupGroupName(groupID), targetID, targetName)
}

// demoteConfirmForTarget is the post-resolution step for `/demote`.
// Not-an-admin → status, no dialog. Self-demote is permitted (the
// existing DemoteConfirm carries targetIsSelf and the last-admin
// guard applies). adminCount/targetIsSelf are derived here so the
// picker selection path computes them the same way the typed path
// does.
func (a *App) demoteConfirmForTarget(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	targetName := a.client.DisplayName(targetID)
	if !a.client.IsGroupAdmin(groupID, targetID) {
		a.statusBar.SetError(targetName + " is not an admin")
		return
	}
	adminCount := len(a.client.GroupAdmins(groupID))
	targetIsSelf := targetID == a.client.UserID()
	a.demoteConfirm.Show(groupID, a.lookupGroupName(groupID), targetID, targetName, adminCount, targetIsSelf)
}

// transferConfirmForTarget is the post-resolution step for `/transfer`.
// targetAlreadyAdmin is derived here so the picker selection path
// computes it the same way the typed path does (the existing
// TransferConfirm flips its wording based on that flag).
func (a *App) transferConfirmForTarget(groupID, targetID string) {
	if a.client == nil || groupID == "" || targetID == "" {
		return
	}
	alreadyAdmin := a.client.IsGroupAdmin(groupID, targetID)
	a.transferConfirm.Show(groupID, a.lookupGroupName(groupID), targetID, a.client.DisplayName(targetID), alreadyAdmin)
}

// handleWhoamiCommand surfaces the local user's display name + role
// in the current context via the status bar.
//
// Roles surfaced (most-privileged label wins):
//   - "server admin" — client.IsAdmin() is true (server-wide admin
//     flag from the user's profile). Shown regardless of context;
//     a server admin is a server admin everywhere.
//   - "group admin" — in a group DM context AND the user is in
//     that group's admin set (via isLocalAdminOfGroup). Server
//     admins in a group context where they're ALSO group admin
//     get "server admin + group admin".
//   - "member" — fallback, no admin role applies.
//
// Previous version only checked the group-admin path, leaving
// server admins showing "member" outside group contexts (e.g.
// in a room or DM, where group-admin doesn't apply).
func (a *App) handleWhoamiCommand(sc *SlashCommandMsg) {
	if a.client == nil {
		return
	}
	name := a.client.DisplayName(a.client.UserID())
	var roles []string
	if a.client.IsAdmin() {
		roles = append(roles, "server admin")
	}
	if sc.Group != "" && a.isLocalAdminOfGroup(sc.Group) {
		roles = append(roles, "group admin")
	}
	role := "member"
	if len(roles) > 0 {
		role = strings.Join(roles, " + ")
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
		a.messages.AddRoomMessage(m, a.client, a.replayingSyncBatch)
		if m.Room == a.messages.room {
			if !a.replayingSyncBatch {
				a.sendReadReceipt()
			}
		} else if !a.replayingSyncBatch {
			// Defence-in-depth (Layer 2a): only count messages the
			// user can actually read. A member added after an epoch
			// rotation holds keys ONLY for post-join epochs; counting
			// pre-join-epoch messages would inflate the badge and leak
			// the existence of room activity the user has no
			// cryptographic access to. RoomEpochKey is a cheap
			// key-presence check (no decrypt). See
			// unread-epoch-leak-fix.md.
			if a.client != nil && a.client.RoomEpochKey(m.Room, m.Epoch) != nil {
				a.sidebar.IncrementUnread(m.Room)
			}
		}
		// Notifications for messages not from self
		if !a.replayingSyncBatch && a.client != nil && m.From != a.client.UserID() {
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
			if !a.replayingSyncBatch {
				a.sendReadReceipt()
			}
		} else if !a.replayingSyncBatch {
			// Defence-in-depth (Layer 2a): groups are per-message
			// wrapped-key (no epoch model). Count only messages this
			// user is a designated recipient of — a late-added member
			// has no wrapped key for pre-join messages, so counting
			// them would inflate the badge and leak pre-join activity.
			// Cheap wrapped-key-presence check, NOT a decrypt (mirrors
			// the room RoomEpochKey gate). See unread-epoch-leak-fix.md.
			if a.client != nil && a.client.IsGroupMessageRecipient(m.WrappedKeys) {
				a.sidebar.IncrementUnread(m.Group)
			}
		}
		if !a.replayingSyncBatch && a.client != nil && m.From != a.client.UserID() {
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
		// Suppress rendering received typing indicators when the
		// local `/typing off` preference is set — symmetric with the
		// send-side gate in input.go. Server still relays (it's
		// authoritative and unaware of this client-local pref); we
		// just drop the display. Nil-guard appConfig defensively
		// (tests may construct App without it; default = show).
		if a.appConfig == nil || !a.appConfig.Notifications.TypingDisabled {
			var m protocol.Typing
			json.Unmarshal(msg.Raw, &m)
			a.messages.SetTyping(m.User, m.Room, m.Group, m.DM)
		}
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
				// Re-add reconciliation: the client-layer room_list handler
				// (client.go) already cleared left_at for every room the
				// server still reports as active, and it runs BEFORE this TUI
				// handler (handleInternal precedes the UI forward). So the
				// store flag is already current here — the only stale state
				// left to reconcile is in-memory: the sidebar grey (marker
				// loop's else, below) and the messages pane's read-only flags
				// (syncMessagesLeftState, below). Do NOT clear those here: a
				// per-room "if IsRoomLeft" guard at this point is always false
				// (the store was just cleared) and would never fire.
				//
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
						seen[ar.ID] = true
						ids = append(ids, ar.ID)
					}
				}
				// V8: retired rooms are omitted from the server room_list
				// (GetUserRoomIDs filters retired = 0), so merge locally-
				// retired rooms back in the same way as left rooms. Without
				// this they vanish from the sidebar on reconnect/restart.
				if retired, err := st.GetRetiredRooms(); err == nil {
					for _, rr := range retired {
						if seen[rr.ID] {
							continue
						}
						seen[rr.ID] = true
						ids = append(ids, rr.ID)
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
					} else {
						// Finding 2 Gap A: clear stale in-memory grey for a
						// re-added room. The store left_at was already cleared
						// (client.go, before this handler), so IsRoomLeft is
						// false — but sidebar.leftRooms can still carry the flag
						// from an earlier same-session leave, and SetRooms does
						// not clear it. Without this else the row stays greyed.
						a.sidebar.MarkRoomRejoined(id)
					}
					// V8: retired rooms render with the retired marker.
					// Forgetting this leaves the merged row present but
					// unstyled. A room can be both left and retired.
					if st.IsRoomRetired(id) {
						a.sidebar.MarkRoomRetired(id)
					}
				}
			}
		}

		// Finding 2 Gap B: if the user is currently viewing a room whose
		// left/retired state just changed in this room_list (e.g. a re-added
		// room), refresh the messages pane's read-only flags so the compose
		// box reactivates without requiring a context switch. Self-guards on
		// nil client/store and non-room contexts.
		a.syncMessagesLeftState()
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
		online := m.Status == "online"
		a.sidebar.SetOnline(m.User, online)
		// StatusText carries the locked-set status (available / away /
		// busy) when the user has set one via /setstatus. Empty
		// resets to the default (Available). Stored separately from
		// online so the dot color can combine both signals.
		a.sidebar.SetStatus(m.User, m.StatusText)
		a.memberPanel.SetPresence(m.User, online, m.StatusText)
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
		// F6 Gate #3 — verify-or-drop the live TUI tombstone (apply-when-no-
		// client keeps the test/degraded path working).
		if a.client == nil || a.client.VerifyDeleteAuthor(m) {
			a.messages.MarkDeleted(m.ID, m.DeletedBy)
		}
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
		if err := json.Unmarshal(msg.Raw, &p); err == nil {
			if p.Retired {
				a.messages.MarkRetired(p.User)
				a.sidebar.MarkRetired(p.User)
			}
			// Option B rename confirmation (rename-collision-ux.md): the self-
			// `profile` broadcast is the durable success signal for a Settings
			// display-name rename. Confirm only when our marker is set, it's our
			// OWN profile, AND the name matches what we attempted — a profile
			// event for a different value (e.g. from a second device) must not
			// falsely confirm this attempt. handleInternal has already updated
			// the cache before this runs, so DisplayName(self) is the new value.
			if a.renameInFlight && a.client != nil &&
				p.User == a.client.UserID() && p.DisplayName == a.renameAttempted {
				a.renameInFlight = false
				a.renameAttempted = ""
				if a.settings.IsVisible() {
					a.settings.SetDisplayNameRenamePending(false)
					a.settings.Refresh(p.DisplayName)
					a.settings.SetNotice("Display name updated to " + p.DisplayName)
				}
			}
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
		// V8: the client layer has already written the keyed cache +
		// persisted member_ids; here we unmarshal the payload directly to
		// learn which room/members to repaint (the old RoomMembersList()
		// singleton accessor is gone).
		a.statusBar.ClearRefreshing()
		if a.client != nil {
			var rml protocol.RoomMembersList
			if err := json.Unmarshal(msg.Raw, &rml); err == nil {
				// V8: ignore a stale response for a room that has since become
				// read-only (race: user hit `r`, then left / the room was
				// retired before the response arrived). Re-populating the
				// panels would re-surface member rows for a room that should
				// show no member-list UI.
				readOnly := false
				if st := a.client.Store(); st != nil {
					readOnly = st.IsRoomRetired(rml.Room) || st.IsRoomLeft(rml.Room)
				}
				if !readOnly {
					a.infoPanel.SetRoomMembers(rml.Room, rml.Members, a.client, a.sidebar.online, a.sidebar.status)
					if a.memberPanel.IsVisible() && a.messages.room == rml.Room {
						a.memberPanel.SetRoomMembers(rml.Members, a.client, a.sidebar.online, a.sidebar.status)
						a.input.SetMembers(a.activeMemberEntries())
						a.input.SetNonMembers(a.activeNonMemberEntries())
					}
				}
			}
		}
	case "device_revoked":
		var m protocol.DeviceRevoked
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.deviceRevoked.Show(m.DeviceID, m.Reason)
		}
	case "device_added":
		// Shadow-device transparency (Tier 1): a brand-new device registered
		// under this identity. NoteAddedDevice records it in the known-set and
		// reports whether it's genuinely newly-seen (dedups against an earlier
		// reconcile), so we alert at most once per device.
		var m protocol.DeviceAdded
		if err := json.Unmarshal(msg.Raw, &m); err == nil && a.client != nil {
			if a.client.NoteAddedDevice(m.DeviceID) {
				a.newDeviceAlert.Show(m.DeviceID, m.CreatedAt)
			}
		}
	case "room_attestation_warning":
		// F7: a room epoch key failed member-attestation verification and was
		// rejected (fail-closed). Synthesized by the client layer (not a wire
		// frame) and surfaced here, mirroring the device-alert path.
		var m struct {
			Room      string `json:"room"`
			Generator string `json:"generator"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.roomAttestation.Show(m.Room, m.Generator, m.Reason)
		}
	case "device_list":
		// Phase 17c Step 6: signal refresh completion for the
		// "refreshing…" keypress-ack indicator.
		a.statusBar.ClearRefreshing()
		var m protocol.DeviceList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.deviceMgr.SetDevices(m.Devices)
			// Tier 1 reconcile: surface any device new to us since we last
			// looked (covers one added while we were offline; the live
			// device_added push covers the online case). First connect on a
			// fresh client seeds silently. No-op without a client handle.
			if a.client != nil {
				for _, d := range a.client.ReconcileDevices(m.Devices) {
					a.newDeviceAlert.Show(d.DeviceID, d.CreatedAt)
				}
			}
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
				a.switchMessageContext("", "", "")
			}
		}
	case "deleted_groups":
		// Sync catchup. Each entry was /delete'd from another device
		// while this one was offline. The client layer has already
		// purged local messages and removed the cached groups row for
		// each entry. Here we
		// drop them from the sidebar and reset the active context if
		// any of them was the currently-viewed group.
		var m protocol.DeletedGroupsList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, groupID := range m.Groups {
				a.sidebar.RemoveGroup(groupID)
				if a.messages.group == groupID {
					a.switchMessageContext("", "", "")
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
				a.switchMessageContext("", "", "")
			}
		}
	case "deleted_rooms":
		// Sync catchup. Each entry was /delete'd from another device
		// while this one was offline. The client layer has already
		// purged local messages and removed the cached room row for
		// each entry. Here we
		// drop them from the sidebar and reset the active context if
		// any of them was the currently-viewed room.
		var m protocol.DeletedRoomsList
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			for _, roomID := range m.Rooms {
				a.sidebar.RemoveRoom(roomID)
				if a.messages.room == roomID {
					a.switchMessageContext("", "", "")
				}
			}
		}
	case "group_renamed":
		// Legacy event kept for back-compat — the server emits this
		// alongside the newer `group_event{Event:"rename"}` (see
		// sshkey-chat session.go where the legacy broadcast is
		// flagged for follow-up removal). The inline system message
		// is now rendered exclusively by the `group_event{rename}`
		// path above (which also honors `m.Quiet`); rendering it
		// here too caused a visible duplicate ("renamed the group to
		// hello" + "renamed the group to \"hello\"" in the same
		// chat). The sidebar entry update DOES stay here — the
		// group_event path renders the inline message but doesn't
		// touch sidebar.RenameGroup, and removing both would leave
		// the sidebar name stale until reconnect.
		var m protocol.GroupRenamed
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.RenameGroup(m.Group, m.Name)
		}
	case "room_event":
		var m protocol.RoomEvent
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			if m.Room == a.messages.room {
				// Phase 20: extended event vocabulary with inline
				// system messages matching the group-side UX.
				switch m.Event {
				case "join":
					// Intentionally NOT rendered for rooms. Rooms are
					// operator-managed, so the actor is always an opaque
					// "os:<uid>" admin — "os:0 added X to the room" carries no
					// useful signal. Room audit events also replay in
					// sync_batch on every reconnect and render only for the
					// active room, so this line surfaced repeatedly and
					// inconsistently across rooms (e.g. shown in the room you
					// happened to be viewing, dropped in the others). Room
					// membership is conveyed by the member panel + the room
					// cache, and the newly-added user gets the room_added_to
					// toast. Groups still render join in the group_event
					// handler — peer-admin adds there are meaningful.
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
			if a.shouldFocusCreatedGroup(m) {
				a.focusSidebarGroupForCompose(m.Group)
			}
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
	case "room_added_to":
		// V3: an operator added the local user to a room. The client layer
		// already wrote the room row + member cache and cleared left_at; here
		// we surface it in the sidebar + a toast / desktop notification.
		// Deliberately does NOT steal focus — this is a sidebar update, not a
		// forced context switch.
		var m protocol.RoomAddedTo
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.sidebar.AddRoom(m.Room)
			// If the user is currently viewing this room (e.g. a left room
			// they still had open), refresh the messages pane's read-only
			// state so the compose box reactivates without a context switch.
			if a.messages.room == m.Room {
				a.syncMessagesLeftState()
			}
			roomName := m.Name
			if roomName == "" {
				roomName = m.Room
			}
			// CLI adds set AddedBy = "os:<uid>"; render a friendly label
			// rather than the raw os:501. (group_added_to never needs this —
			// group adds always come from a real user.)
			addedBy := "server admin"
			if !strings.HasPrefix(m.AddedBy, "os:") {
				addedBy = a.resolveDisplayName(m.AddedBy)
			}
			a.statusBar.SetError(addedBy + " added you to '" + roomName + "'")
			SendDesktopNotification(addedBy, "added you to room '"+roomName+"'")
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
		// Filter out DMs hidden for this caller. Visibility is
		// hidden_for_caller; left_at is history cutoff and does not control
		// sidebar presence.
		active := make([]protocol.DMInfo, 0, len(m.DMs))
		for _, dm := range m.DMs {
			if !dm.HiddenForCaller {
				active = append(active, dm)
			}
		}
		a.sidebar.SetDMs(active)
		// Set the sidebar's selfUserID so it knows which party is
		// "other" in each DM. Defensive redundancy: selfUserID is
		// now canonically set at connect time (connectedWithClient
		// handler) to close the group-dot leak window; this write
		// is retained as a backup and routed through the setter for
		// single-write-path consistency. Behavior unchanged. See
		// presence-dot-self-leak-fix.md.
		if a.client != nil {
			a.sidebar.SetSelfUserID(a.client.UserID())
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
			if a.shouldFocusCreatedDM(m) {
				a.focusSidebarDMForCompose(m.DM)
			}
		}
	case "dm":
		var m protocol.DM
		json.Unmarshal(msg.Raw, &m)
		if m.CorrID != "" && a.client != nil {
			a.client.SendQueue().Ack(m.CorrID)
		}
		// Add to messages view if the active context is this DM.
		// AddDMMessage handles dedup, decryption, attachment loop, and
		// display-name resolution — see messages.go for the rationale.
		if m.DM == a.messages.dm {
			a.messages.AddDMMessage(m, a.client)
			a.refreshMessageContent()
			if !a.replayingSyncBatch {
				a.sendReadReceipt()
			}
		} else if !a.replayingSyncBatch {
			a.sidebar.IncrementUnread(m.DM)
		}
		// Desktop notification
		if !a.replayingSyncBatch && a.client != nil && m.From != a.client.UserID() {
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
				a.switchMessageContext("", "", "")
			}
		}
	case "reaction":
		var m protocol.Reaction
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.messages.AddReactionDecrypted(m, a.client)
			// Reconcile from the local DB copy (written by client.handleInternal
			// before OnMessage dispatch) so render state matches persisted state
			// even if live decryption or in-memory merge paths drift.
			a.messages.SyncReactionsForMessage(a.client, m.ID)
		}
	case "reaction_removed":
		var m protocol.ReactionRemoved
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			// F6: drop a forged/misattributed un-react before mutating the
			// in-memory reaction set, matching the durable client.go gate.
			if a.client == nil || a.client.VerifyUnreactAuthor(m) {
				a.messages.RemoveReaction(m.ReactionID)
				a.messages.SyncReactionsForMessage(a.client, m.ID)
			}
		}
	case "sync_batch":
		var batch protocol.SyncBatch
		json.Unmarshal(msg.Raw, &batch)
		prevReplay := a.replayingSyncBatch
		a.replayingSyncBatch = true
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
		a.replayingSyncBatch = prevReplay
	case "history_result":
		var result protocol.HistoryResult
		json.Unmarshal(msg.Raw, &result)

		// Incoming Result Guard: apply a history_result to the visible pane
		// only if it matches the active request's corr_id AND the active
		// context. Tuple matching alone is not enough for A->B->A (an old A
		// result, after the user returns to A, re-matches the tuple); the
		// corr_id pins it to the exact request. Persistence is handled by the
		// client layer (handleHistoryKeys) regardless, so a non-matching
		// result is simply not shown and does not touch the current context's
		// history state.
		if !a.messages.historyResultMatchesActiveRequest(result.CorrID, result.Room, result.Group, result.DM) {
			break
		}

		// Build display messages and prepend. The server now emits history
		// oldest-first (S3: handleHistory pages server_order and reverses at the
		// store boundary), so we prepend the batch as-is — no TUI reversal. A
		// double-reverse here would silently restore the old reverse-chronological
		// bug. Epoch keys are already unwrapped by the client layer
		// (handleHistoryKeys), and messages are persisted there too
		// (storeRoomMessage/storeGroupMessage).
		var histMsgs []DisplayMessage
		for _, raw := range result.Messages {
			histType, _ := protocol.TypeOf(raw)
			switch histType {
			case "message":
				var pm protocol.Message
				if json.Unmarshal(raw, &pm) == nil {
					// F7 Phase D: skip undecryptable history rows (buildDisplayMsg
					// returns ok=false) so they don't prepend as "(encrypted)"
					// ghosts — they were dropped from the store too.
					if dm, ok := a.messages.buildDisplayMsg(pm, a.client); ok {
						histMsgs = append(histMsgs, dm)
					}
				}
			case "group_message":
				var gm protocol.GroupMessage
				if json.Unmarshal(raw, &gm) == nil {
					histMsgs = append(histMsgs, a.messages.buildDisplayGroup(gm, a.client))
				}
			case "dm":
				var dm protocol.DM
				if json.Unmarshal(raw, &dm) == nil {
					// buildDisplayDM mirrors buildDisplayGroup including
					// the attachment loop the previous inline code
					// silently omitted.
					histMsgs = append(histMsgs, a.messages.buildDisplayDM(dm, a.client))
				}
			case "deleted":
				// Remote scrollback can include tombstones for messages this
				// client never cached (created+deleted before we joined). Render
				// them as generic tombstones rather than silently dropping the
				// row — mirroring the local-DB load path, which already shows
				// Deleted rows. Server-side deleted history carries the deleter,
				// not the original author, so leave From/FromID empty and let the
				// renderer use DeletedBy ("removed by …"); no body/attachments/
				// reactions. Durability of these rows is handled in the client
				// layer (storeCatchupTombstone), so a reload shows them too.
				var d protocol.Deleted
				// F6 Gate #4 — verify-or-drop the history_result TUI tombstone.
				if json.Unmarshal(raw, &d) == nil && (a.client == nil || a.client.VerifyDeleteAuthor(d)) {
					histMsgs = append(histMsgs, DisplayMessage{
						ID:        d.ID,
						TS:        d.TS,
						Room:      d.Room,
						Group:     d.Group,
						DM:        d.DM,
						Deleted:   true,
						DeletedBy: d.DeletedBy,
					})
				}
			}
		}
		// Server history now arrives oldest-first (S3), so prepend as-is.
		if len(histMsgs) > 0 {
			a.messages.PrependMessages(histMsgs)
		}
		// has_more drives the remote state + hint and ends the in-flight
		// load, whether or not any messages were returned (replaces the old
		// len>0 / empty-else split that both set hasMore).
		a.messages.markServerHistoryResult(result.HasMore)

		// Apply reactions from the history batch
		for _, raw := range result.Reactions {
			a.handleServerMessage(ServerMsg{Type: "reaction", Raw: raw})
		}

		// The active request is resolved; release ownership so a late
		// duplicate result can't re-apply into this context.
		a.messages.activeHistoryCorrID = ""
	case "pins":
		var m protocol.Pins
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.ensureRoomPinsMap()
			// Cache pins for every room we hear about so switching rooms can
			// scope the pinned bar correctly without leaking prior room state.
			a.roomPins[m.Room] = append([]string(nil), m.Messages...)
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
						// F7 Phase D: pins survive epoch rotations, so a pinned
						// preview can reference an old epoch — resolve via the
						// history-aware decryptor (display-only; the strict
						// epoch < currentEpoch gate prevents any current-epoch bypass).
						payload, err := a.client.DecryptRoomMessageForHistory(pm.Room, pm.Epoch, pm.Payload)
						if err == nil {
							body = payload.Body
						}
					}
					pinnedDisplayMsgs = append(pinnedDisplayMsgs, DisplayMessage{
						ID:     pm.ID,
						FromID: pm.From,
						From:   a.resolveDisplayName(pm.From),
						Body:   body,
						TS:     pm.TS,
						Room:   pm.Room,
					})
				}
				a.pinnedBar.SetPins(m.Room, m.Messages, pinnedDisplayMsgs)
			}
		}
	case "pinned":
		var m protocol.Pinned
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.ensureRoomPinsMap()
			a.roomPins[m.Room] = appendUniquePinID(a.roomPins[m.Room], m.ID)
			if m.Room == a.messages.room {
				a.pinnedBar.AddPin(m.ID, a.messages.messages)
			}
		}
	case "unpinned":
		var m protocol.Unpinned
		if err := json.Unmarshal(msg.Raw, &m); err == nil {
			a.ensureRoomPinsMap()
			a.roomPins[m.Room] = removePinID(a.roomPins[m.Room], m.ID)
			if len(a.roomPins[m.Room]) == 0 {
				delete(a.roomPins, m.Room)
			}
			if m.Room == a.messages.room {
				a.pinnedBar.RemovePin(m.ID)
			}
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
			// Correlated history errors (invalid_context / invalid_cursor /
			// internal_error — and, once the server echoes corr_id on history
			// rate-limits, rate_limited): drop the queue entry so the retry
			// driver can't resend an abandoned scroll-back request, whether
			// this error is for the active request or a stale one. verb was
			// captured above BEFORE Queue.Error() (which may delete the entry).
			// If it is the active request, abort the visible load too.
			if verb == "history" {
				a.client.SendQueue().Drop(m.CorrID)
				if m.CorrID == a.messages.activeHistoryCorrID {
					a.messages.abortHistoryRequest()
				}
			}
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

		// Option B rename failure (rename-collision-ux.md): set_profile carries
		// no corr_id today, so only empty-correlation errors can be attributed
		// to a pending rename. Correlated errors belong to their originating
		// send-queue entry above and must not clear rename state. Expected
		// set_profile codes: username_taken / invalid_profile defense-in-depth,
		// plus rate_limited pre-validation and internal_error from the server
		// write-hardening. The displayed name was never optimistically changed,
		// so there is nothing to revert.
		if a.renameInFlight && m.CorrID == "" {
			attempted := a.renameAttempted
			a.renameInFlight = false
			a.renameAttempted = ""
			if a.settings.IsVisible() {
				// The status bar is hidden behind Settings — surface the failure
				// in-panel and re-open the edit with the rejected name pre-filled
				// so the user tweaks rather than retypes. Suppress the redundant
				// (invisible) status-bar copy.
				a.settings.SetDisplayNameRenamePending(false)
				a.settings.SetErrorNotice("Name change failed: " + m.Message)
				a.settings.StartEditingDisplayName(attempted)
				return nil
			}
			// Settings already closed — fall back to the status-bar error.
			a.statusBar.SetError("Name change failed: " + m.Message)
			return nil
		}

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
					a.statusBar.SetError("Your admin status may have changed — try /info to refresh")
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

// anyModalVisible reports whether any modal/overlay is currently
// claiming the screen. Used by View to suppress sidebar preview-pane
// image rendering whenever a modal is up — opening a modal acts as
// an implicit "deselect image" trigger.
//
// Listing all the IsVisible callers keeps this rule centralized:
// add a new modal? add it here too. See also the click-bleed-through
// check around line ~2367 — that list is structurally similar but
// not identical (full-screen overlays like help/settings handle
// their own clicks so they don't appear there, but they still
// suppress preview).
func (a *App) anyModalVisible() bool {
	return a.connectFailed.IsVisible() ||
		a.passphrase.IsVisible() ||
		a.memberMenu.IsVisible() ||
		a.contextMenu.IsVisible() ||
		a.quitConfirm.IsVisible() ||
		a.retireConfirm.IsVisible() ||
		a.leaveConfirm.IsVisible() ||
		a.leaveRoomConfirm.IsVisible() ||
		a.saveAttachment.IsVisible() ||
		a.deleteDMConfirm.IsVisible() ||
		a.deleteGroupConfirm.IsVisible() ||
		a.deleteRoomConfirm.IsVisible() ||
		a.auditOverlay.IsVisible() ||
		a.membersOverlay.IsVisible() ||
		a.lastAdminPicker.IsVisible() ||
		a.addConfirm.IsVisible() ||
		a.kickConfirm.IsVisible() ||
		a.promoteConfirm.IsVisible() ||
		a.demoteConfirm.IsVisible() ||
		a.transferConfirm.IsVisible() ||
		a.unverifyConfirm.IsVisible() ||
		a.deviceRevoked.IsVisible() ||
		a.newDeviceAlert.IsVisible() ||
		a.roomAttestation.IsVisible() ||
		a.deviceMgr.IsVisible() ||
		a.keyWarning.IsVisible() ||
		a.verify.IsVisible() ||
		a.help.IsVisible() ||
		a.settings.IsVisible() ||
		a.addServer.IsVisible() ||
		a.infoPanel.IsVisible() ||
		a.pendingPanel.IsVisible() ||
		a.emojiPicker.IsVisible() ||
		a.newConv.IsVisible() ||
		a.threadPanel.IsVisible() ||
		a.search.IsVisible() ||
		a.quickSwitch.IsVisible() ||
		a.statusPicker.IsVisible() ||
		a.picker.IsVisible()
}

// appMinWidth and appMinHeight are the minimum terminal dimensions
// at which the app is willing to render its full UI. Below these,
// View returns a bouncer message (see renderTerminalTooSmall) instead
// of the normal panels.
//
// Why a bouncer instead of "render anyway and hope":
//
//   - The hard-coded layout uses a 20-cell sidebar + ~5 cells of
//     gaps/borders + a 20-cell mainWidth floor (clamped in View),
//     totaling 45 cells of composition width. Below that, lipgloss
//     wraps or truncates the right edge — the user sees missing
//     borders, scroll-offs, and HitTest geometry that no longer
//     matches what's on screen (clicks land on wrong panes).
//
//   - On the height axis, status (1) + input outer (5) + main outer
//     (>= 7) = ~13 rows minimum. Below that, the bottom rows scroll
//     off and the status bar disappears.
//
//   - Specific bugs are even tighter than the layout floor — e.g.
//     `input.go`'s reply-banner truncation does
//     `preview[:width-23]` which panics on widths under ~23.
//
// 80×24 is the conventional TUI minimum (vt100 default) and aligns
// with what users expect from "this is too small." A more aggressive
// floor (e.g. 60×20) would technically work but the rendering is
// cramped enough that the bouncer is less surprising than the
// half-broken UI.
const (
	appMinWidth  = 80
	appMinHeight = 24
)

func (a App) View() string {
	body := a.viewBody()
	// Stateless rasterm clear (kitty terminals only): if this
	// frame's output doesn't carry a kitty placement, prepend a
	// delete escape so any prior placement gets removed from the
	// graphics layer.
	//
	// Two emission paths cover different render scopes. The
	// canonical emission lives inside buildPreviewContent — the
	// preview-pane row's content already differs frame-to-frame
	// when the preview transitions between image escape and
	// placeholder text, so bubbletea's line-diff renderer reliably
	// flushes the escape. THIS path covers the modal-render case:
	// full-screen modals (settings, infoPanel, addServer, search,
	// etc.) early-return their own view here, bypassing
	// buildPreviewContent entirely. Without this fallback the
	// kitty placement would persist behind the modal.
	//
	// Detection is on `\x1b_Ga=T,` (kitty placement). The delete
	// escape itself starts with `\x1b_Ga=d,` so it doesn't
	// false-positive. Idempotent: kitty's `a=d,d=I,i=<id>` on a
	// non-existent image is a no-op, so double-emission on frames
	// where buildPreviewContent already prepended a delete adds
	// ~30 bytes per frame and is harmless.
	if rastermProtocolCache == rastermKitty && !strings.Contains(body, "\x1b_Ga=T,") {
		return rastermDeleteEscape() + body
	}
	return body
}

func (a App) viewBody() string {
	if a.width == 0 || a.height == 0 {
		return "Loading..."
	}
	if a.width < appMinWidth || a.height < appMinHeight {
		return renderTerminalTooSmall(a.width, a.height)
	}

	// Re-sync the member panel's internal focused-state from the
	// canonical a.focus. memberPanel keeps a separate `focused` bool
	// because it's set point-in-time via SetFocused, which can drift
	// from a.focus when focus changes through paths that don't all
	// remember to call SetFocused (notably modal-close paths like
	// the info panel closing back to FocusInput). Re-deriving each
	// render guarantees only the panel matching a.focus shows the
	// focused-border styling — fixes the "two panels look focused
	// at once" class of bug systemically rather than per-call-site.
	a.memberPanel.SetFocused(a.focus == FocusMembers)

	// Connect-failed overlay takes precedence over the err+!connected
	// raw-error fallback below. ErrMsg sets a.err AND calls
	// connectFailed.Show() on first-time auth failures, and we want
	// the guided overlay (with copy-key, retry, and pending-approval
	// framing) to render rather than the raw SSH library error.
	if a.connectFailed.IsVisible() {
		return a.connectFailed.View(a.width)
	}

	// Passphrase overlay must also take precedence over the
	// !connected fallback below. The pre-flight encryption check in
	// connect() dispatches passphraseNeededMsg BEFORE any actual
	// connection attempt — at that moment a.connected is still false,
	// so without this hoist the view would render "Connecting..."
	// and the dialog (visible at the model level) would be invisible
	// to the user. Symptom: terminal sits at "Connecting..." forever
	// because the user can't see the dialog asking them to type the
	// passphrase. See passphrase-prompt-fix.md.
	if a.passphrase.IsVisible() {
		return a.passphrase.View(a.width)
	}

	if a.err != nil && !a.connected {
		return fmt.Sprintf("\n  Connection error: %v\n\n  Press Ctrl+c to quit.\n", a.err)
	}

	if !a.connected {
		return "\n  Connecting...\n"
	}

	// Layout dimensions for rendering. These local ints are derived
	// from the same computeLayout source-of-truth that mouse handlers
	// use (see internal/tui/layout.go), kept as locals because View()
	// uses them in many downstream rendering positions and the int
	// form is more concise than reading struct fields each time.
	// computeLayout is a pure function, so the values here match
	// exactly what HitTest uses on the same width/height/visibility.
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	sidebarWidth := layout.SidebarWidth
	memberWidth := layout.MemberWidth
	mainWidth := layout.MessagesWidth
	statusBarHeight := 1
	inputHeight := 3
	mainHeight := layout.MessagesY1 - 2

	if mainWidth < 20 {
		mainWidth = 20
	}
	if mainHeight < 5 {
		mainHeight = 5
	}

	// Sidebar preview-pane image path is applied at the end of
	// Update() (see Update wrapper), not here. View is value-receiver
	// so a SetPreviewImagePath call here would mutate a discarded
	// copy. The Update-side write keeps previewImagePath authoritative
	// on the persistent App model.

	// Sync active-context to the sidebar so it can highlight the
	// room/group/DM currently shown in the messages pane,
	// independent of which panel has focus. Lets the user see
	// which conversation is active even when cursoring through
	// the sidebar list or composing in the input.
	a.sidebar.SetActiveContext(a.messages.room, a.messages.group, a.messages.dm)

	// Render panels
	// Sidebar inner-content height (style.Height arg). Sidebar's outer
	// rendered height = inner + 2 borders, so to span rows 0..bodyEnd
	// (= height - statusBarHeight rows) we pass height - statusBarHeight
	// - 2. Pre-fix this was `-1` which made sidebar render one row
	// taller than the messages+input column on the right; the extra row
	// pushed the screen one row past the terminal and scrolled the top
	// borders off.
	sidebar := a.sidebar.View(sidebarWidth, a.height-statusBarHeight-2, a.focus == FocusSidebar)

	var mainPanel string
	if a.search.IsVisible() {
		searchView := a.search.View(mainWidth, mainHeight+inputHeight)
		mainPanel = searchView
	} else {
		// Reply-preview / edit-mode banner — sits BETWEEN messages and
		// input as a single styled row (no border). Lives outside the
		// input box so the input pane stays at fixed 3 rows; the
		// messages pane shrinks by `bannerRows` to keep mainPanel's
		// total height constant. See InputModel.BannerView for the bug
		// history.
		bannerRows := a.input.BannerRows()
		banner := a.input.BannerView(mainWidth)
		if banner != "" {
			banner += "\n"
		}

		msgHeight := mainHeight - bannerRows
		if a.showHelpHint {
			msgHeight-- // make room for the hint
		}
		// Hand the pinned-bar render down to the messages model so it
		// can splice it between the room header and the scrolling
		// viewport. PinnedBarModel.View returns "" when there are no
		// pins, which makes the splice a no-op. Width passed is the
		// messages-pane content area (= panel width minus the 2 border
		// cells lipgloss adds).
		a.messages.SetPinnedBar(a.pinnedBar.View(mainWidth - 2))
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
		mainPanel = messages + "\n" + hint + banner + input
	}

	status := a.statusBar.View(a.width)

	var body string
	if a.memberPanel.IsVisible() {
		// Finding 1: refresh live rows for render. viewBody is a value
		// receiver, so this mutates only the render copy (paint-fresh); the
		// persistent Update/mouse paths own action state + @-completion.
		a.refreshMemberPanelLiveRows()
		// See comment on sidebar.View above — same -2 to account for
		// the rounded border's 2-row vertical frame.
		members := a.memberPanel.View(memberWidth, a.height-statusBarHeight-2)
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
		dialog := a.emojiPicker.View(layout.MessagesWidth, layout.InputY0-layout.MessagesY0)
		col := layout.MessagesX0 + 2
		row := layout.InputY0 - lipgloss.Height(dialog)
		if row < 1 {
			row = 1
		}
		return overlay(screen, dialog, col, row, a.width, a.height)
	}
	if a.pendingPanel.IsVisible() {
		return a.pendingPanel.View(a.width, a.height)
	}
	if a.infoPanel.IsVisible() {
		// Finding 1: refresh live rows for render. viewBody is a value
		// receiver, so this mutates only the render copy (paint-fresh) — the
		// persistent Update/mouse paths own action state.
		a.refreshInfoPanelLiveRows()
		return a.infoPanel.ViewWithHeight(a.width, a.height)
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
		// Centered overlay — splice onto the current screen instead
		// of replacing it, so the underlying messages/sidebar stay
		// visible behind the dialog. Compact yes/no prompt feels
		// out of place as a near-full-screen modal.
		dialog := a.quitConfirm.View()
		dialogLines := strings.Split(strings.TrimRight(dialog, "\n"), "\n")
		dialogH := len(dialogLines)
		dialogW := 0
		for _, dl := range dialogLines {
			if w := ansi.StringWidth(dl); w > dialogW {
				dialogW = w
			}
		}
		col := (a.width - dialogW) / 2
		row := (a.height - dialogH) / 2
		if col < 0 {
			col = 0
		}
		if row < 0 {
			row = 0
		}
		return overlay(screen, dialog, col, row, a.width, a.height)
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
	if a.unverifyConfirm.IsVisible() {
		return a.unverifyConfirm.View(a.width)
	}
	if a.saveAttachment.IsVisible() {
		return a.saveAttachment.View(a.width)
	}
	if a.deviceRevoked.IsVisible() {
		return a.deviceRevoked.View(a.width)
	}
	if a.newDeviceAlert.IsVisible() {
		return a.newDeviceAlert.View(a.width)
	}
	if a.roomAttestation.IsVisible() {
		return a.roomAttestation.View(a.width)
	}
	if a.deviceMgr.IsVisible() {
		return a.deviceMgr.View(a.width)
	}
	if a.contextMenu.IsVisible() {
		// True overlay — splice the menu rows into the screen at the
		// click point without shifting layout. Anchor (cx, cy) is the
		// click position; +1 puts the menu just below the clicked
		// message line so it doesn't cover what the user clicked on.
		// overlay() clamps if the menu would overflow the terminal.
		cx, cy := a.contextMenu.AnchorXY()
		return overlay(screen, a.contextMenu.View(), cx, cy+1, a.width, a.height)
	}
	if a.memberMenu.IsVisible() {
		mx, my := a.memberMenu.AnchorXY()
		return overlay(screen, a.memberMenu.View(), mx, my+1, a.width, a.height)
	}
	if a.picker.IsVisible() {
		// Same placement family as the /setstatus picker (#6/§7):
		// indented into the messages pane, anchored higher because
		// the list + filter + footer is taller than the 3-row status
		// picker. overlay() clamps if it would overflow. #6 has
		// already Hidden any prior modal/panel, so this always
		// renders over bare chat regardless of the entry path.
		col := layout.MessagesX0 + 2
		row := layout.InputY0 - 22
		if row < 1 {
			row = 1
		}
		return overlay(screen, a.picker.View(a.width), col, row, a.width, a.height)
	}
	if a.statusPicker.IsVisible() {
		// Anchor: a few cells above the input bar, indented from
		// the left by the sidebar+gap so the dialog appears in the
		// messages-pane region where the user typed /setstatus —
		// rather than centered (which feels disconnected from the
		// input action) or full-screen (which is heavy for a 3-row
		// picker). overlay() clamps if the dialog would overflow.
		col := layout.MessagesX0 + 2
		row := layout.InputY0 - 5
		if row < 0 {
			row = 0
		}
		return overlay(screen, a.statusPicker.View(), col, row, a.width, a.height)
	}
	if a.passphrase.IsVisible() {
		return a.passphrase.View(a.width)
	}

	// which-key popup — a non-focus-stealing hint, painted last so it only
	// appears over bare chat (any modal above already early-returned).
	// Anchored just above the input bar, mirroring the picker/statusPicker
	// overlays.
	if a.navPopupVisible {
		popup := renderNavPopup()
		col := layout.MessagesX0 + 2
		row := layout.InputY0 - lipgloss.Height(popup)
		if row < 1 {
			row = 1
		}
		return overlay(screen, popup, col, row, a.width, a.height)
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
