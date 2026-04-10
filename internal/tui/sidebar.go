package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

var (
	sidebarStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B"))

	sidebarFocusedStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED"))

	sidebarHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#64748B"))

	selectedStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#7C3AED")).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true)

	unreadStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7C3AED")).
		Bold(true)

	onlineDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("●")
	offlineDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B")).Render("○")

	// archivedStyle greys out sidebar entries for rooms/conversations the
	// user has left. The entry stays visible so history can still be read,
	// but visually it fades into the background.
	archivedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Faint(true)

	// verifiedMarker is the badge appended to a DM sidebar entry when the
	// other party's key has been verified via the safety-number flow. A
	// small green check so the user can see at a glance which DMs are with
	// TOFU-trusted parties and which are not.
	verifiedMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("✓")
)

// SidebarModel manages the sidebar panel.
type SidebarModel struct {
	rooms         []string
	groups        []protocol.GroupInfo
	dms           []protocol.DMInfo
	unread        map[string]int    // room/group/dm -> count
	online        map[string]bool   // user -> online
	retired       map[string]bool   // user -> retired
	leftGroups    map[string]bool   // group ID -> user has left (archived, read-only)
	leftRooms     map[string]bool   // room ID -> user has left (archived, read-only)
	retiredRooms  map[string]bool   // room ID -> room was retired by an admin (archived, read-only)
	cursor        int               // position in the combined list
	selectedRoom  string
	selectedGroup string
	selectedDM    string
	resolveName     func(string) string // user nanoid → display name (set by App)
	resolveRoomName func(string) string // room nanoid → display name (set by App)
	resolveVerified func(string) bool   // user nanoid → safety-number verified flag (set by App)
	selfUserID      string              // the current user's ID (for DM "other party" resolve)

	// For message forwarding (set by App)
	msgCh chan ServerMsg
	errCh chan error
}

func NewSidebar() SidebarModel {
	return SidebarModel{
		unread:       make(map[string]int),
		online:       make(map[string]bool),
		retired:      make(map[string]bool),
		leftGroups:   make(map[string]bool),
		leftRooms:    make(map[string]bool),
		retiredRooms: make(map[string]bool),
	}
}

// MarkGroupLeft flags a group DM as archived for the local user.
// The sidebar entry stays visible but renders greyed and read-only. Cleared
// only by /delete (which removes the entry entirely) or by being re-added
// to the group by another member.
func (s *SidebarModel) MarkGroupLeft(groupID string) {
	if s.leftGroups == nil {
		s.leftGroups = make(map[string]bool)
	}
	s.leftGroups[groupID] = true
}

// MarkGroupRejoined clears the archived flag, returning a group DM to
// active state. Called when the user is re-added to a group.
func (s *SidebarModel) MarkGroupRejoined(groupID string) {
	delete(s.leftGroups, groupID)
}

// IsGroupLeft returns true if the user has left this group DM
// (archived/read-only in the sidebar).
func (s *SidebarModel) IsGroupLeft(groupID string) bool {
	return s.leftGroups[groupID]
}

// MarkRoomLeft flags a room as archived for the local user. The sidebar
// entry stays visible but renders greyed and read-only. Cleared by
// MarkRoomRejoined when the user is re-added (admin CLI). /delete for
// rooms is a separate future removal path — it does not share state
// with this flag, so nothing here needs to change for that.
func (s *SidebarModel) MarkRoomLeft(roomID string) {
	if s.leftRooms == nil {
		s.leftRooms = make(map[string]bool)
	}
	s.leftRooms[roomID] = true
}

// MarkRoomRejoined clears the archived flag, returning a room to active
// state. Called when the server's room_list re-includes a room we had
// marked left.
func (s *SidebarModel) MarkRoomRejoined(roomID string) {
	delete(s.leftRooms, roomID)
}

// IsRoomLeft returns true if the user has left this room
// (archived/read-only in the sidebar).
func (s *SidebarModel) IsRoomLeft(roomID string) bool {
	return s.leftRooms[roomID]
}

// MarkRoomRetired flags a room as retired by an admin (Phase 12). The
// sidebar entry stays visible but renders greyed with a (retired) suffix
// so the user knows the difference between "I left" and "an admin
// archived this room for everyone". Retirement is permanent — the only
// way to clear a retired room entry is /delete.
func (s *SidebarModel) MarkRoomRetired(roomID string) {
	if s.retiredRooms == nil {
		s.retiredRooms = make(map[string]bool)
	}
	s.retiredRooms[roomID] = true
}

// IsRoomRetired returns true if the room has been flagged as retired.
func (s *SidebarModel) IsRoomRetired(roomID string) bool {
	return s.retiredRooms[roomID]
}

// RemoveRoom drops a room from the sidebar by ID. Used by the
// room_deleted handler when /delete completes (any device, this device
// or another). Clears unread badge, left/retired flags, and resets the
// selected-room cursor if it pointed at the removed entry.
//
// Distinct from MarkRoomLeft / MarkRoomRetired, both of which keep the
// entry visible but greyed. RemoveRoom deletes the entry entirely — the
// user has explicitly asked for it to be gone from their view.
func (s *SidebarModel) RemoveRoom(roomID string) {
	filtered := make([]string, 0, len(s.rooms))
	for _, existing := range s.rooms {
		if existing != roomID {
			filtered = append(filtered, existing)
		}
	}
	s.rooms = filtered
	delete(s.unread, roomID)
	delete(s.leftRooms, roomID)
	delete(s.retiredRooms, roomID)
	if s.selectedRoom == roomID {
		s.selectedRoom = ""
	}
}

// MarkRetired flags a user as retired. Used to render [retired] on any DM
// the user is a member of.
func (s *SidebarModel) MarkRetired(user string) {
	if s.retired == nil {
		s.retired = make(map[string]bool)
	}
	s.retired[user] = true
}

func (s *SidebarModel) SetRooms(rooms []string) {
	s.rooms = rooms
	if s.selectedRoom == "" && len(rooms) > 0 {
		s.selectedRoom = rooms[0]
	}
}

func (s *SidebarModel) SetGroups(groups []protocol.GroupInfo) {
	s.groups = groups
}

// AddGroup appends a new group DM if it doesn't already exist.
func (s *SidebarModel) AddGroup(g protocol.GroupInfo) {
	for _, existing := range s.groups {
		if existing.ID == g.ID {
			return // already present (dedup)
		}
	}
	s.groups = append(s.groups, g)
}

// RemoveGroup drops a group DM from the sidebar by ID. Used by the
// group_deleted handler when /delete completes (any device, this device
// or another). Clears unread badge, archived flag, and resets the
// selected-group cursor if it pointed at the removed entry.
//
// Distinct from MarkGroupLeft, which keeps the entry visible but greyed.
// RemoveGroup deletes the entry entirely — the user has explicitly asked
// for it to be gone.
func (s *SidebarModel) RemoveGroup(groupID string) {
	filtered := make([]protocol.GroupInfo, 0, len(s.groups))
	for _, existing := range s.groups {
		if existing.ID != groupID {
			filtered = append(filtered, existing)
		}
	}
	s.groups = filtered
	delete(s.unread, groupID)
	delete(s.leftGroups, groupID)
	if s.selectedGroup == groupID {
		s.selectedGroup = ""
	}
}

// RenameGroup updates the display name for a group DM.
func (s *SidebarModel) RenameGroup(groupID, name string) {
	for i, g := range s.groups {
		if g.ID == groupID {
			s.groups[i].Name = name
			return
		}
	}
}

func (s *SidebarModel) SetDMs(dms []protocol.DMInfo) {
	s.dms = dms
}

// AddDM appends a new 1:1 DM if it doesn't already exist.
func (s *SidebarModel) AddDM(dm protocol.DMInfo) {
	for _, existing := range s.dms {
		if existing.ID == dm.ID {
			return
		}
	}
	s.dms = append(s.dms, dm)
}

// RemoveDM drops a 1:1 DM from the sidebar by ID. Used by the dm_left
// handler when /delete completes (any device, this device or another).
// Also clears the unread badge so a stale count doesn't reappear if the
// DM is later re-materialised by a fresh incoming message.
func (s *SidebarModel) RemoveDM(dmID string) {
	filtered := make([]protocol.DMInfo, 0, len(s.dms))
	for _, existing := range s.dms {
		if existing.ID != dmID {
			filtered = append(filtered, existing)
		}
	}
	s.dms = filtered
	delete(s.unread, dmID)
	if s.selectedDM == dmID {
		s.selectedDM = ""
	}
}

func (s *SidebarModel) SetUnread(room string, count int) {
	s.unread[room] = count
}

func (s *SidebarModel) SetUnreadGroup(group string, count int) {
	s.unread[group] = count
}

func (s *SidebarModel) SetUnreadDM(dm string, count int) {
	s.unread[dm] = count
}

// IncrementUnread bumps the unread count for a room or group DM by one.
// Called when a message arrives for a non-active context.
func (s *SidebarModel) IncrementUnread(key string) {
	s.unread[key]++
}

func (s *SidebarModel) SetOnline(user string, online bool) {
	s.online[user] = online
}

func (s *SidebarModel) SelectedRoom() string {
	return s.selectedRoom
}

func (s *SidebarModel) SelectedGroup() string {
	return s.selectedGroup
}

func (s *SidebarModel) SelectedDM() string {
	return s.selectedDM
}

func (s SidebarModel) totalItems() int {
	return len(s.rooms) + len(s.groups) + len(s.dms)
}

func (s SidebarModel) Update(msg tea.KeyMsg, c *client.Client) (SidebarModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		s.updateSelection()
	case "down", "j":
		if s.cursor < s.totalItems()-1 {
			s.cursor++
		}
		s.updateSelection()
	case "enter":
		s.updateSelection()
	}
	return s, nil
}

func (s *SidebarModel) updateSelection() {
	s.selectedRoom = ""
	s.selectedGroup = ""
	s.selectedDM = ""

	if s.cursor < len(s.rooms) {
		s.selectedRoom = s.rooms[s.cursor]
	} else if s.cursor < len(s.rooms)+len(s.groups) {
		idx := s.cursor - len(s.rooms)
		s.selectedGroup = s.groups[idx].ID
	} else {
		idx := s.cursor - len(s.rooms) - len(s.groups)
		if idx < len(s.dms) {
			s.selectedDM = s.dms[idx].ID
		}
	}
}

func (s SidebarModel) View(width, height int, focused bool) string {
	var b strings.Builder

	// Rooms header
	b.WriteString(sidebarHeaderStyle.Render(" Rooms"))
	b.WriteString("\n")

	for i, room := range s.rooms {
		displayName := room
		if s.resolveRoomName != nil {
			displayName = s.resolveRoomName(room)
		}
		isLeft := s.leftRooms[room]
		isRetired := s.retiredRooms[room]
		line := " # " + displayName
		// Retired takes visual priority over left — a retired room is
		// archived for everyone, whereas left is user-specific. Show the
		// more "permanent" label so users know re-adding them to the
		// room isn't possible.
		if isRetired {
			line += " " + helpDescStyle.Render("(retired)")
		} else if isLeft {
			line += " " + helpDescStyle.Render("(left)")
		}
		if count, ok := s.unread[room]; ok && count > 0 && !isLeft && !isRetired {
			line += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}

		// Grey out archived rooms (left or retired)
		if isLeft || isRetired {
			line = archivedStyle.Render(line)
		}

		if i == s.cursor && focused {
			line = selectedStyle.Width(width - 2).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	// Messages header
	b.WriteString("\n")
	b.WriteString(sidebarHeaderStyle.Render(" Messages"))
	b.WriteString("\n")

	for i, g := range s.groups {
		name := g.Name
		if name == "" {
			// Show member display names for unnamed groups
			var names []string
			for _, m := range g.Members {
				displayName := m
				if s.resolveName != nil {
					displayName = s.resolveName(m)
				}
				names = append(names, displayName)
			}
			name = strings.Join(names, ", ")
			if len(name) > width-6 {
				name = name[:width-9] + "..."
			}
		}

		dot := offlineDot
		// Mark any retired member so the sidebar shows it. (Online dot is
		// not driven from group members in chunk A — that was a 1:1-only
		// thing in the legacy code.)
		anyRetired := false
		for _, m := range g.Members {
			if s.online[m] {
				dot = onlineDot
			}
			if s.retired[m] {
				anyRetired = true
			}
		}

		isLeft := s.leftGroups[g.ID]
		line := " " + dot + " " + name
		if anyRetired {
			line += " " + helpDescStyle.Render("[retired]")
		}
		if isLeft {
			line += " " + helpDescStyle.Render("(left)")
		}
		if count, ok := s.unread[g.ID]; ok && count > 0 && !isLeft {
			line += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}

		// Grey out archived groups
		if isLeft {
			line = archivedStyle.Render(line)
		}

		idx := len(s.rooms) + i
		if idx == s.cursor && focused {
			line = selectedStyle.Width(width - 2).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	// DMs header + entries (Rooms → Groups → DMs ordering)
	if len(s.dms) > 0 {
		b.WriteString("\n")
		b.WriteString(sidebarHeaderStyle.Render(" DMs"))
		b.WriteString("\n")

		for i, dm := range s.dms {
			// Resolve the other party's display name
			other := ""
			for _, m := range dm.Members {
				if m != s.selfUserID {
					other = m
					break
				}
			}
			name := other
			if name != "" && s.resolveName != nil {
				name = s.resolveName(other)
			}
			if name == "" {
				name = dm.ID // fallback
			}

			dot := offlineDot
			if s.online[other] {
				dot = onlineDot
			}

			line := " " + dot + " " + name
			if other != "" && s.resolveVerified != nil && s.resolveVerified(other) {
				line += " " + verifiedMarker
			}
			if s.retired[other] {
				line += " " + helpDescStyle.Render("[retired]")
			}
			if count, ok := s.unread[dm.ID]; ok && count > 0 {
				line += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
			}

			idx := len(s.rooms) + len(s.groups) + i
			if idx == s.cursor && focused {
				line = selectedStyle.Width(width - 2).Render(line)
			}

			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Pad to fill height
	content := b.String()
	lines := strings.Count(content, "\n")
	for lines < height-2 {
		content += "\n"
		lines++
	}

	style := sidebarStyle
	if focused {
		style = sidebarFocusedStyle
	}

	return style.Width(width).Height(height).Render(content)
}
