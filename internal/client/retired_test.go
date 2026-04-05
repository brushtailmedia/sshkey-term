package client

import (
	"encoding/json"
	"testing"
)

// TestIsRetiredInitial confirms a fresh client has no retired users.
func TestIsRetiredInitial(t *testing.T) {
	c := New(Config{})
	defer c.Close()
	retired, ts := c.IsRetired("alice")
	if retired || ts != "" {
		t.Errorf("fresh client: IsRetired(alice) = (%v, %q), want (false, \"\")", retired, ts)
	}
	if got := c.RetiredUsers(); len(got) != 0 {
		t.Errorf("RetiredUsers() = %v, want empty", got)
	}
}

// TestUserRetiredEvent verifies that processing a user_retired event marks
// the user as retired with the event's timestamp.
func TestUserRetiredEvent(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	raw := json.RawMessage(`{"type":"user_retired","user":"alice","ts":1712345678}`)
	c.handleInternal("user_retired", raw)

	retired, ts := c.IsRetired("alice")
	if !retired {
		t.Error("alice should be retired after user_retired event")
	}
	if ts == "" {
		t.Error("retired_at should be set")
	}
	// Timestamp is derived from unix epoch — rough sanity check
	if len(ts) < 10 {
		t.Errorf("retired_at looks malformed: %q", ts)
	}
}

// TestRetiredUsersBulk verifies the retired_users broadcast marks all users.
func TestRetiredUsersBulk(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	raw := json.RawMessage(`{"type":"retired_users","users":[
		{"user":"alice","retired_at":"2026-04-01T00:00:00Z"},
		{"user":"bob","retired_at":"2026-04-02T00:00:00Z"}
	]}`)
	c.handleInternal("retired_users", raw)

	if retired, _ := c.IsRetired("alice"); !retired {
		t.Error("alice should be retired")
	}
	if retired, _ := c.IsRetired("bob"); !retired {
		t.Error("bob should be retired")
	}
	if retired, _ := c.IsRetired("carol"); retired {
		t.Error("carol should NOT be retired")
	}

	if got := c.RetiredUsers(); len(got) != 2 {
		t.Errorf("RetiredUsers() = %v, want 2 entries", got)
	}
}

// TestProfileRetiredFieldTracked verifies a profile with retired=true
// marks the user as retired.
func TestProfileRetiredFieldTracked(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	raw := json.RawMessage(`{"type":"profile","user":"alice","display_name":"Alice","pubkey":"ssh-ed25519 AAAA","key_fingerprint":"SHA256:abc","retired":true,"retired_at":"2026-04-05T00:00:00Z"}`)
	c.handleInternal("profile", raw)

	retired, ts := c.IsRetired("alice")
	if !retired {
		t.Error("alice should be retired from profile with retired=true")
	}
	if ts != "2026-04-05T00:00:00Z" {
		t.Errorf("retired_at = %q, want 2026-04-05T00:00:00Z", ts)
	}
}

// TestProfileWithoutRetiredDoesNotMark verifies a profile without retired=true
// does NOT mark the user.
func TestProfileWithoutRetiredDoesNotMark(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	raw := json.RawMessage(`{"type":"profile","user":"alice","display_name":"Alice","pubkey":"ssh-ed25519 AAAA","key_fingerprint":"SHA256:abc"}`)
	c.handleInternal("profile", raw)

	if retired, _ := c.IsRetired("alice"); retired {
		t.Error("alice should NOT be retired (no retired field in profile)")
	}
}

// TestProfileUnretiredClears verifies a profile update with retired=false
// clears the retired state (used on admin un-retire).
func TestProfileUnretiredClears(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	// Mark alice retired
	c.handleInternal("user_retired", json.RawMessage(`{"type":"user_retired","user":"alice","ts":1712345678}`))
	if retired, _ := c.IsRetired("alice"); !retired {
		t.Fatal("precondition: alice should be retired")
	}

	// Send a fresh profile WITHOUT retired=true
	raw := json.RawMessage(`{"type":"profile","user":"alice","display_name":"Alice","pubkey":"ssh-ed25519 AAAA","key_fingerprint":"SHA256:abc"}`)
	c.handleInternal("profile", raw)

	if retired, _ := c.IsRetired("alice"); retired {
		t.Error("alice should be un-retired after non-retired profile")
	}
}

// TestDeviceRevokedHandlerNoPanic verifies the device_revoked case doesn't
// crash the client (it's a no-op at the client layer — UI handles it).
func TestDeviceRevokedHandlerNoPanic(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	raw := json.RawMessage(`{"type":"device_revoked","device_id":"dev_abc","reason":"admin_action"}`)
	c.handleInternal("device_revoked", raw)
	// Should not panic, should not close done channel
	select {
	case <-c.Done():
		t.Error("device_revoked should not close client.Done()")
	default:
		// expected
	}
}

// TestRetiredUsersSnapshot verifies RetiredUsers returns a copy.
func TestRetiredUsersSnapshot(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	c.handleInternal("user_retired", json.RawMessage(`{"type":"user_retired","user":"alice","ts":1712345678}`))

	snap1 := c.RetiredUsers()
	snap1["fake"] = "injected" // mutate the snapshot

	// Internal state should be unaffected
	if retired, _ := c.IsRetired("fake"); retired {
		t.Error("external mutation of snapshot leaked into internal state")
	}
	// Original entry still there
	if retired, _ := c.IsRetired("alice"); !retired {
		t.Error("alice should still be retired")
	}
}

// TestConcurrentReadRetiredSafe verifies concurrent readers don't panic.
func TestConcurrentReadRetiredSafe(t *testing.T) {
	c := New(Config{})
	defer c.Close()

	// Mark some users retired
	for _, u := range []string{"alice", "bob", "carol"} {
		c.handleInternal("user_retired", json.RawMessage(`{"type":"user_retired","user":"`+u+`","ts":1712345678}`))
	}

	// Concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				_, _ = c.IsRetired("alice")
				_ = c.RetiredUsers()
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
