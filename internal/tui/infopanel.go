package tui

import (
	"fmt"
	"sort"
	"strings"

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
}

type memberInfo struct {
	User        string
	DisplayName string
	Online      bool
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

func (i *InfoPanelModel) ShowRoom(room string, c *client.Client, online map[string]bool) {
	i.visible = true
	i.room = room
	i.group = ""
	i.dm = ""
	i.isGroup = false
	i.isDM = false
	i.cursor = 0
	i.left = false
	i.retired = false
	if c != nil {
		if st := c.Store(); st != nil {
			i.left = st.IsRoomLeft(room)
			i.retired = st.IsRoomRetired(room)
		}
	}

	// Start with an empty member list — populated by SetRoomMembers when
	// the server responds to room_members. The caller sends the request.
	i.members = nil
}

// SetRoomMembers populates the member list from a server room_members_list response.
func (i *InfoPanelModel) SetRoomMembers(room string, members []string, c *client.Client, online map[string]bool) {
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
			Admin:       admin,
			Verified:    verified,
		})
	}
	sortMembersAdminsFirst(i.members)
}

func (i *InfoPanelModel) ShowGroup(groupID string, c *client.Client, online map[string]bool) {
	i.visible = true
	i.room = ""
	i.group = groupID
	i.dm = ""
	i.isGroup = true
	i.isDM = false
	i.cursor = 0
	i.left = false
	i.retired = false
	if c != nil {
		if st := c.Store(); st != nil {
			i.left = st.IsGroupLeft(groupID)
		}
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
func (i *InfoPanelModel) ShowDM(dmID string, c *client.Client, online map[string]bool) {
	i.visible = true
	i.room = ""
	i.group = ""
	i.dm = dmID
	i.isGroup = false
	i.isDM = true
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
			Admin:       admin,
			Verified:    verified,
		})
	}
	// No admin-first sort for DMs — stable "self, then other" ordering is
	// more intuitive than alphabetical.
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
		if i.cursor < len(i.members) {
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
		dot := "○"
		if m.Online {
			dot = checkStyle.Render("●")
		}
		line := fmt.Sprintf("   %s %s", dot, m.DisplayName)
		if m.User != m.DisplayName {
			line += helpDescStyle.Render(fmt.Sprintf(" (%s)", m.User))
		}
		if m.Verified {
			line += checkStyle.Render(" ✓")
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
	b.WriteString(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))

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
