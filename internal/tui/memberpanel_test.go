package tui

import (
	"testing"
)

// buildMemberPanel constructs a focused panel with the given members for tests.
func buildMemberPanel(names ...string) MemberPanelModel {
	m := NewMemberPanel()
	m.visible = true
	m.focused = true
	for _, n := range names {
		m.members = append(m.members, memberPanelEntry{User: n, DisplayName: n})
	}
	return m
}

func TestMemberPanel_EnterOpensMenu(t *testing.T) {
	m := buildMemberPanel("alice", "bob", "carol")
	m.cursor = 1

	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter should emit MemberActionMsg")
	}
	action, ok := cmd().(MemberActionMsg)
	if !ok {
		t.Fatalf("expected MemberActionMsg, got %T", cmd())
	}
	if action.Action != "menu" {
		t.Errorf("Enter should open menu, got action=%q", action.Action)
	}
	if action.User != "bob" {
		t.Errorf("expected user=bob, got %q", action.User)
	}
}

func TestMemberPanel_MKeyDirectMessages(t *testing.T) {
	m := buildMemberPanel("alice", "bob")
	m.cursor = 0

	_, cmd := m.Update(keyMsg("m"))
	if cmd == nil {
		t.Fatal("m should emit direct message action")
	}
	action, _ := cmd().(MemberActionMsg)
	if action.Action != "message" {
		t.Errorf("m should send 'message' action, got %q", action.Action)
	}
	if action.User != "alice" {
		t.Errorf("user = %q, want alice", action.User)
	}
}

func TestMemberPanel_NavigationUpDown(t *testing.T) {
	m := buildMemberPanel("a", "b", "c")
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d", m.cursor)
	}
	m, _ = m.Update(keyMsg("down"))
	m, _ = m.Update(keyMsg("down"))
	if m.cursor != 2 {
		t.Errorf("after 2 downs: cursor = %d, want 2", m.cursor)
	}
	m, _ = m.Update(keyMsg("down"))
	if m.cursor != 2 {
		t.Error("cursor should clamp at last member")
	}
	m, _ = m.Update(keyMsg("up"))
	if m.cursor != 1 {
		t.Errorf("after up: cursor = %d, want 1", m.cursor)
	}
}

func TestMemberPanel_NoopWhenUnfocused(t *testing.T) {
	m := buildMemberPanel("alice")
	m.focused = false
	m, cmd := m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("unfocused panel should not emit")
	}
	if m.cursor != 0 {
		t.Error("unfocused panel should ignore navigation")
	}
}

// -- RetireConfirm Shift+Tab fix --

func TestRetireConfirm_ShiftTabCyclesBack(t *testing.T) {
	r := NewRetireConfirm()
	r.Show()
	if r.focused != 0 {
		t.Fatalf("precondition: focused=%d", r.focused)
	}

	// Tab → 1
	r, _ = r.Update(keyMsg("tab"))
	if r.focused != 1 {
		t.Errorf("tab: focused = %d, want 1", r.focused)
	}

	// Shift+Tab → 0 (used to go to 1 again due to the bug)
	r, _ = r.Update(keyMsg("shift+tab"))
	if r.focused != 0 {
		t.Errorf("shift+tab: focused = %d, want 0 (was the bug: both went +1)", r.focused)
	}

	// Shift+Tab → 1
	r, _ = r.Update(keyMsg("shift+tab"))
	if r.focused != 1 {
		t.Errorf("shift+tab wrap: focused = %d, want 1", r.focused)
	}
}
