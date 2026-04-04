package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// InfoPanelModel manages the room/group info overlay.
type InfoPanelModel struct {
	visible      bool
	room         string
	conversation string
	members      []memberInfo
	topic        string
	name         string
	isGroup      bool
	muted        bool
	cursor       int
}

type memberInfo struct {
	User        string
	DisplayName string
	Online      bool
	Verified    bool
}

// MemberActionMsg is sent when the user selects a member from the info panel.
type MemberActionMsg struct {
	Action string // "message", "create_group", "verify", "profile"
	User   string
}

func (i *InfoPanelModel) ShowRoom(room string, c *client.Client, online map[string]bool) {
	i.visible = true
	i.room = room
	i.conversation = ""
	i.isGroup = false
	i.cursor = 0

	// Get room info from client profiles
	i.members = nil
	if c != nil {
		c.ForEachProfile(func(p *protocol.Profile) {
			// Check if user is in this room (we don't have per-room member lists client-side,
			// so show all known users for now — TODO: track room membership)
			i.members = append(i.members, memberInfo{
				User:        p.User,
				DisplayName: p.DisplayName,
				Online:      online[p.User],
			})
		})
	}
}

func (i *InfoPanelModel) ShowConversation(convID string, c *client.Client, online map[string]bool) {
	i.visible = true
	i.room = ""
	i.conversation = convID
	i.isGroup = true
	i.cursor = 0

	i.members = nil
	if c != nil {
		members := c.ConvMembers(convID)
		for _, m := range members {
			p := c.Profile(m)
			displayName := m
			if p != nil {
				displayName = p.DisplayName
			}
			i.members = append(i.members, memberInfo{
				User:        m,
				DisplayName: displayName,
				Online:      online[m],
			})
		}
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
	}
	return i, nil
}

func (i InfoPanelModel) View(width int) string {
	if !i.visible {
		return ""
	}

	var b strings.Builder

	if i.room != "" {
		b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" #%s — info", i.room)))
	} else {
		title := i.conversation
		if i.name != "" {
			title = i.name
		}
		b.WriteString(searchHeaderStyle.Render(fmt.Sprintf(" %s — info", title)))
	}
	b.WriteString("\n\n")

	if i.topic != "" {
		b.WriteString(" Topic: " + i.topic + "\n\n")
	}

	// Mute status
	muteLabel := "off"
	if i.muted {
		muteLabel = "on"
	}
	b.WriteString(fmt.Sprintf(" Muted: [%s]  (press m to toggle)\n\n", muteLabel))

	// Members
	b.WriteString(fmt.Sprintf(" Members (%d):\n", len(i.members)))
	for idx, m := range i.members {
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
		b.WriteString(line + "\n")
	}

	if i.isGroup {
		b.WriteString("\n")
		b.WriteString(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))
	} else {
		b.WriteString("\n")
		b.WriteString(helpDescStyle.Render(" Enter=message  m=mute  Esc=close"))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
