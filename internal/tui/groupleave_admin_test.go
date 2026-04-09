package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// minimalAppForServerMsg constructs an App with just the fields needed
// to drive handleServerMessage for the group_left / group_event tests.
// All other fields are zero-valued; client is nil so resolveDisplayName
// returns the username verbatim (sufficient for asserting display text).
func minimalAppForServerMsg(t *testing.T) *App {
	t.Helper()
	a := &App{
		sidebar:   NewSidebar(),
		messages:  NewMessages(),
		statusBar: NewStatusBar(),
	}
	return a
}

// TestGroupLeftHandler_AdminReasonShowsRemovedStatus is the regression
// test for item 5: when group_left arrives with Reason: "admin", the
// status bar should surface "You were removed from <name> by an admin"
// instead of the generic "Left group" used for self-leave.
func TestGroupLeftHandler_AdminReasonShowsRemovedStatus(t *testing.T) {
	a := minimalAppForServerMsg(t)
	// Pre-populate the sidebar so the handler can resolve the group's
	// display name for the status message.
	a.sidebar.SetGroups([]protocol.GroupInfo{
		{ID: "group_kick", Name: "Project X", Members: []string{"me", "alice"}},
	})

	raw, _ := json.Marshal(protocol.GroupLeft{
		Type:   "group_left",
		Group:  "group_kick",
		Reason: "admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_left", Raw: raw})

	if a.statusBar.errorMsg == "" {
		t.Fatal("expected status bar to be set after group_left{admin}")
	}
	if !strings.Contains(a.statusBar.errorMsg, "removed from Project X") {
		t.Errorf("status bar should contain 'removed from Project X', got %q",
			a.statusBar.errorMsg)
	}
	if !strings.Contains(a.statusBar.errorMsg, "by an admin") {
		t.Errorf("status bar should mention 'by an admin', got %q",
			a.statusBar.errorMsg)
	}

	// Sidebar should be marked as left (greyed)
	if !a.sidebar.IsGroupLeft("group_kick") {
		t.Error("sidebar should mark group_kick as left after admin kick")
	}
}

// TestGroupLeftHandler_EmptyReasonShowsLeftStatus is the regression for
// the self-leave path: when group_left arrives with empty Reason, the
// status bar shows the generic "Left group" message. This protects
// against accidentally breaking the self-leave UX while editing the
// admin branch.
func TestGroupLeftHandler_EmptyReasonShowsLeftStatus(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.sidebar.SetGroups([]protocol.GroupInfo{
		{ID: "group_self", Name: "Self Leave", Members: []string{"me"}},
	})

	raw, _ := json.Marshal(protocol.GroupLeft{
		Type:   "group_left",
		Group:  "group_self",
		Reason: "",
	})
	a.handleServerMessage(ServerMsg{Type: "group_left", Raw: raw})

	if a.statusBar.errorMsg != "Left group" {
		t.Errorf("self-leave should show 'Left group', got %q", a.statusBar.errorMsg)
	}
}

// TestGroupLeftHandler_AdminReasonFallsBackToGroupID verifies that if
// the sidebar doesn't have a name for the group (rare — the group has
// no display name), the status bar falls back to the group ID rather
// than rendering an empty value or panicking.
func TestGroupLeftHandler_AdminReasonFallsBackToGroupID(t *testing.T) {
	a := minimalAppForServerMsg(t)
	// No groups in sidebar at all — simulates a group that the local
	// store somehow doesn't know about (extreme edge case).

	raw, _ := json.Marshal(protocol.GroupLeft{
		Type:   "group_left",
		Group:  "group_unknown",
		Reason: "admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_left", Raw: raw})

	if !strings.Contains(a.statusBar.errorMsg, "group_unknown") {
		t.Errorf("status bar should fall back to group ID, got %q",
			a.statusBar.errorMsg)
	}
	if !strings.Contains(a.statusBar.errorMsg, "by an admin") {
		t.Errorf("status bar should still mention 'by an admin', got %q",
			a.statusBar.errorMsg)
	}
}

// TestGroupEventHandler_AdminLeaveShowsRemovedSystemMessage is the
// regression for item 6: when a group_event{leave, reason: admin}
// arrives for the currently-viewed group, the system message in the
// stream should say "X was removed from the group by an admin"
// instead of the generic "X left the group".
func TestGroupEventHandler_AdminLeaveShowsRemovedSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	// Make group_kick the currently-active context so the system
	// message gets added to the stream.
	a.messages.SetContext("", "group_kick", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:   "group_event",
		Group:  "group_kick",
		Event:  "leave",
		User:   "bob",
		Reason: "admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	// The handler should have added a system message
	if len(a.messages.messages) != 1 {
		t.Fatalf("expected 1 system message added, got %d", len(a.messages.messages))
	}
	sm := a.messages.messages[0]
	if !sm.IsSystem {
		t.Error("the added message should be a system message")
	}
	if !strings.Contains(sm.SystemText, "bob") {
		t.Errorf("system message should mention bob, got %q", sm.SystemText)
	}
	if !strings.Contains(sm.SystemText, "removed from the group by an admin") {
		t.Errorf("system message should mention 'removed from the group by an admin', got %q",
			sm.SystemText)
	}
}

// TestGroupEventHandler_RetirementLeaveShowsRetiredSystemMessage is the
// regression for the retirement-leave path. The retirement reason
// branch should still produce the existing "X's account was retired"
// system message — protecting against accidental breakage when editing
// the admin branch.
func TestGroupEventHandler_RetirementLeaveShowsRetiredSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("", "group_X", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:   "group_event",
		Group:  "group_X",
		Event:  "leave",
		User:   "carol",
		Reason: "retirement",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	if len(a.messages.messages) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(a.messages.messages))
	}
	sm := a.messages.messages[0]
	if !strings.Contains(sm.SystemText, "carol") {
		t.Errorf("system message should mention carol, got %q", sm.SystemText)
	}
	if !strings.Contains(sm.SystemText, "account was retired") {
		t.Errorf("retirement message should mention 'account was retired', got %q",
			sm.SystemText)
	}
}

// TestGroupEventHandler_EmptyReasonShowsLeftSystemMessage is the
// regression for the regular self-leave path: empty Reason produces
// "X left the group".
func TestGroupEventHandler_EmptyReasonShowsLeftSystemMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	a.messages.SetContext("", "group_X", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:  "group_event",
		Group: "group_X",
		Event: "leave",
		User:  "dave",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	if len(a.messages.messages) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(a.messages.messages))
	}
	sm := a.messages.messages[0]
	if !strings.Contains(sm.SystemText, "dave left the group") {
		t.Errorf("self-leave message should be 'dave left the group', got %q",
			sm.SystemText)
	}
}

// TestGroupEventHandler_NotActiveContextDoesNotAddMessage verifies that
// the system message is only added when the affected group is the
// currently-viewed one. If the user is in a different group, the
// event arrives but no system message is added (the user shouldn't see
// "alice was removed from another group" interleaved with the active
// stream).
func TestGroupEventHandler_NotActiveContextDoesNotAddMessage(t *testing.T) {
	a := minimalAppForServerMsg(t)
	// Active context is group_Y, the event is for group_X
	a.messages.SetContext("", "group_Y", "")

	raw, _ := json.Marshal(protocol.GroupEvent{
		Type:   "group_event",
		Group:  "group_X",
		Event:  "leave",
		User:   "bob",
		Reason: "admin",
	})
	a.handleServerMessage(ServerMsg{Type: "group_event", Raw: raw})

	if len(a.messages.messages) != 0 {
		t.Errorf("expected no system message in inactive context, got %d",
			len(a.messages.messages))
	}
}
