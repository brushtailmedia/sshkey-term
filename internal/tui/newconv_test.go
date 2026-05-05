package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewConv_EnterAddsMemberWhenMemberFieldFocused(t *testing.T) {
	n := NewNewConv()
	n.Show([]string{"usr_alice", "usr_bob"})

	model, cmd := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model

	if cmd != nil {
		t.Fatal("enter on member field should add member, not create")
	}
	if !n.selected["usr_alice"] {
		t.Fatal("expected first suggestion to be selected on enter")
	}
	if !n.visible {
		t.Fatal("dialog should stay visible after adding member")
	}
}

func TestNewConv_EnterCreatesWhenNameFieldFocused(t *testing.T) {
	n := NewNewConv()
	n.Show([]string{"usr_alice", "usr_bob"})

	// Select one member first.
	model, _ := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model

	// Move focus to name field and submit.
	model, _ = n.Update(tea.KeyMsg{Type: tea.KeyTab})
	n = model
	n.nameInput.SetValue("Project Team")

	model, cmd := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model

	if cmd == nil {
		t.Fatal("enter on name field should create")
	}
	create, ok := cmd().(CreateConvMsg)
	if !ok {
		t.Fatalf("create cmd returned %T, want CreateConvMsg", cmd())
	}
	if !containsMember(create.Members, "usr_alice") {
		t.Fatalf("create members %v missing selected member usr_alice", create.Members)
	}
	if create.Name != "Project Team" {
		t.Fatalf("create name = %q, want %q", create.Name, "Project Team")
	}
	if n.visible {
		t.Fatal("dialog should hide after create")
	}
}

func TestNewConv_EnterCreatesWhenCreateButtonFocused(t *testing.T) {
	n := NewNewConv()
	n.Show([]string{"usr_alice", "usr_bob"})

	// Select one member first.
	model, _ := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model

	// Tab order with a single selected member:
	// members -> create -> cancel
	model, _ = n.Update(tea.KeyMsg{Type: tea.KeyTab})
	n = model

	model, cmd := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model
	if cmd == nil {
		t.Fatal("enter on [Create] should create")
	}
	create, ok := cmd().(CreateConvMsg)
	if !ok {
		t.Fatalf("create cmd returned %T, want CreateConvMsg", cmd())
	}
	if !containsMember(create.Members, "usr_alice") {
		t.Fatalf("create members %v missing selected member usr_alice", create.Members)
	}
	if n.visible {
		t.Fatal("dialog should hide after create")
	}
}

func TestNewConv_EnterOnCancelHidesWithoutCreate(t *testing.T) {
	n := NewNewConv()
	n.Show([]string{"usr_alice", "usr_bob"})

	// Tab order with no selected members:
	// members -> create -> cancel
	model, _ := n.Update(tea.KeyMsg{Type: tea.KeyTab})
	n = model
	model, _ = n.Update(tea.KeyMsg{Type: tea.KeyTab})
	n = model

	model, cmd := n.Update(tea.KeyMsg{Type: tea.KeyEnter})
	n = model
	if cmd != nil {
		t.Fatal("enter on [Cancel] should not emit create")
	}
	if n.visible {
		t.Fatal("dialog should hide after cancel")
	}
}

func TestNewConv_ViewHasButtonsAndNoHelperText(t *testing.T) {
	n := NewNewConv()
	n.Show([]string{"usr_alice", "usr_bob"})

	view := n.View(100)
	if !strings.Contains(view, "[Create]") {
		t.Fatalf("view missing [Create] button: %q", view)
	}
	if !strings.Contains(view, "[Cancel]") {
		t.Fatalf("view missing [Cancel] button: %q", view)
	}
	if strings.Contains(view, "Enter=add") || strings.Contains(view, "Ctrl+Enter=create") {
		t.Fatalf("helper text should be removed: %q", view)
	}
}

func containsMember(members []string, target string) bool {
	for _, m := range members {
		if m == target {
			return true
		}
	}
	return false
}
