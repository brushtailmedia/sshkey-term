package client

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newClientWithGroupStore(t *testing.T) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := New(Config{})
	c.store = st
	return c
}

func TestDisplayGroupName_ReturnsExplicitName(t *testing.T) {
	c := newClientWithGroupStore(t)
	if err := c.store.StoreGroup("group_1", "Project Team", "usr_a,usr_b"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}
	if got := c.DisplayGroupName("group_1"); got != "Project Team" {
		t.Fatalf("DisplayGroupName = %q, want %q", got, "Project Team")
	}
}

func TestDisplayGroupName_UnnamedFallsBackToMemberDisplayNames(t *testing.T) {
	c := newClientWithGroupStore(t)
	SetProfileForTesting(c, &protocol.Profile{User: "usr_a", DisplayName: "Alice"})
	SetProfileForTesting(c, &protocol.Profile{User: "usr_b", DisplayName: "Bob"})
	if err := c.store.StoreGroup("group_1", "", "usr_a,usr_b"); err != nil {
		t.Fatalf("StoreGroup: %v", err)
	}
	if got := c.DisplayGroupName("group_1"); got != "Alice, Bob" {
		t.Fatalf("DisplayGroupName = %q, want %q", got, "Alice, Bob")
	}
}

func TestDisplayGroupName_UnknownGroupFallsBackToID(t *testing.T) {
	c := newClientWithGroupStore(t)
	if got := c.DisplayGroupName("group_missing"); got != "group_missing" {
		t.Fatalf("DisplayGroupName = %q, want %q", got, "group_missing")
	}
}
