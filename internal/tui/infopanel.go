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

// MuteToggleMsg is sent when the user toggles mute on a room or group DM.
type MuteToggleMsg struct {
	Target string // room ID or group DM ID
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

func (i InfoPanelModel) Update(msg tea.KeyMsg) (InfoPanelModel, tea.Cmd) {
	// User-profile mode has its own action set (m/v/f, no list nav,
	// no admin keys). Esc-close stays uniform with the other modes.
	if i.isUser {
		switch msg.String() {
		case "esc":
			i.Hide()
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

	switch msg.String() {
	case "esc":
		i.Hide()
		return i, nil
	case "up", "k":
		if i.cursor > 0 {
			i.cursor--
		}
	case "down", "j":
		if i.cursor < len(i.members)-1 {
			i.cursor++
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
		target := i.room
		if target == "" {
			target = i.group
		}
		muted := i.muted
		return i, func() tea.Msg {
			return MuteToggleMsg{Target: target, Muted: muted}
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
	// Phase 14 admin-action shortcuts on the focused member row.
	// A/K/P/X emit MemberActionMsg with admin action values that the
	// app.go handler routes to the appropriate confirmation dialog
	// (same dialog as typing /add, /kick, /promote, /demote). The
	// app pre-checks local is_admin and falls through to a status
	// bar error for non-admins. Keys only fire in a group context —
	// rooms and DMs ignore them.
	//
	// "X" is used for demote because "D" means /delete everywhere
	// else in the app. Matches the plan's keybinding.
	case "a":
		if i.isGroup {
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "admin_add", User: ""}
			}
		}
	case "K":
		// Capital K to avoid colliding with lowercase "k" (up).
		if i.isGroup && i.cursor < len(i.members) {
			user := i.members[i.cursor].User
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "admin_kick", User: user}
			}
		}
	case "p":
		if i.isGroup && i.cursor < len(i.members) {
			user := i.members[i.cursor].User
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "admin_promote", User: user}
			}
		}
	case "x":
		if i.isGroup && i.cursor < len(i.members) {
			user := i.members[i.cursor].User
			return i, func() tea.Msg {
				return MemberActionMsg{Action: "admin_demote", User: user}
			}
		}
	}
	return i, nil
}

func (i InfoPanelModel) View(width int) string {
	// User-profile mode is rendered by a dedicated helper to keep
	// the existing room/group/DM render path unchanged. Both modes
	// share the dialogStyle chrome and headline pattern but diverge
	// substantially in body content + footer keys.
	if i.visible && i.isUser {
		return i.viewUser(width)
	}
	if !i.visible {
		return ""
	}

	var b strings.Builder

	if i.room != "" {
		roomDisplay := i.room
		if i.resolveRoomName != nil {
			roomDisplay = i.resolveRoomName(i.room)
		}
		b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" #%s — info", roomDisplay)))
	} else if i.isDM {
		// Header for a 1:1 DM: show "DM with <other>". The "other" party
		// is whichever of the two members is not self. Members list was
		// built in ShowDM with self at index 0 when both parties are
		// known; pick the last entry as "other" so a fallback with only
		// self still renders a sane title.
		other := ""
		if len(i.members) > 0 {
			other = i.members[len(i.members)-1].DisplayName
		}
		if other == "" {
			other = "conversation"
		}
		b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" DM with %s — info", other)))
	} else {
		title := i.group
		if i.name != "" {
			title = i.name
		}
		b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" %s — info", title)))
	}
	b.WriteString("\n\n")

	// Status + /leave + /delete hints. The wording depends on whether
	// the context is:
	//   - a retired room: admin archived it, only /delete remains
	//   - a left room/group: user self-left, only /delete remains
	//   - an active room: both /leave and /delete are options
	//   - an active group: both /leave and /delete are options
	// Retirement takes priority over left (it's the underlying cause).
	if i.retired {
		b.WriteString(" " + errorStyle.Render("Status:") + " this room was archived by an admin (read-only)\n")
		b.WriteString(" " + helpDescStyle.Render("Type /delete to remove from your view.") + "\n\n")
	} else if i.left {
		var label string
		if i.room != "" {
			label = "room"
		} else {
			label = "group"
		}
		b.WriteString(" " + errorStyle.Render("Status:") + " you left this " + label + " (read-only)\n")
		b.WriteString(" " + helpDescStyle.Render("Type /delete to remove from your view.") + "\n\n")
	} else if i.room != "" {
		// Active room: surface both /leave and /delete as the available
		// exit paths. /leave is subject to the server's policy flag, so
		// the user may get a "Forbidden" back — but we don't pre-check
		// here (the server is authoritative and hot-reloadable).
		b.WriteString(" " + helpDescStyle.Render("Type /leave to stop receiving messages, or /delete to remove from your view.") + "\n\n")
	} else if i.isGroup {
		// Active group DM: /leave and /delete are both available.
		b.WriteString(" " + helpDescStyle.Render("Type /leave to stop receiving messages, or /delete to remove from your view.") + "\n\n")
	}

	// 1:1 DMs surface the /delete hint up front — it's the only exit
	// path for a DM, and the refactor doc (§ 398) explicitly asks for
	// this text in the info panel. There's no "left" intermediate state
	// for DMs, so this is always shown for an active DM.
	if i.isDM {
		b.WriteString(" " + helpDescStyle.Render("Type /delete to remove this conversation from your view.") + "\n\n")
	}

	if i.topic != "" {
		b.WriteString(" Topic: " + i.topic + "\n\n")
	}

	// Mute status
	muteLabel := "off"
	if i.muted {
		muteLabel = "on"
	}
	b.WriteString(fmt.Sprintf(" Muted: [%s]  (press m to toggle)\n\n", muteLabel))

	// Split members into admins and non-admins (preserving cursor indices
	// into i.members which is already sorted admins-first after Show*).
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
		// DMs: render the two parties without admin/member split. Self
		// is always first (index 0), other is second. No header text
		// since "Members (2)" is obvious for a 1:1.
		for idx, m := range i.members {
			b.WriteString(renderMember(idx, m) + "\n")
		}
	} else {
		b.WriteString(fmt.Sprintf(" Members (%d):\n", len(i.members)))
		if adminCount > 0 {
			b.WriteString(sidebarHeaderStyle.Render("  [Admins]") + "\n")
			for idx, m := range i.members {
				if !m.Admin {
					continue
				}
				b.WriteString(renderMember(idx, m) + "\n")
			}
		}
		if adminCount < len(i.members) {
			b.WriteString(sidebarHeaderStyle.Render("  [Members]") + "\n")
			for idx, m := range i.members {
				if m.Admin {
					continue
				}
				b.WriteString(renderMember(idx, m) + "\n")
			}
		}
	}

	b.WriteString("\n")
	if i.isDM {
		b.WriteString(helpDescStyle.Render(" m=mute  Esc=close"))
	} else {
		b.WriteString(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}

// viewUser renders the per-user profile mode of the info panel —
// opened by ShowUser via /whois or member-panel "view profile".
// Layout matches the userprofile_mockup_test.go visual: title,
// status row (presence + verified badge), identity block (name /
// id / fingerprint / public key wrapped), history block (first-seen
// / role / retired-since), clipboard-copy notice, action footer.
//
// Action keys are state-dependent:
//   - m=message: hidden when self, retired, or already-in-DM-with
//   - v=verify:  hidden when self or retired
//   - f=copy fingerprint: always shown if a fingerprint is present
func (i InfoPanelModel) viewUser(width int) string {
	var b strings.Builder

	// Title: "Profile: <name>" with " (you)" suffix on self-view
	// to mirror the mockup vocabulary.
	titleName := i.userDisplay
	if i.userIsSelf {
		titleName += " (you)"
	}
	b.WriteString(searchHeaderStyle.Render(" Profile: " + titleName))
	b.WriteString("\n\n")

	// Status row — presence ●/○ + verified ✓ badge + retired marker.
	// Presence dot color reflects locked-set status (Available/Away/
	// Busy) when online; offline always renders the hollow ○. Status
	// label appears next to the dot for clarity ("● away" rather
	// than just a colored dot).
	// Self-views append "(self)" instead of a verified badge since
	// self-verification is meaningless. Unverified is the absence
	// of the ✓ — never explicitly labeled.
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
	b.WriteString(statusRow + "\n\n")

	// Identity block — display, ID, fingerprint, then the wrapped
	// SSH public key. wrapPubKey indents continuation lines so the
	// label column visually anchors the multi-row value.
	b.WriteString("  Display:      " + i.userDisplay + "\n")
	b.WriteString("  User ID:      " + i.userID + "\n")
	if i.userFingerprint != "" {
		b.WriteString("  Fingerprint:  " + i.userFingerprint + "\n")
	}
	if i.userPubKey != "" {
		b.WriteString("  Public key:   ")
		// Wrap at content-width minus the indent (16 cells of
		// "  Public key:   " label). Continuation lines align to
		// column 16 with the same indent.
		const labelIndent = "                "
		// Inner content width = dialog width minus 2 for borders
		// minus 4 for padding. Minus 16 for label column =
		// available pubkey body width.
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
				b.WriteString(i.userPubKey[j:end] + "\n")
				first = false
			} else {
				b.WriteString(labelIndent + i.userPubKey[j:end] + "\n")
			}
		}
	}
	b.WriteString("\n")

	// History block — first-seen, retirement-date, server role.
	// Retirement-date is shown as raw RetiredAt string when
	// available (server-pushed format); otherwise omitted.
	if i.userFirstSeen > 0 {
		b.WriteString("  First seen:   " +
			time.Unix(i.userFirstSeen, 0).UTC().Format("2006-01-02") + "\n")
	}
	if i.userRetired {
		// userRetiredAt is the protocol-string parsed to int64
		// timestamp; we don't currently parse it — placeholder.
		// For now show "retired" indicator without precise date if
		// not parsed. The status row above already shows "retired"
		// so this line is informational.
		b.WriteString("  " + helpDescStyle.Render("Account is retired — DMs are read-only.") + "\n")
	}
	if i.userAdmin {
		b.WriteString("  Server role:  admin\n")
	}
	if i.userFirstSeen > 0 || i.userRetired || i.userAdmin {
		b.WriteString("\n")
	}

	// Clipboard notice — reflects the most recent copy action, set
	// either by ShowUser (auto-copy of public key on open) or by
	// the f-action handler (fingerprint copy). Empty string skips
	// the line entirely.
	if i.userClipboardNotice != "" {
		b.WriteString("  " + helpDescStyle.Render(i.userClipboardNotice) + "\n\n")
	}

	// Action footer — keys vary by user state:
	//   - self/retired: just f=copy fingerprint + Esc=close
	//   - already in DM with this user: hide m=message
	//   - everyone else: full action set
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
	b.WriteString(helpDescStyle.Render("  " + strings.Join(actions, "  ")))

	return dialogStyle.Width(width - 4).Render(b.String())
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
