package tui

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func dmResolver(m map[string]string) func(string) string {
	return func(id string) string { return m[id] }
}

// TestQuickSwitch_IncludesDMs: rooms, groups, AND 1:1 DMs all appear, with DMs
// labeled by the partner's display name and `@`-prefixed.
func TestQuickSwitch_IncludesDMs(t *testing.T) {
	q := NewQuickSwitch()
	q.Show(
		[]string{"room_general"},
		[]protocol.GroupInfo{{ID: "grp_1", Name: "Team"}},
		[]protocol.DMInfo{
			{ID: "dm_alice", Members: []string{"usr_me", "usr_alice"}},
			{ID: "dm_bob", Members: []string{"usr_me", "usr_bob"}},
		},
		nil, nil,
		dmResolver(map[string]string{"dm_alice": "Alice", "dm_bob": "Bob"}),
	)

	if len(q.filtered) != 4 {
		t.Fatalf("filtered = %d, want 4 (1 room + 1 group + 2 DMs)", len(q.filtered))
	}
	// DMs come after rooms+groups and carry the dm ID + @-prefixed label.
	found := map[string]string{}
	for _, it := range q.filtered {
		if it.dm != "" {
			found[it.dm] = it.label
		}
	}
	if found["dm_alice"] != "@Alice" {
		t.Fatalf("dm_alice label = %q, want @Alice", found["dm_alice"])
	}
	if found["dm_bob"] != "@Bob" {
		t.Fatalf("dm_bob label = %q, want @Bob", found["dm_bob"])
	}
}

// TestQuickSwitch_FilterMatchesDMByPartnerName: typing a partner name narrows
// to that DM.
func TestQuickSwitch_FilterMatchesDMByPartnerName(t *testing.T) {
	q := NewQuickSwitch()
	q.Show(nil, nil,
		[]protocol.DMInfo{
			{ID: "dm_alice", Members: []string{"usr_me", "usr_alice"}},
			{ID: "dm_bob", Members: []string{"usr_me", "usr_bob"}},
		},
		nil, nil,
		dmResolver(map[string]string{"dm_alice": "Alice", "dm_bob": "Bob"}),
	)

	q.input.SetValue("ali")
	q.updateFiltered()
	if len(q.filtered) != 1 || q.filtered[0].dm != "dm_alice" {
		t.Fatalf("filter 'ali' → %d items, want 1 (dm_alice)", len(q.filtered))
	}
}

// TestQuickSwitch_EnterEmitsDMSelection: selecting a DM row emits a
// QuickSwitchMsg carrying the DM (and nothing else).
func TestQuickSwitch_EnterEmitsDMSelection(t *testing.T) {
	q := NewQuickSwitch()
	q.Show(nil, nil,
		[]protocol.DMInfo{{ID: "dm_alice", Members: []string{"usr_me", "usr_alice"}}},
		nil, nil,
		dmResolver(map[string]string{"dm_alice": "Alice"}),
	)
	q.cursor = 0

	_, cmd := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on a DM should emit a cmd")
	}
	qs, ok := cmd().(QuickSwitchMsg)
	if !ok {
		t.Fatalf("expected QuickSwitchMsg, got %T", cmd())
	}
	if qs.DM != "dm_alice" {
		t.Fatalf("QuickSwitchMsg.DM = %q, want dm_alice", qs.DM)
	}
	if qs.Room != "" || qs.Group != "" {
		t.Fatalf("DM selection should not set Room/Group (room=%q group=%q)", qs.Room, qs.Group)
	}
}

// TestApp_QuickSwitchDMSwitchesContext: the App handler routes a DM selection to
// the right sidebar item and switches the active conversation to it.
func TestApp_QuickSwitchDMSwitchesContext(t *testing.T) {
	a, _ := newEditAppHarness(t)
	// Encoder seam so any read-receipt send during the switch writes to a
	// buffer instead of panicking on the disconnected harness client.
	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"room_general"})
	a.sidebar.SetDMs([]protocol.DMInfo{{ID: "dm_alice", Members: []string{"usr_alice", "usr_bob"}}})
	a.messages.SetContext("room_general", "", "")

	model, _ := a.Update(QuickSwitchMsg{DM: "dm_alice"})
	updated := model.(App)

	if updated.messages.dm != "dm_alice" {
		t.Fatalf("active dm = %q, want dm_alice after quick-switch DM selection", updated.messages.dm)
	}
	if updated.messages.room != "" || updated.messages.group != "" {
		t.Fatalf("room/group should clear when switching to a DM (room=%q group=%q)", updated.messages.room, updated.messages.group)
	}
}
