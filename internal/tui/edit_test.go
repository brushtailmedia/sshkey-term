package tui

import (
	"strings"
	"testing"
)

// Phase 15: TUI edit-mode tests. Covers the in-memory state
// machinery on InputModel and the `(edited)` rendering on
// MessagesModel. App-layer integration (Up-arrow entry, Enter
// dispatch, context-switch clear) is exercised via manual testing
// and the server-side handler tests — the TUI tests here lock in
// the model-level invariants that the app layer depends on.

// TestInput_EnterEditModePopulatesBuffer verifies that EnterEditMode
// sets the input buffer to the given body and flips the edit flag.
func TestInput_EnterEditModePopulatesBuffer(t *testing.T) {
	i := NewInput()
	i.EnterEditMode("msg_123", "Hello world")

	if !i.IsEditing() {
		t.Error("input should be in edit mode after EnterEditMode")
	}
	if i.EditTarget() != "msg_123" {
		t.Errorf("edit target = %q, want msg_123", i.EditTarget())
	}
	if i.Value() != "Hello world" {
		t.Errorf("buffer = %q, want Hello world", i.Value())
	}
}

// TestInput_ExitEditModeClearsState verifies that ExitEditMode drops
// the edit flag and target but does NOT clear the buffer (that's a
// separate ClearInput call).
func TestInput_ExitEditModeClearsState(t *testing.T) {
	i := NewInput()
	i.EnterEditMode("msg_x", "body")
	i.ExitEditMode()

	if i.IsEditing() {
		t.Error("input should not be in edit mode after ExitEditMode")
	}
	if i.EditTarget() != "" {
		t.Errorf("edit target should be cleared, got %q", i.EditTarget())
	}
	// Buffer intentionally not cleared — caller decides.
	if i.Value() != "body" {
		t.Errorf("buffer should be preserved by ExitEditMode, got %q", i.Value())
	}
}

// TestInput_IsEmptyReportsCorrectly exercises the helper used by the
// app-layer Up-arrow gate. Empty input = true; any content = false.
func TestInput_IsEmptyReportsCorrectly(t *testing.T) {
	i := NewInput()
	if !i.IsEmpty() {
		t.Error("fresh input should be empty")
	}
	i.EnterEditMode("msg_1", "something")
	if i.IsEmpty() {
		t.Error("input with content should not be empty")
	}
	i.ClearInput()
	if !i.IsEmpty() {
		t.Error("input should be empty after ClearInput")
	}
}

// TestMessages_ApplyEditUpdatesBodyAndEditedAt verifies that
// ApplyEdit replaces the body, sets EditedAt, and clears reactions
// on the target message.
func TestMessages_ApplyEditUpdatesBodyAndEditedAt(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{
			ID: "msg_edit", From: "Alice", Body: "original", TS: 1000,
			ReactionsByUser: map[string]map[string][]string{
				"bob": {"👍": {"react_1"}},
			},
		},
		{
			ID: "msg_other", From: "Bob", Body: "unrelated", TS: 1001,
		},
	}

	m.ApplyEdit("msg_edit", "updated text", 5000)

	if m.messages[0].Body != "updated text" {
		t.Errorf("body = %q, want updated text", m.messages[0].Body)
	}
	if m.messages[0].EditedAt != 5000 {
		t.Errorf("edited_at = %d, want 5000", m.messages[0].EditedAt)
	}
	if m.messages[0].ReactionsByUser != nil {
		t.Error("reactions should be cleared after edit")
	}
	// The other message is untouched.
	if m.messages[1].Body != "unrelated" {
		t.Errorf("adjacent message body changed: %q", m.messages[1].Body)
	}
}

// TestMessages_ApplyEditNoOpOnMissingID verifies that ApplyEdit is a
// silent no-op when the target msgID isn't in the loaded list. Lets
// the dispatch path call it unconditionally without guarding.
func TestMessages_ApplyEditNoOpOnMissingID(t *testing.T) {
	m := NewMessages()
	m.messages = []DisplayMessage{
		{ID: "msg_a", From: "Alice", Body: "a", TS: 100},
	}
	m.ApplyEdit("msg_does_not_exist", "new", 500)
	// The only loaded message is untouched.
	if m.messages[0].Body != "a" {
		t.Errorf("unrelated body mutated: %q", m.messages[0].Body)
	}
}

// TestMessages_EditedMarkerRenders verifies the "(edited)" marker
// appears in the header when EditedAt > 0. Uses stripANSI (defined in
// topic_test.go) to make the assertion against plain text.
func TestMessages_EditedMarkerRenders(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_test", "", "")
	m.messages = []DisplayMessage{
		{
			ID: "msg_edit", FromID: "alice_id", From: "Alice",
			Body: "edited body", TS: 1000, EditedAt: 2000,
			Room: "room_test",
		},
	}

	out := stripANSI(m.View(80, 20, false))
	if !strings.Contains(out, "(edited)") {
		t.Errorf("expected '(edited)' marker in output:\n%s", out)
	}
	if !strings.Contains(out, "edited body") {
		t.Errorf("expected new body in output:\n%s", out)
	}
}

// TestMessages_UneditedHasNoMarker verifies the marker is absent when
// EditedAt = 0 (unedited rows).
func TestMessages_UneditedHasNoMarker(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_test", "", "")
	m.messages = []DisplayMessage{
		{
			ID: "msg_plain", FromID: "alice_id", From: "Alice",
			Body: "plain body", TS: 1000, EditedAt: 0,
			Room: "room_test",
		},
	}

	out := stripANSI(m.View(80, 20, false))
	if strings.Contains(out, "(edited)") {
		t.Errorf("unedited message should not show marker:\n%s", out)
	}
}

// TestExtractMentionsInline_MultipleMentions verifies the mention
// extractor handles multiple @mentions in one body.
func TestExtractMentionsInline_MultipleMentions(t *testing.T) {
	mentions := extractMentionsInline("hey @alice and @bob please check")
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d: %v", len(mentions), mentions)
	}
	if mentions[0] != "alice" || mentions[1] != "bob" {
		t.Errorf("got %v, want [alice bob]", mentions)
	}
}

// TestExtractMentionsInline_NoMentions verifies empty result on a
// body with no mentions.
func TestExtractMentionsInline_NoMentions(t *testing.T) {
	mentions := extractMentionsInline("just a plain message")
	if len(mentions) != 0 {
		t.Errorf("expected 0 mentions, got %v", mentions)
	}
}
