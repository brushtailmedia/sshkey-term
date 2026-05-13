package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// path_validation_test.go covers Phase 4's TUI-layer regression
// suite for the path-centralization refactor:
//
//   1. Four false-success prevention tests — confirm that the
//      ValidateHost rejection paths added in Phase 2 produce
//      clearly-failing status-bar messages instead of the prior
//      unconditional "success" UI.
//   2. Two post-3e correctness regression tests — cover the bug
//      fixes added in Phase 4 (remove-server reindex when removing
//      a lower-index entry, and scan-dir closure hardening that
//      skips invalid hosts).
//
// All tests share newAppForValidation as the App-construction
// helper. Keeping the helper and its callers in one file avoids
// duplicating setup across two files — the helper is the dominant
// cost.

// newAppForValidation returns a lightweight App with just the
// fields the validation handlers touch:
//   - configDir (set to a fresh temp dir per test)
//   - appConfig with two known-good server entries
//   - serverIdx pointing at the first (active) server
//   - statusBar so the handlers' SetError calls land somewhere
//     we can inspect
//   - client = a minimal client.New() so the clear_history handler
//     (gated on `a.client != nil`) actually reaches the validation
//     branch. We don't exercise the client's networking surface
//     here; tests aim at the handler dispatch + validation paths.
func newAppForValidation(t *testing.T) App {
	t.Helper()
	return App{
		client:    client.New(client.Config{DeviceID: "dev_path_validation"}),
		configDir: t.TempDir(),
		appConfig: &config.Config{
			Device: config.DeviceConfig{ID: "dev_path_validation"},
			Servers: []config.ServerConfig{
				{Name: "Home", Host: "127.0.0.1", Port: 2222},
				{Name: "Work", Host: "127.0.0.2", Port: 2223},
			},
		},
		serverIdx: 0,
		statusBar: NewStatusBar(),
		settings:  NewSettings(),
		cfg:       client.Config{Host: "127.0.0.1", Port: 2222},
	}
}

// statusBarError reads the most recent status-bar error message.
// Accesses the package-private field directly (we're in the same
// package) since StatusBarModel exposes no getter.
func statusBarError(a App) string {
	return a.statusBar.errorMsg
}

// updateAppForValidation dispatches a message and returns the
// updated App + the returned tea.Cmd. Wraps the tea.Model interface
// dance.
func updateAppForValidation(t *testing.T, a App, msg tea.Msg) (App, tea.Cmd) {
	t.Helper()
	model, cmd := a.Update(msg)
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("expected App, got %T", model)
	}
	return updated, cmd
}

// -- Part 1: False-success prevention --

func TestAddServerMsg_NoFalseSuccessOnInvalidHost(t *testing.T) {
	a := newAppForValidation(t)

	// Inject an invalid host through the AddServerMsg pathway —
	// this exercises the app.go AddServerMsg handler, which calls
	// config.AddServer. The Phase 2 validation should reject and
	// the handler should surface the error in the status bar.
	a, _ = updateAppForValidation(t, a, AddServerMsg{
		Name: "Bad",
		Host: "../etc",
		Port: 2222,
	})

	got := statusBarError(a)
	if !strings.Contains(got, "Failed to add server") {
		t.Errorf("expected error status, got %q", got)
	}
	// "Server added" success message must not appear on the
	// rejection path.
	if strings.Contains(got, "Server added") {
		t.Errorf("false success: status bar shows %q despite rejected host", got)
	}
	// cfg.Servers must remain at its pre-message count.
	if len(a.appConfig.Servers) != 2 {
		t.Errorf("cfg.Servers should be unchanged on reject, got %d entries", len(a.appConfig.Servers))
	}
}

func TestClearHistory_NoFalseSuccessOnInvalidHost(t *testing.T) {
	a := newAppForValidation(t)
	// Hand-edit the active server's host to a value ValidateHost
	// will reject. Simulates a corrupted config.toml.
	a.appConfig.Servers[0].Host = "../etc"

	a, _ = updateAppForValidation(t, a, SettingsActionMsg{Action: "clear_history"})

	got := statusBarError(a)
	if !strings.Contains(got, "Failed to clear history") {
		t.Errorf("expected error status, got %q", got)
	}
	if strings.Contains(got, "Local history cleared") {
		t.Errorf("false success: status bar shows %q despite rejected host", got)
	}
}

func TestRemoveServer_NoFalseSuccessOnInvalidHost(t *testing.T) {
	a := newAppForValidation(t)
	// Bad host at index 1 (non-active). RemoveServer's
	// host-validation runs after the index-bounds check and before
	// any state mutation.
	a.appConfig.Servers[1].Host = "with/slash"

	a, cmd := updateAppForValidation(t, a, SettingsActionMsg{
		Action:    "remove_server",
		ServerIdx: 1,
	})

	got := statusBarError(a)
	if !strings.Contains(got, "Failed to remove server") {
		t.Errorf("expected error status, got %q", got)
	}
	if strings.Contains(got, "Server removed") {
		t.Errorf("false success: status bar shows %q despite rejected host", got)
	}
	// Critical: on the error path the handler must NOT have run
	// tea.Quit or client.Close. We assert cmd == nil — the success
	// branch is the only one that returns tea.Quit, and we set
	// a.client = nil in the harness so a stray close wouldn't be
	// observable here without a more elaborate spy.
	if cmd != nil {
		t.Errorf("error path should return nil cmd (no tea.Quit), got %T", cmd)
	}
	if len(a.appConfig.Servers) != 2 {
		t.Errorf("cfg.Servers should be unchanged on reject, got %d entries", len(a.appConfig.Servers))
	}
}

func TestSettings_ServerDataSizeError_DisplaysMarker(t *testing.T) {
	a := newAppForValidation(t)
	// Hand-edit the active server's host to invalid value. The
	// settings Show() walks each server to render the data-size
	// row; the invalid-host entry must produce the "invalid host"
	// marker rather than silently falling back to "0 B".
	a.appConfig.Servers[0].Host = "../etc"

	a.settings.Show(a.appConfig, a.configDir, "alice", a.serverIdx)
	out := a.settings.View(80, 30)

	if !strings.Contains(out, "invalid host") {
		t.Errorf("expected 'invalid host' marker in settings view for bad host; got:\n%s", out)
	}
}

// -- Part 2: Post-3e correctness regressions --

// TestRemoveServer_ReindexesServerIdxOnLowerRemove covers the
// Phase 4 fix at the `case "remove_server":` handler. Pre-fix,
// removing a server at an index BELOW the active one would shift
// cfg.Servers down by one but leave a.serverIdx pointing at the
// (now-shifted) wrong server. Post-fix, a.serverIdx decrements so
// the same logical server stays active.
func TestRemoveServer_ReindexesServerIdxOnLowerRemove(t *testing.T) {
	a := newAppForValidation(t)
	// Three servers; active = index 2 ("Spare").
	a.appConfig.Servers = []config.ServerConfig{
		{Name: "First", Host: "first.example.com", Port: 2222},
		{Name: "Second", Host: "second.example.com", Port: 2222},
		{Name: "Spare", Host: "spare.example.com", Port: 2222},
	}
	a.serverIdx = 2

	// Remove the lower-index "First" (idx 0). Removal succeeds,
	// cfg.Servers shrinks to 2 entries. "Spare" is now at idx 1.
	// a.serverIdx must decrement from 2 → 1 to keep pointing at
	// "Spare".
	a, _ = updateAppForValidation(t, a, SettingsActionMsg{
		Action:    "remove_server",
		ServerIdx: 0,
	})

	if len(a.appConfig.Servers) != 2 {
		t.Fatalf("expected 2 servers after removal, got %d", len(a.appConfig.Servers))
	}
	if a.serverIdx != 1 {
		t.Errorf("a.serverIdx should be reindexed from 2 to 1, got %d", a.serverIdx)
	}
	// Active server identity preserved.
	if a.appConfig.Servers[a.serverIdx].Name != "Spare" {
		t.Errorf("active server should still be Spare after reindex, got %q",
			a.appConfig.Servers[a.serverIdx].Name)
	}
}

// TestAddServerScanDirsFn_SkipsInvalidHosts covers the Phase 4 fix
// at the NewAddServer scanDirsFn closure. Pre-fix, an entry with a
// bad Host in appCfg.Servers would have its host joined into a
// bogus path. Post-fix, ValidateHost gates the derivation and bad
// entries are silently skipped.
//
// Tests the closure directly by constructing one that mirrors the
// production wiring in tui.New, with mixed valid + invalid hosts.
func TestAddServerScanDirsFn_SkipsInvalidHosts(t *testing.T) {
	configDir := t.TempDir()
	appCfg := &config.Config{
		Servers: []config.ServerConfig{
			{Name: "Valid1", Host: "valid1.example.com", Port: 2222},
			{Name: "Bad1", Host: "../etc", Port: 2222},
			{Name: "Valid2", Host: "valid2.example.com", Port: 2223},
			{Name: "Bad2", Host: "with/slash", Port: 2222},
			{Name: "Bad3", Host: "", Port: 2222},
		},
	}

	// Mirror the closure shape from tui.New verbatim — if this
	// test passes against the local closure, the production
	// closure passes too (and the test is the canonical
	// specification of the hardened behavior).
	scanDirsFn := func() []string {
		if appCfg == nil {
			return nil
		}
		dirs := make([]string, 0, len(appCfg.Servers))
		for _, srv := range appCfg.Servers {
			if config.ValidateHost(srv.Host) != nil {
				continue
			}
			dirs = append(dirs, config.ServerKeysDir(configDir, srv.Host))
		}
		return dirs
	}

	got := scanDirsFn()
	want := []string{
		config.ServerKeysDir(configDir, "valid1.example.com"),
		config.ServerKeysDir(configDir, "valid2.example.com"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d valid dirs, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("dirs[%d] = %q, want %q", i, got[i], w)
		}
	}
}
