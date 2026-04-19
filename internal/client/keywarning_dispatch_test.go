package client

// Phase 21 F3.a closure 2026-04-19 — tests the dispatch contract
// between StoreProfile (client package) and the TUI's
// KeyWarningModel (via the OnKeyWarning callback on Config).
//
// The contract under test:
//   - On first encounter of a user (no existing pin), StoreProfile
//     MUST NOT fire OnKeyWarning. Only pins.
//   - On subsequent encounters with the SAME fingerprint, StoreProfile
//     MUST NOT fire OnKeyWarning. No change → no event.
//   - On a detected fingerprint mismatch for an existing user ID,
//     StoreProfile MUST fire OnKeyWarning with (user, old, new).
//     Under the no-rotation protocol invariant this is always an
//     anomaly (see PROTOCOL.md "Keys as Identities"); the callback
//     drives the TUI's blocking KeyWarningModel.
//
// These tests intentionally live in the client package (not the tui
// package) so they exercise the callback from the store-side without
// needing a full tea.Program harness.

import (
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func newKeyWarningTestClient(t *testing.T) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	c.store = st
	return c
}

func TestStoreProfile_FirstEncounterDoesNotFireWarning(t *testing.T) {
	c := newKeyWarningTestClient(t)

	var fired bool
	c.cfg.OnKeyWarning = func(user, oldFP, newFP string) {
		fired = true
	}

	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
		PubKey:         "ssh-ed25519 A",
	})

	if fired {
		t.Error("OnKeyWarning should not fire on first encounter (no existing pin)")
	}

	// Sanity: the pin landed.
	fp, _, _ := c.store.GetPinnedKey("usr_alice")
	if fp != "SHA256:abc" {
		t.Errorf("pin not stored; got fingerprint %q, want SHA256:abc", fp)
	}
}

func TestStoreProfile_SameFingerprintDoesNotFireWarning(t *testing.T) {
	c := newKeyWarningTestClient(t)

	var fired bool
	c.cfg.OnKeyWarning = func(user, oldFP, newFP string) {
		fired = true
	}

	// Pin first.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
		PubKey:         "ssh-ed25519 A",
	})
	// Same fingerprint a second time — routine repeat from a later
	// profile broadcast. Must not fire warning.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
		PubKey:         "ssh-ed25519 A",
	})

	if fired {
		t.Error("OnKeyWarning should not fire when fingerprint matches existing pin")
	}
}

func TestStoreProfile_FingerprintMismatchFiresWarning(t *testing.T) {
	c := newKeyWarningTestClient(t)

	var (
		mu        sync.Mutex
		fireCount int
		gotUser   string
		gotOld    string
		gotNew    string
	)
	c.cfg.OnKeyWarning = func(user, oldFP, newFP string) {
		mu.Lock()
		defer mu.Unlock()
		fireCount++
		gotUser = user
		gotOld = oldFP
		gotNew = newFP
	}

	// Pin the first fingerprint.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:abc",
		PubKey:         "ssh-ed25519 A",
	})

	// Second profile with a DIFFERENT fingerprint for the same user
	// ID. Under no-rotation this is anomalous; the callback MUST fire.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:def",
		PubKey:         "ssh-ed25519 B",
	})

	if fireCount != 1 {
		t.Fatalf("OnKeyWarning fireCount = %d, want 1", fireCount)
	}
	if gotUser != "usr_alice" {
		t.Errorf("user = %q, want usr_alice", gotUser)
	}
	if gotOld != "SHA256:abc" {
		t.Errorf("old fingerprint = %q, want SHA256:abc", gotOld)
	}
	if gotNew != "SHA256:def" {
		t.Errorf("new fingerprint = %q, want SHA256:def", gotNew)
	}

	// Post-fix state: schema auto-clears verified; the new fingerprint
	// is pinned. Both the ClearVerified call in StoreProfile AND the
	// schema ON-CONFLICT-CASE are attack-path code (see
	// audit_v0.2.0.md#F32); test the observable result to guard
	// against stripping either.
	fp, verified, _ := c.store.GetPinnedKey("usr_alice")
	if fp != "SHA256:def" {
		t.Errorf("post-warning pin = %q, want SHA256:def", fp)
	}
	if verified {
		t.Error("post-warning verified flag should be cleared")
	}
}

func TestStoreProfile_NilCallbackDoesNotPanic(t *testing.T) {
	c := newKeyWarningTestClient(t)
	// Deliberately leave c.cfg.OnKeyWarning nil. StoreProfile must
	// handle that gracefully — a test client or a client with no TUI
	// attached (e.g., scripted integration test) should not crash on
	// detected key change.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:abc",
	})
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:def",
	})
	// If we got here without panicking, the nil-callback path works.
}

func TestStoreProfile_VerifiedFlagSurvivesSameFingerprint(t *testing.T) {
	// Regression guard: verifying a peer, then receiving a repeat
	// profile with the same fingerprint, must NOT reset the verified
	// flag. The schema ON-CONFLICT-CASE is specifically scoped to
	// fingerprint mismatch; this test proves it.
	c := newKeyWarningTestClient(t)
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:abc",
	})
	if err := c.store.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}

	// Repeat profile receive — same fingerprint.
	c.StoreProfile(&protocol.Profile{
		User:           "usr_alice",
		KeyFingerprint: "SHA256:abc",
	})

	_, verified, _ := c.store.GetPinnedKey("usr_alice")
	if !verified {
		t.Error("verified flag should survive repeat profile with same fingerprint")
	}
}
