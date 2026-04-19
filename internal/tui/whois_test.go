package tui

// Tests for the `/whois <user>` slash command (Phase 21 F30 closure
// 2026-04-19). The command surfaces full locally-known identity info
// for a user — display name, user ID, key fingerprint, verified
// state, and first-seen / last-key-updated timestamps — so operators
// investigating "did Alice's key actually rotate?" can check
// immediately without launching the full VerifyModel. Also copies the
// fingerprint to the clipboard to match `/mykey`'s ergonomic.
//
// Tests cover: happy path, unknown user, nil client, empty arg, live-
// profile-only fallback (no pinned entry), pinned-only fallback (no
// live profile), retired / admin flag rendering, verified-state
// rendering, and the @-prefix / display-name resolution
// passthrough from F29.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// newWhoisTestApp returns an App with a live Client + opened store in
// a temp dir. The caller can populate profiles + pinned keys before
// calling handleWhoisCommand.
func newWhoisTestApp(t *testing.T) (*App, *client.Client, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenUnencrypted(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)

	a := &App{
		client:    c,
		statusBar: NewStatusBar(),
	}
	return a, c, st
}

func TestWhois_HappyPath(t *testing.T) {
	a, c, st := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abcdef123456",
		PubKey:         "ssh-ed25519 AAAA...",
	})
	if err := st.PinKey("usr_alice", "SHA256:abcdef123456", "ssh-ed25519 AAAA..."); err != nil {
		t.Fatalf("PinKey: %v", err)
	}
	if err := st.MarkVerified("usr_alice"); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}

	a.handleWhoisCommand("Alice")

	msg := a.statusBar.errorMsg
	for _, want := range []string{"Alice", "usr_alice", "SHA256:abcdef123456", "verified", "fingerprint copied"} {
		if !strings.Contains(msg, want) {
			t.Errorf("status bar missing %q; got %q", want, msg)
		}
	}
	if strings.Contains(msg, "unverified") {
		t.Errorf("should show verified, not unverified; got %q", msg)
	}
}

func TestWhois_UnverifiedRendersUnverified(t *testing.T) {
	a, c, st := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
	})
	st.PinKey("usr_alice", "SHA256:abc", "ssh-ed25519 k") // not MarkVerified'd

	a.handleWhoisCommand("Alice")

	if !strings.Contains(a.statusBar.errorMsg, "unverified") {
		t.Errorf("status bar should contain 'unverified'; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_ResolvesAtPrefix(t *testing.T) {
	// Inherits F29's resolveUserByName behaviour; this drift-guards
	// the integration so a future refactor that breaks /whois doesn't
	// go unnoticed just because /verify's tests still pass.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
	})

	a.handleWhoisCommand("@Alice")

	if !strings.Contains(a.statusBar.errorMsg, "usr_alice") {
		t.Errorf("status bar should show resolved user id; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_ResolvesRawUserID(t *testing.T) {
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_alice",
		DisplayName:    "Alice",
		KeyFingerprint: "SHA256:abc",
	})

	a.handleWhoisCommand("usr_alice")

	if !strings.Contains(a.statusBar.errorMsg, "Alice") {
		t.Errorf("status bar should resolve raw user id to display name; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_UnknownUser(t *testing.T) {
	a, _, _ := newWhoisTestApp(t)
	a.handleWhoisCommand("nobody")
	if !strings.Contains(a.statusBar.errorMsg, "unknown user") {
		t.Errorf("status bar should say unknown user; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_EmptyArgShowsUsage(t *testing.T) {
	a, _, _ := newWhoisTestApp(t)
	a.handleWhoisCommand("")
	if !strings.Contains(a.statusBar.errorMsg, "unknown user") {
		// Empty arg fails resolution → "unknown user" error. That's
		// the same user-facing message as an unknown user; either
		// outcome is fine as long as nothing panics.
		// But if the handler ever surfaces an explicit "Usage:"
		// message, that's also acceptable.
		if !strings.Contains(a.statusBar.errorMsg, "Usage:") {
			t.Errorf("status bar should show usage or unknown-user error; got %q", a.statusBar.errorMsg)
		}
	}
}

func TestWhois_NilClient(t *testing.T) {
	a := &App{statusBar: NewStatusBar()}
	// No client attached; handler must not panic.
	a.handleWhoisCommand("alice")
	if !strings.Contains(a.statusBar.errorMsg, "Usage") {
		t.Errorf("nil-client path should show usage; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_LiveProfileWithoutPinnedEntry(t *testing.T) {
	// Profile is in the live cache but nothing has been pinned yet
	// (e.g., brand-new connection, no prior message exchange).
	// /whois should still work, showing the live fingerprint but
	// without timestamps.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_bob",
		DisplayName:    "Bob",
		KeyFingerprint: "SHA256:bobkey",
	})

	a.handleWhoisCommand("Bob")

	msg := a.statusBar.errorMsg
	if !strings.Contains(msg, "SHA256:bobkey") {
		t.Errorf("should show live-profile fingerprint; got %q", msg)
	}
	if strings.Contains(msg, "first seen") {
		t.Errorf("should omit 'first seen' when no pinned entry; got %q", msg)
	}
	if strings.Contains(msg, "key updated") {
		t.Errorf("should omit 'key updated' when no pinned entry; got %q", msg)
	}
}

func TestWhois_PinnedOnlyFallback(t *testing.T) {
	// Opposite scenario: pinned entry exists but no live profile
	// (e.g., retired user whose profile broadcast we missed). The
	// command must fall back to the pinned fingerprint.
	a, _, st := newWhoisTestApp(t)
	// No live profile — just a pinned entry.
	st.PinKey("usr_carol", "SHA256:carolkey", "ssh-ed25519 c")

	// Without a live profile, FindUserByName won't match "Carol"
	// (profile cache is empty); must resolve by user ID.
	a.handleWhoisCommand("usr_carol")

	msg := a.statusBar.errorMsg
	if !strings.Contains(msg, "SHA256:carolkey") {
		t.Errorf("should show pinned-fallback fingerprint; got %q", msg)
	}
	if !strings.Contains(msg, "first seen") {
		t.Errorf("pinned entry should render first-seen timestamp; got %q", msg)
	}
}

func TestWhois_RetiredFlagRendered(t *testing.T) {
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_retired",
		DisplayName:    "Dave",
		KeyFingerprint: "SHA256:davekey",
		Retired:        true,
	})

	a.handleWhoisCommand("Dave")

	if !strings.Contains(a.statusBar.errorMsg, "retired") {
		t.Errorf("status bar should include 'retired' marker; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_AdminFlagRendered(t *testing.T) {
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_admin",
		DisplayName:    "Eve",
		KeyFingerprint: "SHA256:evekey",
		Admin:          true,
	})

	a.handleWhoisCommand("Eve")

	if !strings.Contains(a.statusBar.errorMsg, "admin") {
		t.Errorf("status bar should include 'admin' marker; got %q", a.statusBar.errorMsg)
	}
}

func TestWhois_KeyUpdatedOmittedWhenEqualsFirstSeen(t *testing.T) {
	// When a profile is pinned for the first time, first_seen ==
	// updated_at. Rendering both would be redundant; the handler
	// should only render "key updated" when it differs from
	// "first seen".
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_fresh",
		DisplayName:    "Frank",
		KeyFingerprint: "SHA256:frank1",
	})
	// Note: PinKey sets first_seen = updated_at = now, so they're equal.
	if st := c.Store(); st != nil {
		st.PinKey("usr_fresh", "SHA256:frank1", "ssh-ed25519 f")
	}

	a.handleWhoisCommand("Frank")

	msg := a.statusBar.errorMsg
	if !strings.Contains(msg, "first seen") {
		t.Errorf("should render first-seen on first pin; got %q", msg)
	}
	if strings.Contains(msg, "key updated") {
		t.Errorf("should NOT render 'key updated' when equal to first-seen; got %q", msg)
	}
}

func TestWhois_KeyUpdatedRenderedWhenChanged(t *testing.T) {
	// After a key change, updated_at advances past first_seen. The
	// handler should render both timestamps to surface the change
	// date.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_rotated",
		DisplayName:    "Gina",
		KeyFingerprint: "SHA256:gina2",
	})

	st := c.Store()
	st.PinKey("usr_rotated", "SHA256:gina1", "ssh-ed25519 g1")
	// Force first_seen and updated_at to diverge. We can't easily
	// advance time in this test without hook injection, so directly
	// write a known timestamp via the exposed PinKey path with a
	// contrived sleep (avoid flakiness: 1.1s is enough for the store
	// to record distinct Unix-second values).
	time.Sleep(1100 * time.Millisecond)
	st.PinKey("usr_rotated", "SHA256:gina2", "ssh-ed25519 g2") // key change

	a.handleWhoisCommand("Gina")

	msg := a.statusBar.errorMsg
	if !strings.Contains(msg, "first seen") {
		t.Errorf("should render first-seen; got %q", msg)
	}
	if !strings.Contains(msg, "key updated") {
		t.Errorf("should render 'key updated' when different from first-seen; got %q", msg)
	}
}

func TestWhois_NoFingerprintAnywhere(t *testing.T) {
	// Edge case: the resolved user exists in the profile cache but
	// the profile has no KeyFingerprint, AND no pinned entry exists.
	// The handler must surface a clean "no profile or pinned key"
	// error rather than rendering a malformed status line.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:        "usr_nokey",
		DisplayName: "Helen",
		// KeyFingerprint deliberately empty.
	})

	a.handleWhoisCommand("Helen")

	msg := a.statusBar.errorMsg
	if !strings.Contains(msg, "no profile or pinned key") {
		t.Errorf("should surface 'no profile or pinned key'; got %q", msg)
	}
}
