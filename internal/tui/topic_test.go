package tui

import (
	"strings"
	"testing"
)

// Phase 18: regression tests for the two-line room header in
// MessagesModel.View() and the /topic slash command. The header renders
// the room name on line 1 (always) and the topic on line 2 (rooms only,
// omitted when empty). Groups and 1:1 DMs never show a topic line.
//
// The tests exercise the View() output directly because the header is
// pure render logic — no network, no store, no async. Stripping ANSI
// styling via stripANSI (defined below) keeps the assertions readable
// against the styled output of searchHeaderStyle and helpDescStyle.

// TestMessagesHeader_ShowsRoomNameAndTopic verifies that a room context
// with a non-empty topic renders BOTH the room name and the topic in
// the header block above the message stream.
func TestMessagesHeader_ShowsRoomNameAndTopic(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_general", "", "")
	m.resolveRoomName = func(id string) string {
		if id == "room_general" {
			return "general"
		}
		return id
	}
	m.SetRoomTopic("General chat — please be nice")

	out := stripANSI(m.View(80, 20, false))

	if !strings.Contains(out, "general") {
		t.Errorf("header should contain room name %q; got:\n%s", "general", out)
	}
	if !strings.Contains(out, "General chat — please be nice") {
		t.Errorf("header should contain topic; got:\n%s", out)
	}
	// Ordering: room name must appear BEFORE the topic in the output.
	nameIdx := strings.Index(out, "general")
	topicIdx := strings.Index(out, "General chat")
	if nameIdx < 0 || topicIdx < 0 || nameIdx >= topicIdx {
		t.Errorf("room name should precede topic; name@%d topic@%d", nameIdx, topicIdx)
	}
}

// TestMessagesHeader_OmitsTopicLineWhenEmpty verifies that a room
// context with NO topic renders only the room name — no blank "topic:"
// line, no leftover whitespace artifact. The blank separator line
// between header and message stream stays, but the topic line itself
// is entirely absent.
func TestMessagesHeader_OmitsTopicLineWhenEmpty(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_quiet", "", "")
	m.resolveRoomName = func(id string) string {
		if id == "room_quiet" {
			return "quiet"
		}
		return id
	}
	m.SetRoomTopic("") // explicitly empty

	out := stripANSI(m.View(80, 20, false))

	if !strings.Contains(out, "quiet") {
		t.Errorf("header should contain room name; got:\n%s", out)
	}
	// "Topic:" label should not appear — that's an info-panel style,
	// the messages header shows topics bare without a label.
	if strings.Contains(out, "Topic:") {
		t.Errorf("header should not include 'Topic:' label; got:\n%s", out)
	}
}

// TestMessagesHeader_GroupContext_NoTopicLine verifies that a group
// context never renders a topic line even if SetRoomTopic was
// mistakenly called (defense in depth — groups have no topics by
// design, and the render path should ignore any non-empty topic value
// when m.room is empty).
func TestMessagesHeader_GroupContext_NoTopicLine(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_project", "")
	// SetRoomTopic after SetContext — this simulates a caller bug.
	// The View() should still not render the topic because it's gated
	// on m.room != "" not on roomTopic != "".
	m.SetRoomTopic("This topic should not appear")

	out := stripANSI(m.View(80, 20, false))

	if strings.Contains(out, "This topic should not appear") {
		t.Errorf("group context must not render topic line; got:\n%s", out)
	}
	// Group name (via group ID, since no resolveRoomName resolution
	// applies to groups) should still appear as the title.
	if !strings.Contains(out, "group_project") {
		t.Errorf("group context should contain group ID as title; got:\n%s", out)
	}
}

// TestMessagesHeader_GroupContext_UsesResolvedGroupName verifies that group
// contexts prefer the resolved display name (when available) instead of the
// raw group nanoid.
func TestMessagesHeader_GroupContext_UsesResolvedGroupName(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "group_project", "")
	m.resolveGroupName = func(id string) string {
		if id == "group_project" {
			return "Project Team"
		}
		return id
	}

	out := stripANSI(m.View(80, 20, false))

	if !strings.Contains(out, "Project Team") {
		t.Errorf("group context should render resolved group name; got:\n%s", out)
	}
}

// TestMessagesHeader_DMContext_NoTopicLine verifies that a 1:1 DM
// context renders only the other party's display name — no topic
// line, no group subtitle, just the name.
func TestMessagesHeader_DMContext_NoTopicLine(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "", "dm_abc")
	m.resolveDMName = func(id string) string {
		if id == "dm_abc" {
			return "Bob"
		}
		return id
	}

	out := stripANSI(m.View(80, 20, false))

	if !strings.Contains(out, "Bob") {
		t.Errorf("DM context should render resolved peer name; got:\n%s", out)
	}
	// Just make sure no stray topic-label artifact is present.
	if strings.Contains(out, "Topic:") {
		t.Errorf("DM context must not include 'Topic:' label; got:\n%s", out)
	}
}

// TestMessagesHeader_DMContext_UsesResolveDMName ensures DM headers resolve
// via dm-id specific resolver instead of treating the dm ID as a user ID.
func TestMessagesHeader_DMContext_UsesResolveDMName(t *testing.T) {
	m := NewMessages()
	m.SetContext("", "", "dm_abc")
	m.resolveName = func(id string) string { return "WRONG:" + id }
	m.resolveDMName = func(id string) string {
		if id == "dm_abc" {
			return "Alice"
		}
		return ""
	}

	out := stripANSI(m.View(80, 20, false))
	if !strings.Contains(out, "Alice") {
		t.Fatalf("DM context should render resolved DM peer name, got:\n%s", out)
	}
	if strings.Contains(out, "WRONG:dm_abc") {
		t.Fatalf("DM context should not use resolveName on dm ID, got:\n%s", out)
	}
}

// TestMessagesHeader_EmptyContext_ShowsFallback verifies that an empty
// context (no room, no group, no DM) renders the "no room selected"
// fallback title and no topic line.
func TestMessagesHeader_EmptyContext_ShowsFallback(t *testing.T) {
	m := NewMessages()
	// Default zero-value state: no room/group/dm set.

	out := stripANSI(m.View(80, 20, false))

	if !strings.Contains(out, "no room selected") {
		t.Errorf("empty context should show 'no room selected' fallback; got:\n%s", out)
	}
}

// TestMessagesHeader_SetContextClearsTopic verifies that switching
// context via SetContext clears any previously-set topic, so a stale
// topic from the prior room doesn't leak into the new context. The app
// layer calls applyRoomTopic() after every SetContext, but that lives
// outside the MessagesModel — this test locks the MessagesModel
// invariant independently.
func TestMessagesHeader_SetContextClearsTopic(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_a", "", "")
	m.SetRoomTopic("Topic from room A")

	// Switch context without immediately re-setting the topic.
	m.SetContext("room_b", "", "")

	if topic := m.RoomTopic(); topic != "" {
		t.Errorf("SetContext should clear topic; got %q", topic)
	}
}

// TestRoomTopic_Accessor verifies the RoomTopic() getter returns what
// SetRoomTopic() persisted. Used by the /topic slash command path to
// check whether a topic is set before formatting the status bar
// message.
func TestRoomTopic_Accessor(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_x", "", "")
	if m.RoomTopic() != "" {
		t.Errorf("fresh context should have empty topic, got %q", m.RoomTopic())
	}
	m.SetRoomTopic("Hello topic")
	if m.RoomTopic() != "Hello topic" {
		t.Errorf("RoomTopic after SetRoomTopic = %q, want %q", m.RoomTopic(), "Hello topic")
	}
}

// stripANSI removes ANSI escape sequences from styled lipgloss output
// so test assertions can match plain substrings. Only used in test
// code. lipgloss wraps styled text in CSI sequences like \x1b[38;5;6m
// and terminates with \x1b[0m.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip CSI sequence: ESC [ ... letter
			j := i + 2
			for j < len(s) {
				c := s[j]
				j++
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					break
				}
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
