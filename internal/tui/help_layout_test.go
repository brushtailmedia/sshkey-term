package tui

import (
	"strings"
	"testing"
)

func TestHelpView_UsesReadableSections(t *testing.T) {
	var h HelpModel
	h.Toggle()

	view := h.View(120, 40)
	if !strings.Contains(view, "Message Actions") {
		t.Fatalf("help view missing Message Actions section:\n%s", view)
	}
	if !strings.Contains(view, "Member Panel") {
		t.Fatalf("help view missing Member Panel section:\n%s", view)
	}
	if !strings.Contains(view, "Ctrl+q") {
		t.Fatalf("help view missing Ctrl+q shortcut:\n%s", view)
	}
	if strings.Contains(view, "m member panel:") {
		t.Fatalf("legacy cramped row label should not appear:\n%s", view)
	}
}

// §1: the help panel is a complete reference — every command is always
// shown regardless of role/context; admin-only verbs are *labelled*,
// never hidden. Replaces the removed TestHelpView_AdminCommandVisibility,
// which locked the old hide-when-not-admin gate (missing.md §1).
func TestHelpView_AllCommandsShownAndAdminLabelled(t *testing.T) {
	var h HelpModel // no SetContext — role-gating removed
	h.Toggle()
	view := h.View(120, 220)

	for _, want := range []string{
		"/add <user>", "/kick <user>", "/promote <user>",
		"/demote <user>", "/transfer <user>", "/rename <name>", "/undo",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("help must always list admin verb %q (no role-gating), got:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "(group admin only)") {
		t.Fatalf("admin-only verbs must be labelled \"(group admin only)\", got:\n%s", view)
	}
}

func TestHelpView_ShowsSlashParityCommands(t *testing.T) {
	var h HelpModel
	h.Toggle()
	view := h.View(120, 220)

	for _, want := range []string{
		"/groupinfo",
		"/groupcreate",
		"/dmcreate",
		"/rename <name>",
		"/typing [on|off]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %s, got:\n%s", want, view)
		}
	}
}
