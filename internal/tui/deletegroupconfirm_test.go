package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDeleteGroupConfirm_InitialHidden(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	if d.IsVisible() {
		t.Error("fresh model should not be visible")
	}
}

func TestDeleteGroupConfirm_Show(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	if !d.IsVisible() {
		t.Error("after Show should be visible")
	}
	if d.group != "group_abc" {
		t.Errorf("group = %q, want group_abc", d.group)
	}
	if d.groupName != "Project X" {
		t.Errorf("groupName = %q, want Project X", d.groupName)
	}
}

func TestDeleteGroupConfirm_HideClearsState(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	d.Hide()
	if d.IsVisible() {
		t.Error("after Hide should not be visible")
	}
	if d.group != "" || d.groupName != "" {
		t.Errorf("Hide should clear state, got group=%q name=%q", d.group, d.groupName)
	}
}

func TestDeleteGroupConfirm_YEmitsConfirm(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d.IsVisible() {
		t.Error("after y, dialog should hide")
	}
	if cmd == nil {
		t.Fatal("y should emit a DeleteGroupConfirmMsg")
	}
	msg, ok := cmd().(DeleteGroupConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteGroupConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_abc" {
		t.Errorf("emitted Group = %q, want group_abc", msg.Group)
	}
}

func TestDeleteGroupConfirm_EnterEmitsConfirm(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should emit a DeleteGroupConfirmMsg")
	}
	msg, ok := cmd().(DeleteGroupConfirmMsg)
	if !ok {
		t.Fatalf("expected DeleteGroupConfirmMsg, got %T", cmd())
	}
	if msg.Group != "group_abc" {
		t.Errorf("emitted Group = %q, want group_abc", msg.Group)
	}
}

func TestDeleteGroupConfirm_NCancels(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if d.IsVisible() {
		t.Error("after n, dialog should hide")
	}
	if cmd != nil {
		t.Error("n should NOT emit a confirm message")
	}
}

func TestDeleteGroupConfirm_EscCancels(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.IsVisible() {
		t.Error("after Esc, dialog should hide")
	}
	if cmd != nil {
		t.Error("Esc should NOT emit a confirm message")
	}
}

func TestDeleteGroupConfirm_View_ContainsKeyElements(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	d.Show("group_abc", "Project X")
	view := d.View(80)

	expectedFragments := []string{
		"Delete group conversation",
		"Project X",
		"every device on your",
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

func TestDeleteGroupConfirm_ViewWhenHiddenIsEmpty(t *testing.T) {
	d := DeleteGroupConfirmModel{}
	if got := d.View(80); got != "" {
		t.Errorf("hidden view should be empty, got %q", got)
	}
}
