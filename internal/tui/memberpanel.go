package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

var (
	memberPanelStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#64748B"))

	memberPanelFocusedStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#875FFF"))
)

// MemberPanelModel manages the persistent right-side member panel.
type MemberPanelModel struct {
	visible bool
	members []memberPanelEntry
	cursor  int
	focused bool
	// noticeMessage (V8) is a static line rendered in place of the member
	// list; when set, navigation/actions are inert (there are no rows). Two
	// uses: read-only rooms ("room retired" / "you are not a member of this
	// room") and the active-room cache-miss bug signal ("(members
	// unavailable — press r to refresh)") so an empty cache doesn't look
	// like a zero-member room.
	noticeMessage string
	// readOnly (V8) is true only for retired/left rooms — distinguishes the
	// two noticeMessage uses. Read-only rooms make `r` inert (matching the
	// info panel); the active-room cache-miss case keeps `r` live so the
	// "press r to refresh" hint is actionable.
	readOnly bool
}

type memberPanelEntry struct {
	User        string
	DisplayName string
	Online      bool
	Status      string // locked-set: StatusAvailable | StatusAway | StatusBusy | "" (default = available)
	Verified    bool
	Retired     bool
}

func NewMemberPanel() MemberPanelModel {
	return MemberPanelModel{}
}

func (m *MemberPanelModel) Toggle() {
	m.visible = !m.visible
}

func (m *MemberPanelModel) IsVisible() bool {
	return m.visible
}

func (m *MemberPanelModel) SetFocused(focused bool) {
	m.focused = focused
}

// Refresh updates the member list for the current room, group DM, or 1:1 DM.
// status carries the locked-set per-user status (Available/Away/Busy)
// alongside the online bool — without it the dot color can't reflect
// /setstatus changes for individual members.
func (m *MemberPanelModel) Refresh(room, group, dm string, c *client.Client, online map[string]bool, status map[string]string) {
	m.refreshRows(room, group, dm, c, online, status, true)
}

// RefreshPreservingSelection rebuilds the member rows like Refresh but keeps
// the cursor on the previously selected USER (Finding 1). The App bridge calls
// this before every member-panel Update/View/mouse-hit while the panel is
// visible, so the open panel reflects live membership without jumping the
// cursor to row 0 on each render. Restores to the selected user when still
// present; otherwise clamps in range. Read-only/cache-miss notice semantics are
// preserved by refreshRows.
func (m *MemberPanelModel) RefreshPreservingSelection(room, group, dm string, c *client.Client, online map[string]bool, status map[string]string) {
	selectedUser := m.SelectedUser()
	m.refreshRows(room, group, dm, c, online, status, false)
	if selectedUser != "" {
		for idx, row := range m.members {
			if row.User == selectedUser {
				m.cursor = idx
				return
			}
		}
	}
	if m.cursor >= len(m.members) {
		m.cursor = len(m.members) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// refreshRows is the shared row-builder. Refresh passes resetCursor=true (jump
// to the top — the toggle/context-switch behavior); RefreshPreservingSelection
// passes false and restores the cursor itself. Retired/left rooms set
// noticeMessage + readOnly and render no rows.
func (m *MemberPanelModel) refreshRows(room, group, dm string, c *client.Client, online map[string]bool, status map[string]string, resetCursor bool) {
	m.members = nil
	if resetCursor {
		m.cursor = 0
	}
	m.noticeMessage = ""
	m.readOnly = false

	if c == nil {
		return
	}

	// V8 read-only short-circuit: retired/left rooms have no member-list UI.
	// Set the static message and leave members empty so View renders it
	// instead of rows, and Update treats navigation/actions as inert.
	// Retired takes precedence over left (it's the cause).
	if room != "" {
		if st := c.Store(); st != nil {
			switch {
			case st.IsRoomRetired(room):
				m.noticeMessage = "room retired"
				m.readOnly = true
				return
			case st.IsRoomLeft(room):
				m.noticeMessage = "you are not a member of this room"
				m.readOnly = true
				return
			}
		}
	}

	if group != "" {
		// Group DM members
		members := c.GroupMembers(group)
		for _, user := range members {
			p := c.Profile(user)
			displayName := user
			if p != nil {
				displayName = p.DisplayName
			}
			retired, _ := c.IsRetired(user)
			m.members = append(m.members, memberPanelEntry{
				User:        user,
				DisplayName: displayName,
				Online:      online[user],
				Status:      status[user],
				Retired:     retired,
			})
		}
	} else if room != "" {
		// V8: render room members straight from the client's local cache
		// (hydrated on startup, maintained by room_list / room_event). No
		// RequestRoomMembers fetch — SetRoomMembers is now only used by the
		// explicit `r` refresh response path.
		members, ok := c.RoomMembers(room)
		if !ok {
			// Active room (read-only already short-circuited above) with no
			// cache entry — a bug state; startup hydration + room_list should
			// have populated it. Signal it rather than rendering an empty
			// list that looks like a zero-member room.
			m.noticeMessage = "(members unavailable — press r to refresh)"
		}
		for _, user := range members {
			p := c.Profile(user)
			displayName := user
			if p != nil {
				displayName = p.DisplayName
			}
			retired, _ := c.IsRetired(user)
			m.members = append(m.members, memberPanelEntry{
				User:        user,
				DisplayName: displayName,
				Online:      online[user],
				Status:      status[user],
				Retired:     retired,
			})
		}
	} else if dm != "" {
		// 1:1 DM members come from the cached pair.
		pair := c.DMMembers(dm)
		seen := make(map[string]bool)
		for _, user := range pair {
			if user == "" || seen[user] {
				continue
			}
			seen[user] = true
			p := c.Profile(user)
			displayName := user
			if p != nil {
				displayName = p.DisplayName
			}
			retired, _ := c.IsRetired(user)
			m.members = append(m.members, memberPanelEntry{
				User:        user,
				DisplayName: displayName,
				Online:      online[user],
				Status:      status[user],
				Retired:     retired,
			})
		}
	}

	// Check verified status from store
	if c.Store() != nil {
		for i, mem := range m.members {
			_, verified, err := c.Store().GetPinnedKey(mem.User)
			if err == nil {
				m.members[i].Verified = verified
			}
		}
	}
}

// SetRoomMembers populates the member list from a server room_members_list response.
func (m *MemberPanelModel) SetRoomMembers(members []string, c *client.Client, online map[string]bool, status map[string]string) {
	m.members = nil
	for _, user := range members {
		p := c.Profile(user)
		displayName := user
		if p != nil {
			displayName = p.DisplayName
		}
		retired, _ := c.IsRetired(user)
		m.members = append(m.members, memberPanelEntry{
			User:        user,
			DisplayName: displayName,
			Online:      online[user],
			Status:      status[user],
			Retired:     retired,
		})
	}
	// Update verified status
	if c.Store() != nil {
		for i, mem := range m.members {
			_, verified, err := c.Store().GetPinnedKey(mem.User)
			if err == nil {
				m.members[i].Verified = verified
			}
		}
	}
	// Clamp the cursor: a shorter room_members_list refresh response must not
	// leave it pointing past the new end (Finding 1).
	if m.cursor >= len(m.members) {
		m.cursor = len(m.members) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// SetPresence applies a presence/status update in-place to any visible member
// rows for the given user. Unlike Refresh, this preserves cursor position and
// avoids rebuilding the whole panel.
func (m *MemberPanelModel) SetPresence(user string, online bool, status string) {
	for i := range m.members {
		if m.members[i].User != user {
			continue
		}
		m.members[i].Online = online
		m.members[i].Status = status
	}
}

// SelectedUser returns the currently selected member's username.
func (m *MemberPanelModel) SelectedUser() string {
	if m.cursor >= 0 && m.cursor < len(m.members) {
		return m.members[m.cursor].User
	}
	return ""
}

// MemberNames returns all member usernames (for @completion).
func (m *MemberPanelModel) MemberNames() []string {
	names := make([]string, len(m.members))
	for i, mem := range m.members {
		names[i] = mem.User
	}
	return names
}

// MemberEntries returns username + display name pairs for @completion.
func (m *MemberPanelModel) MemberEntries() []MemberEntry {
	entries := make([]MemberEntry, len(m.members))
	for i, mem := range m.members {
		entries[i] = MemberEntry{UserID: mem.User, DisplayName: mem.DisplayName}
	}
	return entries
}

func (m MemberPanelModel) Update(msg tea.KeyMsg) (MemberPanelModel, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	// V8: `r` refreshes the room member list from member-panel focus, same as
	// the info panel. Handled before the notice guard so the active-room
	// cache-miss notice's "press r to refresh" is actionable. Inert for
	// read-only rooms (matching the info panel). The app-level
	// RefreshRequestMsg handler does the fetch and no-ops for non-room
	// contexts (a.messages.room == "").
	if msg.String() == "r" {
		if m.readOnly {
			return m, nil
		}
		return m, func() tea.Msg { return RefreshRequestMsg{Kind: "room_members"} }
	}
	// Read-only room or cache-miss — no member rows, so navigation and
	// member-targeted actions are inert.
	if m.noticeMessage != "" {
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.members)-1 {
			m.cursor++
		}
	case "enter":
		// Open member menu for the selected row. Mouse clicks in the
		// member pane only move selection; Enter is the explicit open.
		if m.cursor < len(m.members) {
			user := m.members[m.cursor].User
			return m, func() tea.Msg {
				return MemberActionMsg{Action: "menu", User: user}
			}
		}
	case "m":
		// Direct message (bypass the menu). Kept as a shortcut for
		// the most common action.
		if m.cursor < len(m.members) {
			user := m.members[m.cursor].User
			return m, func() tea.Msg {
				return MemberActionMsg{Action: "message", User: user}
			}
		}
	}
	return m, nil
}

func (m MemberPanelModel) View(width, height int) string {
	if !m.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(sidebarHeaderStyle.Render(" Members"))
	b.WriteString("\n")

	// V8: read-only room — render the static state message in place of the
	// member list. No rows, no cursor, no actions.
	if m.noticeMessage != "" {
		contentWidth := width - 2
		if contentWidth < 1 {
			contentWidth = 1
		}
		line := ansi.Truncate(" "+helpDescStyle.Render(m.noticeMessage), contentWidth, "")
		b.WriteString(line + "\n")
		content := b.String()
		lines := strings.Count(content, "\n")
		for lines < height-2 {
			content += "\n"
			lines++
		}
		style := memberPanelStyle
		if m.focused {
			style = memberPanelFocusedStyle
		}
		return style.Width(width).Height(height).Render(content)
	}

	for i, mem := range m.members {
		dot := PresenceDot(mem.Online, mem.Status)

		name := mem.DisplayName
		if mem.Verified {
			name = checkStyle.Render("✓") + " " + name
		}
		if mem.Retired {
			name += " " + helpDescStyle.Render("[retired]")
		}

		line := " " + dot + " " + name
		// Keep each member row to a single visual line. Long names (for example
		// nanoids) previously wrapped in the narrow 18-col panel, which shifted
		// visual row positions and broke mouse hit-testing.
		contentWidth := width - 2
		if contentWidth < 1 {
			contentWidth = 1
		}
		line = ansi.Truncate(line, contentWidth, "")

		if i == m.cursor && m.focused {
			// Selected rows are padded/highlighted to full width;
			// truncation above guarantees this render path remains
			// single-line. Uses selectedMsgStyle (dark grey bg) for
			// a uniform cursor-highlight treatment across the
			// sidebar, member panel, and messages pane — a heavy
			// purple-bg + white-fg highlight overpowered the
			// colored presence dots and verified/retired markers.
			line = selectedMsgStyle.Width(contentWidth).Render(line)
		}

		b.WriteString(line + "\n")
	}

	// Pad to height
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < height-2 {
		content += "\n"
		lines++
	}

	style := memberPanelStyle
	if m.focused {
		style = memberPanelFocusedStyle
	}

	return style.Width(width).Height(height).Render(content)
}
