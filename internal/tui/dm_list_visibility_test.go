package tui

import (
	"encoding/json"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestDMListFiltersByHiddenNotLeftAt(t *testing.T) {
	a := minimalAppForServerMsg(t)

	raw, _ := json.Marshal(protocol.DMList{
		Type: "dm_list",
		DMs: []protocol.DMInfo{
			{
				ID:              "dm_hidden",
				Members:         []string{"alice", "bob"},
				HiddenForCaller: true,
				LeftAtForCaller: 0,
			},
			{
				ID:              "dm_visible_cutoff",
				Members:         []string{"alice", "carol"},
				HiddenForCaller: false,
				LeftAtForCaller: 1700002000,
			},
		},
	})

	a.handleServerMessage(ServerMsg{Type: "dm_list", Raw: raw})

	if len(a.sidebar.dms) != 1 {
		t.Fatalf("expected 1 visible DM, got %d", len(a.sidebar.dms))
	}
	if a.sidebar.dms[0].ID != "dm_visible_cutoff" {
		t.Fatalf("visible DM id = %q, want dm_visible_cutoff", a.sidebar.dms[0].ID)
	}
}
