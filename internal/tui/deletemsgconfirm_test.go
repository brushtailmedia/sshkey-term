package tui

// Deleting a message asks first. d (and the menu's Delete) open a compact
// dialog that quotes the message; y sends the delete, n/Esc/q cancel and
// change nothing, Enter does nothing. The gate and the permission check
// run before any dialog, so offline reports and a message you cannot
// delete opens nothing.

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

type deleteCall struct{ id, room, group, dm string }

// captureDeletes replaces the delete send seam for the test and returns the
// calls it saw.
func captureDeletes(t *testing.T) *[]deleteCall {
	t.Helper()
	var calls []deleteCall
	prev := sendMessageDelete
	sendMessageDelete = func(c *client.Client, id, room, group, dm string) {
		calls = append(calls, deleteCall{id, room, group, dm})
	}
	t.Cleanup(func() { sendMessageDelete = prev })
	return &calls
}

// deleteApp is a live session in general, focus on the pane, with one
// message from the given sender under the cursor.
func deleteApp(t *testing.T, from string, opts ...testAppOption) App {
	t.Helper()
	opts = append([]testAppOption{withContext("general", "", ""), withFocus(FocusMessages), withSelfUser("usr_self")}, opts...)
	a := newTestApp(t, opts...)
	a.messages.messages = []DisplayMessage{{ID: "m1", From: "Me", FromID: from, Body: "first line\nsecond line", TS: 1, Room: "general"}}
	_ = a.messages.View(80, 16, true)
	a.messages.cursor = 0
	return a
}

func TestDelete_DAsksAndYSends(t *testing.T) {
	calls := captureDeletes(t)
	a := deleteApp(t, "usr_self")

	model, cmd := a.Update(keyRune('d'))
	got := deliver(t, model.(App), cmd)
	if !got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 {
		t.Fatalf("d must ask, not delete: dialog %v, sends %d", got.deleteMsgConfirm.IsVisible(), len(*calls))
	}
	view := stripANSI(got.View())
	if !strings.Contains(view, "Delete message?") || !strings.Contains(view, "Me: first line") || strings.Contains(view, "admin") {
		t.Fatalf("the dialog must quote the message and show no admin line for your own:\n%s", view)
	}

	got = pressFocusKey(t, got, "enter")
	if !got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 {
		t.Fatal("Enter must do nothing in the dialog")
	}

	model, cmd = got.Update(keyRune('y'))
	got = deliver(t, model.(App), cmd)
	if got.deleteMsgConfirm.IsVisible() || len(*calls) != 1 || (*calls)[0] != (deleteCall{"m1", "general", "", ""}) {
		t.Fatalf("y must send one delete for m1 in general: dialog %v, calls %v", got.deleteMsgConfirm.IsVisible(), *calls)
	}
	if got.focus != FocusMessages || got.messages.cursor != 0 {
		t.Fatalf("after y focus %v cursor %d; want the pane with the cursor kept", got.focus, got.messages.cursor)
	}
}

func TestDelete_CancelKeysChangeNothing(t *testing.T) {
	for _, key := range []string{"n", "esc", "q"} {
		calls := captureDeletes(t)
		a := deleteApp(t, "usr_self")
		model, cmd := a.Update(keyRune('d'))
		got := deliver(t, model.(App), cmd)
		got = pressFocusKey(t, got, key)
		if got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 || got.focus != FocusMessages || got.messages.cursor != 0 {
			t.Fatalf("%s: dialog %v, sends %d, focus %v, cursor %d; want the dialog gone and nothing else changed",
				key, got.deleteMsgConfirm.IsVisible(), len(*calls), got.focus, got.messages.cursor)
		}
	}
}

func TestDelete_MenuDeleteAsksToo(t *testing.T) {
	calls := captureDeletes(t)
	a := deleteApp(t, "usr_self")
	model, _ := a.Update(MessageAction{Action: "delete", Msg: a.messages.messages[0]})
	got := model.(App)
	if !got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 {
		t.Fatal("the menu's Delete must open the same dialog and send nothing")
	}
}

func TestDelete_NothingForAMessageYouCannotDelete(t *testing.T) {
	calls := captureDeletes(t)
	a := deleteApp(t, "usr_bob") // not yours, and you are no admin
	model, cmd := a.Update(keyRune('d'))
	got := deliver(t, model.(App), cmd)
	if got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 {
		t.Fatal("no dialog and no send for a message you may not delete")
	}
}

// The admin override is the server's rule: a server admin may delete any
// message in a ROOM; groups and DMs are own-message-only, so there `d` on
// someone else's message opens nothing even for an admin — and a stray
// confirmation for such a message sends nothing either.
func TestDelete_AdminOverrideOnlyInRooms(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  DisplayMessage
		want bool
	}{
		{"room", DisplayMessage{ID: "m1", From: "Bob", FromID: "usr_bob", Body: "x", Room: "general"}, true},
		{"group", DisplayMessage{ID: "m2", From: "Bob", FromID: "usr_bob", Body: "x", Group: "grp_1"}, false},
		{"dm", DisplayMessage{ID: "m3", From: "Bob", FromID: "usr_bob", Body: "x", DM: "dm_1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := captureDeletes(t)
			a := newTestApp(t, withFocus(FocusMessages), withSelfUser("usr_self"))
			client.SetAdminForTesting(a.client, true)
			a.messages.SetContext(tc.msg.Room, tc.msg.Group, tc.msg.DM)
			a.messages.messages = []DisplayMessage{tc.msg}
			_ = a.messages.View(80, 16, true)
			a.messages.cursor = 0

			model, cmd := a.Update(keyRune('d'))
			got := deliver(t, model.(App), cmd)
			if got.deleteMsgConfirm.IsVisible() != tc.want {
				t.Fatalf("dialog %v, want %v", got.deleteMsgConfirm.IsVisible(), tc.want)
			}
			if tc.want {
				if v := stripANSI(got.View()); !strings.Contains(v, "room admin") {
					t.Fatalf("the room-admin line must show:\n%s", v)
				}
				model, cmd = got.Update(keyRune('y'))
				got = deliver(t, model.(App), cmd)
				if len(*calls) != 1 {
					t.Fatalf("y must send once in a room, got %d", len(*calls))
				}
				return
			}
			model, _ = got.Update(DeleteMsgConfirmedMsg{Msg: tc.msg})
			_ = model.(App)
			if len(*calls) != 0 {
				t.Fatalf("no send outside rooms, got %d", len(*calls))
			}
		})
	}
}

func TestDelete_OfflineReportsBeforeAnyDialog(t *testing.T) {
	calls := captureDeletes(t)
	a := deleteApp(t, "usr_self", withOffline())
	model, cmd := a.Update(keyRune('d'))
	got := deliver(t, model.(App), cmd)
	if got.deleteMsgConfirm.IsVisible() || len(*calls) != 0 || !strings.Contains(got.statusBar.errorMsg, "offline") {
		t.Fatalf("offline: dialog %v, sends %d, status %q; want the offline message and no dialog",
			got.deleteMsgConfirm.IsVisible(), len(*calls), got.statusBar.errorMsg)
	}
}

func TestDeleteMsgConfirm_ViewDetails(t *testing.T) {
	var d DeleteMsgConfirmModel
	d.Show(DisplayMessage{From: "Bob", FromID: "usr_b", Body: "x"}, true)
	if v := stripANSI(d.View()); !strings.Contains(v, "admin") || !strings.Contains(v, "Bob: x") {
		t.Fatalf("another user's message must show the admin line:\n%s", v)
	}
	d.Show(DisplayMessage{From: "Me", FromID: "usr_self", Body: "x"}, false)
	if v := stripANSI(d.View()); strings.Contains(v, "admin") {
		t.Fatalf("your own message must not show the admin line:\n%s", v)
	}
	d.Show(DisplayMessage{From: "Me", Attachments: []DisplayAttachment{{Name: "notes.pdf"}}}, false)
	if v := stripANSI(d.View()); !strings.Contains(v, "notes.pdf") {
		t.Fatalf("a file-only message must be quoted by its attachment name:\n%s", v)
	}
	d.Show(DisplayMessage{From: "Me", Body: strings.Repeat("long ", 40)}, false)
	if v := stripANSI(d.View()); !strings.Contains(v, "…") {
		t.Fatalf("a long body must be cut with an ellipsis:\n%s", v)
	}
}

// The quit and delete prompts share the compact box.
func TestConfirmBox_QuitAndDeleteShareTheShape(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("srv", 0)
	var d DeleteMsgConfirmModel
	d.Show(DisplayMessage{From: "Me", Body: "x"}, false)
	qv, dv := stripANSI(q.View()), stripANSI(d.View())
	if !strings.HasPrefix(qv, "╭") || !strings.HasPrefix(dv, "╭") {
		t.Fatal("both prompts render in the rounded box")
	}
	if !strings.Contains(qv, "[y] Quit  [n] Cancel") || !strings.Contains(dv, "[y] Delete  [n] Cancel") {
		t.Fatalf("both prompts carry the [y] … [n] Cancel footer:\n%s\n%s", qv, dv)
	}
}
