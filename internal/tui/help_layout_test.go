package tui

import (
	"strings"
	"testing"
)

func TestHelpView_UsesReadableSections(t *testing.T) {
	var h HelpModel
	h.SetContext(true)
	h.Toggle()

	view := h.View(120, 40)
	if !strings.Contains(view, "Message Actions") {
		t.Fatalf("help view missing Message Actions section:\n%s", view)
	}
	if !strings.Contains(view, "Member Panel") {
		t.Fatalf("help view missing Member Panel section:\n%s", view)
	}
	if strings.Contains(view, "m member panel:") {
		t.Fatalf("legacy cramped row label should not appear:\n%s", view)
	}
}

func TestHelpView_AdminCommandVisibility(t *testing.T) {
	{
		var h HelpModel
		h.SetContext(false)
		h.Toggle()
		view := h.View(120, 220)
		if strings.Contains(view, "/add <user>") {
			t.Fatalf("non-admin help should hide admin verbs, got:\n%s", view)
		}
	}
	{
		var h HelpModel
		h.SetContext(true)
		h.Toggle()
		view := h.View(120, 220)
		if !strings.Contains(view, "/add <user>") {
			t.Fatalf("admin help should show admin verbs, got:\n%s", view)
		}
	}
}
