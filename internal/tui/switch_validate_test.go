package tui

// Regression test for switchServerByIndex's validate-before-close
// ordering (2026-05-21). Pre-fix, the function closed the active
// client BEFORE running config.ValidateHost on the target server's
// host — a hand-edited invalid host in config.toml would disconnect
// the user from the working server, then fail the switch with a
// status-bar error, leaving them with no connection at all. Post-fix
// the validation runs first; the close only happens once the target
// is known good.

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
)

func TestSwitchServerByIndex_InvalidHostDoesNotCloseCurrentClient(t *testing.T) {
	a := newNavModeAppHarness(t)
	// Replace the harness's valid second server with one whose Host
	// fails ValidateHost (path separator is rejected — see
	// config.ValidateHost). The hand-edited-config scenario.
	a.appConfig.Servers = []config.ServerConfig{
		{Name: "Good", Host: "127.0.0.1", Port: 2222},
		{Name: "Bad", Host: "../traversal", Port: 2223},
	}
	a.serverIdx = 0

	// Attach a real client whose Done() channel we can monitor for
	// close. No live connection — we never call Connect — but
	// Close() still flips c.done, which is the predicate the rest
	// of the codebase uses to detect shutdown.
	c := client.New(client.Config{})
	a.client = c

	cmd := a.switchServerByIndex(1)

	if cmd != nil {
		t.Errorf("switch to invalid host returned a cmd; should refuse with status, not kick off connect")
	}
	if a.serverIdx != 0 {
		t.Errorf("serverIdx changed to %d after rejected switch; want 0", a.serverIdx)
	}
	select {
	case <-c.Done():
		t.Errorf("active client closed despite rejected switch — user stranded with no connection")
	default:
	}
	// The user-visible signal that the switch was refused.
	if got := a.statusBar.errorMsg; got == "" {
		t.Errorf("expected status-bar error for rejected switch; got empty")
	}
}

// TestSwitchServerByIndex_ValidHostStillClosesCurrentClient locks
// the happy path so the reorder doesn't accidentally suppress the
// close on valid switches.
func TestSwitchServerByIndex_ValidHostStillClosesCurrentClient(t *testing.T) {
	a := newNavModeAppHarness(t)
	// Default harness has two valid servers; switch from 0→1.
	a.serverIdx = 0

	c := client.New(client.Config{})
	a.client = c

	cmd := a.switchServerByIndex(1)

	if cmd == nil {
		t.Errorf("valid switch returned nil cmd; should produce a connect cmd")
	}
	if a.serverIdx != 1 {
		t.Errorf("serverIdx = %d after valid switch; want 1", a.serverIdx)
	}
	select {
	case <-c.Done():
		// Expected — Close ran.
	default:
		t.Errorf("valid switch did not close the current client")
	}
}
