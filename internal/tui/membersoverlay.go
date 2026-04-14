package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// MembersOverlayModel is the Phase 14 /members and /admins one-shot
// overlay. Shows a list of group members with admin markers. Reads
// state from the client's in-memory groupMembers + groupAdmins maps
// at Show() time; subsequent promote/demote events do NOT update
// the already-visible overlay (close and reopen to refresh). This
// matches the one-shot semantics of /audit and /pending.
//
// Mode toggles between "members" (everyone) and "admins" (admin
// subset). Same model + rendering for both — /admins is just a
// pre-filtered view.
type MembersOverlayModel struct {
	visible    bool
	groupID    string
	groupName  string
	mode       string // "members" or "admins"
	rows       []memberRow
	cursor     int
	resolveName func(string) string
}

// memberRow is a flattened entry for rendering: display name + admin
// flag. Built once at Show() time from the client state.
type memberRow struct {
	UserID      string
	DisplayName string
	IsAdmin     bool
}

// Show populates the overlay with the given member + admin lists.
// If adminsOnly is true, members are pre-filtered to just the admin
// subset and the header reads "Admins" instead of "Members".
func (m *MembersOverlayModel) Show(groupID, groupName string, members []string, admins map[string]bool, adminsOnly bool, resolveName func(string) string) {
	m.visible = true
	m.groupID = groupID
	m.groupName = groupName
	m.cursor = 0
	m.resolveName = resolveName
	if adminsOnly {
		m.mode = "admins"
	} else {
		m.mode = "members"
	}

	m.rows = m.rows[:0]
	for _, uid := range members {
		isAdmin := admins[uid]
		if adminsOnly && !isAdmin {
			continue
		}
		name := uid
		if resolveName != nil {
			name = resolveName(uid)
		}
		m.rows = append(m.rows, memberRow{
			UserID:      uid,
			DisplayName: name,
			IsAdmin:     isAdmin,
		})
	}
	// Sort: admins first (in the members mode), then by display name.
	// Stable so equal-status rows stay in member-list order.
	for i := 1; i < len(m.rows); i++ {
		for j := i; j > 0; j-- {
			a, b := m.rows[j-1], m.rows[j]
			if a.IsAdmin == b.IsAdmin {
				if a.DisplayName <= b.DisplayName {
					break
				}
			} else if a.IsAdmin {
				break
			}
			m.rows[j-1], m.rows[j] = b, a
		}
	}
}

func (m *MembersOverlayModel) Hide() {
	m.visible = false
	m.groupID = ""
	m.groupName = ""
	m.mode = ""
	m.rows = nil
	m.cursor = 0
}

func (m *MembersOverlayModel) IsVisible() bool {
	return m.visible
}

func (m MembersOverlayModel) Update(msg tea.KeyMsg) (MembersOverlayModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.Hide()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	}
	return m, nil
}

func (m MembersOverlayModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	title := "Members"
	if m.mode == "admins" {
		title = "Admins"
	}
	groupLabel := m.groupName
	if groupLabel == "" {
		groupLabel = m.groupID
	}
	b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" %s — %s (%d)", title, groupLabel, len(m.rows))))
	b.WriteString("\n\n")

	if len(m.rows) == 0 {
		if m.mode == "admins" {
			b.WriteString(helpDescStyle.Render("  No admins in this group."))
		} else {
			b.WriteString(helpDescStyle.Render("  No members."))
		}
		b.WriteString("\n")
	} else {
		for idx, r := range m.rows {
			marker := "  "
			if r.IsAdmin {
				marker = helpDescStyle.Render("★ ")
			}
			line := "  " + marker + r.DisplayName
			if idx == m.cursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render(" Esc=close  j/k=scroll"))
	return dialogStyle.Width(width - 4).Render(b.String())
}
