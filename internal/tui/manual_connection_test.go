package tui

// Offline-browse Step 6 (offline-startup-browse R7/R9/R10/D9): the manual
// connection controls, the Ctrl+g o three-state toggle, and the bounded
// initial-connect ladder for servers with cached history.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func TestOfflineCommand_WhileOnlineKeepsStoreAndContent(t *testing.T) {
	a := newNavModeAppHarness(t) // live session: connected client + open store
	a.sidebar = NewSidebar()
	a.sidebar.SetRooms([]string{"room_x", "room_y"})
	a.messages.SetContext("room_x", "", "")
	genBefore := a.connGen

	if cmd := a.handleSlashCommandCmd(&SlashCommandMsg{Command: "/offline"}); cmd != nil {
		t.Fatal("/offline must not return a cmd")
	}
	if a.client == nil || a.client.IsConnected() {
		t.Fatal("/offline must disconnect the client but keep it")
	}
	if a.client.Store() == nil {
		t.Fatal("/offline must keep the local store open (browse-preserving disconnect)")
	}
	if a.connState != ConnStateOffline || !a.statusBar.offline {
		t.Fatalf("connState=%v statusBar.offline=%v, want Offline/true", a.connState, a.statusBar.offline)
	}
	if a.connGen != genBefore+1 {
		t.Fatalf("/offline must bump the gen to stale in-flight work: %d → %d", genBefore, a.connGen)
	}
	if len(a.sidebar.rooms) != 2 || a.messages.room != "room_x" {
		t.Fatal("/offline must never wipe visible state (R9)")
	}
	if a.statusBar.errorMsg != "offline - local history only" {
		t.Fatalf("status = %q", a.statusBar.errorMsg)
	}
	// Idempotent via the alias.
	a.handleSlashCommandCmd(&SlashCommandMsg{Command: "/disconnect"})
	if a.connState != ConnStateOffline || a.connGen != genBefore+1 {
		t.Fatal("/disconnect while already offline must be a no-op")
	}
}

func TestOnlineCommand_FromOfflineReconnectsAndAdoptsNewClient(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.messages.SetContext("room_x", "", "")
	a.handleSlashCommandCmd(&SlashCommandMsg{Command: "/offline"})
	old := a.client
	genBefore := a.connGen

	cmd := a.handleSlashCommandCmd(&SlashCommandMsg{Command: "/online"})
	if cmd == nil {
		t.Fatal("/online must return the connect cmd — it is what dials")
	}
	if a.connGen != genBefore+1 || a.connState != ConnStateConnecting {
		t.Fatalf("gen %d→%d state %v, want gen bump + Connecting", genBefore, a.connGen, a.connState)
	}
	if a.statusBar.errorMsg != "connecting..." {
		t.Fatalf("status = %q, want connecting...", a.statusBar.errorMsg)
	}

	// Simulated success for the current gen.
	fresh := client.New(client.Config{})
	client.SetConnectedForTesting(fresh, true)
	na := runUpdate(t, &a, connectedWithClient{client: fresh, gen: a.connGen})
	if na.connState != ConnStateOnline || na.statusBar.offline {
		t.Fatalf("after reconnect: state=%v offline=%v, want Online/false", na.connState, na.statusBar.offline)
	}
	if na.client != fresh {
		t.Fatal("reconnect must adopt the new client")
	}
	// R20: the old local-only client's store must be closed (Disconnect
	// already closed its done channel, so test the store handle itself).
	if _, err := old.Store().GetState("probe"); err == nil {
		t.Fatal("old client's store must be closed when the new connection is adopted (R20)")
	}
	if na.messages.room != "room_x" {
		t.Fatal("selected context must survive a reconnect (R9)")
	}
	if cmd := na.handleSlashCommandCmd(&SlashCommandMsg{Command: "/connect"}); cmd != nil || na.statusBar.errorMsg != "already connected" {
		t.Fatalf("/connect while online: cmd=%v status=%q, want nil + already connected", cmd, na.statusBar.errorMsg)
	}
}

func TestCtrlGo_ThreeStateToggle(t *testing.T) {
	a := newNavModeAppHarness(t) // Online
	cmd, handled := a.handleNavModeKey(navRune('o'))
	if !handled || cmd != nil || a.connState != ConnStateOffline {
		t.Fatalf("online → o: handled=%v cmd=%v state=%v, want offline with no cmd", handled, cmd, a.connState)
	}
	cmd, handled = a.handleNavModeKey(navRune('o'))
	if !handled || cmd == nil || a.connState != ConnStateConnecting {
		t.Fatalf("offline → o: handled=%v cmd=%v state=%v, want a connect cmd + Connecting", handled, cmd, a.connState)
	}
	cmd, handled = a.handleNavModeKey(navRune('o'))
	if !handled || cmd != nil || a.connState != ConnStateOffline {
		t.Fatalf("connecting → o: handled=%v cmd=%v state=%v, want cancel back to offline", handled, cmd, a.connState)
	}
	if a.navMode {
		t.Fatal("o must exit nav mode")
	}
}

// newLadderApp is a minimal App whose client has an open store and, when
// cached, a last_synced watermark — the HasLocalCache predicate the ladder
// keys on.
func newLadderApp(t *testing.T, cached bool) *App {
	t.Helper()
	a := newGenTokenApp(5)
	st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := client.New(client.Config{})
	client.SetStoreForTesting(c, st)
	if cached {
		client.SetLastSyncedForTesting(c, "2026-01-01T00:00:00Z")
	}
	a.client = c
	return a
}

func TestInitialLadder_CachedServerStopsAfterFourAttempts(t *testing.T) {
	a := newLadderApp(t, true)
	dial := errors.New("dial: connection refused")

	// Attempt 1 (the immediate one) fails → the ladder starts, no overlay.
	if cmd := a.handleConnectError(dial); cmd == nil {
		t.Fatal("first failure on a cached server must schedule a retry tick")
	}
	if a.initialAttempt != 1 || a.connState != ConnStateConnecting || a.connectFailed.IsVisible() {
		t.Fatalf("after failure 1: initialAttempt=%d state=%v overlay=%v", a.initialAttempt, a.connState, a.connectFailed.IsVisible())
	}

	for attempt := 2; attempt <= 4; attempt++ {
		gen := a.connGen
		a = runUpdate(t, a, initialRetryMsg{attempt: attempt, gen: gen})
		if a.connGen != gen+1 {
			t.Fatalf("ladder tick %d must bump the gen (startConnect)", attempt)
		}
		cmd := a.handleConnectError(dial)
		if attempt < 4 {
			if cmd == nil || a.initialAttempt != attempt || a.connState != ConnStateConnecting {
				t.Fatalf("after failure %d: cmd=%v initialAttempt=%d state=%v", attempt, cmd, a.initialAttempt, a.connState)
			}
			continue
		}
		if cmd != nil {
			t.Fatal("the 4th failure must NOT schedule another tick")
		}
		if a.connState != ConnStateOffline || a.initialAttempt != 0 || !a.statusBar.offline {
			t.Fatalf("after failure 4: state=%v initialAttempt=%d offline=%v, want Offline/0/true", a.connState, a.initialAttempt, a.statusBar.offline)
		}
		if a.statusBar.errorMsg != "offline - connect failed" {
			t.Fatalf("status = %q", a.statusBar.errorMsg)
		}
	}
	if a.connectFailed.IsVisible() {
		t.Fatal("a cached server must never show the connect-failed overlay")
	}
	// A stale tick (older gen) does nothing.
	gen := a.connGen
	a = runUpdate(t, a, initialRetryMsg{attempt: 2, gen: gen - 1})
	if a.connGen != gen || a.connState != ConnStateOffline {
		t.Fatal("a stale ladder tick must be dropped")
	}
	// The mid-session reconnect path is untouched by the ladder.
	if a.reconnectAttempt != 0 {
		t.Fatalf("reconnectAttempt = %d, the ladder must never touch it", a.reconnectAttempt)
	}
}

func TestInitialLadder_NoCacheServerShowsOverlayImmediately(t *testing.T) {
	a := newLadderApp(t, false)
	if cmd := a.handleConnectError(errors.New("dial: refused")); cmd != nil {
		t.Fatal("a no-cache first failure must not schedule a ladder tick")
	}
	if !a.connectFailed.IsVisible() {
		t.Fatal("a no-cache first failure must show the connect-failed (pending-approval) overlay — regression pin")
	}
	if a.initialAttempt != 0 || a.connState != ConnStateConnecting {
		t.Fatalf("initialAttempt=%d state=%v, want 0/Connecting", a.initialAttempt, a.connState)
	}
	// Very first contact: no client at all → same overlay.
	b := newGenTokenApp(3)
	if cmd := b.handleConnectError(errors.New("dial: refused")); cmd != nil || !b.connectFailed.IsVisible() {
		t.Fatal("first contact (nil client) must show the overlay with no ladder")
	}
}

// Connection-progress statuses live in the status bar's error slot, which
// persists until the next user action: a successful connect must clear
// them itself, or "connecting..." outlives the connection it announced.
func TestConnectSuccess_ClearsConnectingStatus(t *testing.T) {
	for _, progress := range []string{
		"connecting...",
		"connecting... (attempt 3/4)",
		"offline - connect failed",
		"offline - local history only",
	} {
		a := newGenTokenApp(4)
		a.statusBar.SetError(progress)
		fresh := client.New(client.Config{})
		client.SetConnectedForTesting(fresh, true)
		na := runUpdate(t, a, connectedWithClient{client: fresh, gen: 4})
		if na.connState != ConnStateOnline {
			t.Fatalf("%q: expected Online after connect", progress)
		}
		if na.statusBar.errorMsg != "" {
			t.Fatalf("%q must be cleared on a successful connect, still %q", progress, na.statusBar.errorMsg)
		}
	}

	// Unrelated statuses are left alone.
	a := newGenTokenApp(5)
	a.statusBar.SetError("Upload cancelled: the connection changed while the picker was open")
	fresh := client.New(client.Config{})
	client.SetConnectedForTesting(fresh, true)
	na := runUpdate(t, a, connectedWithClient{client: fresh, gen: 5})
	if !strings.HasPrefix(na.statusBar.errorMsg, "Upload cancelled") {
		t.Fatalf("a non-progress status must survive a connect, got %q", na.statusBar.errorMsg)
	}
}
