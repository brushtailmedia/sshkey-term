package client

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func TestDisplayDMName_ResolvesOtherDisplayName(t *testing.T) {
	c := New(Config{})
	c.userID = "usr_me"
	c.dms["dm_1"] = [2]string{"usr_me", "usr_alice"}
	SetProfileForTesting(c, &protocol.Profile{User: "usr_alice", DisplayName: "Alice"})

	if got := c.DisplayDMName("dm_1"); got != "Alice" {
		t.Fatalf("DisplayDMName = %q, want %q", got, "Alice")
	}
}

func TestDisplayDMName_FallsBackToRawIDWhenUnknown(t *testing.T) {
	c := New(Config{})
	c.userID = "usr_me"

	if got := c.DisplayDMName("dm_missing"); got != "dm_missing" {
		t.Fatalf("DisplayDMName = %q, want %q", got, "dm_missing")
	}
}
