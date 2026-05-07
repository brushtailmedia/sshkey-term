package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpView_ScrollDownChangesVisibleWindow(t *testing.T) {
	var h HelpModel
	h.SetContext(true)
	h.Toggle()

	before := h.View(100, 14)
	h.Update(tea.KeyMsg{Type: tea.KeyDown}, 100, 14)
	after := h.View(100, 14)

	if before == after {
		t.Fatalf("expected help view to change after scrolling down")
	}
}

func TestHelpView_ScrollClampsAtTop(t *testing.T) {
	var h HelpModel
	h.SetContext(true)
	h.Toggle()

	h.Update(tea.KeyMsg{Type: tea.KeyUp}, 100, 14)
	if h.scroll != 0 {
		t.Fatalf("scroll at top = %d, want 0", h.scroll)
	}
}

func TestHelpView_ShowsNavPrefixBindings(t *testing.T) {
	var h HelpModel
	h.Toggle()

	view := h.View(100, 24)
	for _, token := range []string{"Ctrl+g", "Ctrl+g k", "Ctrl+g n", "Ctrl+g /", "Ctrl+g s"} {
		if !strings.Contains(view, token) {
			t.Fatalf("help view missing nav binding %q", token)
		}
	}
}
