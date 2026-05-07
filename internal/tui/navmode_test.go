package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
)

func newNavModeAppHarness(t *testing.T) App {
	t.Helper()

	a, _ := newEditAppHarness(t)
	a.quickSwitch = NewQuickSwitch()
	a.newConv = NewNewConv()
	a.memberPanel = NewMemberPanel()
	a.search = NewSearch()
	a.settings = NewSettings()
	a.addServer = NewAddServer()
	a.statusBar = NewStatusBar()
	a.navModeTimeout = 2 * time.Second
	a.configDir = t.TempDir()
	a.appConfig = &config.Config{
		Device: config.DeviceConfig{ID: "dev_nav_mode"},
		Servers: []config.ServerConfig{
			{Name: "Home", Host: "127.0.0.1", Port: 2222, Key: "~/.ssh/id_ed25519"},
			{Name: "Work", Host: "127.0.0.2", Port: 2223, Key: "~/.ssh/id_ed25519"},
		},
	}
	a.serverIdx = 0
	a.cfg = client.Config{
		Host:     "127.0.0.1",
		Port:     2222,
		KeyPath:  "~/.ssh/id_ed25519",
		DeviceID: "dev_nav_mode",
		DataDir:  filepath.Join(a.configDir, "127.0.0.1"),
	}

	return a
}

func navCtrlG() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyCtrlG}
}

func navRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func updateNavApp(t *testing.T, a App, msg tea.Msg) App {
	t.Helper()
	model, _ := a.Update(msg)
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("expected App model, got %T", model)
	}
	return updated
}

func TestNavMode_Enter(t *testing.T) {
	a := newNavModeAppHarness(t)
	model, cmd := a.Update(navCtrlG())
	updated := model.(App)

	if !updated.navMode {
		t.Fatal("Ctrl+g should enter nav mode")
	}
	if cmd == nil {
		t.Fatal("Ctrl+g should schedule timeout tick when timeout > 0")
	}
	if !strings.Contains(updated.statusBar.View(80), "navigation mode") {
		t.Fatal("status bar should show navigation mode indicator")
	}
}

func TestNavMode_EnterFromAnyFocus(t *testing.T) {
	cases := []struct {
		name  string
		focus Focus
	}{
		{name: "input", focus: FocusInput},
		{name: "sidebar", focus: FocusSidebar},
		{name: "messages", focus: FocusMessages},
		{name: "members", focus: FocusMembers},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newNavModeAppHarness(t)
			a.focus = tc.focus
			updated := updateNavApp(t, a, navCtrlG())
			if !updated.navMode {
				t.Fatalf("Ctrl+g should enter nav mode from focus=%v", tc.focus)
			}
		})
	}
}

func TestNavMode_FromInputFocusDoesNotTypeG(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.focus = FocusInput

	updated := updateNavApp(t, a, navCtrlG())
	if got := updated.input.Value(); got != "" {
		t.Fatalf("input should remain unchanged, got %q", got)
	}
}

func TestNavMode_RecognizedKeysFireAndExit(t *testing.T) {
	t.Run("k quick switch", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('k'))
		if !a.quickSwitch.IsVisible() {
			t.Fatal("Ctrl+g k should open quick switch")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("n new conversation", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('n'))
		if !a.newConv.IsVisible() {
			t.Fatal("Ctrl+g n should open new conversation")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("m toggle members", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('m'))
		if !a.memberPanel.IsVisible() {
			t.Fatal("Ctrl+g m should toggle member panel on")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("i open info panel", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.messages.SetContext("", "", "dm_test")
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('i'))
		if !a.infoPanel.IsVisible() {
			t.Fatal("Ctrl+g i should open info panel in active context")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("s open settings", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('s'))
		if !a.settings.IsVisible() {
			t.Fatal("Ctrl+g s should open settings")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("slash open search", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('/'))
		if !a.search.IsVisible() {
			t.Fatal("Ctrl+g / should open search")
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})

	t.Run("2 switch server tab", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('2'))
		if a.serverIdx != 1 {
			t.Fatalf("serverIdx = %d, want 1", a.serverIdx)
		}
		if a.cfg.Host != "127.0.0.2" {
			t.Fatalf("cfg.Host = %q, want 127.0.0.2", a.cfg.Host)
		}
		if a.navMode {
			t.Fatal("nav mode should exit after recognized key")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar should clear indicator after recognized-key exit")
		}
	})
}

func TestNavMode_NumberOutOfRangeExitsWithoutSwitch(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.appConfig.Servers = a.appConfig.Servers[:1]
	a.serverIdx = 0

	a = updateNavApp(t, a, navCtrlG())
	a = updateNavApp(t, a, navRune('3'))

	if a.serverIdx != 0 {
		t.Fatalf("serverIdx = %d, want 0", a.serverIdx)
	}
	if a.navMode {
		t.Fatal("nav mode should exit on out-of-range number")
	}
}

// TestNavMode_NumberKeysAllSwitch verifies every digit 1..9 routes to
// the corresponding server tab, not just the lone `2` case in the
// shared RecognizedKeys table. The default harness only has 2
// servers; this test builds a 9-server config inline so each digit
// has a target to land on. Closes a gap from the doc's D13-19 spec
// which called for full-range coverage.
func TestNavMode_NumberKeysAllSwitch(t *testing.T) {
	servers := make([]config.ServerConfig, 9)
	for i := 0; i < 9; i++ {
		servers[i] = config.ServerConfig{
			Name: "Server" + string(rune('0'+i+1)),
			// IPv4 octets max at 255 — well above 9. Distinct hosts
			// per server so the post-switch a.cfg.Host check would
			// have signal if we wanted to extend the assertion.
			Host: "127.0.0." + string(rune('0'+i+1)),
			Port: 2222 + i,
			Key:  "~/.ssh/id_ed25519",
		}
	}

	for digit := 1; digit <= 9; digit++ {
		t.Run("digit "+string(rune('0'+digit)), func(t *testing.T) {
			a := newNavModeAppHarness(t)
			a.appConfig.Servers = servers
			a.serverIdx = 0

			a = updateNavApp(t, a, navCtrlG())
			a = updateNavApp(t, a, navRune(rune('0'+digit)))

			if a.serverIdx != digit-1 {
				t.Errorf("Ctrl+g %d: serverIdx = %d, want %d", digit, a.serverIdx, digit-1)
			}
			if a.navMode {
				t.Errorf("Ctrl+g %d: nav mode should exit after switch", digit)
			}
		})
	}
}

func TestNavMode_UnrecognizedKeyExitsAndFallsThroughInput(t *testing.T) {
	a := newNavModeAppHarness(t)
	// Keep this test local-only; no outbound typing side effects.
	a.client = nil
	a.focus = FocusInput

	a = updateNavApp(t, a, navCtrlG())
	a = updateNavApp(t, a, navRune('z'))

	if a.navMode {
		t.Fatal("unrecognized key should exit nav mode")
	}
	if got := a.input.Value(); got != "z" {
		t.Fatalf("unrecognized key should fall through to input, got %q", got)
	}
	if strings.Contains(a.statusBar.View(80), "navigation mode") {
		t.Fatal("status bar should clear indicator after unrecognized-key exit")
	}
}

func TestNavMode_CancelKeys(t *testing.T) {
	t.Run("g cancels", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('g'))
		if a.navMode {
			t.Fatal("Ctrl+g g should cancel nav mode")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar navigation indicator should clear after cancel")
		}
	})

	t.Run("esc cancels", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, tea.KeyMsg{Type: tea.KeyEsc})
		if a.navMode {
			t.Fatal("Esc should cancel nav mode")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar navigation indicator should clear after Esc cancel")
		}
	})
}

func TestNavMode_TimeoutHandling(t *testing.T) {
	t.Run("matching generation exits", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a = updateNavApp(t, a, navCtrlG())
		gen := a.navModeTickGen
		a = updateNavApp(t, a, navModeTimeoutMsg{Gen: gen})
		if a.navMode {
			t.Fatal("timeout message should exit nav mode")
		}
		if strings.Contains(a.statusBar.View(80), "navigation mode") {
			t.Fatal("status bar navigation indicator should clear on timeout")
		}
	})

	t.Run("stale generation ignored", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		_ = a.enterNavMode()
		stale := a.navModeTickGen
		_ = a.enterNavMode()
		if a.navModeTickGen == stale {
			t.Fatal("precondition failed: generation should increment")
		}
		a = updateNavApp(t, a, navModeTimeoutMsg{Gen: stale})
		if !a.navMode {
			t.Fatal("stale timeout should not exit nav mode")
		}
	})

	t.Run("zero timeout disables auto tick", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.navModeTimeout = 0
		model, cmd := a.Update(navCtrlG())
		updated := model.(App)
		if !updated.navMode {
			t.Fatal("Ctrl+g should still enter nav mode when timeout is zero")
		}
		if cmd != nil {
			t.Fatal("timeout=0 should not schedule a timeout tick")
		}
	})
}

func TestNavMode_ModalPrecedence(t *testing.T) {
	t.Run("add server Ctrl+g remains modal-local", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.addServer.Show()
		a.addServer.inputs[1].SetValue("chat.example.com")

		a = updateNavApp(t, a, navCtrlG())
		if a.navMode {
			t.Fatal("global nav mode should stay off while add-server modal is open")
		}
		if a.addServer.mode != addServerGenerate {
			t.Fatalf("add-server modal should handle Ctrl+g locally, mode=%d", a.addServer.mode)
		}
	})

	t.Run("representative modals suppress global nav mode", func(t *testing.T) {
		cases := []struct {
			name string
			show func(*App)
		}{
			{
				name: "help",
				show: func(a *App) { a.help.Toggle() },
			},
			{
				name: "settings",
				show: func(a *App) { a.settings.Show(a.appConfig, a.configDir, "alice", a.serverIdx) },
			},
			{
				name: "search",
				show: func(a *App) { a.search.Show() },
			},
			{
				name: "info",
				show: func(a *App) {
					a.infoPanel.ShowDM("dm_test", a.client, a.sidebar.online, a.sidebar.status)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				a := newNavModeAppHarness(t)
				tc.show(&a)
				a = updateNavApp(t, a, navCtrlG())
				if a.navMode {
					t.Fatalf("modal %s should suppress global nav mode", tc.name)
				}
			})
		}
	})
}

// TestNavMode_LegacyDirectBindingsRemoved asserts the migrated
// Ctrl+<key> direct handlers are gone — pressing the bare ctrl
// chord must NOT trigger the action that used to be wired to it.
//
// Coverage limitation: bubbletea v1.3.10 only represents `ctrl+`
// chords as named KeyType values for letters A-Z (KeyCtrlA..KeyCtrlZ)
// plus a fixed set of punctuation aliases (KeyCtrlAt, KeyCtrlOpenBracket,
// etc.). There is NO KeyMsg representation for `ctrl+,` or `ctrl+1`
// through `ctrl+9` — Key.String() requires either a known KeyType or
// the KeyRunes path, and the latter only renders alt+ as a modifier
// prefix. So the legacy `case "ctrl+,"` and `case "ctrl+1"`-`case
// "ctrl+9"` handlers in app.go could never have matched any input
// the bubbletea reader actually produces (the doc explicitly notes
// xterm/Linux console produce nothing for `ctrl+1` either). They
// were dead code — removing them is purely cleanup.
//
// What we CAN test directly:
//   - KeyCtrlK / KeyCtrlN / KeyCtrlF — distinct KeyTypes, real input
//     path, asserts the switch case is gone.
//
// What's untestable here:
//   - Ctrl+, → settings (no KeyMsg representation).
//   - Ctrl+1-9 → server switch (no KeyMsg representation in this
//     bubbletea version).
//
// Functional coverage for those paths comes via the nav-mode tests:
// TestNavMode_NumberKeysAllSwitch proves Ctrl+g 1-9 IS the only path
// that switches servers (because no other path is testable end-to-
// end), and TestNavMode_RecognizedKeysFireAndExit / "s open settings"
// proves Ctrl+g s is the only path to settings. If a bubbletea
// upgrade later starts representing those chords, extend this table.
func TestNavMode_LegacyDirectBindingsRemoved(t *testing.T) {
	a := newNavModeAppHarness(t)

	a = updateNavApp(t, a, tea.KeyMsg{Type: tea.KeyCtrlK})
	if a.quickSwitch.IsVisible() {
		t.Fatal("legacy Ctrl+k should not open quick switch directly")
	}

	a = updateNavApp(t, a, tea.KeyMsg{Type: tea.KeyCtrlN})
	if a.newConv.IsVisible() {
		t.Fatal("legacy Ctrl+n should not open new conversation directly")
	}

	a = updateNavApp(t, a, tea.KeyMsg{Type: tea.KeyCtrlF})
	if a.search.IsVisible() {
		t.Fatal("legacy Ctrl+f should not open search directly")
	}
}
