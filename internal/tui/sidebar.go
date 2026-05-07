package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

	onlineDot  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("●")
	offlineDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B")).Render("○")

	// archivedStyle greys out sidebar entries for rooms/conversations the
	// user has left. The entry stays visible so history can still be read,
	// but visually it fades into the background.
	archivedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Faint(true)

	// Preview-pane placeholder styles. Used by buildPreviewPlaceholder
	// when no image is selected: a small frame around the
	// "sshkey-term" title in the same slate color as the status-bar
	// "E2E encrypted" text, with a white "no image selected" label
	// below. Frame color tracks the sidebar's focus state to mirror
	// the outer-border behaviour — purple when the sidebar is the
	// active panel (matches sidebarFocusedStyle's #7C3AED), slate
	// otherwise (matches sidebarStyle's #64748B).
	previewFrameStyleFocused   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	previewFrameStyleUnfocused = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B"))
	previewTitleStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B"))
	previewLabelStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))

	// verifiedMarker is the badge appended to a DM sidebar entry when the
	// other party's key has been verified via the safety-number flow. A
	// small green check so the user can see at a glance which DMs are with
	// TOFU-trusted parties and which are not.
	verifiedMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("✓")
)

// SidebarModel manages the sidebar panel.
type SidebarModel struct {
	rooms           []string
	groups          []protocol.GroupInfo
	dms             []protocol.DMInfo
	unread          map[string]int  // room/group/dm -> count
	online          map[string]bool // user -> online
	retired         map[string]bool // user -> retired
	leftGroups      map[string]bool // group ID -> user has left (archived, read-only)
	leftRooms       map[string]bool // room ID -> user has left (archived, read-only)
	retiredRooms    map[string]bool // room ID -> room was retired by an admin (archived, read-only)
	cursor          int             // position in the combined list
	selectedRoom    string
	selectedGroup   string
	selectedDM      string
	resolveName     func(string) string // user nanoid → display name (set by App)
	resolveDMName   func(string) string // dm nanoid → other user's display name (set by App)
	resolveDMOther  func(string) string // dm nanoid → other user's userID (set by App)
	resolveRoomName func(string) string // room nanoid → display name (set by App)
	resolveVerified func(string) bool   // user nanoid → safety-number verified flag (set by App)
	// Phase 14: resolveIsLocalAdmin returns true if the local user is
	// currently an admin of the given group. Reads authoritative state
	// from the client layer's in-memory admin set (which is updated
	// live by group_event{promote,demote}), so the sidebar ★ marker
	// updates immediately without waiting for a group_list refetch.
	// If nil, the sidebar falls back to checking GroupInfo.Admins
	// from its cached slice (which only refreshes on group_list).
	resolveIsLocalAdmin func(string) bool
	selfUserID          string // the current user's ID (for DM "other party" resolve)

	// For message forwarding (set by App)
	msgCh         chan ServerMsg
	errCh         chan error
	keyWarnCh     chan KeyChangeEvent       // Phase 21 F3.a
	attachReadyCh chan AttachmentReadyEvent // auto-preview image downloads

	// previewImagePath, when non-empty, points at a locally-cached
	// image file to render in the bottom preview pane area instead
	// of the default placeholder. Set by App.View each frame from
	// MessagesModel.SelectedImagePath when focus is on the messages
	// pane and no modal is open. Empty otherwise (preview shows the
	// default sshkey-term placeholder).
	previewImagePath string

	// activeRoom / activeGroup / activeDM — the room, group, or DM
	// currently shown in the messages pane. Distinct from
	// selectedRoom/Group/DM (which track cursor position): the user
	// can cursor through the sidebar without actually switching the
	// active context. App.View calls SetActiveContext each frame
	// from messages.room/group/dm so the sidebar can highlight the
	// active entry — letting the user see which conversation
	// they're in regardless of which panel currently has focus.
	activeRoom  string
	activeGroup string
	activeDM    string
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

// dmOtherUserID resolves the other party's userID for a 1:1 DM.
// Prefers the app-provided resolver (client-backed) and falls back to the
// DMInfo member pair in case the client cache hasn't populated yet.
func (s SidebarModel) dmOtherUserID(dm protocol.DMInfo) string {
	if s.resolveDMOther != nil {
		if other := strings.TrimSpace(s.resolveDMOther(dm.ID)); other != "" {
			return other
		}
	}
	for _, m := range dm.Members {
		if m != "" && m != s.selfUserID {
			return m
		}
	}
	return ""
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

// SetPreviewImagePath updates the path of the image to render in the
// preview pane. App.View calls this each frame with the current
// derived state (cursor-on-image AND focus-on-messages AND no modal),
// or "" otherwise. Empty path means render the default placeholder.
func (s *SidebarModel) SetPreviewImagePath(path string) {
	s.previewImagePath = path
}

// SetActiveContext updates the room/group/DM currently shown in the
// messages pane. The sidebar highlights the matching entry so the
// user can see which conversation is active regardless of focus.
// Only one of (room, group, dm) is non-empty at a time — the others
// are cleared. App.View calls this each frame from
// messages.room/group/dm.
func (s *SidebarModel) SetActiveContext(room, group, dm string) {
	s.activeRoom = room
	s.activeGroup = group
	s.activeDM = dm
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

type sidebarListLine struct {
	text      string
	cursorIdx int // -1 for headers / separators (not selectable)
}

const (
	// Fixed preview-box height (inner content rows) at the bottom of the
	// sidebar. Set to 13 = imgMaxRows (12, messages.go) + 1 for the
	// divider, so the image area within the preview is exactly 12 rows
	// — matching the height the previous inline-image rendering used
	// in the messages pane. Landscape photos cap at ~9 rows by aspect
	// math; portraits get partial crop on tall 3:4/9:16 sources.
	sidebarPreviewFixedRows = 13
	// One row for the horizontal divider between list and preview.
	sidebarPreviewDividerRows = 1
	// If the sidebar is too short, preserve list usability by hiding the
	// preview section entirely.
	sidebarMinListRows = 6
)

// sidebarSectionHeights splits the sidebar's inner height into a top list area
// and a fixed bottom preview area. Preview is hidden when the window is too
// short to keep a usable list.
func sidebarSectionHeights(height int) (listRows, previewRows int) {
	if height < 1 {
		return 0, 0
	}
	need := sidebarPreviewFixedRows + sidebarPreviewDividerRows + sidebarMinListRows
	if height < need {
		return height, 0
	}
	return height - sidebarPreviewFixedRows - sidebarPreviewDividerRows, sidebarPreviewFixedRows
}

func sidebarScrollStart(totalRows, selectedRow, windowRows int) int {
	if totalRows <= windowRows || windowRows <= 0 {
		return 0
	}
	if selectedRow < 0 {
		selectedRow = 0
	}
	start := selectedRow - windowRows + 1
	if start < 0 {
		start = 0
	}
	maxStart := totalRows - windowRows
	if start > maxStart {
		start = maxStart
	}
	return start
}

func (s SidebarModel) buildListLines(contentWidth int, focused bool) []sidebarListLine {
	var lines []sidebarListLine
	add := func(text string, idx int) {
		lines = append(lines, sidebarListLine{text: text, cursorIdx: idx})
	}

	// Rooms header
	add(sidebarHeaderStyle.Render(" Rooms"), -1)
	for i, room := range s.rooms {
		displayName := room
		if s.resolveRoomName != nil {
			displayName = s.resolveRoomName(room)
		}
		isLeft := s.leftRooms[room]
		isRetired := s.retiredRooms[room]
		suffix := ""
		if isRetired {
			suffix += " " + helpDescStyle.Render("(retired)")
		} else if isLeft {
			suffix += " " + helpDescStyle.Render("(left)")
		}
		if count, ok := s.unread[room]; ok && count > 0 && !isLeft && !isRetired {
			suffix += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}
		line := fitSidebarLine(" # ", displayName, suffix, contentWidth)
		if isLeft || isRetired {
			line = archivedStyle.Render(line)
		}
		// Highlight when this entry is either the active context
		// (always) or the cursor under sidebar focus (existing nav
		// feedback). Active highlight persists when focus moves
		// elsewhere so the user can see which room they're in.
		if room == s.activeRoom || (i == s.cursor && focused) {
			line = selectedStyle.Width(contentWidth).Render(line)
		}
		add(line, i)
	}

	// Groups header
	add("", -1)
	add(sidebarHeaderStyle.Render(" Groups"), -1)
	for i, g := range s.groups {
		name := g.Name
		if name == "" {
			var names []string
			for _, m := range g.Members {
				displayName := m
				if s.resolveName != nil {
					displayName = s.resolveName(m)
				}
				names = append(names, displayName)
			}
			name = strings.Join(names, ", ")
		}

		dot := offlineDot
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
		var isLocalAdmin bool
		if s.resolveIsLocalAdmin != nil {
			isLocalAdmin = s.resolveIsLocalAdmin(g.ID)
		} else {
			for _, a := range g.Admins {
				if a == s.selfUserID {
					isLocalAdmin = true
					break
				}
			}
		}

		prefix := " " + dot + " "
		if isLocalAdmin && !isLeft {
			prefix += helpDescStyle.Render("★") + " "
		}
		suffix := ""
		if anyRetired {
			suffix += " " + helpDescStyle.Render("[retired]")
		}
		if isLeft {
			suffix += " " + helpDescStyle.Render("(left)")
		}
		if count, ok := s.unread[g.ID]; ok && count > 0 && !isLeft {
			suffix += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
		}
		line := fitSidebarLine(prefix, name, suffix, contentWidth)
		if isLeft {
			line = archivedStyle.Render(line)
		}

		idx := len(s.rooms) + i
		// Active or cursor-under-focus → highlight (see rooms loop
		// for full rationale).
		if g.ID == s.activeGroup || (idx == s.cursor && focused) {
			line = selectedStyle.Width(contentWidth).Render(line)
		}
		add(line, idx)
	}

	// DMs section
	if len(s.dms) > 0 {
		add("", -1)
		add(sidebarHeaderStyle.Render(" DMs"), -1)

		for i, dm := range s.dms {
			other := s.dmOtherUserID(dm)
			name := ""
			if s.resolveDMName != nil {
				name = strings.TrimSpace(s.resolveDMName(dm.ID))
			}
			if name == "" && other != "" && s.resolveName != nil {
				name = s.resolveName(other)
			}
			if name == "" {
				name = other
			}
			if name == "" {
				name = dm.ID
			}

			dot := offlineDot
			if s.online[other] {
				dot = onlineDot
			}

			prefix := " " + dot + " "
			suffix := ""
			if other != "" && s.resolveVerified != nil && s.resolveVerified(other) {
				suffix += " " + verifiedMarker
			}
			if s.retired[other] {
				suffix += " " + helpDescStyle.Render("[retired]")
			}
			if count, ok := s.unread[dm.ID]; ok && count > 0 {
				suffix += unreadStyle.Render(fmt.Sprintf(" (%d)", count))
			}
			line := fitSidebarLinePreferName(prefix, name, suffix, contentWidth)

			idx := len(s.rooms) + len(s.groups) + i
			// Active or cursor-under-focus → highlight (see rooms
			// loop for full rationale).
			if dm.ID == s.activeDM || (idx == s.cursor && focused) {
				line = selectedStyle.Width(contentWidth).Render(line)
			}
			add(line, idx)
		}
	}

	return lines
}

// CursorAtRow maps a visual row inside the sidebar's bordered content area
// (0-indexed inside the panel body) to a cursor index, or -1 if the row is
// non-selectable.
//
// The sidebar body is split into:
//   - top scroll window (rooms/groups/dms list)
//   - divider
//   - fixed preview area
//
// Only rows in the top scroll window can select items. Divider + preview rows
// always return -1 (clicking there should focus the sidebar without changing
// selection).
func (s SidebarModel) CursorAtRow(visualRow int, height int) int {
	if visualRow < 0 {
		return -1
	}
	listRows, _ := sidebarSectionHeights(height)
	if visualRow >= listRows {
		// Divider + preview area: focus sidebar only; no item selection.
		return -1
	}

	lines := s.buildListLines(1, false)
	if len(lines) == 0 {
		return -1
	}
	selectedRow := -1
	for i, line := range lines {
		if line.cursorIdx == s.cursor {
			selectedRow = i
			break
		}
	}
	start := sidebarScrollStart(len(lines), selectedRow, listRows)
	row := start + visualRow
	if row < 0 || row >= len(lines) {
		return -1
	}
	return lines[row].cursorIdx
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

// fitSidebarLine composes a one-line sidebar row while preserving the suffix
// (state markers like "(left)" / "(retired)" / unread counts). It truncates
// the name segment first so important state remains visible in narrow widths.
func fitSidebarLine(prefix, name, suffix string, contentWidth int) string {
	if contentWidth < 1 {
		return ""
	}
	fixed := ansi.StringWidth(prefix) + ansi.StringWidth(suffix)
	nameBudget := contentWidth - fixed
	if nameBudget < 0 {
		return ansi.Truncate(prefix+suffix, contentWidth, "")
	}
	truncTail := ""
	if nameBudget >= 3 {
		truncTail = "..."
	}
	namePart := ansi.Truncate(name, nameBudget, truncTail)
	line := prefix + namePart + suffix
	return ansi.Truncate(line, contentWidth, "")
}

// fitSidebarLinePreferName is used for DM rows where the contact identity is
// the primary affordance. It ensures some portion of the name remains visible
// by trimming suffix badges first when space is tight.
func fitSidebarLinePreferName(prefix, name, suffix string, contentWidth int) string {
	if contentWidth < 1 {
		return ""
	}
	prefixWidth := ansi.StringWidth(prefix)
	if prefixWidth >= contentWidth {
		return ansi.Truncate(prefix, contentWidth, "")
	}
	available := contentWidth - prefixWidth
	if name == "" {
		return ansi.Truncate(prefix+suffix, contentWidth, "")
	}

	// Keep at least one visible name cell when there is room, trimming
	// suffix badges first so DM rows don't collapse to a bare status dot.
	maxSuffixWidth := available - 1
	if maxSuffixWidth < 0 {
		maxSuffixWidth = 0
	}
	suffixPart := suffix
	if ansi.StringWidth(suffixPart) > maxSuffixWidth {
		suffixPart = ansi.Truncate(suffixPart, maxSuffixWidth, "")
	}
	nameBudget := available - ansi.StringWidth(suffixPart)
	if nameBudget < 1 {
		nameBudget = available
		suffixPart = ""
	}
	truncTail := ""
	if nameBudget >= 3 {
		truncTail = "..."
	}
	namePart := ansi.Truncate(name, nameBudget, truncTail)
	line := prefix + namePart + suffixPart
	return ansi.Truncate(line, contentWidth, "")
}

func (s SidebarModel) View(width, height int, focused bool) string {
	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	if height < 1 {
		height = 1
	}

	listRows, previewRows := sidebarSectionHeights(height)
	lines := s.buildListLines(contentWidth, focused)
	selectedRow := -1
	for i, line := range lines {
		if line.cursorIdx == s.cursor {
			selectedRow = i
			break
		}
	}
	start := sidebarScrollStart(len(lines), selectedRow, listRows)

	var out []string
	for i := 0; i < listRows; i++ {
		row := start + i
		if row >= 0 && row < len(lines) {
			out = append(out, lines[row].text)
		} else {
			out = append(out, "")
		}
	}

	if previewRows > 0 {
		// Full-width `─` divider sized to the actual inner content
		// area, NOT `contentWidth`. lipgloss's Width(W) treats W as
		// the inner area (it adds borders outside), so the inner
		// width is `width`, not `width - 2`. The pre-existing
		// `contentWidth = width - 2` is a double-subtraction quirk
		// that leaves every list row with a 2-cell trailing gap
		// (invisible because spaces blend in) — not worth fixing
		// globally since it's also acting as right-side breathing
		// room for the list, but the divider needs the full span
		// to tee into both side borders cleanly. The post-render
		// step below swaps the edge `│` chars for `├`/`┤`.
		//
		// Color mirrors the outer panel border via `focused` so the
		// `├` / `─` / `┤` characters are visually continuous (the
		// post-render tee swap inherits the panel border color, so
		// the inner `─` chars need to match — otherwise focused
		// renders as `purple-├ slate-─── purple-┤`).
		dividerStyle := previewFrameStyleUnfocused
		if focused {
			dividerStyle = previewFrameStyleFocused
		}
		divider := dividerStyle.Render(strings.Repeat("─", width))
		out = append(out, divider)

		// Two paths for the preview-area content. When an image is
		// currently selected (s.previewImagePath != ""), render the
		// image cell-by-cell at the preview-pane dimensions.
		// Otherwise emit the default placeholder ("sshkey-term"
		// brand mark + "no image selected" label). Both paths
		// produce exactly previewRows-1 rows so the divider +
		// content fills the preview budget cleanly.
		out = append(out, s.buildPreviewContent(width, previewRows-1)...)
	}
	// Safety pad in case the preview-builder returned fewer rows than
	// expected, or previewRows == 0 (no preview section).
	for len(out) < height {
		out = append(out, "")
	}
	content := strings.Join(out, "\n")

	style := sidebarStyle
	if focused {
		style = sidebarFocusedStyle
	}

	rendered := style.Width(width).Height(height).Render(content)

	// Tee the divider row's edges into the panel border. Lipgloss
	// renders the divider line as `│─────│` (border + content +
	// border, with the border `│` chars added by Render and the
	// `─` chars from our content). Swap the leftmost and rightmost
	// `│` on the divider row for `├`/`┤` so the visual continues
	// the border instead of cutting it. Row index = 1 (top border)
	// + listRows (list content) — see sidebarSectionHeights for
	// the height-budget split.
	if previewRows > 0 {
		rows := strings.Split(rendered, "\n")
		dividerIdx := 1 + listRows
		if dividerIdx >= 0 && dividerIdx < len(rows) {
			rows[dividerIdx] = teeBorderEdges(rows[dividerIdx])
			rendered = strings.Join(rows, "\n")
		}
	}
	return rendered
}

// teeBorderEdges replaces the first `│` with `├` and the last `│`
// with `┤` in the given rendered row, leaving every other byte
// (including SGR escape sequences emitted by lipgloss for border
// color) untouched. Used by the sidebar to make the list/preview
// divider visually continuous with the panel's side borders.
func teeBorderEdges(row string) string {
	const pipe = "│"
	first := strings.Index(row, pipe)
	last := strings.LastIndex(row, pipe)
	if first < 0 {
		return row
	}
	if first == last {
		return row[:first] + "├" + row[first+len(pipe):]
	}
	return row[:first] + "├" + row[first+len(pipe):last] + "┤" + row[last+len(pipe):]
}

// buildPreviewContent returns the rows for the preview area below
// the divider. Dispatches between two render paths based on whether
// an image is currently selected:
//
//   - previewImagePath set → render the image as cell-aligned block
//     characters via RenderImageInline, then pad to fill the area
//   - previewImagePath empty → render the default placeholder
//     (sshkey-term brand mark)
//
// Output length is exactly `rows`, matching the area below the
// divider so that divider + content fills the preview budget.
func (s SidebarModel) buildPreviewContent(width, rows int) []string {
	if s.previewImagePath == "" {
		return buildPreviewPlaceholder(width, rows)
	}
	return buildPreviewImageRows(s.previewImagePath, width, rows)
}

// buildPreviewImageRows renders the image at imgPath as cell-aligned
// terminal escape sequences sized for the preview pane, centered
// both horizontally and vertically within the (width × rows) area.
// On render failure (decode panic recovered, file missing, etc.)
// the function falls through to a blank fill so the UI doesn't
// break — the placeholder isn't substituted because the caller
// already decided "image mode," and a momentary blank pane is less
// jarring than flashing back to the brand mark.
//
// Centering rationale: RenderImageInline preserves source aspect
// ratio, so the rendered image is rarely exactly width × rows.
// Landscape sources are width-bound and leave vertical headroom;
// portrait sources are height-bound and leave horizontal headroom.
// Without centering, images render top-left-aligned with empty
// trailing space, which looks unfinished. Centering both axes
// produces a balanced layout regardless of source aspect.
func buildPreviewImageRows(imgPath string, width, rows int) []string {
	out := make([]string, rows)
	for i := range out {
		out[i] = ""
	}
	if rows <= 0 || width <= 0 {
		return out
	}
	rendered := RenderImageInline(imgPath, width, rows)
	if rendered == "" {
		return out
	}

	imgRows := strings.Split(rendered, "\n")

	// Compute rendered image width from the first row's visible cell
	// count. Rows have ANSI escape sequences for fg/bg colors, so we
	// use ansi.StringWidth to get the cell-correct width. All rows
	// in the rendered image have the same visible width since the
	// renderer pads each row to the cell grid width.
	imgWidth := 0
	if len(imgRows) > 0 {
		imgWidth = ansi.StringWidth(imgRows[0])
	}

	// Horizontal padding: spaces prepended to each row to center the
	// image within `width`. Spaces have no fg/bg styling so they
	// pick up the terminal background — visually transparent.
	hPad := 0
	if imgWidth < width {
		hPad = (width - imgWidth) / 2
	}
	hPadStr := strings.Repeat(" ", hPad)

	// Vertical padding: number of blank rows above the image. Cap at
	// (rows - len(imgRows)) / 2 ≥ 0; if the image has more rows than
	// the preview can show, the bottom is truncated.
	vPad := 0
	if len(imgRows) < rows {
		vPad = (rows - len(imgRows)) / 2
	}

	for i, r := range imgRows {
		dst := vPad + i
		if dst >= rows {
			break
		}
		out[dst] = hPadStr + r
	}
	return out
}

// buildPreviewPlaceholder returns the rows that fill the sidebar's
// preview area when no image is currently selected. Output length
// is exactly `rows` (typically previewRows-1, the area below the
// divider).
//
// Layout: a small purple-bordered frame containing the "sshkey-term"
// title in slate, with a white "no image selected" label below it.
// Both centered horizontally within the given width. Frame is 15
// cells wide, label is 17 cells; if the sidebar is narrower than
// either, that element is omitted gracefully.
//
// Frame is always purple regardless of sidebar focus state — it's a
// brand mark, not chrome, so it doesn't dim when the sidebar isn't
// active. The list/preview divider above (rendered in the View
// function, not here) DOES follow focus state to remain visually
// continuous with the outer panel border.
//
// Layout positioning (in a typical 12-row preview area):
//
//	rows 0-3:  blank (top padding)
//	row 4:     frame top  ╭─────────────╮
//	row 5:     frame mid  │ sshkey-term │
//	row 6:     frame bot  ╰─────────────╯
//	row 7:     blank
//	row 8:     "no image selected" label (white)
//	rows 9-11: blank (bottom padding)
//
// Smaller `rows` budgets compress the top/bottom padding first.
func buildPreviewPlaceholder(width, rows int) []string {
	out := make([]string, rows)
	for i := range out {
		out[i] = ""
	}
	if rows <= 0 {
		return out
	}

	const frameWidth = 15
	const labelWidth = 17

	// Frame rows (3 rows). Skip if width too narrow. Frame is always
	// purple regardless of focus state — the title surround is its
	// own visual element, not part of the panel border, so it doesn't
	// need to dim with the rest of the chrome when the sidebar isn't
	// active.
	var frameTop, frameMid, frameBot string
	if width >= frameWidth {
		framePad := (width - frameWidth) / 2
		pad := strings.Repeat(" ", framePad)
		frameTop = pad + previewFrameStyleFocused.Render("╭─────────────╮")
		frameMid = pad +
			previewFrameStyleFocused.Render("│") + " " +
			previewTitleStyle.Render("sshkey-term") + " " +
			previewFrameStyleFocused.Render("│")
		frameBot = pad + previewFrameStyleFocused.Render("╰─────────────╯")
	}

	// Label row (1 row). Skip if width too narrow.
	var label string
	if width >= labelWidth {
		labelPad := (width - labelWidth) / 2
		label = strings.Repeat(" ", labelPad) +
			previewLabelStyle.Render("no image selected")
	}

	// Center the placeholder vertically within the row budget.
	// Need 5 content rows: 3 frame + 1 blank + 1 label.
	// Top padding = (rows - 5) / 2, capped at 0.
	const contentRows = 5
	topPad := (rows - contentRows) / 2
	if topPad < 0 {
		topPad = 0
	}

	// Place each piece if there's room.
	if frameTop != "" && topPad < rows {
		out[topPad] = frameTop
	}
	if frameMid != "" && topPad+1 < rows {
		out[topPad+1] = frameMid
	}
	if frameBot != "" && topPad+2 < rows {
		out[topPad+2] = frameBot
	}
	if label != "" && topPad+4 < rows {
		out[topPad+4] = label
	}
	return out
}
