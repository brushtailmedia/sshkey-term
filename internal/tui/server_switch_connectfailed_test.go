package tui

// Regression tests for the server-switch dead-end fix:
// from the ConnectFailedModel a user must be able to Ctrl+g <n>
// to switch to another configured server instead of being stuck
// on retry-or-quit. See fix-server-switching.md.
//
// Uses newNavModeAppHarness / navCtrlG / navRune / updateNavApp
// from navmode_test.go (same package): 2 servers configured
// (Home 127.0.0.1, Work 127.0.0.2), serverIdx 0.

import "testing"

// Core: modal up → Ctrl+g 2 → switches to server 2 AND dismisses
// the modal (Part 1 carve-out + Part 2 Hide()).
func TestConnectFailed_CtrlGDigitSwitchesServerAndDismissesModal(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.connectFailed.Show("dial tcp 127.0.0.1:2222: connect: connection refused", "SHA256:abc", "ssh-ed25519 AAA")
	if !a.connectFailed.IsVisible() {
		t.Fatal("precondition: connectFailed should be visible")
	}

	// Ctrl+g enters nav mode even with the modal up.
	a = updateNavApp(t, a, navCtrlG())
	if !a.navMode {
		t.Fatal("Ctrl+g from the modal should enter nav mode")
	}
	if !a.connectFailed.IsVisible() {
		t.Fatal("modal should still be visible after just the Ctrl+g prefix")
	}

	// The digit switches server and the switch dismisses the modal.
	a = updateNavApp(t, a, navRune('2'))
	if a.serverIdx != 1 {
		t.Errorf("serverIdx = %d, want 1 (switched to server 2)", a.serverIdx)
	}
	if a.cfg.Host != "127.0.0.2" {
		t.Errorf("cfg.Host = %q, want 127.0.0.2", a.cfg.Host)
	}
	if a.connectFailed.IsVisible() {
		t.Error("modal must be dismissed once the switch is initiated (not painted over the new connect)")
	}
	if a.navMode {
		t.Error("nav mode should have exited after the digit")
	}
}

// Option A guard: a non-server nav key (k = quick-switch) from the
// modal must NOT open the (empty, pre-connection) quick-switch —
// it just cancels the nav prefix and leaves the modal up.
func TestConnectFailed_CtrlGKDoesNotOpenQuickSwitch(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.connectFailed.Show("dial tcp: connection refused", "SHA256:abc", "")

	a = updateNavApp(t, a, navCtrlG())
	a = updateNavApp(t, a, navRune('k'))

	if a.quickSwitch.IsVisible() {
		t.Error("Ctrl+g k from the failure modal must NOT open quick-switch (Option A: digits + cancel only)")
	}
	if !a.connectFailed.IsVisible() {
		t.Error("modal should stay visible (k just cancels the nav prefix)")
	}
	if a.navMode {
		t.Error("nav mode should have exited (prefix cancelled)")
	}
	if a.serverIdx != 0 {
		t.Errorf("serverIdx = %d, want 0 (k must not switch servers)", a.serverIdx)
	}
}

// Regression: with no nav prefix active, the carve-out must not
// disturb the modal's own keys — an unrecognized key still routes
// to connectFailed.Update (which ignores it; modal stays).
func TestConnectFailed_ModalKeysUnchangedWithoutNavPrefix(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.connectFailed.Show("dial tcp: connection refused", "SHA256:abc", "")

	a = updateNavApp(t, a, navRune('x')) // not r/c/q/esc, not ctrl+g, navMode false

	if !a.connectFailed.IsVisible() {
		t.Error("an unrecognized key with no nav prefix should leave the modal up (unchanged behavior)")
	}
	if a.navMode {
		t.Error("a plain key must not enter nav mode")
	}
	if a.serverIdx != 0 {
		t.Errorf("serverIdx = %d, want 0 (no switch without Ctrl+g prefix)", a.serverIdx)
	}
}
