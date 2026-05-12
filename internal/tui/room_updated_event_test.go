package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newRoomUpdatedAppHarness(t *testing.T) (App, *store.Store) {
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
		statusBar: NewStatusBar(),
		sidebar:   NewSidebar(),
	}
	a.messages.SetContext("rm_general", "", "")
	a.messages.SetRoomTopic("old topic")
	return a, st
}

func TestRoomUpdatedEvent_RefreshesCurrentRoomTopicAndClearsPendingStatus(t *testing.T) {
	a, st := newRoomUpdatedAppHarness(t)
	if err := st.UpsertRoom("rm_general", "general", "old topic", 5); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	a.statusBar.SetError(topicUpdatePendingStatus)

	if err := st.UpdateRoomNameTopic("rm_general", "general", "new topic"); err != nil {
		t.Fatalf("update room: %v", err)
	}

	model, _ := a.Update(RoomUpdatedEvent{Room: "rm_general"})
	updated := model.(App)

	if got := updated.messages.RoomTopic(); got != "new topic" {
		t.Fatalf("messages room topic = %q, want new topic", got)
	}
	if got := updated.statusBar.errorMsg; got != "Topic updated" {
		t.Fatalf("status = %q, want %q", got, "Topic updated")
	}
}

func TestRoomUpdatedEvent_OtherRoomDoesNotTouchCurrentHeaderOrStatus(t *testing.T) {
	a, st := newRoomUpdatedAppHarness(t)
	if err := st.UpsertRoom("rm_general", "general", "old topic", 5); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := st.UpsertRoom("rm_other", "other", "other topic", 2); err != nil {
		t.Fatalf("seed other room: %v", err)
	}
	a.statusBar.SetError(topicUpdatePendingStatus)

	model, _ := a.Update(RoomUpdatedEvent{Room: "rm_other"})
	updated := model.(App)

	if got := updated.messages.RoomTopic(); got != "old topic" {
		t.Fatalf("messages room topic = %q, want unchanged old topic", got)
	}
	if got := updated.statusBar.errorMsg; got != topicUpdatePendingStatus {
		t.Fatalf("status = %q, want unchanged pending status", got)
	}
}

func TestRoomEventTopicRendersInlineSystemMessage(t *testing.T) {
	a, st := newRoomUpdatedAppHarness(t)
	if err := st.UpsertRoom("rm_general", "general", "old topic", 5); err != nil {
		t.Fatalf("seed room: %v", err)
	}

	raw, err := json.Marshal(protocol.RoomEvent{
		Type:  "room_event",
		Room:  "rm_general",
		Event: "topic",
		By:    "alice",
		Name:  "roadmap",
	})
	if err != nil {
		t.Fatalf("marshal room_event: %v", err)
	}

	a.handleServerMessage(ServerMsg{Type: "room_event", Raw: raw})

	msgs := a.messages.messages
	if len(msgs) == 0 {
		t.Fatal("expected an inline system message for topic room_event")
	}
	last := msgs[len(msgs)-1]
	if !last.IsSystem {
		t.Fatalf("last message is not system: %+v", last)
	}
	if !strings.Contains(last.SystemText, "changed the topic to \"roadmap\"") {
		t.Fatalf("system text = %q, want topic-change text", last.SystemText)
	}
}
