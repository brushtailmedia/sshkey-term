package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// newEditAppHarness is a LIVE session on a real store, as usr_alice: the
// offline-browse gates (client.IsConnected + App.connState) must see it as
// online or every server-bound action under test stops at "not connected".
// Many files share it; it is the builder with two options.
func newEditAppHarness(t *testing.T) (App, *store.Store) {
	t.Helper()
	a := newTestApp(t, withStore(), withSelfUser("usr_alice"))
	return a, a.client.Store()
}

// editFixture is a room where alice (the local user) posted first and last
// with bob in between: msg_3 is the one message the server will accept an
// edit for.
func editFixture(a *App) {
	a.messages.SetContext("room_edit", "", "")
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", Body: "first", TS: 1},
		{ID: "msg_2", FromID: "usr_bob", Body: "second", TS: 2},
		{ID: "msg_3", FromID: "usr_alice", Body: "latest from me", TS: 3},
	}
}

// `a` on the editable message enters edit mode with its body in the
// composer, the pane scrolled to it and focus on the input.
func TestApp_AKeyEditsTheEditableMessage(t *testing.T) {
	a, _ := newEditAppHarness(t)
	editFixture(&a)
	a.setFocus(FocusMessages)
	a.messages.cursor = 2 // msg_3

	model, cmd := a.Update(keyRune('a'))
	updated := deliver(t, model.(App), cmd) // the pane answers with the edit action
	if !updated.input.IsEditing() || updated.input.EditTarget() != "msg_3" {
		t.Fatalf("editing=%v target=%q; want edit mode on msg_3", updated.input.IsEditing(), updated.input.EditTarget())
	}
	if updated.input.Value() != "latest from me" {
		t.Fatalf("input value = %q, want the message body", updated.input.Value())
	}
	if updated.focus != FocusInput || updated.messages.cursor != 2 {
		t.Fatalf("focus %v, cursor %d; want the input with the pane cursor on msg_3", updated.focus, updated.messages.cursor)
	}
}

// `a` on any other message — alice's older one, bob's — does nothing.
func TestApp_AKeyIgnoresOtherMessages(t *testing.T) {
	for _, cursor := range []int{0, 1} {
		a, _ := newEditAppHarness(t)
		editFixture(&a)
		a.setFocus(FocusMessages)
		a.messages.cursor = cursor

		model, cmd := a.Update(keyRune('a'))
		got := deliver(t, model.(App), cmd)
		if got.input.IsEditing() || got.focus != FocusMessages {
			t.Fatalf("a on message %d: editing=%v focus=%v; want nothing to happen", cursor, got.input.IsEditing(), got.focus)
		}
	}
}

// Up on an empty input no longer edits: it is a focus move to the pane
// (focus_arrows_test.go has the cursor rules); the draft stays empty.
func TestApp_UpOnEmptyInputDoesNotEnterEditMode(t *testing.T) {
	a, _ := newEditAppHarness(t)
	editFixture(&a)

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)
	if updated.input.IsEditing() || updated.input.Value() != "" {
		t.Fatalf("editing=%v value=%q; Up must not enter edit mode", updated.input.IsEditing(), updated.input.Value())
	}
	if updated.focus != FocusMessages {
		t.Fatalf("focus = %v, want the message pane", updated.focus)
	}
}

// With a draft in progress Up still goes up to the pane — the composer is a
// single line, so Up has no editing meaning — and the draft, its cursor and
// the non-edit state all survive the trip.
func TestApp_UpWithDraftGoesToPaneAndKeepsTheDraft(t *testing.T) {
	a, _ := newEditAppHarness(t)
	editFixture(&a)
	a.input.textInput.SetValue("draft in progress")
	a.input.textInput.SetCursor(5)

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)
	if updated.focus != FocusMessages || updated.input.IsEditing() {
		t.Fatalf("focus %v editing %v; want the pane, not editing", updated.focus, updated.input.IsEditing())
	}
	if got := updated.input.Value(); got != "draft in progress" || updated.input.textInput.Position() != 5 {
		t.Fatalf("draft %q at %d; want the draft and its cursor preserved", got, updated.input.textInput.Position())
	}
}

// Edit is unavailable in a left or retired room: no editable message, so
// `a` does nothing there — while Up, now navigation, still reaches the pane.
func TestApp_EditUnavailableInLeftOrRetiredRoom(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *MessagesModel)
	}{
		{"left", func(m *MessagesModel) { m.SetLeft(true) }},
		{"retired", func(m *MessagesModel) { m.SetRoomRetired(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newEditAppHarness(t)
			editFixture(&a)
			tc.setup(&a.messages)
			if a.editableMessageID() != "" {
				t.Fatalf("editable = %q, want none in a %s room", a.editableMessageID(), tc.name)
			}

			model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
			got := model.(App)
			if got.focus != FocusMessages || got.input.IsEditing() {
				t.Fatalf("Up: focus %v editing %v; want the pane, not editing", got.focus, got.input.IsEditing())
			}
			got.messages.cursor = 2
			model, cmd := got.Update(keyRune('a'))
			got = deliver(t, model.(App), cmd)
			if got.input.IsEditing() || got.focus != FocusMessages {
				t.Fatalf("a: editing %v focus %v; want nothing to happen", got.input.IsEditing(), got.focus)
			}
		})
	}
}

func TestApp_EscCancelsEditMode(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.input.EnterEditMode("msg_esc", "editing body")

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := model.(App)

	if updated.input.IsEditing() {
		t.Fatal("esc should exit edit mode")
	}
	if got := updated.input.Value(); got != "" {
		t.Fatalf("esc should clear input buffer, got %q", got)
	}
}

func TestApp_EditModeDispatchesCorrectVerbPerContext(t *testing.T) {
	tests := []struct {
		name        string
		room        string
		group       string
		dm          string
		msg         store.StoredMessage
		errContains string
	}{
		{
			name:        "room",
			room:        "room_dispatch",
			msg:         store.StoredMessage{ServerOrder: 1, ID: "msg_room_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, Room: "room_dispatch", Epoch: 1},
			errContains: "no epoch key for room room_dispatch",
		},
		{
			name:        "group",
			group:       "group_dispatch",
			msg:         store.StoredMessage{ServerOrder: 1, ID: "msg_group_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, Group: "group_dispatch"},
			errContains: "no members for group group_dispatch",
		},
		{
			name:        "dm",
			dm:          "dm_dispatch",
			msg:         store.StoredMessage{ServerOrder: 1, ID: "msg_dm_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, DM: "dm_dispatch"},
			errContains: "no members for DM dm_dispatch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, st := newEditAppHarness(t)
			a.messages.SetContext(tc.room, tc.group, tc.dm)
			if _, err := st.InsertMessage(tc.msg); err != nil {
				t.Fatalf("InsertMessage: %v", err)
			}
			a.input.EnterEditMode(tc.msg.ID, "edited body")

			model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
			updated := model.(App)

			if updated.input.IsEditing() {
				t.Fatal("enter should exit edit mode after dispatch attempt")
			}
			if got := updated.input.Value(); got != "" {
				t.Fatalf("enter should clear input after dispatch attempt, got %q", got)
			}
			if !strings.Contains(updated.statusBar.errorMsg, tc.errContains) {
				t.Fatalf("status error = %q, want substring %q", updated.statusBar.errorMsg, tc.errContains)
			}
		})
	}
}

func TestApp_EditWindowExpiredExitsEditMode(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.input.EnterEditMode("msg_expired", "draft")

	raw, _ := json.Marshal(protocol.Error{
		Type:    "error",
		Code:    protocol.ErrEditWindowExpired,
		Message: "window expired",
	})

	model, _ := a.Update(a.serverMsg("error", raw))
	updated := model.(App)

	if updated.input.IsEditing() {
		t.Fatal("edit_window_expired should exit edit mode")
	}
	if got := updated.input.Value(); got != "" {
		t.Fatalf("edit_window_expired should clear input, got %q", got)
	}
	if !strings.Contains(updated.statusBar.errorMsg, "Edit window expired") {
		t.Fatalf("status message = %q, want friendly edit-window message", updated.statusBar.errorMsg)
	}
}

func TestApp_ContextSwitchClearsEditMode(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_a", "", "")
	a.input.EnterEditMode("msg_switch", "draft text")

	a.messages.SetContext("room_b", "", "")
	a.onContextSwitch()

	if a.input.IsEditing() {
		t.Fatal("context switch should exit edit mode")
	}
	if got := a.input.Value(); got != "" {
		t.Fatalf("context switch should clear input, got %q", got)
	}
}

func TestApp_ContextSwitchClearsReplyAndDraft(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_a", "", "")
	a.input.SetReplyContext("msg_1", "Bob: hello", "room_a", "", "")
	a.input.textInput.SetValue("half-written reply")
	a.input.textInput.SetCursor(len("half-written reply"))

	a.messages.SetContext("room_b", "", "")
	a.onContextSwitch()

	if a.input.IsReplying() {
		t.Fatal("context switch should clear reply state")
	}
	if got := a.input.Value(); got != "" {
		t.Fatalf("context switch should clear draft input, got %q", got)
	}
}

// Esc cancels the reply and KEEPS the draft (clause 4; it used to wipe it).
func TestApp_EscCancelsReplyAndKeepsDraft(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_a", "", "")
	a.input.SetReplyContext("msg_1", "Bob: hello", "room_a", "", "")
	a.input.textInput.SetValue("draft")
	a.input.textInput.SetCursor(len("draft"))

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := model.(App)

	if updated.input.IsReplying() {
		t.Fatal("esc should cancel reply state in input focus")
	}
	if got := updated.input.Value(); got != "draft" {
		t.Fatalf("esc must keep the draft, got %q", got)
	}
}

func TestApp_ReplyActionUsesResolvedDisplayName(t *testing.T) {
	a, _ := newEditAppHarness(t)
	client.SetProfileForTesting(a.client, &protocol.Profile{
		User:        "usr_bob",
		DisplayName: "Bob",
	})

	model, _ := a.Update(MessageAction{
		Action: "reply",
		Msg: DisplayMessage{
			ID:     "msg_1",
			FromID: "usr_bob",
			From:   "usr_bob",
			Body:   "hello",
		},
	})
	updated := model.(App)

	if updated.focus != FocusInput {
		t.Fatalf("focus = %v, want FocusInput", updated.focus)
	}
	if updated.input.replyTo != "msg_1" {
		t.Fatalf("replyTo = %q, want msg_1", updated.input.replyTo)
	}
	if got := updated.input.replyText; got != "Bob: hello" {
		t.Fatalf("reply text = %q, want %q", got, "Bob: hello")
	}
	if updated.input.replyRoom != "" || updated.input.replyGroup != "" || updated.input.replyDM != "" {
		t.Fatalf("reply context should be empty when action carries no context, got room=%q group=%q dm=%q",
			updated.input.replyRoom, updated.input.replyGroup, updated.input.replyDM)
	}
}
