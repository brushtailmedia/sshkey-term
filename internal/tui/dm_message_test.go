package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestAddDMMessage_DedupesByID pins the dedup guard added 2026-05-07.
// The TUI's case "sync_batch": handler dispatches inner messages back
// through the live message handlers, including DMs. If a DM is already
// present from a prior LoadFromDB call (sync_batch overlapping the
// locally-persisted catchup window), AddDMMessage must skip the
// duplicate rather than appending a second copy. Without this guard,
// fresh boot showed visibly duplicated DMs (and their attachments)
// until the user switched contexts.
func TestAddDMMessage_DedupesByID(t *testing.T) {
	m := NewMessages()
	m.dm = "dm_test"

	// Seed an existing message with the same ID we'll try to re-add.
	m.messages = []DisplayMessage{
		{ID: "msg_dup", DM: "dm_test", From: "Alice", Body: "first", TS: 1000},
	}

	// Attempt to add a message with the same ID. AddDMMessage should
	// detect the duplicate and return without appending.
	dup := protocol.DM{
		ID:   "msg_dup",
		From: "alice_id",
		DM:   "dm_test",
		TS:   2000, // later TS — would be visible if dedup failed
	}
	m.AddDMMessage(dup, nil)

	if len(m.messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 (dedup failed; duplicate appended)", len(m.messages))
	}
	if m.messages[0].Body != "first" {
		t.Errorf("messages[0].Body = %q, want %q (dedup did not preserve original)",
			m.messages[0].Body, "first")
	}
}

// TestAddDMMessage_AppendsNewID verifies the dedup guard doesn't
// over-fire — a DM with a NEW ID must still be appended.
func TestAddDMMessage_AppendsNewID(t *testing.T) {
	m := NewMessages()
	m.dm = "dm_test"
	m.messages = []DisplayMessage{
		{ID: "msg_existing", DM: "dm_test", From: "Alice", Body: "first", TS: 1000},
	}

	newMsg := protocol.DM{
		ID:   "msg_new",
		From: "bob_id",
		DM:   "dm_test",
		TS:   2000,
	}
	m.AddDMMessage(newMsg, nil)

	if len(m.messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2 (new message not appended)", len(m.messages))
	}
	if m.messages[1].ID != "msg_new" {
		t.Errorf("messages[1].ID = %q, want msg_new", m.messages[1].ID)
	}
}

// TestAddDMMessage_DropsWrongDM ensures messages for an inactive DM
// are silently dropped — same context-guard pattern as AddRoomMessage
// and AddGroupMessage.
func TestAddDMMessage_DropsWrongDM(t *testing.T) {
	m := NewMessages()
	m.dm = "dm_active"

	wrongCtx := protocol.DM{
		ID:   "msg_x",
		From: "alice_id",
		DM:   "dm_other",
		TS:   1000,
	}
	m.AddDMMessage(wrongCtx, nil)

	if len(m.messages) != 0 {
		t.Errorf("len(messages) = %d, want 0 (DM for inactive context was added)", len(m.messages))
	}
}

// TestAddDMMessage_NilClientGracefulDegrade verifies that with a nil
// client, the message is still appended with placeholder body
// "(encrypted)" — same defensive shape as AddRoomMessage /
// AddGroupMessage. Catches regressions where someone "tightens" the
// nil-client path into a hard-fail.
func TestAddDMMessage_NilClientGracefulDegrade(t *testing.T) {
	m := NewMessages()
	m.dm = "dm_test"

	msg := protocol.DM{
		ID:   "msg_nil_client",
		From: "alice_id",
		DM:   "dm_test",
		TS:   1000,
	}
	m.AddDMMessage(msg, nil)

	if len(m.messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(m.messages))
	}
	if m.messages[0].Body != "(encrypted)" {
		t.Errorf("messages[0].Body = %q, want (encrypted)", m.messages[0].Body)
	}
	// FromID falls through to the raw From (no display-name lookup
	// available without a client).
	if m.messages[0].FromID != "alice_id" {
		t.Errorf("messages[0].FromID = %q, want alice_id", m.messages[0].FromID)
	}
}
