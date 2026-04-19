package tui

// Tests for Phase 21 F29 closure — `/verify` and `/unverify` must
// accept display names and "@alice" syntax in addition to raw user
// IDs, matching the completion affordance of `/add`. Both verbs route
// through `App.resolveUserByName`, which strips the leading "@",
// trims whitespace, and delegates to `client.FindUserByName`.
//
// The tests below drift-guard the wrapper behaviour without
// re-testing FindUserByName itself (which is the client package's
// responsibility).

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// newAppWithAlice returns an App whose Client has a single profile
// for usr_alice (display name "Alice"). Sufficient for exercising the
// resolveUserByName resolution paths.
func newAppWithAlice() *App {
	c := client.New(client.Config{})
	client.SetProfileForTesting(c, &protocol.Profile{
		User:        "usr_alice",
		DisplayName: "Alice",
	})
	return &App{client: c}
}

func TestResolveUserByName_DisplayName(t *testing.T) {
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("Alice")
	if !ok {
		t.Fatal("expected Alice to resolve")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_DisplayNameCaseInsensitive(t *testing.T) {
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("alice")
	if !ok {
		t.Fatal("expected case-insensitive display-name match")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_AtPrefix(t *testing.T) {
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("@Alice")
	if !ok {
		t.Fatal("expected @Alice to resolve (prefix stripped)")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_WhitespaceTrimmed(t *testing.T) {
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("  Alice  ")
	if !ok {
		t.Fatal("expected whitespace-padded input to resolve")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_AtAndWhitespaceCombined(t *testing.T) {
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("  @Alice  ")
	if !ok {
		t.Fatal("expected @-prefix + whitespace-padding to resolve together")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_RawUserID(t *testing.T) {
	// Backward-compatibility check: raw user IDs must continue to work
	// so pre-F29 /verify usr_alice invocations still resolve. This is
	// FindUserByName's second branch (uid match) which the wrapper
	// relies on transitively.
	a := newAppWithAlice()
	id, ok := a.resolveUserByName("usr_alice")
	if !ok {
		t.Fatal("expected raw user ID to resolve (backward compat)")
	}
	if id != "usr_alice" {
		t.Errorf("id = %q, want usr_alice", id)
	}
}

func TestResolveUserByName_Unknown(t *testing.T) {
	a := newAppWithAlice()
	_, ok := a.resolveUserByName("dave")
	if ok {
		t.Error("unknown user should return (_, false)")
	}
}

func TestResolveUserByName_Empty(t *testing.T) {
	a := newAppWithAlice()
	_, ok := a.resolveUserByName("")
	if ok {
		t.Error("empty input should return (_, false)")
	}
}

func TestResolveUserByName_OnlyAtOrWhitespace(t *testing.T) {
	// Trims to empty after stripping "@" + whitespace — should short-
	// circuit without calling FindUserByName.
	a := newAppWithAlice()
	cases := []string{"@", "   ", "@   ", "   @"}
	for _, in := range cases {
		if _, ok := a.resolveUserByName(in); ok {
			t.Errorf("%q should return (_, false)", in)
		}
	}
}

func TestResolveUserByName_NilClient(t *testing.T) {
	a := &App{} // no client
	_, ok := a.resolveUserByName("Alice")
	if ok {
		t.Error("nil client should return (_, false)")
	}
}

// TestResolveNonMemberByName_DelegatesToResolveUserByName drift-guards
// the relationship between the two named wrappers. Both are thin
// delegates to FindUserByName today; if a future refactor moves
// /add-specific logic into resolveNonMemberByName without updating
// this test, the drift is caught.
func TestResolveNonMemberByName_DelegatesToResolveUserByName(t *testing.T) {
	a := newAppWithAlice()
	id1, ok1 := a.resolveNonMemberByName("@Alice")
	id2, ok2 := a.resolveUserByName("@Alice")
	if ok1 != ok2 || id1 != id2 {
		t.Errorf("resolveNonMemberByName and resolveUserByName diverge: (%q,%v) vs (%q,%v)",
			id1, ok1, id2, ok2)
	}
}
