package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newEditAppHarness(t *testing.T) (App, *store.Store) {
	t.Helper()
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := client.New(client.Config{DeviceID: "dev_test"})
	client.SetStoreForTesting(c, st)
	client.SetUserIDForTesting(c, "usr_alice")

	a := App{
		client:    c,
		messages:  NewMessages(),
		input:     NewInput(),
		statusBar: NewStatusBar(),
		focus:     FocusInput,
	}
	return a, st
}

func TestApp_UpArrowEntersEditModeOnEmpty(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_edit", "", "")
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", Body: "first", TS: 1},
		{ID: "msg_2", FromID: "usr_bob", Body: "second", TS: 2},
		{ID: "msg_3", FromID: "usr_alice", Body: "latest from me", TS: 3},
	}

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)

	if !updated.input.IsEditing() {
		t.Fatal("expected edit mode to be entered")
	}
	if updated.input.EditTarget() != "msg_3" {
		t.Fatalf("edit target = %q, want msg_3", updated.input.EditTarget())
	}
	if updated.input.Value() != "latest from me" {
		t.Fatalf("input value = %q, want latest message body", updated.input.Value())
	}
}

func TestApp_UpArrowNoopWhenInputHasContent(t *testing.T) {
	a, _ := newEditAppHarness(t)
	// Keep this case focused on the Up-arrow edit gate itself. With a non-empty
	// buffer, focus-input flow falls through to InputModel.Update, which can
	// emit typing indicators via c.SendTyping. A nil client avoids transport
	// setup noise and keeps the assertion targeted.
	a.client = nil
	a.messages.SetContext("room_edit", "", "")
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", Body: "first", TS: 1},
	}
	a.input.textInput.SetValue("draft in progress")
	a.input.textInput.SetCursor(len("draft in progress"))

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)

	if updated.input.IsEditing() {
		t.Fatal("up-arrow with non-empty input should not enter edit mode")
	}
	if got := updated.input.Value(); got != "draft in progress" {
		t.Fatalf("input value = %q, want preserved draft", got)
	}
}

func TestApp_UpArrowNoopOnLeftRoom(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_left", "", "")
	a.messages.SetLeft(true)
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", Body: "first", TS: 1},
	}

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)

	if updated.input.IsEditing() {
		t.Fatal("up-arrow in archived context should not enter edit mode")
	}
}

func TestApp_UpArrowNoopOnRetiredRoom(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_retired", "", "")
	a.messages.SetRoomRetired(true)
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", Body: "first", TS: 1},
	}

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := model.(App)

	if updated.input.IsEditing() {
		t.Fatal("up-arrow in retired room should not enter edit mode")
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
			msg:         store.StoredMessage{ID: "msg_room_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, Room: "room_dispatch", Epoch: 1},
			errContains: "no epoch key for room room_dispatch",
		},
		{
			name:        "group",
			group:       "group_dispatch",
			msg:         store.StoredMessage{ID: "msg_group_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, Group: "group_dispatch"},
			errContains: "no members for group group_dispatch",
		},
		{
			name:        "dm",
			dm:          "dm_dispatch",
			msg:         store.StoredMessage{ID: "msg_dm_dispatch", Sender: "usr_alice", Body: "orig", TS: 1, DM: "dm_dispatch"},
			errContains: "no members for DM dm_dispatch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, st := newEditAppHarness(t)
			a.messages.SetContext(tc.room, tc.group, tc.dm)
			if err := st.InsertMessage(tc.msg); err != nil {
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

	model, _ := a.Update(ServerMsg{Type: "error", Raw: raw})
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
