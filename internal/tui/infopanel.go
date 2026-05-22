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
	isGroupAdmin    bool // local user is an admin of i.group (set by ShowGroup; gates the a/r/p/x footer keys + admin-hint text)
	isDM            bool
	left            bool // true when the user has left this context (read-only)
	retired         bool // true when the room has been retired by an admin (Phase 12)
	muted           bool
	cursor          int
	scroll          int                 // panel-local scroll offset (line-based viewport)
	viewportWidth   int                 // terminal width at last key-handling pass
	viewportHeight  int                 // terminal height at last key-handling pass
	resolveRoomName func(string) string // room nanoid → display name

	// memberNotice (V8) is set for an ACTIVE room whose member cache is
	// unexpectedly empty (a bug state — startup hydration + room_list should
	// have populated it). Rendered in place of the Members section so an
	// empty cache doesn't read as a zero-member room. Empty for groups/DMs
	// and for healthy rooms. Read-only rooms use the short-circuit instead.
	memberNotice string

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

// InfoPanelAdminKeyMsg is emitted by the group info panel's four
// admin-action footer keys (a/r/p/x) when the local user is a group
// admin (`isGroupAdmin`). App routes per Verb: "/add" → openPicker
// (target is not in the panel list — non-member); "/kick" /
// "/promote" / "/demote" → the matching `<verb>ConfirmForTarget`
// for the highlighted member. The panel is Hide()d BEFORE this
// message is queued (#6 modal lifecycle — Hide-before-Show prevents
// the original freeze-class bug where a confirm opened behind the
// still-visible modal panel). See group-infopanel-picker-rework.md
// §1; do NOT restore the old inline MemberActionMsg{admin_*}
// emission.
type InfoPanelAdminKeyMsg struct {
	Verb     string // "/add", "/kick", "/promote", "/demote"
	Group    string
	TargetID string // selected member's ID; empty for "/add" (target picked by /add picker, not the panel)
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
	i.visible = true
	i.room = room
	i.group = ""
	i.dm = ""
	i.isGroup = false
	i.isGroupAdmin = false
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

	// V8: populate members straight from the client's local cache (no
	// RequestRoomMembers fetch). SetRoomMembers remains for the explicit
	// `r` refresh response. Retired/left rooms have no member-list UI, so
	// skip the cache read for them (read-only short-circuit handles render).
	i.members = nil
	i.memberNotice = ""
	if c != nil && !i.left && !i.retired {
		if members, ok := c.RoomMembers(room); ok {
			i.SetRoomMembers(room, members, c, online, status)
		} else {
			// Active room with no cache entry — bug signal, not a zero-member
			// room. (SetRoomMembers above also clears memberNotice on success.)
			i.memberNotice = "(members unavailable — press r to refresh)"
		}
	}
}

// SetRoomMembers populates the member list from a server room_members_list response.
func (i *InfoPanelModel) SetRoomMembers(room string, members []string, c *client.Client, online map[string]bool, status map[string]string) {
	if room != i.room || !i.visible {
		return
	}
	i.members = nil
	i.memberNotice = "" // a populated response clears the cache-miss signal
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

// SetLiveMemberIDs rebuilds member rows from the live cache for the active
// context (Finding 1). Unlike ShowRoom/ShowGroup/ShowDM it does NOT reset
// visibility/cursor/scroll — it preserves the selected USER (so admin
// re-sorting or a membership change does not move the highlight to the wrong
// person) and re-clamps the cursor. The App bridge calls this immediately
// before the panel's Update and View so an open panel reflects live
// membership-set changes (join/leave/promote/demote), not just display
// fields. Rooms/groups sort admins-first; DMs keep self/other order.
func (i *InfoPanelModel) SetLiveMemberIDs(c *client.Client, online map[string]bool, status map[string]string) {
	if c == nil || !i.visible || i.isUser {
		return
	}
	selectedUser := ""
	if i.cursor >= 0 && i.cursor < len(i.members) {
		selectedUser = i.members[i.cursor].User
	}
	i.memberNotice = ""

	// Re-derive read-only flags so a room retired/left WHILE the panel is
	// open transitions live (ShowRoom froze these at open time).
	if i.room != "" {
		i.left = false
		i.retired = false
		if st := c.Store(); st != nil {
			i.left = st.IsRoomLeft(i.room)
			i.retired = st.IsRoomRetired(i.room)
		}
		if i.left || i.retired {
			i.members = nil // read-only short-circuit handles render
			i.cursor = 0
			i.scroll = 0
			return
		}
	}

	var ids []string
	switch {
	case i.room != "":
		var ok bool
		ids, ok = c.RoomMembers(i.room)
		if !ok {
			i.members = nil
			i.memberNotice = "(members unavailable — press r to refresh)"
			i.cursor = 0
			i.scroll = 0
			return
		}
	case i.group != "":
		// Refresh the LOCAL user's group-admin gate too — it controls the
		// footer/admin action keys (a/r/p/x), not just per-row badges. Without
		// this, a live promote/demote of self leaves stale admin actions until
		// close/reopen.
		i.isGroupAdmin = c.IsGroupAdmin(i.group, c.UserID())
		ids = c.GroupMembers(i.group)
	case i.isDM:
		// Fixed 2-member set, but re-pull the cached pair when available so a
		// panel opened before dm_list hydration can fill in the "other"
		// participant later. Fall back to the existing rows otherwise.
		self := c.UserID()
		other := c.DMOther(i.dm)
		ids = []string{self}
		if other != "" && other != self {
			ids = append(ids, other)
		} else if existing := userIDs(i.members); len(existing) > 0 {
			ids = existing
		}
	}

	rows := make([]memberInfo, 0, len(ids))
	for _, user := range ids {
		if user == "" {
			continue
		}
		row := memberInfo{
			User:        user,
			DisplayName: user, // fallback until Profile(user) is known
			Online:      online[user],
			Status:      status[user],
		}
		p := c.Profile(user)
		if p != nil {
			row.DisplayName = p.DisplayName
		}
		if i.group != "" {
			row.Admin = c.IsGroupAdmin(i.group, user)
		} else if p != nil {
			row.Admin = p.Admin
		}
		if st := c.Store(); st != nil {
			_, row.Verified, _ = st.GetPinnedKey(user)
		}
		rows = append(rows, row)
	}
	if !i.isDM {
		sortMembersAdminsFirst(rows) // DMs keep self/other order for the header
	}
	i.members = rows
	if selectedUser != "" {
		for idx, row := range i.members {
			if row.User == selectedUser {
				i.cursor = idx
				i.syncScrollToSelection()
				return
			}
		}
	}
	if i.cursor >= len(i.members) {
		i.cursor = len(i.members) - 1
	}
	if i.cursor < 0 {
		i.cursor = 0
	}
	i.syncScrollToSelection()
}

// userIDs extracts the User field from each member row (skipping empties).
// Local helper for SetLiveMemberIDs' DM fallback, which reuses the existing
// rows when the cached DM pair is not yet available.
func userIDs(members []memberInfo) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		if m.User != "" {
			out = append(out, m.User)
		}
	}
	return out
}

func (i *InfoPanelModel) ShowGroup(groupID string, c *client.Client, online map[string]bool, status map[string]string) {
	i.visible = true
	i.memberNotice = "" // cache-miss notice is room-only
	i.room = ""
	i.group = groupID
	i.dm = ""
	i.isGroup = true
	// Role-gate the admin footer keys (a/r/p/x) + hint text: shown
	// only when the LOCAL user is an admin of this group
	// (group-infopanel-picker-rework.md §1). A non-admin in the same
	// group sees the generic footer with the keys inert. Computed
	// from the live client at Show time; if the admin set changes
	// while the panel is open (server broadcasts a promote/demote),
	// the panel reopens with fresh state on the next entry.
	if c != nil {
		i.isGroupAdmin = c.IsGroupAdmin(groupID, c.UserID())
	} else {
		i.isGroupAdmin = false
	}
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
	i.memberNotice = "" // cache-miss notice is room-only
	i.room = ""
	i.group = ""
	i.dm = dmID
	i.isGroup = false
	i.isGroupAdmin = false
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
	i.isGroupAdmin = false
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
		// V8: a read-only room (retired/left) has no Muted: line and no
		// m=mute footer hint — the key is inert here too, matching the
		// render-layer short-circuit.
		if i.room != "" && (i.retired || i.left) {
			return i, nil
		}
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
		// Two contexts share the `r` key:
		//   - room: refresh the room member list (Phase 17c Step 6).
		//   - group as admin: /kick the highlighted member
		//     (group-infopanel-picker-rework.md §1 step 6 re-enable).
		// The contexts are mutually exclusive (i.room=="" in groups,
		// i.isGroup=false in rooms), so the two branches never collide.
		// V8: a read-only room has no member list to refresh — inert here
		// (the footer hides r=refresh; the app-level RefreshRequestMsg gate
		// is the backstop for any other emitter).
		if i.room != "" && (i.retired || i.left) {
			return i, nil
		}
		if i.room != "" {
			return i, func() tea.Msg {
				return RefreshRequestMsg{Kind: "room_members"}
			}
		}
		if i.isGroup && i.isGroupAdmin && i.cursor >= 0 && i.cursor < len(i.members) {
			groupID, target := i.group, i.members[i.cursor].User
			i.Hide() // #6 modal lifecycle — Hide BEFORE the next modal opens
			return i, func() tea.Msg {
				return InfoPanelAdminKeyMsg{Verb: "/kick", Group: groupID, TargetID: target}
			}
		}
	case "a":
		// /add: target is a NON-member (not in the panel list) — this
		// is the ONLY footer key that hands off to the shared picker.
		// App routes per Source/Verb to openPicker.
		if i.isGroup && i.isGroupAdmin {
			groupID := i.group
			i.Hide()
			return i, func() tea.Msg {
				return InfoPanelAdminKeyMsg{Verb: "/add", Group: groupID}
			}
		}
	case "p":
		// /promote the highlighted member. Already-admin → status
		// message via promoteConfirmForTarget (mirrors typed path).
		if i.isGroup && i.isGroupAdmin && i.cursor >= 0 && i.cursor < len(i.members) {
			groupID, target := i.group, i.members[i.cursor].User
			i.Hide()
			return i, func() tea.Msg {
				return InfoPanelAdminKeyMsg{Verb: "/promote", Group: groupID, TargetID: target}
			}
		}
	case "x":
		// /demote the highlighted member. Existing DemoteConfirm
		// safeguards (adminCount, targetIsSelf, last-admin) apply via
		// demoteConfirmForTarget; not-an-admin → status message.
		if i.isGroup && i.isGroupAdmin && i.cursor >= 0 && i.cursor < len(i.members) {
			groupID, target := i.group, i.members[i.cursor].User
			i.Hide()
			return i, func() tea.Msg {
				return InfoPanelAdminKeyMsg{Verb: "/demote", Group: groupID, TargetID: target}
			}
		}
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

	// Finding 1: i.members is kept live by the App bridge
	// (refreshInfoPanelLiveRows → SetLiveMemberIDs), called before both Update
	// and View, so the rows already reflect the current membership set +
	// display state. Read it directly.
	members := i.members

	if i.room != "" {
		roomDisplay := i.room
		if i.resolveRoomName != nil {
			roomDisplay = i.resolveRoomName(i.room)
		}
		appendLine(searchHeaderStyle.Render(fmt.Sprintf(" #%s — info", roomDisplay)))
	} else if i.isDM {
		other := ""
		if len(members) > 0 {
			other = members[len(members)-1].DisplayName
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

	// V8 read-only-room short-circuit (rooms only — left groups/DMs keep
	// their normal panels). Retired/left rooms have no member-list UI:
	// drop the Muted: line, the Members section, and the member-action
	// footer; render a close-only footer. The status lines above already
	// state which read-only condition applies. Left groups are intentionally
	// NOT short-circuited here.
	if i.room != "" && (i.retired || i.left) {
		appendBlank()
		appendLine(helpDescStyle.Render(" Esc=close"))
		return lines, selectedLine
	}

	muteLabel := "off"
	if i.muted {
		muteLabel = "on"
	}
	appendLine(fmt.Sprintf(" Muted: [%s]  (press m to toggle)", muteLabel))
	appendBlank()

	var adminCount int
	for _, m := range members {
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
		for idx, m := range members {
			if idx == i.cursor {
				selectedLine = len(lines)
			}
			appendLine(renderMember(idx, m))
		}
	} else if i.memberNotice != "" {
		// V8: active room with an unexpectedly empty member cache — show the
		// bug signal instead of "Members (0):" so it doesn't read as a
		// zero-member room.
		appendLine(" " + helpDescStyle.Render(i.memberNotice))
	} else {
		appendLine(fmt.Sprintf(" Members (%d):", len(members)))
		if adminCount > 0 {
			appendLine(sidebarHeaderStyle.Render("  [Admins]"))
			for idx, m := range members {
				if !m.Admin {
					continue
				}
				if idx == i.cursor {
					selectedLine = len(lines)
				}
				appendLine(renderMember(idx, m))
			}
		}
		if adminCount < len(members) {
			appendLine(sidebarHeaderStyle.Render("  [Members]"))
			for idx, m := range members {
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
		// Group admin-action hints (group-infopanel-picker-rework.md §1
		// step 6): shown ONLY to group admins; non-admins see the
		// generic footer. `a` hands off to the /add picker (the one
		// footer→picker path); `r/p/x` act directly on the highlighted
		// member via the existing kick/promote/demote confirms.
		if i.isGroupAdmin {
			appendLine(helpDescStyle.Render(" Enter=message  a=add  r=remove  p=promote  x=demote  m=mute  Esc=close"))
		} else {
			appendLine(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))
		}
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
