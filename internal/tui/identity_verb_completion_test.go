package tui

// Drift-guard tests for the Phase 21 F29 + F30 identity-verb
// completion case in CompleteWithContext. `/verify`, `/unverify`, and
// `/whois` all complete against the union of (groupMembers ∪
// nonMemberPool) — the broadest pool — so operators can complete any
// known user's name regardless of whether they're in the current
// group. Pre-F30, these verbs fell to the default `groupMembers`-
// only case, which missed everyone outside the current group.

import (
	"testing"
)

func identityPool() (groupMembers, nonMembers []MemberEntry) {
	groupMembers = []MemberEntry{
		{UserID: "usr_alice123", DisplayName: "Alice"},
		{UserID: "usr_bob456", DisplayName: "Bob"},
	}
	nonMembers = []MemberEntry{
		{UserID: "usr_carol789", DisplayName: "Carol"},
		{UserID: "usr_dave000", DisplayName: "Dave"},
	}
	return
}

func assertCompletionContains(t *testing.T, m *CompletionModel, want string) {
	t.Helper()
	if m == nil {
		t.Fatalf("expected completion results, got nil (want entry for %q)", want)
	}
	for _, it := range m.items {
		if it.Display == want {
			return
		}
	}
	var got []string
	for _, it := range m.items {
		got = append(got, it.Display)
	}
	t.Errorf("completion items %v missing expected entry %q", got, want)
}

// TestCompletion_WhoisResolvesGroupMember — drift-guards that /whois
// completes a current-group member.
func TestCompletion_WhoisResolvesGroupMember(t *testing.T) {
	members, nonMembers := identityPool()
	got := CompleteWithContext("/whois @Al", len("/whois @Al"), members, nonMembers)
	assertCompletionContains(t, got, "@Alice")
}

// TestCompletion_WhoisResolvesNonMember — drift-guards the critical
// F30 property: /whois completes users NOT in the current group.
// Without the merged pool, this test fails — the default case only
// searches groupMembers.
func TestCompletion_WhoisResolvesNonMember(t *testing.T) {
	members, nonMembers := identityPool()
	got := CompleteWithContext("/whois @Ca", len("/whois @Ca"), members, nonMembers)
	assertCompletionContains(t, got, "@Carol")
}

func TestCompletion_VerifyResolvesNonMember(t *testing.T) {
	members, nonMembers := identityPool()
	got := CompleteWithContext("/verify @Ca", len("/verify @Ca"), members, nonMembers)
	assertCompletionContains(t, got, "@Carol")
}

func TestCompletion_UnverifyResolvesNonMember(t *testing.T) {
	members, nonMembers := identityPool()
	got := CompleteWithContext("/unverify @Ca", len("/unverify @Ca"), members, nonMembers)
	assertCompletionContains(t, got, "@Carol")
}

// TestCompletion_WhoisEmptyPoolReturnsNil verifies that in contexts
// with no loaded members (e.g., the home screen), completion
// silently returns nil rather than crashing. Users can still type
// the name manually.
func TestCompletion_WhoisEmptyPoolReturnsNil(t *testing.T) {
	got := CompleteWithContext("/whois @Al", len("/whois @Al"), nil, nil)
	if got != nil {
		t.Errorf("expected nil with empty pools, got %+v", got.items)
	}
}

// TestCompletion_WhoisDedupNotNeeded drift-guards the assumption
// that groupMembers and nonMemberPool are disjoint by construction
// (a user is either in the current group or not). If a future
// refactor violates this by letting the same user appear in both,
// completion will show duplicate entries — fix at the source, not
// by adding a dedup pass in completion.
func TestCompletion_WhoisNoDuplicatesFromDisjointPools(t *testing.T) {
	members := []MemberEntry{{UserID: "usr_alice", DisplayName: "Alice"}}
	nonMembers := []MemberEntry{{UserID: "usr_bob", DisplayName: "Bob"}}
	// Alice is in members only; Bob in non-members only. Each shows
	// exactly once in the merged pool.
	got := CompleteWithContext("/whois @A", len("/whois @A"), members, nonMembers)
	if got == nil {
		t.Fatal("expected completion results")
	}
	seen := map[string]int{}
	for _, it := range got.items {
		seen[it.Display]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("duplicate completion entry for %q (count=%d); groupMembers and nonMemberPool should be disjoint", name, count)
		}
	}
}

// TestCompletion_AddUsesNonMemberPoolNotMerged — regression guard
// that /add's filter semantics are preserved (non-members only).
// The /whois change added a new case; make sure /add still routes
// to the non-member-only branch instead of falling into the merged
// identity-verbs pool.
func TestCompletion_AddUsesNonMemberPoolNotMerged(t *testing.T) {
	members, nonMembers := identityPool()
	got := CompleteWithContext("/add @Al", len("/add @Al"), members, nonMembers)
	// Alice is a current-group member; /add should NOT complete her.
	if got != nil {
		for _, it := range got.items {
			if it.Display == "@Alice" {
				t.Errorf("/add should not complete current-group members; got %q", it.Display)
			}
		}
	}
	// Carol IS a non-member; /add should complete her.
	got = CompleteWithContext("/add @Ca", len("/add @Ca"), members, nonMembers)
	assertCompletionContains(t, got, "@Carol")
}
