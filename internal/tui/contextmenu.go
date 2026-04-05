package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ContextMenuItem is a single item in a context menu.
type ContextMenuItem struct {
	Label  string
	Action string
	Data   string // optional payload (e.g., the emoji to remove)
}

// ContextMenuModel shows a popup menu for a selected message.
type ContextMenuModel struct {
	visible bool
	items   []ContextMenuItem
	cursor  int
	msg     DisplayMessage
	x, y    int // screen position
}

func NewContextMenu() ContextMenuModel {
	return ContextMenuModel{}
}

// Show displays the context menu for the given message. myReactionEmojis is
// the set of emojis the current user has already reacted with on this
// message — each one gets a "Remove reaction: <emoji>" menu item, which on
// activation sends unreact for that specific emoji.
func (c *ContextMenuModel) Show(msg DisplayMessage, x, y int, isOwnMessage, isAdmin, isRoom bool, pinnedIDs []string, myReactionEmojis []string) {
	c.visible = true
	c.msg = msg
	c.cursor = 0
	c.x = x
	c.y = y

	c.items = nil
	c.items = append(c.items, ContextMenuItem{Label: "Reply", Action: "reply"})
	c.items = append(c.items, ContextMenuItem{Label: "React", Action: "react"})

	// One "Remove reaction: <emoji>" item per emoji the current user has
	// reacted with. Explicit per-emoji removal rather than toggle-via-
	// re-picking (see PROTOCOL.md Reactions section).
	for _, emoji := range myReactionEmojis {
		c.items = append(c.items, ContextMenuItem{
			Label:  "Remove reaction: " + emoji,
			Action: "unreact",
			Data:   emoji,
		})
	}

	if isRoom {
		isPinned := false
		for _, id := range pinnedIDs {
			if id == msg.ID {
				isPinned = true
				break
			}
		}
		if isPinned {
			c.items = append(c.items, ContextMenuItem{Label: "Unpin", Action: "pin"})
		} else {
			c.items = append(c.items, ContextMenuItem{Label: "Pin to room", Action: "pin"})
		}
	}

	// Attachments
	if len(msg.Attachments) > 0 {
		c.items = append(c.items, ContextMenuItem{Label: "Open attachment", Action: "open_attachment"})
		c.items = append(c.items, ContextMenuItem{Label: "Save attachment", Action: "save_attachment"})
	}

	// Delete — own messages or admin in rooms
	if isOwnMessage || (isAdmin && isRoom) {
		c.items = append(c.items, ContextMenuItem{Label: "Delete", Action: "delete"})
	}

	c.items = append(c.items, ContextMenuItem{Label: "Copy text", Action: "copy"})
}

func (c *ContextMenuModel) Hide() {
	c.visible = false
}

func (c *ContextMenuModel) IsVisible() bool {
	return c.visible
}

func (c ContextMenuModel) Update(msg tea.KeyMsg) (ContextMenuModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.Hide()
		return c, nil
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(c.items)-1 {
			c.cursor++
		}
	case "enter":
		if c.cursor < len(c.items) {
			action := c.items[c.cursor].Action
			data := c.items[c.cursor].Data
			target := c.msg
			c.Hide()
			return c, func() tea.Msg {
				return MessageAction{Action: action, Msg: target, Data: data}
			}
		}
	}
	return c, nil
}

func (c ContextMenuModel) View() string {
	if !c.visible {
		return ""
	}

	var b strings.Builder
	for i, item := range c.items {
		line := "  " + item.Label
		if i == c.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return dialogStyle.Render(b.String())
}
