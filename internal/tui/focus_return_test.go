package tui

// Clause 1 of the focus design, as reversed on 2026-10-26: closing an
// overlay returns focus to where it was when the overlay opened. Clause 2:
// a jump lands on the message; committing a conversation, in the sidebar
// or the quick switch, lands on the composer.

import (
	"testing"
)

func focusName(f Focus) string {
	switch f {
	case FocusInput:
		return "input"
	case FocusSidebar:
		return "sidebar"
	case FocusMessages:
		return "messages"
	case FocusMembers:
		return "members"
	}
	return "?"
}

// Every overlay in the opener table, opened from the input, the pane and
// the sidebar, returns there when closed (Esc, or q where Esc is not its
// key), with the pane cursor untouched.
func TestFocus_ClosingReturnsYouWhereYouWere(t *testing.T) {
	for _, op := range overlayOpeners {
		if op.field == "memberPanel" {
			continue // a panel, not an overlay
		}
		for _, from := range []Focus{FocusInput, FocusMessages, FocusSidebar} {
			t.Run(op.field+"/from "+focusName(from), func(t *testing.T) {
				a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(from))
				op.open(t, &a)
				a.noteFocusReturn(from)     // what the Update wrapper records when a key opens it
				cursor := a.messages.cursor // as it stands while the overlay is up
				got := pressFocusKey(t, a, "esc")
				if op.visible(&got) {
					got = pressFocusKey(t, got, "q")
				}
				if op.visible(&got) {
					t.Fatalf("%s: neither Esc nor q closed it", op.field)
				}
				if got.focus != from {
					t.Fatalf("%s closed from the %s: focus on the %s; want back where it was", op.field, focusName(from), focusName(got.focus))
				}
				if got.messages.cursor != cursor {
					t.Fatalf("%s: the pane cursor moved from %d to %d", op.field, cursor, got.messages.cursor)
				}
			})
		}
	}
}

// The real path: the key that opens an overlay is what records where you
// were, and closing it brings you back.
func TestFocus_RealKeysRecordWhereYouWere(t *testing.T) {
	for _, tc := range []struct {
		name, open, close string
		visible           func(a *App) bool
	}{
		{"help", "?", "esc", func(a *App) bool { return a.help.IsVisible() }},
		{"info panel", "i", "esc", func(a *App) bool { return a.infoPanel.IsVisible() }},
		{"thread view", "t", "q", func(a *App) bool { return a.threadPanel.IsVisible() }},
		{"context menu", "enter", "esc", func(a *App) bool { return a.contextMenu.IsVisible() }},
		{"emoji picker", "e", "esc", func(a *App) bool { return a.emojiPicker.IsVisible() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages))
			model, cmd := a.Update(focusKey(tc.open))
			got := deliver(t, model.(App), cmd)
			if !tc.visible(&got) {
				t.Fatalf("%s did not open on %q", tc.name, tc.open)
			}
			model, cmd = got.Update(focusKey(tc.close))
			got = deliver(t, model.(App), cmd)
			if tc.visible(&got) || got.focus != FocusMessages {
				t.Fatalf("%s: open %v, focus %s; want it closed and focus back on the pane", tc.name, tc.visible(&got), focusName(got.focus))
			}
		})
	}
}

// One overlay replacing another keeps the first origin: the thread view
// opened from the pane, then e on a message closes it and opens the emoji
// picker; Esc on the picker returns to the pane.
func TestFocus_NestedOverlaysReturnToTheFirstOrigin(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages))
	model, cmd := a.Update(focusKey("t"))
	got := deliver(t, model.(App), cmd)
	model, cmd = got.Update(focusKey("e"))
	got = deliver(t, model.(App), cmd)
	if got.threadPanel.IsVisible() || !got.emojiPicker.IsVisible() {
		t.Fatal("precondition: e in the thread view closes it and opens the picker")
	}
	got = pressFocusKey(t, got, "esc")
	if got.emojiPicker.IsVisible() || got.focus != FocusMessages {
		t.Fatalf("focus on the %s; want the pane the thread view was opened from", focusName(got.focus))
	}
}

// Clause 2: a search jump lands on the message pane.
func TestFocus_SearchJumpLandsOnThePane(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusInput))
	a.search.Show()
	a.noteFocusReturn(FocusInput)
	model, _ := a.Update(SearchJumpMsg{Room: "general", MessageID: "mid"})
	got := model.(App)
	if got.search.IsVisible() || got.focus != FocusMessages {
		t.Fatalf("search %v, focus %s; want the search closed and the pane focused", got.search.IsVisible(), focusName(got.focus))
	}
}

// Clause 2 (G3): a click on a conversation row commits it like Enter and
// lands on the composer. A click elsewhere in the sidebar only focuses the
// sidebar (sidebar_preview_test.go covers the preview area).
func TestFocus_SidebarClickOnAConversationLandsOnTheComposer(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""), withFocus(FocusMessages))
	a.sidebar.SetRooms([]string{"general", "random"})
	layout := computeLayout(a.width, a.height, false)
	inner := layout.SidebarY1 - layout.SidebarY0 - 2
	row := -1
	for v := 0; v < inner; v++ {
		if a.sidebar.CursorAtRow(v, inner) == 1 {
			row = v
			break
		}
	}
	if row < 0 {
		t.Fatal("precondition: the second room has a visible row")
	}
	model, _ := a.handleMouseClick(layout.SidebarX0+1, layout.SidebarY0+1+row)
	got := model.(App)
	if got.messages.room != "random" || got.focus != FocusInput {
		t.Fatalf("click on random: room %q, focus %s; want random with the composer focused", got.messages.room, focusName(got.focus))
	}
}

// Clause 2: committing a conversation lands on the composer — Enter in the
// sidebar, and the quick switch even when it was opened from the pane.
func TestFocus_CommittingAConversationLandsOnTheComposer(t *testing.T) {
	a := newTestApp(t, withFocus(FocusSidebar))
	a.sidebar.SetRooms([]string{"general", "random"})
	a.sidebar.cursor = 1
	model, cmd := a.Update(focusKey("enter"))
	got := deliver(t, model.(App), cmd)
	if got.messages.room != "random" || got.focus != FocusInput {
		t.Fatalf("sidebar Enter: room %q, focus %s; want random with the composer focused", got.messages.room, focusName(got.focus))
	}

	b := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages))
	b.sidebar.SetRooms([]string{"general", "random"})
	b.openQuickSwitch()
	b.noteFocusReturn(FocusMessages)
	model, _ = b.Update(QuickSwitchMsg{Room: "random"})
	got = model.(App)
	if got.focus != FocusInput {
		t.Fatalf("quick switch from the pane: focus %s; want the composer", focusName(got.focus))
	}
}
