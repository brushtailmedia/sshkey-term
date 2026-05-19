package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// InfoPanelModel manages the room/group/DM info overlay.
type InfoPanelModel struct {
	visible         bool
	room            string
	group           string
	dm              string
	members         []memberInfo
	topic           string
	name            string
	isGroup         bool
	isDM            bool
	left            bool // true when the user has left this context (read-only)
	retired         bool // true when the room has been retired by an admin (Phase 12)
	muted           bool
	cursor          int
	scroll          int                 // panel-local scroll offset (line-based viewport)
	viewportWidth   int                 // terminal width at last key-handling pass
	viewportHeight  int                 // terminal height at last key-handling pass
	resolveRoomName func(string) string // room nanoid → display name

	// User-profile mode — set by ShowUser when the panel is opened
	// for a single-user view (via /whois or member-panel "profile"
	// action) instead of a room/group/DM context. When isUser is
	// true, all the other-mode fields above are unused, the View
	// renders the per-user profile layout, and Update handles the
	// per-user action keys.
	isUser          bool
	userID          string
	userDisplay     string
	userOnline      bool
	userStatus      string // locked-set: StatusAvailable | StatusAway | StatusBusy | "" (default = available)
	userLastSeen    string // human-friendly "2 hours ago", empty if online or unknown
	userVerified    bool
	userAdmin       bool   // server-level admin (from Profile.Admin)
	userRetired     bool   // account retired
	userRetiredAt   int64  // server-pushed retirement timestamp, 0 if unknown
	userFirstSeen   int64  // pinned-keys table FirstSeen
	userFingerprint string // SHA256:... (or empty if profile unavailable)
	userPubKey      string // ssh-ed25519 AAAA... user@host
	userIsSelf      bool
	userInDMWith    bool // true when caller is currently in a 1:1 DM with userID — hides "m=message"

	// userClipboardNotice is the most recent "X copied to clipboard."
	// message rendered inside the panel. Populated on Show (auto-copy
	// of public key) and updated when the user presses f= to copy
	// the fingerprint. Empty string suppresses the line. Lets the f
	// action have visible confirmation without bouncing through the
	// status bar.
	userClipboardNotice string
}

type memberInfo struct {
	User        string
	DisplayName string
	Online      bool
	Status      string // locked-set: StatusAvailable | StatusAway | StatusBusy | "" (default)
	Verified    bool
	Admin       bool
}

// MuteToggleMsg is sent when the user toggles mute on a room, group
// DM, or 1:1 DM. Target is the raw ID (room/group/DM nanoid). Kind
// disambiguates which resolver the App should use to fetch the
// human display name for the status-bar confirmation —
// DisplayRoomName / DisplayGroupName / DisplayDMName.
type MuteToggleMsg struct {
	Target string // room ID, group DM ID, or 1:1 DM ID (raw nanoid)
	Kind   string // "room" | "group" | "dm" — selects the resolver in the App handler
	Muted  bool
}

// MemberActionMsg is sent when the user selects a member from the info panel.
type MemberActionMsg struct {
	Action string // "message", "create_group", "verify", "profile"
	User   string
}

// RefreshRequestMsg is emitted by refresh-key bindings in info-panel /
// device-manager / the global Ctrl+Shift+R handler. The app routes it
// to the matching client request verb and starts the "refreshing…"
// status-line keypress-ack indicator. Phase 17c Step 6.
type RefreshRequestMsg struct {
	// Kind is the refresh target: "room_members", "device_list", or
	// "reconnect" (full handshake via Ctrl+Shift+R).
	Kind string
}

func (i *InfoPanelModel) ShowRoom(room string, c *client.Client, online map[string]bool, status map[string]string) {
	// status is unused here (members are populated later via
	// SetRoomMembers when the server responds), but accepted in
	// the signature for symmetry with the other Show* methods so
	// callers don't need to special-case which params to pass.
	_ = status
	i.visible = true
	i.room = room
	i.group = ""
	i.dm = ""
	i.isGroup = false
	i.isDM = false
	i.isUser = false // clear the user-profile mode flag so a prior ShowUser doesn't leak through
	i.cursor = 0
	i.scroll = 0
	i.left = false
	i.retired = false
	i.topic = "" // Phase 18: populated below via DisplayRoomTopic
	i.name = ""
	if c != nil {
		if st := c.Store(); st != nil {
			i.left = st.IsRoomLeft(room)
			i.retired = st.IsRoomRetired(room)
		}
		// Phase 18: populate the topic field that the existing render
		// code at line ~380 has been guarding with `if i.topic != ""`
		// since v0.1.0 but never had data in until now. Read through
		// DisplayRoomTopic so the call chain is uniform with the
		// messages header which uses the same resolver.
		i.topic = c.DisplayRoomTopic(room)
	}

	// Start with an empty member list — populated by SetRoomMembers when
	// the server responds to room_members. The caller sends the request.
	i.members = nil
}

// SetRoomMembers populates the member list from a server room_members_list response.
func (i *InfoPanelModel) SetRoomMembers(room string, members []string, c *client.Client, online map[string]bool, status map[string]string) {
	if room != i.room || !i.visible {
		return
	}
	i.members = nil
	for _, user := range members {
		p := c.Profile(user)
		displayName := user
		admin := false
		if p != nil {
			displayName = p.DisplayName
			admin = p.Admin
		}
		verified := false
		if st := c.Store(); st != nil {
			_, verified, _ = st.GetPinnedKey(user)
		}
		i.members = append(i.members, memberInfo{
			User:        user,
			DisplayName: displayName,
			Online:      online[user],
			Status:      status[user],
			Admin:       admin,
			Verified:    verified,
		})
	}
	sortMembersAdminsFirst(i.members)
}

func (i *InfoPanelModel) ShowGroup(groupID string, c *client.Client, online map[string]bool, status map[string]string) {
	i.visible = true
	i.room = ""
	i.group = groupID
	i.dm = ""
	i.isGroup = true
	i.isDM = false
	i.isUser = false // clear the user-profile mode flag so a prior ShowUser doesn't leak through
	i.cursor = 0
	i.scroll = 0
	i.left = false
	i.retired = false
	i.topic = ""
	i.name = ""
	if c != nil {
		if st := c.Store(); st != nil {
			i.left = st.IsGroupLeft(groupID)
		}
		i.name = c.DisplayGroupName(groupID)
	}

	i.members = nil
	if c != nil {
		members := c.GroupMembers(groupID)
		for _, m := range members {
			p := c.Profile(m)
			displayName := m
			if p != nil {
				displayName = p.DisplayName
			}
			// Phase 14: per-member admin state comes from the in-memory
			// group admin set (sourced from group_list + live
			// group_event{promote,demote} + sync replay), NOT from the
			// global profile.Admin flag (which tracks server-wide
			// admin status and is unrelated to per-group governance).
			admin := c.IsGroupAdmin(groupID, m)
			verified := false
			if st := c.Store(); st != nil {
				_, verified, _ = st.GetPinnedKey(m)
			}
			i.members = append(i.members, memberInfo{
				User:        m,
				DisplayName: displayName,
				Online:      online[m],
				Status:      status[m],
				Admin:       admin,
				Verified:    verified,
			})
		}
	}
	sortMembersAdminsFirst(i.members)
}

// ShowDM displays the info panel for a 1:1 DM. Unlike rooms and group
// DMs, a 1:1 has exactly two members (always self + other) and has no
// topic, no admin list, and no group name. The panel instead surfaces
// the /delete hint from the refactor design doc and renders both parties
// with their verification and retired status.
func (i *InfoPanelModel) ShowDM(dmID string, c *client.Client, online map[string]bool, status map[string]string) {
	i.visible = true
	i.room = ""
	i.group = ""
	i.dm = dmID
	i.isGroup = false
	i.isDM = true
	i.isUser = false // clear the user-profile mode flag so a prior ShowUser doesn't leak through
	i.cursor = 0
	i.scroll = 0
	// 1:1 DMs do not have a "left / read-only" intermediate state in the
	// TUI — /delete is the only exit path and it removes the sidebar
	// entry entirely. If we somehow end up rendering a DM whose local
	// left_at is set, treat it as active (the sidebar should have already
	// filtered it out via dm_list).
	i.left = false
	i.retired = false
	i.muted = false
	i.topic = ""
	i.name = ""

	i.members = nil
	if c == nil {
		return
	}

	self := c.UserID()
	other := c.DMOther(dmID)
	// Fallback: if the client hasn't yet cached the DM (e.g. stale
	// invocation before dm_list arrives), fall back to a single-entry
	// list with just self. Panel is still usable.
	people := []string{self}
	if other != "" {
		people = append(people, other)
	}
	for _, m := range people {
		p := c.Profile(m)
		displayName := m
		admin := false
		if p != nil {
			displayName = p.DisplayName
			admin = p.Admin
		}
		verified := false
		if st := c.Store(); st != nil {
			_, verified, _ = st.GetPinnedKey(m)
		}
		i.members = append(i.members, memberInfo{
			User:        m,
			DisplayName: displayName,
			Online:      online[m],
			Status:      status[m],
			Admin:       admin,
			Verified:    verified,
		})
	}
	// No admin-first sort for DMs — stable "self, then other" ordering is
	// more intuitive than alphabetical.
}

// ShowUser opens the panel in user-profile mode for a single user.
// Same panel chrome as the room/group/DM modes; different content.
// Used by both /whois (slash command) and the member-panel "view
// profile" action — single rendering path so the two entry points
// produce identical output.
//
// `currentDM` is the dm-context the caller is currently in (typically
// a.messages.dm). When non-empty AND its other party matches userID,
// the panel hides the "m=message" action since the user is already
// in a DM with this person — opening another DM is a no-op.
//
// Auto-copies the user's public key to the clipboard on open,
// matching the /mykey + /whois ergonomics: SSH-format pubkey is
// what users typically want to paste into other tools, and a
// passive on-open copy is convenient.
func (i *InfoPanelModel) ShowUser(userID string, c *client.Client, online map[string]bool, status map[string]string, currentDM string) {
	i.visible = true

	// Reset other modes so a previous Show{Room,Group,DM} doesn't
	// leak state into this view.
	i.room = ""
	i.group = ""
	i.dm = ""
	i.isGroup = false
	i.isDM = false
	i.cursor = 0
	i.scroll = 0
	i.members = nil
	i.topic = ""
	i.name = ""
	i.left = false
	i.retired = false
	i.muted = false

	// User-mode fields — start clean each call.
	i.isUser = true
	i.userID = userID
	i.userDisplay = userID
	i.userOnline = false
	i.userStatus = ""
	i.userLastSeen = ""
	i.userVerified = false
	i.userAdmin = false
	i.userRetired = false
	i.userRetiredAt = 0
	i.userFirstSeen = 0
	i.userFingerprint = ""
	i.userPubKey = ""
	i.userIsSelf = false
	i.userInDMWith = false

	if c == nil {
		return
	}

	i.userIsSelf = userID == c.UserID()

	// "Are we currently in a DM with this user?" — used to suppress
	// the m=message action since opening a DM with someone you're
	// already DMing is a no-op. Only matters for non-self users; a
	// DM-with-self isn't a real concept here.
	if !i.userIsSelf && currentDM != "" {
		if other := c.DMOther(currentDM); other == userID {
			i.userInDMWith = true
		}
	}

	// Live profile is the authoritative source for current display
	// name, pubkey, fingerprint, admin/retired flags. Falls back to
	// pinned-keys (offline-cached) if the live profile hasn't been
	// pushed yet — same fallback chain as the verify modal.
	live := c.Profile(userID)
	if live != nil {
		i.userDisplay = live.DisplayName
		i.userPubKey = live.PubKey
		i.userFingerprint = live.KeyFingerprint
		i.userAdmin = live.Admin
		i.userRetired = live.Retired
	}

	// Pinned-keys gives us first-seen + verified state always (live
	// profile doesn't carry verified — that's a local-device choice).
	// Also fills in pubkey/fingerprint if the live profile was nil.
	if st := c.Store(); st != nil {
		info, _ := st.GetPinnedKeyInfo(userID)
		i.userFirstSeen = info.FirstSeen
		i.userVerified = info.Verified
		if i.userPubKey == "" {
			i.userPubKey = info.Pubkey
		}
		if i.userFingerprint == "" {
			i.userFingerprint = info.Fingerprint
		}
	}

	// Display-name fallback: client.DisplayName resolves nanoid →
	// human name from the in-memory profile cache.
	if i.userDisplay == userID || i.userDisplay == "" {
		i.userDisplay = c.DisplayName(userID)
	}

	i.userOnline = online[userID]
	i.userStatus = status[userID]

	// Last-seen for offline users isn't tracked yet — Presence pushes
	// carry it but we don't store it. Field stays empty; View
	// renders "○ offline" without the parenthetical timestamp until
	// the plumbing is added in a follow-up.
	i.userLastSeen = ""

	// Auto-copy public key to clipboard on open. Matches /mykey
	// behavior: pubkey is the unwieldy thing users actually want to
	// paste elsewhere; fingerprint can be eyeballed from screen.
	// Populates userClipboardNotice so the panel renders an inline
	// confirmation line (without this, the auto-copy is silent).
	if i.userPubKey != "" {
		CopyToClipboard(i.userPubKey)
		i.userClipboardNotice = "Public key copied to clipboard."
	} else {
		i.userClipboardNotice = ""
	}
}

func (i *InfoPanelModel) Hide() {
	i.visible = false
}

func (i *InfoPanelModel) IsVisible() bool {
	return i.visible
}

func (i *InfoPanelModel) ToggleMute() {
	i.muted = !i.muted
}

// SetViewport snapshots the active terminal dimensions for keyboard-local
// paging behavior (pgup/pgdn/home/end) while the info panel is open.
func (i *InfoPanelModel) SetViewport(width, height int) {
	if width > 0 {
		i.viewportWidth = width
	}
	if height > 0 {
		i.viewportHeight = height
	}
}

func (i InfoPanelModel) Update(msg tea.KeyMsg) (InfoPanelModel, tea.Cmd) {
	// User-profile mode has its own action set (m/v/f, no list nav,
	// no admin keys). Esc-close stays uniform with the other modes.
	if i.isUser {
		switch msg.String() {
		case "esc":
			i.Hide()
			return i, nil
		case "up", "k":
			i.scroll--
			i.clampScrollForUser()
			return i, nil
		case "down", "j":
			i.scroll++
			i.clampScrollForUser()
			return i, nil
		case "pageup", "pgup":
			i.scroll -= i.pageRows()
			i.clampScrollForUser()
			return i, nil
		case "pagedown", "pgdown":
			i.scroll += i.pageRows()
			i.clampScrollForUser()
			return i, nil
		case "home":
			i.scroll = 0
			return i, nil
		case "end":
			i.scroll = i.maxScrollForUser()
			return i, nil
		case "m":
			// Message: hidden when self, retired, or already-in-
			// DM-with — but consume the keypress regardless to
			// avoid leaking it through to a parent handler. In
			// the hidden case, just no-op.
			if i.userIsSelf || i.userRetired || i.userInDMWith {
				return i, nil
			}
			user := i.userID
			i.Hide()
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "message", User: user}
			}
		case "v":
			if i.userIsSelf || i.userRetired {
				return i, nil
			}
			user := i.userID
			i.Hide()
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "verify", User: user}
			}
		case "f":
			// Copy fingerprint to clipboard + swap the inline notice
			// so the user gets visible confirmation. Without the
			// notice swap, the action would feel unresponsive — the
			// keypress would update clipboard contents silently.
			if i.userFingerprint != "" {
				CopyToClipboard(i.userFingerprint)
				i.userClipboardNotice = "Fingerprint copied to clipboard."
			}
			return i, nil
		}
		return i, nil
	}

	movedCursor := false
	switch msg.String() {
	case "esc":
		i.Hide()
		return i, nil
	case "up", "k":
		if i.cursor > 0 {
			i.cursor--
			movedCursor = true
		}
	case "down", "j":
		if i.cursor < len(i.members)-1 {
			i.cursor++
			movedCursor = true
		}
	case "pageup", "pgup":
		if len(i.members) > 0 {
			step := i.pageRows()
			if step < 1 {
				step = 1
			}
			i.cursor -= step
			if i.cursor < 0 {
				i.cursor = 0
			}
			movedCursor = true
		}
	case "pagedown", "pgdown":
		if len(i.members) > 0 {
			step := i.pageRows()
			if step < 1 {
				step = 1
			}
			i.cursor += step
			if i.cursor > len(i.members)-1 {
				i.cursor = len(i.members) - 1
			}
			movedCursor = true
		}
	case "home":
		if len(i.members) > 0 {
			i.cursor = 0
			movedCursor = true
		}
	case "end":
		if len(i.members) > 0 {
			i.cursor = len(i.members) - 1
			movedCursor = true
		}
	case "enter":
		// In a 1:1 DM info panel we're already in a DM with the selected
		// user, so Enter should not emit another create_dm action.
		if !i.isDM && i.cursor < len(i.members) {
			user := i.members[i.cursor].User
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "message", User: user}
			}
		}
	case "m":
		i.ToggleMute()
		// Pick the active context: room, group, or DM. Previous code
		// only checked room/group, leaving DM-mode mute presses with
		// an empty target (status bar showed "Muted: " with nothing
		// after). Kind lets the App resolve the right display name.
		var target, kind string
		switch {
		case i.room != "":
			target = i.room
			kind = "room"
		case i.group != "":
			target = i.group
			kind = "group"
		case i.dm != "":
			target = i.dm
			kind = "dm"
		}
		muted := i.muted
		return i, func() tea.Msg {
			return MuteToggleMsg{Target: target, Kind: kind, Muted: muted}
		}
	case "r":
		// Phase 17c Step 6: refresh room member list. Only meaningful
		// in room context (groups use group_event broadcasts to stay
		// current; DMs have no member list). App.go handles the
		// actual server request + statusBar.SetRefreshing.
		if i.room != "" {
			return i, func() tea.Msg {
				return RefreshRequestMsg{Kind: "room_members"}
			}
		}
		// Phase 14 admin-action shortcuts (a=add, K=remove, p=promote,
		// x=demote) are DISABLED 2026-05-19. They were mis-wired: K/x opened
		// a confirmation dialog that stayed trapped behind the still-modal
		// info panel (the app appeared frozen); a/p were effectively no-ops.
		// Rather than fix wiring that is about to be replaced, the keys are
		// removed here pending the locked picker-hand-off rework (a/r/p/x →
		// close the panel + open the verb's picker). Do NOT re-add inline
		// MemberActionMsg{admin_*} emission from the info panel — see
		// missing.md §2a. Until the picker lands, group admin actions are
		// via the typed /add /kick /promote /demote commands; these
		// keystrokes are intentionally inert no-ops in the info panel.
	}
	if movedCursor {
		i.syncScrollToSelection()
	}
	return i, nil
}

func (i InfoPanelModel) View(width int) string {
	return i.ViewWithHeight(width, 0)
}

// ViewWithHeight renders the info panel with an explicit terminal height so
// overlay-local scrolling can clip and paginate content safely.
func (i InfoPanelModel) ViewWithHeight(width, height int) string {
	if !i.visible {
		return ""
	}

	var lines []string
	selectedLine := -1
	if i.isUser {
		lines = i.renderUserContent(width)
	} else {
		lines, selectedLine = i.renderContextContent()
	}
	if len(lines) == 0 {
		lines = []string{""}
	}

	rows := i.pageRowsWithHeight(height)
	maxScroll := len(lines) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := i.scroll
	if selectedLine >= 0 {
		if selectedLine < scroll {
			scroll = selectedLine
		}
		if selectedLine >= scroll+rows {
			scroll = selectedLine - rows + 1
		}
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + rows
	if end > len(lines) {
		end = len(lines)
	}
	visible := strings.Join(lines[scroll:end], "\n")

	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	return dialogStyle.Width(innerWidth).Render(visible)
}

func (i InfoPanelModel) renderContextContent() ([]string, int) {
	lines := make([]string, 0, 64)
	selectedLine := -1
	appendLine := func(s string) {
		lines = append(lines, s)
	}
	appendBlank := func() {
		lines = append(lines, "")
	}

	if i.room != "" {
		roomDisplay := i.room
		if i.resolveRoomName != nil {
			roomDisplay = i.resolveRoomName(i.room)
		}
		appendLine(searchHeaderStyle.Render(fmt.Sprintf(" #%s — info", roomDisplay)))
	} else if i.isDM {
		other := ""
		if len(i.members) > 0 {
			other = i.members[len(i.members)-1].DisplayName
		}
		if other == "" {
			other = "conversation"
		}
		appendLine(searchHeaderStyle.Render(fmt.Sprintf(" DM with %s — info", other)))
	} else {
		title := i.group
		if i.name != "" {
			title = i.name
		}
		appendLine(searchHeaderStyle.Render(fmt.Sprintf(" %s — info", title)))
	}
	appendBlank()

	if i.retired {
		appendLine(" " + errorStyle.Render("Status:") + " this room was archived by an admin (read-only)")
		appendLine(" " + helpDescStyle.Render("Type /delete to remove from your view."))
		appendBlank()
	} else if i.left {
		label := "group"
		if i.room != "" {
			label = "room"
		}
		appendLine(" " + errorStyle.Render("Status:") + " you left this " + label + " (read-only)")
		appendLine(" " + helpDescStyle.Render("Type /delete to remove from your view."))
		appendBlank()
	} else if i.room != "" {
		appendLine(" " + helpDescStyle.Render("Type /leave to stop receiving messages, or /delete to remove from your view."))
		appendBlank()
	} else if i.isGroup {
		appendLine(" " + helpDescStyle.Render("Type /leave to stop receiving messages, or /delete to remove from your view."))
		appendBlank()
	}

	if i.isDM {
		appendLine(" " + helpDescStyle.Render("Type /delete to remove this conversation from your view."))
		appendBlank()
	}

	if i.topic != "" {
		appendLine(" Topic: " + i.topic)
		appendBlank()
	}

	muteLabel := "off"
	if i.muted {
		muteLabel = "on"
	}
	appendLine(fmt.Sprintf(" Muted: [%s]  (press m to toggle)", muteLabel))
	appendBlank()

	var adminCount int
	for _, m := range i.members {
		if m.Admin {
			adminCount++
		}
	}

	renderMember := func(idx int, m memberInfo) string {
		dot := PresenceDot(m.Online, m.Status)
		line := fmt.Sprintf("   %s %s", dot, m.DisplayName)
		if m.Verified {
			line += checkStyle.Render(" ✓ verified")
		}
		if idx == i.cursor {
			line = completionSelectedStyle.Render(line)
		}
		return line
	}

	if i.isDM {
		for idx, m := range i.members {
			if idx == i.cursor {
				selectedLine = len(lines)
			}
			appendLine(renderMember(idx, m))
		}
	} else {
		appendLine(fmt.Sprintf(" Members (%d):", len(i.members)))
		if adminCount > 0 {
			appendLine(sidebarHeaderStyle.Render("  [Admins]"))
			for idx, m := range i.members {
				if !m.Admin {
					continue
				}
				if idx == i.cursor {
					selectedLine = len(lines)
				}
				appendLine(renderMember(idx, m))
			}
		}
		if adminCount < len(i.members) {
			appendLine(sidebarHeaderStyle.Render("  [Members]"))
			for idx, m := range i.members {
				if m.Admin {
					continue
				}
				if idx == i.cursor {
					selectedLine = len(lines)
				}
				appendLine(renderMember(idx, m))
			}
		}
	}

	appendBlank()
	if i.isDM {
		appendLine(helpDescStyle.Render(" m=mute  Esc=close"))
	} else if i.isGroup {
		// Group admin-action hints intentionally omitted: the a/K/p/x
		// keys are disabled pending the picker rework (missing.md §2a).
		// Re-add the hint here when the picker hand-off is wired.
		appendLine(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))
	} else {
		// Room context: surface r=refresh — `r` performs a real
		// room-member-list refresh here (missing.md §2c). It is a no-op
		// in DM/group context (case "r" is guarded `if i.room != ""`),
		// so it is advertised only on this room branch.
		appendLine(helpDescStyle.Render(" Enter=message  r=refresh  m=mute  Esc=close"))
	}

	return lines, selectedLine
}

// renderUserContent renders the per-user profile mode of the info panel as
// plain lines so the shared viewport slicer can paginate it.
func (i InfoPanelModel) renderUserContent(width int) []string {
	lines := make([]string, 0, 64)
	appendLine := func(s string) {
		lines = append(lines, s)
	}
	appendBlank := func() {
		lines = append(lines, "")
	}

	titleName := i.userDisplay
	if i.userIsSelf {
		titleName += " (you)"
	}
	appendLine(searchHeaderStyle.Render(" Profile: " + titleName))
	appendBlank()

	var presence string
	if i.userOnline {
		presence = PresenceDot(true, i.userStatus) + " " + presenceLabel(i.userStatus)
	} else {
		presence = offlineDot + " offline"
		if i.userLastSeen != "" {
			presence += " " + helpDescStyle.Render("(last seen "+i.userLastSeen+")")
		}
	}
	statusRow := "  " + presence
	if i.userIsSelf {
		statusRow += "   " + helpDescStyle.Render("(self)")
	} else if i.userRetired {
		statusRow += "   " + helpDescStyle.Render("retired")
	} else if i.userVerified {
		statusRow += "   " + checkStyle.Render("✓ verified")
	}
	appendLine(statusRow)
	appendBlank()

	appendLine("  Display:      " + i.userDisplay)
	appendLine("  User ID:      " + i.userID)
	if i.userFingerprint != "" {
		appendLine("  Fingerprint:  " + i.userFingerprint)
	}
	if i.userPubKey != "" {
		const labelIndent = "                "
		bodyWidth := (width - 2 - 4) - len(labelIndent)
		if bodyWidth < 20 {
			bodyWidth = 20
		}
		first := true
		for j := 0; j < len(i.userPubKey); j += bodyWidth {
			end := j + bodyWidth
			if end > len(i.userPubKey) {
				end = len(i.userPubKey)
			}
			if first {
				appendLine("  Public key:   " + i.userPubKey[j:end])
				first = false
			} else {
				appendLine(labelIndent + i.userPubKey[j:end])
			}
		}
	}
	appendBlank()

	if i.userFirstSeen > 0 {
		appendLine("  First seen:   " +
			time.Unix(i.userFirstSeen, 0).UTC().Format("2006-01-02"))
	}
	if i.userRetired {
		appendLine("  " + helpDescStyle.Render("Account is retired — DMs are read-only."))
	}
	if i.userAdmin {
		appendLine("  Server role:  admin")
	}
	if i.userFirstSeen > 0 || i.userRetired || i.userAdmin {
		appendBlank()
	}

	if i.userClipboardNotice != "" {
		appendLine("  " + helpDescStyle.Render(i.userClipboardNotice))
		appendBlank()
	}

	var actions []string
	canMessage := !i.userIsSelf && !i.userRetired && !i.userInDMWith
	canVerify := !i.userIsSelf && !i.userRetired
	if canMessage {
		actions = append(actions, "m=message")
	}
	if canVerify {
		actions = append(actions, "v=verify")
	}
	if i.userFingerprint != "" {
		actions = append(actions, "f=copy fingerprint")
	}
	actions = append(actions, "Esc=close")
	appendLine(helpDescStyle.Render("  " + strings.Join(actions, "  ")))

	return lines
}

func (i InfoPanelModel) pageRows() int {
	return i.pageRowsWithHeight(0)
}

func (i InfoPanelModel) pageRowsWithHeight(height int) int {
	h := height
	if h <= 0 {
		h = i.viewportHeight
	}
	if h <= 0 {
		// No viewport known (unit tests or pre-first layout): effectively
		// disable clipping until a real terminal height is provided.
		return 1 << 30
	}
	rows := h - 4 // 2 borders + 2 vertical padding rows from dialogStyle
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (i InfoPanelModel) calcWidth() int {
	if i.viewportWidth > 0 {
		return i.viewportWidth
	}
	return 80
}

func (i InfoPanelModel) maxScrollForUser() int {
	lines := i.renderUserContent(i.calcWidth())
	rows := i.pageRows()
	max := len(lines) - rows
	if max < 0 {
		return 0
	}
	return max
}

func (i *InfoPanelModel) clampScrollForUser() {
	if i.scroll < 0 {
		i.scroll = 0
	}
	max := i.maxScrollForUser()
	if i.scroll > max {
		i.scroll = max
	}
}

func (i *InfoPanelModel) syncScrollToSelection() {
	lines, selectedLine := i.renderContextContent()
	if selectedLine < 0 {
		i.scroll = 0
		return
	}
	rows := i.pageRows()
	maxScroll := len(lines) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if i.scroll < 0 {
		i.scroll = 0
	}
	if i.scroll > maxScroll {
		i.scroll = maxScroll
	}
	if selectedLine < i.scroll {
		i.scroll = selectedLine
	}
	if selectedLine >= i.scroll+rows {
		i.scroll = selectedLine - rows + 1
	}
	if i.scroll < 0 {
		i.scroll = 0
	}
	if i.scroll > maxScroll {
		i.scroll = maxScroll
	}
}

// sortMembersAdminsFirst orders admins before non-admins, alphabetical within
// each group. This keeps cursor indices stable with the rendered section order.
func sortMembersAdminsFirst(members []memberInfo) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].Admin != members[j].Admin {
			return members[i].Admin // admins first
		}
		return members[i].User < members[j].User
	})
}
