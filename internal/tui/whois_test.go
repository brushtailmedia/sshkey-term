package tui

// Tests for the `/whois <user>` slash command. After the 2026-05 UX
// pass /whois opens the per-user info panel (same panel as the
// member-panel "view profile" action) instead of dumping a one-line
// summary to the status bar. So tests assert against the panel's
// populated state — IsVisible() + the per-user fields — rather than
// status-bar string contents.
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("info panel should be visible after /whois on a known user")
	}
	if !a.infoPanel.isUser {
		t.Fatal("info panel should be in user-profile mode")
	}
	if a.infoPanel.userID != "usr_alice" {
		t.Errorf("userID = %q, want usr_alice", a.infoPanel.userID)
	}
	if a.infoPanel.userDisplay != "Alice" {
		t.Errorf("userDisplay = %q, want Alice", a.infoPanel.userDisplay)
	}
	if a.infoPanel.userFingerprint != "SHA256:abcdef123456" {
		t.Errorf("userFingerprint = %q", a.infoPanel.userFingerprint)
	}
	if !a.infoPanel.userVerified {
		t.Error("userVerified should be true after MarkVerified")
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("info panel should be visible")
	}
	if a.infoPanel.userVerified {
		t.Error("userVerified should be false for unpinned-but-not-verified")
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("info panel should be visible after @-prefixed name resolution")
	}
	if a.infoPanel.userID != "usr_alice" {
		t.Errorf("userID = %q, want usr_alice (resolved from @Alice)", a.infoPanel.userID)
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("info panel should be visible after raw user-ID lookup")
	}
	if a.infoPanel.userDisplay != "Alice" {
		t.Errorf("userDisplay = %q, want Alice (resolved from raw ID)", a.infoPanel.userDisplay)
	}
}

func TestWhois_UnknownUser(t *testing.T) {
	a, _, _ := newWhoisTestApp(t)
	a.handleWhoisCommand("nobody")
	if !strings.Contains(a.statusBar.errorMsg, "unknown user") {
		t.Errorf("status bar should say unknown user; got %q", a.statusBar.errorMsg)
	}
	if a.infoPanel.IsVisible() {
		t.Error("info panel should NOT open for an unknown user")
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
	// /whois should still open the panel with the live fingerprint
	// but a zero first-seen.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:           "usr_bob",
		DisplayName:    "Bob",
		KeyFingerprint: "SHA256:bobkey",
	})

	a.handleWhoisCommand("Bob")

	if !a.infoPanel.IsVisible() {
		t.Fatal("panel should open with live-profile-only data")
	}
	if a.infoPanel.userFingerprint != "SHA256:bobkey" {
		t.Errorf("userFingerprint = %q, want SHA256:bobkey", a.infoPanel.userFingerprint)
	}
	if a.infoPanel.userFirstSeen != 0 {
		t.Errorf("userFirstSeen = %d, want 0 (no pinned entry)", a.infoPanel.userFirstSeen)
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("panel should open from pinned-only data")
	}
	if a.infoPanel.userFingerprint != "SHA256:carolkey" {
		t.Errorf("userFingerprint = %q, want SHA256:carolkey (from pinned)", a.infoPanel.userFingerprint)
	}
	if a.infoPanel.userFirstSeen == 0 {
		t.Error("userFirstSeen should be populated from pinned-keys row")
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("panel should open for retired user")
	}
	if !a.infoPanel.userRetired {
		t.Error("userRetired should be true")
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

	if !a.infoPanel.IsVisible() {
		t.Fatal("panel should open for admin user")
	}
	if !a.infoPanel.userAdmin {
		t.Error("userAdmin should be true")
	}
}

func TestWhois_NoFingerprintAnywhere(t *testing.T) {
	// Edge case: the resolved user exists in the profile cache but
	// the profile has no KeyFingerprint, AND no pinned entry exists.
	// The handler must surface a clean "no profile or pinned key"
	// error rather than opening a panel with no identity data.
	a, c, _ := newWhoisTestApp(t)
	client.SetProfileForTesting(c, &protocol.Profile{
		User:        "usr_nokey",
		DisplayName: "Helen",
		// KeyFingerprint deliberately empty.
	})

	a.handleWhoisCommand("Helen")

	if !strings.Contains(a.statusBar.errorMsg, "no profile or pinned key") {
		t.Errorf("should surface 'no profile or pinned key'; got %q", a.statusBar.errorMsg)
	}
	if a.infoPanel.IsVisible() {
		t.Error("info panel should NOT open without any fingerprint data")
	}
}
