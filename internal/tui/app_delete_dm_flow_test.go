package tui

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestApp_DeleteCommandInDMOpensConfirm(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.sidebar.SetDMs([]protocol.DMInfo{
		{ID: "dm_ab", Members: []string{"usr_alice", "usr_bob"}},
	})
	a.messages.SetContext("", "", "dm_ab")

	a.handleSlashCommand(&SlashCommandMsg{
		Command: "/delete",
		DM:      "dm_ab",
	})

	if !a.deleteDMConfirm.IsVisible() {
		t.Fatal("/delete in DM context should open DeleteDM confirmation")
	}
}

func TestApp_DeleteDMConfirmMsgSendsLeaveDMAndDMLeftClearsView(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.sidebar = NewSidebar()
	a.sidebar.SetDMs([]protocol.DMInfo{
		{ID: "dm_ab", Members: []string{"usr_alice", "usr_bob"}},
	})
	a.sidebar.updateSelection()
	a.messages.SetContext("", "", "dm_ab")

	var out bytes.Buffer
	client.SetEncoderForTesting(a.client, protocol.NewEncoder(&out))

	model, _ := a.Update(DeleteDMConfirmMsg{DM: "dm_ab"})
	updated := model.(App)

	var frame map[string]any
	if err := json.NewDecoder(&out).Decode(&frame); err != nil {
		t.Fatalf("decode outbound frame: %v", err)
	}
	if got, _ := frame["type"].(string); got != "leave_dm" {
		t.Fatalf("outbound type = %q, want leave_dm", got)
	}
	if got, _ := frame["dm"].(string); got != "dm_ab" {
		t.Fatalf("outbound dm = %q, want dm_ab", got)
	}

	raw, _ := json.Marshal(protocol.DMLeft{
		Type: "dm_left",
		DM:   "dm_ab",
	})
	updated.handleServerMessage(ServerMsg{Type: "dm_left", Raw: raw})

	if updated.messages.dm != "" || updated.messages.room != "" || updated.messages.group != "" {
		t.Fatalf("expected message context cleared after dm_left, got room=%q group=%q dm=%q",
			updated.messages.room, updated.messages.group, updated.messages.dm)
	}
	for _, dm := range updated.sidebar.dms {
		if dm.ID == "dm_ab" {
			t.Fatal("dm_ab should be removed from sidebar after dm_left")
		}
	}
}
