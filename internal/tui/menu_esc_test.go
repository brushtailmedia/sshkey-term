package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestApp_EscDismissesContextMenu pins the regression for the
// 2026-05-05 user report: with the message context menu open, Esc
// did not close it — only selecting an item dismissed it. The fix
// handles Esc at the App level for both menus rather than relying
// solely on the menu's inner Update method, since the inner Update
// passed unit tests yet the real-world dismiss didn't fire.
func TestApp_EscDismissesContextMenu(t *testing.T) {
	a := App{}
	a.contextMenu = NewContextMenu()
	a.contextMenu.Show(
		DisplayMessage{ID: "m1", FromID: "u1", From: "alice", Body: "hi"},
		5, 5,
		false, false, false,
		nil, nil,
	)
	if !a.contextMenu.IsVisible() {
		t.Fatal("precondition: contextMenu should be visible after Show")
	}

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := model.(App)
	if got.contextMenu.IsVisible() {
		t.Errorf("contextMenu should be hidden after Esc through App.Update")
	}
}

// TestApp_EscDismissesMemberMenu mirrors the contextMenu test for
// the member panel's right-click menu.
func TestApp_EscDismissesMemberMenu(t *testing.T) {
	a := App{}
	a.memberMenu = NewMemberMenu()
	a.memberMenu.Show("usr_bob", "Bob", 10, 10)
	if !a.memberMenu.IsVisible() {
		t.Fatal("precondition: memberMenu should be visible after Show")
	}

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := model.(App)
	if got.memberMenu.IsVisible() {
		t.Errorf("memberMenu should be hidden after Esc through App.Update")
	}
}

// TestApp_NonEscRoutesToContextMenuUpdate verifies the App-level
// Esc handler doesn't accidentally swallow OTHER keys — Up/Down/Enter
// must still flow through to the menu's Update so navigation works.
func TestApp_NonEscRoutesToContextMenuUpdate(t *testing.T) {
	a := App{}
	a.contextMenu = NewContextMenu()
	a.contextMenu.Show(
		DisplayMessage{ID: "m1", FromID: "u1", From: "alice", Body: "hi"},
		5, 5,
		false, false, false,
		nil, nil,
	)
	cursorBefore := a.contextMenu.cursor

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := model.(App)
	if !got.contextMenu.IsVisible() {
		t.Errorf("contextMenu should still be visible after Down (only Esc dismisses)")
	}
	if got.contextMenu.cursor == cursorBefore {
		t.Errorf("contextMenu cursor didn't advance — Down arrow not routed to inner Update")
	}
}
