package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LastAdminPickerModel is the Phase 14 inline promote-picker shown
// when the server rejects /leave or /delete with ErrForbidden because
// the local user is the last admin of the group. The user selects a
// successor from the list of non-admin members; on confirm the client
// sends promote_group_admin + leave_group (or delete_group) in
// sequence. The server serializes the writes so the leave lands
// after the promote and the "at least one admin" invariant stays
// intact during the transition.
//
// Escape cancels the picker and returns the user to the normal
// message view without making any change.
type LastAdminPickerModel struct {
	visible   bool
	group     string
	groupName string
	// triggerDelete distinguishes "this was a /leave" from "this was
	// a /delete" so the confirmation on-y runs the right follow-up.
	// Same picker UI, different outcome on confirm.
	triggerDelete bool
	members       []pickerMember
	cursor        int
}

type pickerMember struct {
	UserID      string
	DisplayName string
}

// LastAdminPickerMsg is emitted on confirm. The app handles it by
// sending promote_group_admin for Successor then leave_group (or
// delete_group if TriggerDelete is true). Server serialization
// guarantees the promote lands first, satisfying the "at least
// one admin" invariant for the subsequent leave.
type LastAdminPickerMsg struct {
	Group         string
	Successor     string
	TriggerDelete bool
}

// Show populates the picker with the group's non-admin members.
// members is the raw list of candidate users (the caller filters out
// the local user, admins, and retired users before passing them).
func (m *LastAdminPickerModel) Show(groupID, groupName string, triggerDelete bool, candidates []pickerMember) {
	m.visible = true
	m.group = groupID
	m.groupName = groupName
	m.triggerDelete = triggerDelete
	m.members = candidates
	m.cursor = 0
}

func (m *LastAdminPickerModel) Hide() {
	m.visible = false
	m.group = ""
	m.groupName = ""
	m.triggerDelete = false
	m.members = nil
	m.cursor = 0
}

func (m *LastAdminPickerModel) IsVisible() bool {
	return m.visible
}

func (m LastAdminPickerModel) Update(msg tea.KeyMsg) (LastAdminPickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.Hide()
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.members)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.members) == 0 || m.cursor >= len(m.members) {
			return m, nil
		}
		groupID := m.group
		successor := m.members[m.cursor].UserID
		triggerDelete := m.triggerDelete
		m.Hide()
		return m, func() tea.Msg {
			return LastAdminPickerMsg{
				Group:         groupID,
				Successor:     successor,
				TriggerDelete: triggerDelete,
			}
		}
	}
	return m, nil
}

func (m LastAdminPickerModel) View(width int) string {
	if !m.visible {
		return ""
	}
	var b strings.Builder
	action := "leave"
	if m.triggerDelete {
		action = "delete"
	}
	groupLabel := m.groupName
	if groupLabel == "" {
		groupLabel = "this group"
	}
	b.WriteString(searchHeaderStyle.Render(" You are the last admin"))
	b.WriteString("\n\n")
	b.WriteString("  " + errorStyle.Render(groupLabel) + " needs an admin to " + action + ".\n")
	b.WriteString("  Promote another member first:\n\n")
	if len(m.members) == 0 {
		b.WriteString("  " + helpDescStyle.Render("No other members available to promote.") + "\n")
		b.WriteString("  " + helpDescStyle.Render("Wait for someone to join, or use /delete to dissolve") + "\n")
		b.WriteString("  " + helpDescStyle.Render("the group if you are genuinely alone.") + "\n\n")
	} else {
		for idx, mem := range m.members {
			line := "    " + mem.DisplayName
			if idx == m.cursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(helpDescStyle.Render("  Enter=promote and " + action + "  Esc=cancel"))
	return dialogStyle.Width(width - 4).Render(b.String())
}
