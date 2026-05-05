package tui

import (
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// TestSidebar_DMRowKeepsNameVisibleWhenBadgesAreLong guards a regression
// where 1:1 DM rows could collapse to just status/badge markers in narrow
// sidebars, making the DM effectively disappear.
func TestSidebar_DMRowKeepsNameVisibleWhenBadgesAreLong(t *testing.T) {
	s := NewSidebar()
	s.selfUserID = "me"
	other := "usr_partner"
	s.SetDMs([]protocol.DMInfo{
		{ID: "dm_1", Members: []string{"me", other}},
	})
	s.resolveName = func(id string) string {
		if id == other {
			return "Q_partner_name"
		}
		return id
	}
	s.resolveVerified = func(id string) bool { return id == other }
	s.MarkRetired(other)
	s.SetUnreadDM("dm_1", 99)

	out := stripANSI(s.View(20, 20, false))
	if !strings.Contains(out, "Q") {
		t.Fatalf("expected DM row to keep some name text visible, got:\n%s", out)
	}
}
