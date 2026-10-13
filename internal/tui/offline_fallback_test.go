package tui

// Offline-browse Step 2 (offline-startup-browse R3/R20): the
// offlineWithClient landing. A stale-gen arrival must close its client
// and change nothing (same contract as connectedWithClient); a current-gen
// arrival adopts the local-only client WITHOUT flipping connected, and
// routes the dial error through the exact same handler as ErrMsg.

import (
	"errors"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

func TestStaleOfflineWithClient_DropsAndClosesClient(t *testing.T) {
	a := newGenTokenApp(2)
	stale := client.New(client.Config{})

	a2 := runUpdate(t, a, offlineWithClient{client: stale, err: errors.New("dial: boom"), gen: 1})

	if a2.client != nil {
		t.Errorf("stale offlineWithClient overwrote a.client")
	}
	if a2.connState == ConnStateOnline {
		t.Errorf("stale offlineWithClient flipped a.connected")
	}
	if a2.connectFailed.IsVisible() {
		t.Errorf("stale offlineWithClient must not show the connect-failed overlay")
	}
	select {
	case <-stale.Done():
	default:
		t.Errorf("stale offlineWithClient did not close msg.client — leaks the store handle")
	}
}

func TestCurrentGenOfflineWithClient_AdoptsClientAndMatchesErrMsg(t *testing.T) {
	dialErr := errors.New("dial: connection refused")

	offline := newGenTokenApp(3)
	local := client.New(client.Config{})
	offline2 := runUpdate(t, offline, offlineWithClient{client: local, err: dialErr, gen: 3})

	if offline2.client != local {
		t.Fatalf("current-gen offlineWithClient must adopt the local-only client")
	}
	if offline2.connState == ConnStateOnline {
		t.Fatalf("offline landing must never flip a.connected")
	}
	select {
	case <-local.Done():
		t.Fatalf("current-gen offlineWithClient must NOT close the adopted client")
	default:
	}

	// Behavioral equivalence of the extracted handleConnectError: an
	// identical ErrMsg on an identical App must leave the same error +
	// overlay + reconnect state behind.
	plain := newGenTokenApp(3)
	plain2 := runUpdate(t, plain, ErrMsg{Err: dialErr, gen: 3})

	if offline2.connectFailed.IsVisible() != plain2.connectFailed.IsVisible() {
		t.Errorf("overlay state diverged: offline=%v errmsg=%v",
			offline2.connectFailed.IsVisible(), plain2.connectFailed.IsVisible())
	}
	if !offline2.connectFailed.IsVisible() {
		t.Errorf("first-fail path must show the connect-failed overlay (Step 2 is zero-UX-change)")
	}
	if offline2.err == nil || plain2.err == nil || offline2.err.Error() != plain2.err.Error() {
		t.Errorf("a.err diverged: offline=%v errmsg=%v", offline2.err, plain2.err)
	}
	if offline2.reconnectAttempt != plain2.reconnectAttempt {
		t.Errorf("reconnectAttempt diverged: offline=%d errmsg=%d", offline2.reconnectAttempt, plain2.reconnectAttempt)
	}
}
