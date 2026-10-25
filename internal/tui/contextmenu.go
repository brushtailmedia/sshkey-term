package tui

import (
	"fmt"
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

// ContextMenuOptions is what the menu needs to know about the message and
// the viewer to decide which items to offer.
type ContextMenuOptions struct {
	IsOwnMessage bool
	IsAdmin      bool
	IsRoom       bool
	PinnedIDs    []string
	// MyReactionEmojis is the set of emojis the current user has already
	// reacted with on this message — each one gets a "Remove reaction:
	// <emoji>" item, which on activation sends unreact for that specific
	// emoji (explicit per-emoji removal rather than toggle-via-re-picking;
	// see PROTOCOL.md Reactions section).
	MyReactionEmojis []string
	// ThreadAvailable: the thread view has something to show for this
	// message — it has replies, is a reply, or has reactions (the pane's
	// t=thread rule). ThreadReplies is the count under it, for the label.
	ThreadAvailable bool
	ThreadReplies   int
	// CanEdit: this is the ONE message the server will accept an edit for
	// — the viewer's most recent non-deleted message in an active
	// conversation (edit_not_most_recent otherwise). The App decides.
	CanEdit bool
}

// Show displays the context menu for the given message at screen position
// (x, y), offering the items opts allows.
func (c *ContextMenuModel) Show(msg DisplayMessage, x, y int, opts ContextMenuOptions) {
	c.visible = true
	c.msg = msg
	c.cursor = 0
	c.x = x
	c.y = y

	c.items = nil
	c.items = append(c.items, ContextMenuItem{Label: "Reply", Action: "reply"})
	// Edit sits with Reply: both go to the composer. Offered only on the
	// one eligible message, so the menu never lists an edit the server
	// would refuse. (The Up-arrow shortcut on an empty input is the fast
	// path; this is the discoverable one.)
	if opts.CanEdit {
		c.items = append(c.items, ContextMenuItem{Label: "Edit", Action: "edit"})
	}
	c.items = append(c.items, ContextMenuItem{Label: "React", Action: "react"})
	for _, emoji := range opts.MyReactionEmojis {
		c.items = append(c.items, ContextMenuItem{
			Label:  "Remove reaction: " + emoji,
			Action: "unreact",
			Data:   emoji,
		})
	}

	// Thread — the menu's form of the pane's `t`, offered under the same
	// rule as the pane's t=thread hint, with the reply count when there is
	// one. It sits with the read actions, above pin and delete. No root is
	// sent: the App resolves the thread's top-level message as the pane's
	// `t` does.
	if opts.ThreadAvailable {
		label := "Thread"
		if n := opts.ThreadReplies; n > 0 {
			label = fmt.Sprintf("Thread (%d %s)", n, pluralize(n, "reply", "replies"))
		}
		c.items = append(c.items, ContextMenuItem{Label: label, Action: "thread"})
	}

	// Links: open in the browser / copy to the clipboard. With several
	// links in the message the App offers a picker; the menu only says
	// the action exists.
	if len(extractLinks(msg.Body)) > 0 {
		c.items = append(c.items, ContextMenuItem{Label: "Open link", Action: "open_link"})
		c.items = append(c.items, ContextMenuItem{Label: "Copy link", Action: "copy_link"})
	}

	if opts.IsRoom {
		isPinned := false
		for _, id := range opts.PinnedIDs {
			if id == msg.ID {
				isPinned = true
				break
			}
		}
		if isPinned {
			c.items = append(c.items, ContextMenuItem{Label: "Unpin", Action: "unpin"})
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
	if opts.IsOwnMessage || (opts.IsAdmin && opts.IsRoom) {
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

// AnchorXY returns the (col, row) anchor where the menu should be drawn.
// This is the click position passed to Show — the App's overlay() helper
// clamps it to the visible terminal so the menu stays on-screen.
func (c ContextMenuModel) AnchorXY() (int, int) {
	return c.x, c.y
}

func (c ContextMenuModel) Update(msg tea.KeyMsg) (ContextMenuModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// `q` closes any surface without a text field (the owner's
		// preference; see q-closes-everywhere.md) — the menu has none.
		// The App-level guard for this menu accepts the same two keys.
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
