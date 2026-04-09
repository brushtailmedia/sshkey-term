package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDeleteDMConfirm_InitialHidden(t *testing.T) {
	d := DeleteDMConfirmModel{}
	if d.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestDeleteDMConfirm_Show(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	if !d.IsVisible() {
		t.Error("after Show should be visible")
	}
	if d.dm != "dm_ab" {
		t.Errorf("dm = %q, want dm_ab", d.dm)
	}
	if d.otherName != "Alice" {
		t.Errorf("otherName = %q, want Alice", d.otherName)
	}
}

func TestDeleteDMConfirm_HideClearsState(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	d.Hide()
	if d.IsVisible() {
		t.Error("after Hide should not be visible")
	}
	if d.dm != "" || d.otherName != "" {
		t.Errorf("Hide should clear state, got dm=%q name=%q", d.dm, d.otherName)
	}
}

func TestDeleteDMConfirm_YEmitsConfirm(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d.IsVisible() {
		t.Error("after y, dialog should hide")
	}
	if cmd == nil {
		t.Fatal("y should emit a DeleteDMConfirmMsg")
	}
	msg, ok := cmd().(DeleteDMConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteDMConfirmMsg, got %T", cmd())
	}
	if msg.DM != "dm_ab" {
		t.Errorf("emitted DM = %q, want dm_ab", msg.DM)
	}
}

func TestDeleteDMConfirm_EnterEmitsConfirm(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should emit a DeleteDMConfirmMsg")
	}
	msg, ok := cmd().(DeleteDMConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteDMConfirmMsg, got %T", cmd())
	}
	if msg.DM != "dm_ab" {
		t.Errorf("emitted DM = %q, want dm_ab", msg.DM)
	}
}

func TestDeleteDMConfirm_NCancels(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if d.IsVisible() {
		t.Error("after n, dialog should hide")
	}
	if cmd != nil {
		t.Error("n should NOT emit a confirm message")
	}
}

func TestDeleteDMConfirm_EscCancels(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.IsVisible() {
		t.Error("after Esc, dialog should hide")
	}
	if cmd != nil {
		t.Error("Esc should NOT emit a confirm message")
	}
}

func TestDeleteDMConfirm_View_ContainsKeyElements(t *testing.T) {
	d := DeleteDMConfirmModel{}
	d.Show("dm_ab", "Alice")
	view := d.View(80)

	expectedFragments := []string{
		"Delete conversation",
		"Alice",
		"every device",
		"new conversation with no history",
		"[y] Delete",
		"[n] Cancel",
	}
	for _, frag := range expectedFragments {
		if !strings.Contains(view, frag) {
			t.Errorf("view missing fragment %q", frag)
		}
	}
}

func TestDeleteDMConfirm_ViewWhenHiddenIsEmpty(t *testing.T) {
	d := DeleteDMConfirmModel{}
	if got := d.View(80); got != "" {
		t.Errorf("hidden view should be empty, got %q", got)
	}
}
