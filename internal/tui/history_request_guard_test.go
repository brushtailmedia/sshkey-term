package tui

import "testing"

// Regression tests for the Outgoing Request Guard (history-state-model.md):
// a HistoryRequestMsg is produced by an async tea.Cmd, so it must carry the
// connection generation it was created under (so a request from a superseded
// connection is dropped) and its context tuple must still match the active
// context (so a request created before a context switch can't load the old
// context's messages into the new pane).

func TestHistoryRequest_StampsConnGen(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 10)
	m.hintVisible = true // so the request is allowed to fire
	m.connGen = 7        // App-synced generation

	cmd := m.requestHistory()
	if cmd == nil {
		t.Fatal("requestHistory should fire with a visible hint")
	}
	hist, ok := cmd().(HistoryRequestMsg)
	if !ok {
		t.Fatalf("expected HistoryRequestMsg, got %#v", cmd())
	}
	if hist.Gen != 7 {
		t.Errorf("HistoryRequestMsg.Gen = %d, want 7 (stamped from connGen)", hist.Gen)
	}
	if hist.Room != "room_support" || hist.BeforeID != "msg_000" {
		t.Errorf("stamped context wrong: room=%q before=%q", hist.Room, hist.BeforeID)
	}
}

func TestHistoryRequest_UsesEarliestPersistedCursor(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	m.messages = []DisplayMessage{
		{IsSystem: true, Body: "local divider"},
		{ID: "", Body: "local row"},
		{ID: "msg_001", Room: "room_support", Body: "oldest persisted"},
		{ID: "msg_002", Room: "room_support", Body: "newer persisted"},
	}
	m.hintVisible = true

	cmd := m.requestHistory()
	if cmd == nil {
		t.Fatal("requestHistory should fire using the first persisted row as cursor")
	}
	hist, ok := cmd().(HistoryRequestMsg)
	if !ok {
		t.Fatalf("expected HistoryRequestMsg, got %#v", cmd())
	}
	if hist.BeforeID != "msg_001" {
		t.Errorf("BeforeID = %q, want earliest persisted ID msg_001", hist.BeforeID)
	}
}

func TestHistoryRequest_NoPersistedCursorDoesNotLoad(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	m.messages = []DisplayMessage{
		{IsSystem: true, Body: "local divider"},
		{ID: "", Room: "room_support", Body: "local-only row"},
	}
	m.hintVisible = true

	if cmd := m.requestHistory(); cmd != nil {
		t.Fatal("requestHistory must not fire without a non-empty persisted cursor")
	}
	if m.loadingHistory || m.probeDone {
		t.Fatalf("missing cursor must not set loading/probe state: loading=%v probeDone=%v", m.loadingHistory, m.probeDone)
	}
}

func TestHistoryRequest_InvalidCurrentContextDoesNotLoad(t *testing.T) {
	tests := []struct {
		name  string
		room  string
		group string
		dm    string
	}{
		{name: "empty"},
		{name: "ambiguous room group", room: "room_x", group: "group_x"},
		{name: "ambiguous room dm", room: "room_x", dm: "dm_x"},
		{name: "ambiguous group dm", group: "group_x", dm: "dm_x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMessages()
			m.SetContext(tc.room, tc.group, tc.dm)
			m.messages = []DisplayMessage{{ID: "msg_001", Room: tc.room, Group: tc.group, DM: tc.dm}}
			m.hintVisible = true

			if cmd := m.requestHistory(); cmd != nil {
				t.Fatal("requestHistory must not fire for empty or ambiguous contexts")
			}
			if m.loadingHistory || m.probeDone {
				t.Fatalf("invalid context must not set loading/probe state: loading=%v probeDone=%v", m.loadingHistory, m.probeDone)
			}
		})
	}
}

func TestHistoryRequestMatches_TupleEquality(t *testing.T) {
	// Room context: only the exact room tuple matches.
	m := NewMessages()
	m.SetContext("room_x", "", "")
	if !m.historyRequestMatches("room_x", "", "") {
		t.Error("same room tuple must match")
	}
	if m.historyRequestMatches("room_y", "", "") {
		t.Error("different room must not match")
	}
	if m.historyRequestMatches("room_x", "grp", "") {
		t.Error("room match but stray group field must not match (full-tuple equality)")
	}

	// Group context: a room-shaped request from the previous context fails.
	g := NewMessages()
	g.SetContext("", "grp_x", "")
	if !g.historyRequestMatches("", "grp_x", "") {
		t.Error("same group tuple must match")
	}
	if g.historyRequestMatches("room_x", "", "") {
		t.Error("a stale room request must not match a group context")
	}

	// DM context.
	d := NewMessages()
	d.SetContext("", "", "dm_x")
	if !d.historyRequestMatches("", "", "dm_x") {
		t.Error("same DM tuple must match")
	}
	if d.historyRequestMatches("", "", "dm_y") {
		t.Error("different DM must not match")
	}
}
