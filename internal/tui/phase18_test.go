package tui

// Phase 18 — tests for the info-panel topic line and /topic slash
// command. Complements topic_test.go (which covers the messages-pane
// header) by exercising the remaining two deliverables.

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// TestInfoPanel_ShowsRoomTopic verifies the info panel renders the
// "Topic: " line when a topic is set. The existing render code at
// infopanel.go has gated this line on `if i.topic != ""` since
// v0.1.0; Phase 18 populates the field via DisplayRoomTopic during
// ShowRoom.
func TestInfoPanel_ShowsRoomTopic(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "rm_general",
		name:    "general",
		topic:   "General chat, please be nice",
	}

	view := i.View(80)
	if !strings.Contains(view, "Topic:") {
		t.Errorf("info panel with non-empty topic should render 'Topic:' label, got:\n%s", view)
	}
	if !strings.Contains(view, "General chat, please be nice") {
		t.Errorf("info panel should render the topic text, got:\n%s", view)
	}
}

// TestInfoPanel_OmitsTopicLineWhenEmpty verifies the render code
// correctly drops the topic line when no topic is set. Complements
// the positive case above.
func TestInfoPanel_OmitsTopicLineWhenEmpty(t *testing.T) {
	i := InfoPanelModel{
		visible: true,
		room:    "rm_general",
		name:    "general",
		topic:   "",
	}

	view := i.View(80)
	if strings.Contains(view, "Topic:") {
		t.Errorf("info panel with empty topic should not render 'Topic:' label, got:\n%s", view)
	}
}

// TestSlashTopic_RoomContext_ShowsCurrentTopic verifies the read path —
// typing /topic in a room with a topic set surfaces "#<name> — <topic>"
// in the status bar.
func TestSlashTopic_RoomContext_ShowsCurrentTopic(t *testing.T) {
	// Minimal client with a store so DisplayRoomName / DisplayRoomTopic
	// can resolve.
	dir := t.TempDir()
	st, err := store.OpenUnencrypted(dir + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.UpsertRoom("rm_general", "general", "General chat, please be nice", 5); err != nil {
		t.Fatalf("upsert room: %v", err)
	}

	c := &client.Client{}
	client.SetStoreForTesting(c, st)

	a := &App{
		statusBar: NewStatusBar(),
		client:    c,
	}

	a.handleTopicCommand(&SlashCommandMsg{Room: "rm_general"})

	if a.statusBar.errorMsg == "" {
		t.Fatal("expected status bar to be set after /topic in room context")
	}
	if !strings.Contains(a.statusBar.errorMsg, "general") {
		t.Errorf("status bar should contain room name 'general', got %q", a.statusBar.errorMsg)
	}
	if !strings.Contains(a.statusBar.errorMsg, "General chat, please be nice") {
		t.Errorf("status bar should contain topic text, got %q", a.statusBar.errorMsg)
	}
}

// TestSlashTopic_RoomContext_NoTopicSet verifies the no-topic branch of
// the same read path — "#<name> has no topic set".
func TestSlashTopic_RoomContext_NoTopicSet(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenUnencrypted(dir + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Room exists but has empty topic.
	if err := st.UpsertRoom("rm_engineering", "engineering", "", 3); err != nil {
		t.Fatalf("upsert room: %v", err)
	}

	c := &client.Client{}
	client.SetStoreForTesting(c, st)

	a := &App{
		statusBar: NewStatusBar(),
		client:    c,
	}

	a.handleTopicCommand(&SlashCommandMsg{Room: "rm_engineering"})

	if !strings.Contains(a.statusBar.errorMsg, "has no topic set") {
		t.Errorf("status bar should say 'has no topic set' for topicless room, got %q",
			a.statusBar.errorMsg)
	}
}

// TestSlashTopic_GroupContext_ShowsNotAvailable verifies /topic is
// rejected in group/DM contexts with a friendly message. No client
// required — the empty-Room check in handleTopicCommand triggers
// before any client access.
func TestSlashTopic_GroupContext_ShowsNotAvailable(t *testing.T) {
	a := &App{
		statusBar: NewStatusBar(),
	}

	a.handleTopicCommand(&SlashCommandMsg{Room: ""})

	if !strings.Contains(a.statusBar.errorMsg, "only available in rooms") {
		t.Errorf("status bar should explain /topic is room-only, got %q",
			a.statusBar.errorMsg)
	}
}
