package tui

// Clause 4 of the focus design: Esc cancels context, never typed text. A
// reply in progress loses its target and keeps the draft; an edit is left
// with the composer cleared (it held the old message); a plain draft is
// untouched; from another panel Esc comes back to the input with the draft
// as it was.

import (
	"testing"
)

func draftApp(t *testing.T, focus Focus, draft string) App {
	t.Helper()
	a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(focus))
	a.input.textInput.SetValue(draft)
	a.input.textInput.SetCursor(len(draft))
	return a
}

func TestEsc_CancelsReplyAndKeepsDraft(t *testing.T) {
	for _, from := range []Focus{FocusInput, FocusMessages} {
		a := draftApp(t, from, "half a reply")
		a.input.SetReplyContext("mid", "Bob: a reply", "general", "", "")
		got := pressFocusKey(t, a, "esc")
		if got.input.IsReplying() {
			t.Fatalf("from %v: Esc must drop the reply target", from)
		}
		if got.input.Value() != "half a reply" || got.focus != FocusInput {
			t.Fatalf("from %v: draft %q, focus %v; want the draft kept and the input focused", from, got.input.Value(), got.focus)
		}
	}
}

func TestEsc_LeavesEditModeWithTheComposerCleared(t *testing.T) {
	a := draftApp(t, FocusInput, "")
	a.input.EnterEditMode("mid", "the old message")
	got := pressFocusKey(t, a, "esc")
	if got.input.IsEditing() || got.input.Value() != "" || got.focus != FocusInput {
		t.Fatalf("editing %v, draft %q, focus %v; want edit mode left with the composer cleared", got.input.IsEditing(), got.input.Value(), got.focus)
	}
}

func TestEsc_WithAPlainDraftChangesNothing(t *testing.T) {
	a := draftApp(t, FocusInput, "half a message")
	a.input.textInput.SetCursor(4)
	got := pressFocusKey(t, a, "esc")
	if got.input.Value() != "half a message" || got.input.textInput.Position() != 4 || got.focus != FocusInput {
		t.Fatalf("draft %q at %d, focus %v; want nothing changed", got.input.Value(), got.input.textInput.Position(), got.focus)
	}
}

func TestEsc_FromAnotherPanelReturnsToTheInputWithTheDraft(t *testing.T) {
	for _, from := range []Focus{FocusMessages, FocusSidebar} {
		a := draftApp(t, from, "keep me")
		got := pressFocusKey(t, a, "esc")
		if got.focus != FocusInput || got.input.Value() != "keep me" {
			t.Fatalf("from %v: focus %v, draft %q; want the input with the draft kept", from, got.focus, got.input.Value())
		}
	}
}
