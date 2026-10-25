package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Edit sits directly after Reply, and only when the App says the message
// is editable.
func TestContextMenu_EditItem(t *testing.T) {
	msg := DisplayMessage{ID: "m1", FromID: "usr_self", From: "me", Body: "typo"}
	var c ContextMenuModel

	c.Show(msg, 5, 5, ContextMenuOptions{})
	if menuIndexOf(c, "edit") >= 0 {
		t.Errorf("no Edit unless eligible: %v", menuLabels(c))
	}

	c.Show(msg, 5, 5, ContextMenuOptions{CanEdit: true, IsOwnMessage: true})
	got := menuLabels(c)
	want := []string{"Reply", "Edit", "React", "Delete", "Copy text"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("items = %v, want %v", got, want)
		}
	}
}

// ownAndOtherMessages: the viewer (usr_self) has an older message and a
// most recent one; someone else wrote the last message in the room.
func ownAndOtherMessages() []DisplayMessage {
	return []DisplayMessage{
		{ID: "mine_old", From: "me", FromID: "usr_self", Body: "first draft", TS: 1000},
		{ID: "theirs", From: "Bob", FromID: "usr_bob", Body: "hello", TS: 1001},
		{ID: "mine_new", From: "me", FromID: "usr_self", Body: "second draft", TS: 1002},
		{ID: "theirs_last", From: "Bob", FromID: "usr_bob", Body: "bye", TS: 1003},
	}
}

// Eligibility: only the viewer's most recent non-deleted message, in an
// active context.
func TestApp_CanEditMessage(t *testing.T) {
	a := newPaneThreadApp(t)
	a.messages.messages = ownAndOtherMessages()
	byID := func(id string) DisplayMessage {
		for _, m := range a.messages.messages {
			if m.ID == id {
				return m
			}
		}
		t.Fatalf("no message %s", id)
		return DisplayMessage{}
	}

	if !a.canEditMessage(byID("mine_new")) {
		t.Error("the most recent own message must be editable")
	}
	if a.canEditMessage(byID("mine_old")) {
		t.Error("an older own message is not editable (the server would refuse it)")
	}
	if a.canEditMessage(byID("theirs_last")) {
		t.Error("someone else's message is never editable")
	}

	a.messages.messages[2].Deleted = true // mine_new deleted → mine_old becomes the most recent
	if !a.canEditMessage(byID("mine_old")) {
		t.Error("with the newer one deleted, the older own message is the editable one")
	}

	a.messages.SetLeft(true)
	if a.canEditMessage(byID("mine_old")) {
		t.Error("an archived context offers no edit")
	}
}

// End to end: Enter on the eligible message offers Edit; choosing it puts
// the body in the composer in edit mode with focus on the input. The Up
// arrow path is unchanged and agrees on the target.
func TestApp_ContextMenuEdit_EntersEditMode(t *testing.T) {
	a := newPaneThreadApp(t)
	a.messages.messages = ownAndOtherMessages()
	a.messages.cursor = 2 // mine_new

	model, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open_menu
	a = deliver(t, model.(App), cmd)
	if !a.contextMenu.IsVisible() {
		t.Fatal("Enter must open the context menu")
	}
	idx := menuIndexOf(a.contextMenu, "edit")
	if idx < 0 {
		t.Fatalf("menu lacks Edit on the most recent own message: %v", menuLabels(a.contextMenu))
	}
	for a.contextMenu.cursor != idx {
		model, _ = a.Update(keyRune('j'))
		a = model.(App)
	}
	model, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = deliver(t, model.(App), cmd)
	if a.contextMenu.IsVisible() {
		t.Error("choosing Edit must close the menu")
	}
	if !a.input.IsEditing() || a.input.EditTarget() != "mine_new" {
		t.Fatalf("editing=%v target=%q; want edit mode on mine_new", a.input.IsEditing(), a.input.EditTarget())
	}
	if a.input.Value() != "second draft" {
		t.Errorf("composer = %q, want the message body", a.input.Value())
	}
	if a.focus != FocusInput {
		t.Errorf("focus = %v, want the input", a.focus)
	}

	// The older own message gets no Edit item.
	a.input.ExitEditMode()
	a.input.ClearInput()
	a.focus = FocusMessages
	a.messages.cursor = 0 // mine_old
	model, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = deliver(t, model.(App), cmd)
	if menuIndexOf(a.contextMenu, "edit") >= 0 {
		t.Errorf("an older own message must not offer Edit: %v", menuLabels(a.contextMenu))
	}
}
