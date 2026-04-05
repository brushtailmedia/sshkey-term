package tui

import (
	"testing"
)

// setupMessagesWithOne returns a MessagesModel with a single test message.
func setupMessagesWithOne() *MessagesModel {
	m := NewMessages()
	m.currentUser = "me"
	m.messages = []DisplayMessage{
		{ID: "msg_1", From: "other", Body: "hello", TS: 1712345678},
	}
	return &m
}

// clearReactionTracker resets the package-level tracker between tests so
// they don't leak state.
func clearReactionTracker() {
	for k := range reactionTracker {
		delete(reactionTracker, k)
	}
}

func TestReactions_AddAndDisplay(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.addReactionRecord("msg_1", "react_2", "bob", "👍")
	m.addReactionRecord("msg_1", "react_3", "alice", "🎉")

	counts := m.messages[0].DisplayReactions()
	if counts["👍"] != 2 {
		t.Errorf("👍 count = %d, want 2 (alice + bob)", counts["👍"])
	}
	if counts["🎉"] != 1 {
		t.Errorf("🎉 count = %d, want 1 (alice)", counts["🎉"])
	}
}

func TestReactions_DuplicateSameUserSameEmojiCountsOnce(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	// Same user reacts with same emoji twice (zombie from multi-device race)
	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.addReactionRecord("msg_1", "react_2", "alice", "👍")

	counts := m.messages[0].DisplayReactions()
	if counts["👍"] != 1 {
		t.Errorf("alice's two 👍 should count as 1 user, got %d", counts["👍"])
	}

	// But both reaction_ids are tracked
	ids := m.messages[0].UserReactionIDs("alice", "👍")
	if len(ids) != 2 {
		t.Errorf("expected 2 reaction_ids tracked, got %d", len(ids))
	}
}

func TestReactions_UserHasReacted(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")

	if !m.messages[0].UserHasReacted("alice", "👍") {
		t.Error("alice should have reacted with 👍")
	}
	if m.messages[0].UserHasReacted("alice", "🎉") {
		t.Error("alice should not have reacted with 🎉")
	}
	if m.messages[0].UserHasReacted("bob", "👍") {
		t.Error("bob should not have reacted with 👍")
	}
}

func TestReactions_UserEmojis(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.addReactionRecord("msg_1", "react_2", "alice", "🎉")
	m.addReactionRecord("msg_1", "react_3", "alice", "❤️")

	emojis := m.messages[0].UserEmojis("alice")
	if len(emojis) != 3 {
		t.Fatalf("alice should have 3 emojis, got %d", len(emojis))
	}
	// Verify all three are present
	present := make(map[string]bool)
	for _, e := range emojis {
		present[e] = true
	}
	for _, want := range []string{"👍", "🎉", "❤️"} {
		if !present[want] {
			t.Errorf("missing emoji: %s", want)
		}
	}
}

func TestReactions_UserEmojisEmpty(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()
	emojis := m.messages[0].UserEmojis("alice")
	if len(emojis) != 0 {
		t.Errorf("no reactions should give empty emojis, got %v", emojis)
	}
}

func TestReactions_RemoveOne(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.addReactionRecord("msg_1", "react_2", "bob", "👍")

	m.RemoveReaction("react_1")

	counts := m.messages[0].DisplayReactions()
	if counts["👍"] != 1 {
		t.Errorf("after removing alice's react: 👍 count = %d, want 1 (bob)", counts["👍"])
	}
	if m.messages[0].UserHasReacted("alice", "👍") {
		t.Error("alice should no longer have 👍")
	}
	if !m.messages[0].UserHasReacted("bob", "👍") {
		t.Error("bob should still have 👍")
	}
}

func TestReactions_RemoveOneOfDuplicates(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.addReactionRecord("msg_1", "react_2", "alice", "👍")

	// Remove first; user should still have 👍 via the second record
	m.RemoveReaction("react_1")

	if !m.messages[0].UserHasReacted("alice", "👍") {
		t.Error("alice should still have 👍 (one zombie left)")
	}
	ids := m.messages[0].UserReactionIDs("alice", "👍")
	if len(ids) != 1 {
		t.Errorf("should have 1 remaining reaction_id, got %d", len(ids))
	}

	// Remove second; now cleared
	m.RemoveReaction(ids[0])
	if m.messages[0].UserHasReacted("alice", "👍") {
		t.Error("alice should no longer have 👍 after removing both")
	}
	counts := m.messages[0].DisplayReactions()
	if counts["👍"] != 0 {
		t.Errorf("👍 count should be 0, got %d", counts["👍"])
	}
}

func TestReactions_RemoveUnknownReactionID(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()
	// Should not panic on unknown reaction_id
	m.RemoveReaction("react_does_not_exist")
}

func TestReactions_RemoveCleansUpUserEntry(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_1", "alice", "👍")
	m.RemoveReaction("react_1")

	// After removal, alice's entry in ReactionsByUser should be cleaned up
	if _, ok := m.messages[0].ReactionsByUser["alice"]; ok {
		t.Error("alice's entry should be deleted when no reactions remain")
	}
}

func TestReactions_TrackerPopulated(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_xyz", "alice", "👍")

	meta, ok := reactionTracker["react_xyz"]
	if !ok {
		t.Fatal("reactionTracker should contain react_xyz")
	}
	if meta.msgID != "msg_1" || meta.user != "alice" || meta.emoji != "👍" {
		t.Errorf("tracker metadata wrong: %+v", meta)
	}
}

func TestReactions_TrackerClearedOnRemove(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	m.addReactionRecord("msg_1", "react_xyz", "alice", "👍")
	m.RemoveReaction("react_xyz")

	if _, ok := reactionTracker["react_xyz"]; ok {
		t.Error("tracker entry should be cleared after remove")
	}
}

func TestReactions_MultipleEmojisFromSameUser(t *testing.T) {
	clearReactionTracker()
	m := setupMessagesWithOne()

	// alice reacts with three different emojis (Slack/Discord model)
	m.addReactionRecord("msg_1", "r1", "alice", "👍")
	m.addReactionRecord("msg_1", "r2", "alice", "🎉")
	m.addReactionRecord("msg_1", "r3", "alice", "❤️")

	counts := m.messages[0].DisplayReactions()
	if counts["👍"] != 1 || counts["🎉"] != 1 || counts["❤️"] != 1 {
		t.Errorf("all three emojis should have count 1, got: %v", counts)
	}
	if len(m.messages[0].UserEmojis("alice")) != 3 {
		t.Error("alice should have 3 distinct emojis")
	}
}

func TestReactions_DisplayReactionsNilSafe(t *testing.T) {
	d := DisplayMessage{ID: "msg_1"}
	counts := d.DisplayReactions()
	if counts != nil {
		t.Errorf("nil ReactionsByUser should return nil display, got %v", counts)
	}
	if d.UserHasReacted("alice", "👍") {
		t.Error("nil map should return false for UserHasReacted")
	}
	if ids := d.UserReactionIDs("alice", "👍"); ids != nil {
		t.Errorf("nil map should return nil from UserReactionIDs, got %v", ids)
	}
	if emojis := d.UserEmojis("alice"); emojis != nil {
		t.Errorf("nil map should return nil from UserEmojis, got %v", emojis)
	}
}

// -- Context menu integration --

func TestContextMenu_ShowsRemoveReactionItems(t *testing.T) {
	c := NewContextMenu()
	msg := DisplayMessage{ID: "msg_1", From: "alice", Body: "hi"}
	myEmojis := []string{"👍", "🎉"}
	c.Show(msg, 0, 0, false, false, false, nil, myEmojis)

	var removeItems []ContextMenuItem
	for _, item := range c.items {
		if item.Action == "unreact" {
			removeItems = append(removeItems, item)
		}
	}
	if len(removeItems) != 2 {
		t.Fatalf("expected 2 unreact items, got %d", len(removeItems))
	}
	if removeItems[0].Data != "👍" || removeItems[1].Data != "🎉" {
		t.Errorf("unreact items data = %q, %q", removeItems[0].Data, removeItems[1].Data)
	}
	for _, item := range removeItems {
		if item.Label == "" {
			t.Error("unreact item should have a label")
		}
	}
}

func TestContextMenu_NoRemoveItemsIfNoReactions(t *testing.T) {
	c := NewContextMenu()
	msg := DisplayMessage{ID: "msg_1"}
	c.Show(msg, 0, 0, false, false, false, nil, nil)

	for _, item := range c.items {
		if item.Action == "unreact" {
			t.Error("should not show unreact items when user has no reactions")
		}
	}
}

// -- Keyboard shortcuts in the messages panel --

func TestMessages_UKeyEmitsUnreact(t *testing.T) {
	m := setupMessagesWithOne()
	m.cursor = 0
	_, cmd := m.Update(keyMsg("u"))
	if cmd == nil {
		t.Fatal("'u' should emit an action")
	}
	action, ok := cmd().(MessageAction)
	if !ok {
		t.Fatalf("expected MessageAction, got %T", cmd())
	}
	if action.Action != "unreact" {
		t.Errorf("action = %q, want unreact", action.Action)
	}
	if action.Data != "" {
		t.Errorf("keyboard 'u' should emit empty Data (first-emoji fallback), got %q", action.Data)
	}
	if action.Msg.ID != "msg_1" {
		t.Errorf("wrong message: %s", action.Msg.ID)
	}
}

func TestMessages_UKeyNoCursor(t *testing.T) {
	m := setupMessagesWithOne()
	m.cursor = -1 // no selection
	_, cmd := m.Update(keyMsg("u"))
	if cmd != nil {
		t.Error("'u' with no selection should not emit")
	}
}

func TestMessages_EnterEmitsOpenMenu(t *testing.T) {
	m := setupMessagesWithOne()
	m.cursor = 0
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter should emit open_menu action")
	}
	action, ok := cmd().(MessageAction)
	if !ok {
		t.Fatalf("expected MessageAction, got %T", cmd())
	}
	if action.Action != "open_menu" {
		t.Errorf("action = %q, want open_menu", action.Action)
	}
}

func TestMessages_EKeyEmitsReact(t *testing.T) {
	// Sanity: existing 'e' shortcut still works
	m := setupMessagesWithOne()
	m.cursor = 0
	_, cmd := m.Update(keyMsg("e"))
	if cmd == nil {
		t.Fatal("'e' should emit react action")
	}
	action, _ := cmd().(MessageAction)
	if action.Action != "react" {
		t.Errorf("action = %q, want react", action.Action)
	}
}

func TestContextMenu_UnreactActionEmitsData(t *testing.T) {
	c := NewContextMenu()
	msg := DisplayMessage{ID: "msg_1"}
	c.Show(msg, 0, 0, false, false, false, nil, []string{"👍"})

	// Cursor starts at 0 (Reply). Skip Reply + React, cursor=2 is unreact.
	c.cursor = 2
	_, cmd := c.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter should emit MessageAction")
	}
	action, ok := cmd().(MessageAction)
	if !ok {
		t.Fatalf("expected MessageAction, got %T", cmd())
	}
	if action.Action != "unreact" {
		t.Errorf("action = %q, want unreact", action.Action)
	}
	if action.Data != "👍" {
		t.Errorf("data = %q, want 👍", action.Data)
	}
}
