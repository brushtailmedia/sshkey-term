package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSettings_EditNameValid(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("  New Name  ")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("valid name should emit ProfileUpdateMsg")
	}
	msg := cmd()
	pu, ok := msg.(ProfileUpdateMsg)
	if !ok {
		t.Fatalf("expected ProfileUpdateMsg, got %T", msg)
	}
	if pu.DisplayName != "New Name" {
		t.Errorf("name = %q, want trimmed 'New Name'", pu.DisplayName)
	}
	if s.editing {
		t.Error("should exit edit mode on valid name")
	}
}

func TestSettings_EditNameTooShort(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("A")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("invalid name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode on invalid name")
	}
}

func TestSettings_EditNameEmpty(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("   ")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("whitespace-only name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_EditNameTooLong(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("abcdefghijklmnopqrstuvwxyz1234567") // 33 chars

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("too-long name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_EditNameZeroWidth(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("test\u200Bname")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("name with zero-width chars should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_EditStatusNoValidation(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_status"
	s.editInput.SetValue("A") // would fail name validation but status has no restriction

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("status should emit without validation")
	}
	msg := cmd()
	su, ok := msg.(StatusUpdateMsg)
	if !ok {
		t.Fatalf("expected StatusUpdateMsg, got %T", msg)
	}
	if su.Text != "A" {
		t.Errorf("status = %q", su.Text)
	}
}
