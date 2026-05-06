package tui

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestSidebar_DMRowUsesResolvedOtherPartyName(t *testing.T) {
	s := NewSidebar()
	self := "usr_self"
	other := "usr_other"
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{self, other}},
	})

	s.resolveName = func(id string) string {
		switch id {
		case self:
			return "SELF_NAME"
		case other:
			return "OTHER_NAME"
		default:
			return id
		}
	}

	// Regresses a case where sidebar selfUserID wasn't available yet and
	// member-order fallback could pick the local user.
	s.resolveDMOther = func(dmID string) string {
		if dmID == "dm_1" {
			return other
		}
		return ""
	}
	s.resolveDMName = func(dmID string) string {
		if dmID == "dm_1" {
			return "OTHER_NAME"
		}
		return ""
	}

	out := stripANSI(s.View(80, 20, false))
	if !strings.Contains(out, "OTHER_NAME") {
		t.Fatalf("expected DM row to show other party display name, got:\n%s", out)
	}
	if strings.Contains(out, "SELF_NAME") {
		t.Fatalf("DM row should not show local display name, got:\n%s", out)
	}
}
