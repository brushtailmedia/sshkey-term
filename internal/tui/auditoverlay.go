package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// AuditOverlayModel is the Phase 14 /audit one-shot overlay showing
// recent admin actions for the current group. Populated from the
// local group_events table via store.GetRecentGroupEvents (which
// covers both live broadcasts and sync replay).
//
// Read-only, Esc-dismissable, j/k or up/down to scroll. Mirrors the
// PendingPanelModel shape. Modelled after the legacy paneled
// overlays rather than a modal dialog because the content is
// informational, not decision-requiring.
type AuditOverlayModel struct {
	visible   bool
	groupID   string
	groupName string
	events    []store.GroupEventRow
	cursor    int
	// resolveName converts userID → display name for rendering.
	// Set by the App when the overlay is shown (same pattern as
	// the sidebar.resolveName callback).
	resolveName func(string) string
}

func (o *AuditOverlayModel) Show(groupID, groupName string, events []store.GroupEventRow, resolveName func(string) string) {
	o.visible = true
	o.groupID = groupID
	o.groupName = groupName
	o.events = events
	o.cursor = 0
	o.resolveName = resolveName
}

func (o *AuditOverlayModel) Hide() {
	o.visible = false
	o.groupID = ""
	o.groupName = ""
	o.events = nil
	o.cursor = 0
}

func (o *AuditOverlayModel) IsVisible() bool {
	return o.visible
}

func (o AuditOverlayModel) Update(msg tea.KeyMsg) (AuditOverlayModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		o.Hide()
	case "up", "k":
		if o.cursor > 0 {
			o.cursor--
		}
	case "down", "j":
		if o.cursor < len(o.events)-1 {
			o.cursor++
		}
	}
	return o, nil
}

// renderEvent produces one human-readable line for a single
// group_events row. Mirrors the system-message text in the live
// group_event handler so /audit and the live stream tell the same
// story. Retired users are NOT annotated here — the audit is a
// historical record and retirement markers are a separate concern.
func (o AuditOverlayModel) renderEvent(e store.GroupEventRow) string {
	resolve := o.resolveName
	if resolve == nil {
		resolve = func(uid string) string { return uid }
	}
	userName := resolve(e.User)
	byName := ""
	if e.By != "" {
		byName = resolve(e.By)
	}
	ts := time.Unix(e.TS, 0).Format("01-02 15:04")

	var text string
	switch e.Event {
	case "join":
		if byName != "" {
			text = fmt.Sprintf("%s added %s", byName, userName)
		} else {
			text = fmt.Sprintf("%s joined", userName)
		}
	case "leave":
		switch e.Reason {
		case "retirement":
			text = fmt.Sprintf("%s was retired", userName)
		case "removed":
			if byName != "" {
				text = fmt.Sprintf("%s removed %s", byName, userName)
			} else {
				text = fmt.Sprintf("%s was removed", userName)
			}
		case "admin":
			// Legacy pre-Phase-14 reason code.
			text = fmt.Sprintf("%s was removed by an admin", userName)
		default:
			text = fmt.Sprintf("%s left", userName)
		}
	case "promote":
		if e.Reason == "retirement_succession" {
			text = fmt.Sprintf("%s promoted to admin (previous admin retired)", userName)
		} else if byName != "" {
			text = fmt.Sprintf("%s promoted %s to admin", byName, userName)
		} else {
			text = fmt.Sprintf("%s promoted to admin", userName)
		}
	case "demote":
		if byName != "" {
			text = fmt.Sprintf("%s demoted %s", byName, userName)
		} else {
			text = fmt.Sprintf("%s demoted", userName)
		}
	case "rename":
		text = fmt.Sprintf("%s renamed the group to %q", userName, e.Name)
	default:
		text = fmt.Sprintf("%s %s", e.Event, userName)
	}
	return fmt.Sprintf("  %s  %s", ts, text)
}

func (o AuditOverlayModel) View(width int) string {
	if !o.visible {
		return ""
	}
	var b strings.Builder
	groupLabel := o.groupName
	if groupLabel == "" {
		groupLabel = o.groupID
	}
	b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" Audit — %s (%d)", groupLabel, len(o.events))))
	b.WriteString("\n\n")
	if len(o.events) == 0 {
		b.WriteString(helpDescStyle.Render("  No admin actions recorded yet."))
		b.WriteString("\n")
	} else {
		for idx, e := range o.events {
			line := o.renderEvent(e)
			if idx == o.cursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render(" Esc=close  j/k=scroll"))
	return dialogStyle.Width(width - 4).Render(b.String())
}
