package tui

// Tests for the server navigation ring (server-nav-ux): Ctrl+g h/l/j,
// digit-opens-wizard, Add Server as a ring slot, the server quick-switch
// picker, submit-from-ring, and the connect-failed escape hatch. Driven
// through handleNavModeKey / the PickerSelectedMsg + AddServerMsg handlers
// with a multi-server appConfig. Switching actually connects, so we assert
// the intent (target index / wizard or picker visibility / non-nil cmd)
// rather than a completed dial — mirroring switch_validate_test.go.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// threeServers is a 3-server config with distinct valid hosts for ring tests.
func threeServers() []config.ServerConfig {
	return []config.ServerConfig{
		{Name: "S1", Host: "127.0.0.1", Port: 2221},
		{Name: "S2", Host: "127.0.0.2", Port: 2222},
		{Name: "S3", Host: "127.0.0.3", Port: 2223},
	}
}

func navAltG() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}, Alt: true}
}

// --- h/l ring movement from a normal (connected) server -------------------

func TestServerRing_HLFromServers(t *testing.T) {
	t.Run("l next from middle", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('l'))
		if a.serverIdx != 2 {
			t.Fatalf("Ctrl+g l from idx 1: serverIdx = %d, want 2", a.serverIdx)
		}
	})

	t.Run("h prev from middle", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('h'))
		if a.serverIdx != 0 {
			t.Fatalf("Ctrl+g h from idx 1: serverIdx = %d, want 0", a.serverIdx)
		}
	})

	t.Run("h from first opens Add Server (ring slot)", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 0
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('h'))
		if !a.addServer.IsVisible() {
			t.Fatal("Ctrl+g h from first server should open Add Server")
		}
		if a.serverIdx != 0 {
			t.Fatalf("serverIdx should be unchanged (0), got %d", a.serverIdx)
		}
	})

	t.Run("l from last opens Add Server (ring slot)", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 2
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('l'))
		if !a.addServer.IsVisible() {
			t.Fatal("Ctrl+g l from last server should open Add Server")
		}
		if a.serverIdx != 2 {
			t.Fatalf("serverIdx should be unchanged (2), got %d", a.serverIdx)
		}
	})
}

func TestServerRing_SingleServerOpensAddServer(t *testing.T) {
	for _, key := range []rune{'h', 'l'} {
		t.Run(string(key)+" opens Add Server", func(t *testing.T) {
			a := newNavModeAppHarness(t)
			a.appConfig.Servers = a.appConfig.Servers[:1]
			a.serverIdx = 0
			a = updateNavApp(t, a, navCtrlG())
			a = updateNavApp(t, a, navRune(key))
			if !a.addServer.IsVisible() {
				t.Fatalf("Ctrl+g %c on single server should open Add Server", key)
			}
		})
	}
}

// --- ring movement FROM the Add Server slot (slot-relative first/last) -----

func TestServerRing_FromAddServerSlot(t *testing.T) {
	t.Run("l switches to first server and hides Add Server", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1 // underlying server while the wizard is the active slot
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('l'))
		if a.serverIdx != 0 {
			t.Fatalf("Ctrl+g l from Add Server: serverIdx = %d, want 0 (first)", a.serverIdx)
		}
		if a.addServer.IsVisible() {
			t.Fatal("Add Server should be hidden after switching away")
		}
	})

	t.Run("h switches to last server and hides Add Server", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('h'))
		if a.serverIdx != 2 {
			t.Fatalf("Ctrl+g h from Add Server: serverIdx = %d, want 2 (last)", a.serverIdx)
		}
		if a.addServer.IsVisible() {
			t.Fatal("Add Server should be hidden after switching away")
		}
	})

	t.Run("j hides Add Server and opens the picker", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('j'))
		if a.addServer.IsVisible() {
			t.Fatal("Ctrl+g j from Add Server should hide the wizard")
		}
		if !a.picker.IsVisible() {
			t.Fatal("Ctrl+g j from Add Server should open the server picker")
		}
	})
}

// TestServerRing_NoOpSwitchStillHidesAddServer is the Finding A guard: from
// the Add Server slot, a move targeting the server you're already on is a
// no-op switch (idx == serverIdx). It must STILL dismiss the wizard — this is
// why switchServerByIndex hides Add Server ABOVE its early-return. If the hide
// were placed next to connectFailed.Hide (below the early-return), every
// multi-server test above would still pass while these would break.
func TestServerRing_NoOpSwitchStillHidesAddServer(t *testing.T) {
	t.Run("single server l reveals current, no reconnect", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = a.appConfig.Servers[:1]
		a.serverIdx = 0
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		model, cmd := a.Update(navRune('l'))
		a = model.(App)
		if a.addServer.IsVisible() {
			t.Fatal("Ctrl+g l from Add Server (single server) must hide the wizard")
		}
		if a.serverIdx != 0 {
			t.Fatalf("serverIdx should stay 0 (no switch), got %d", a.serverIdx)
		}
		if cmd != nil {
			t.Fatal("re-selecting the current server should not return a connect cmd")
		}
	})

	t.Run("single server h reveals current, no reconnect", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = a.appConfig.Servers[:1]
		a.serverIdx = 0
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		model, cmd := a.Update(navRune('h'))
		a = model.(App)
		if a.addServer.IsVisible() {
			t.Fatal("Ctrl+g h from Add Server (single server) must hide the wizard")
		}
		if cmd != nil {
			t.Fatal("no-op switch should not reconnect")
		}
	})

	t.Run("current digit reveals current, no reconnect", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a.addServer.Show()
		a = updateNavApp(t, a, navCtrlG())
		model, cmd := a.Update(navRune('2')) // server 2 == current index 1
		a = model.(App)
		if a.addServer.IsVisible() {
			t.Fatal("Ctrl+g <current digit> from Add Server must hide the wizard")
		}
		if a.serverIdx != 1 {
			t.Fatalf("serverIdx should stay 1, got %d", a.serverIdx)
		}
		if cmd != nil {
			t.Fatal("re-selecting the current server should not reconnect")
		}
	})
}

// --- plain h/l/j are NOT server nav without the Ctrl+g prefix ---------------

func TestServerRing_PlainKeysUnchanged(t *testing.T) {
	t.Run("plain l in normal state does not switch", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a.client = nil
		a.focus = FocusInput
		a = updateNavApp(t, a, navRune('l')) // no Ctrl+g prefix
		if a.serverIdx != 1 {
			t.Fatalf("plain l should not switch servers; serverIdx = %d", a.serverIdx)
		}
	})

	t.Run("plain h inside Add Server types into the field", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.addServer.Show() // focuses the Name field
		a = updateNavApp(t, a, navRune('h'))
		if a.serverIdx != 0 {
			t.Fatalf("plain h in Add Server should not switch; serverIdx = %d", a.serverIdx)
		}
		if got := a.addServer.inputs[fieldName].Value(); got != "h" {
			t.Fatalf("plain h should type into the focused field, got %q", got)
		}
	})
}

// --- ephemeral CLI session (serverIdx == -1) uses the list edges -----------

func TestServerRing_EphemeralEdge(t *testing.T) {
	t.Run("l goes to the first configured server", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.serverIdx = -1
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('l'))
		if a.serverIdx != 0 {
			t.Fatalf("Ctrl+g l from ephemeral: serverIdx = %d, want 0", a.serverIdx)
		}
	})

	t.Run("h goes to the last configured server", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.serverIdx = -1
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('h'))
		if a.serverIdx != 1 {
			t.Fatalf("Ctrl+g h from ephemeral: serverIdx = %d, want 1 (last)", a.serverIdx)
		}
	})
}

// --- digit-opens-wizard on overflow, normal switch in range -----------------

func TestServerRing_DigitWizard(t *testing.T) {
	t.Run("digit past count opens the wizard", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = a.appConfig.Servers[:1]
		a.serverIdx = 0
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('2'))
		if !a.addServer.IsVisible() {
			t.Fatal("Ctrl+g 2 with one server should open Add Server")
		}
	})

	t.Run("digit 1 stays on server 1, no wizard", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = a.appConfig.Servers[:1]
		a.serverIdx = 0
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('1'))
		if a.addServer.IsVisible() {
			t.Fatal("Ctrl+g 1 should not open the wizard")
		}
		if a.serverIdx != 0 {
			t.Fatalf("serverIdx = %d, want 0", a.serverIdx)
		}
	})
}

// --- server quick-switch picker (openServerSwitcher + switch_server verb) ---

func TestOpenServerSwitcher(t *testing.T) {
	t.Run("builds server rows + Add row, marks current", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 1
		a.openServerSwitcher()
		if !a.picker.IsVisible() {
			t.Fatal("openServerSwitcher should show the picker")
		}
		if len(a.picker.all) != 4 {
			t.Fatalf("picker should have 3 servers + Add row = 4 items, got %d", len(a.picker.all))
		}
		if a.picker.all[3].ID != serverPickerAddID {
			t.Fatalf("last item should be the Add sentinel, got ID %q", a.picker.all[3].ID)
		}
		if !strings.Contains(a.picker.all[1].Secondary, "(current)") {
			t.Fatalf("current server (idx 1) should be marked; secondary = %q", a.picker.all[1].Secondary)
		}
		if a.picker.req.Verb != "switch_server" {
			t.Fatalf("picker verb = %q, want switch_server", a.picker.req.Verb)
		}
	})

	t.Run("no configured servers falls back to the wizard", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = nil
		a.openServerSwitcher()
		if a.picker.IsVisible() {
			t.Fatal("no servers: the picker should not show")
		}
		if !a.addServer.IsVisible() {
			t.Fatal("no servers: should open the wizard instead")
		}
	})
}

func TestSwitchServerVerbRouting(t *testing.T) {
	t.Run("index switches and returns a non-nil connect cmd", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.serverIdx = 0
		model, cmd := a.Update(PickerSelectedMsg{
			Request:    PickerRequest{Verb: "switch_server"},
			SelectedID: "1",
		})
		a = model.(App)
		if a.serverIdx != 1 {
			t.Fatalf("switch_server SelectedID=1: serverIdx = %d, want 1", a.serverIdx)
		}
		if cmd == nil {
			t.Fatal("switch_server to a different index must return a non-nil connect cmd (load-bearing)")
		}
	})

	t.Run("add sentinel opens the wizard with a nil cmd", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		model, cmd := a.Update(PickerSelectedMsg{
			Request:    PickerRequest{Verb: "switch_server"},
			SelectedID: serverPickerAddID,
		})
		a = model.(App)
		if !a.addServer.IsVisible() {
			t.Fatal("the add sentinel should open the wizard")
		}
		if cmd != nil {
			t.Fatal("the add sentinel should not return a connect cmd")
		}
	})
}

// --- Add Server submit from the ring (AddServerMsg) -------------------------

func TestAddServerSubmitSwitches(t *testing.T) {
	t.Run("new server appends and switches to it", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		before := len(a.appConfig.Servers)
		model, cmd := a.Update(AddServerMsg{Name: "New", Host: "127.0.0.9", Port: 2299})
		a = model.(App)
		if len(a.appConfig.Servers) != before+1 {
			t.Fatalf("server count = %d, want %d", len(a.appConfig.Servers), before+1)
		}
		if a.serverIdx != before {
			t.Fatalf("should switch to the new server idx %d, got %d", before, a.serverIdx)
		}
		if cmd == nil {
			t.Fatal("successful add should return the connect cmd (load-bearing)")
		}
	})

	t.Run("duplicate add stays put with a status error", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.serverIdx = 0
		// harness server 0 is 127.0.0.1:2222 — re-adding it is a duplicate.
		model, cmd := a.Update(AddServerMsg{Name: "Dup", Host: "127.0.0.1", Port: 2222})
		a = model.(App)
		if a.serverIdx != 0 {
			t.Fatalf("duplicate add should not switch; serverIdx = %d", a.serverIdx)
		}
		if cmd != nil {
			t.Fatal("duplicate add should not connect")
		}
		if a.statusBar.errorMsg == "" {
			t.Fatal("duplicate add should surface a status error")
		}
	})

	t.Run("nil config initializes and adds the first server", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig = nil
		a.serverIdx = -1
		model, _ := a.Update(AddServerMsg{Name: "First", Host: "127.0.0.5", Port: 2255})
		a = model.(App)
		if a.appConfig == nil {
			t.Fatal("nil-config submit should initialize the config, not no-op")
		}
		if len(a.appConfig.Servers) != 1 {
			t.Fatalf("server count = %d, want 1", len(a.appConfig.Servers))
		}
	})
}

// --- Add Server [Generate new key] row (steps 6/8) --------------------------

func TestAddServerGenerateRow(t *testing.T) {
	t.Run("Tab from key field reaches the gen row", func(t *testing.T) {
		a := NewAddServer(nil)
		a.Show()
		a.focused = fieldKey
		a, _ = a.Update(tea.KeyMsg{Type: tea.KeyTab})
		if a.focused != a.focusGenRow() {
			t.Fatalf("Tab from key field should focus the gen row (%d), got %d", a.focusGenRow(), a.focused)
		}
	})

	t.Run("Down from key field reaches the gen row", func(t *testing.T) {
		a := NewAddServer(nil)
		a.Show()
		a.focused = fieldKey
		a, _ = a.Update(tea.KeyMsg{Type: tea.KeyDown})
		if a.focused != a.focusGenRow() {
			t.Fatalf("Down from key field should focus the gen row (%d), got %d", a.focusGenRow(), a.focused)
		}
	})

	t.Run("Enter on gen row opens generate (host present)", func(t *testing.T) {
		a := NewAddServer(nil)
		a.Show()
		a.inputs[fieldHost].SetValue("chat.example.com")
		a.focused = a.focusGenRow()
		a, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if a.mode != addServerGenerate {
			t.Fatalf("Enter on gen row should open generate, mode=%d", a.mode)
		}
	})

	t.Run("Enter on gen row without host errors and stays in form", func(t *testing.T) {
		a := NewAddServer(nil)
		a.Show()
		a.focused = a.focusGenRow()
		a, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if a.mode == addServerGenerate {
			t.Fatal("Enter on gen row without a host must not open generate")
		}
		if a.formErr == "" {
			t.Fatal("expected a form error prompting for a hostname")
		}
		if a.focused != fieldHost {
			t.Fatalf("focus should move to the host field, got %d", a.focused)
		}
	})

	t.Run("mouse click on the gen row opens generate (host present)", func(t *testing.T) {
		a := NewAddServer(nil)
		a.Show()
		a.inputs[fieldHost].SetValue("chat.example.com")
		a, _ = a.HandleMouse(tea.MouseMsg{
			Action: tea.MouseActionRelease,
			Button: tea.MouseButtonLeft,
			Y:      4 + len(a.inputs)*2, // the gen-row line
		})
		if a.mode != addServerGenerate {
			t.Fatalf("click on the gen row should open generate, mode=%d", a.mode)
		}
	})
}

// --- connect-failed escape hatch follows the ring ---------------------------

func TestConnectFailedNavHatch(t *testing.T) {
	t.Run("l follows the server ring and hides the failure screen", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 0
		a.connectFailed.Show("connection refused", "", "")
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('l'))
		if a.serverIdx != 1 {
			t.Fatalf("Ctrl+g l from connect-failed: serverIdx = %d, want 1", a.serverIdx)
		}
		if a.connectFailed.IsVisible() {
			t.Fatal("switching away should hide the connect-failed overlay")
		}
	})

	t.Run("j opens the picker and hides the failure screen", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = threeServers()
		a.serverIdx = 0
		a.connectFailed.Show("connection refused", "", "")
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('j'))
		if !a.picker.IsVisible() {
			t.Fatal("Ctrl+g j should open the server picker")
		}
		if a.connectFailed.IsVisible() {
			t.Fatal("opening the picker should hide the connect-failed overlay")
		}
	})

	t.Run("out-of-range digit opens the wizard and hides the failure screen", func(t *testing.T) {
		a := newNavModeAppHarness(t)
		a.appConfig.Servers = a.appConfig.Servers[:1]
		a.serverIdx = 0
		a.connectFailed.Show("connection refused", "", "")
		a = updateNavApp(t, a, navCtrlG())
		a = updateNavApp(t, a, navRune('2'))
		if !a.addServer.IsVisible() {
			t.Fatal("out-of-range digit should open the wizard")
		}
		if a.connectFailed.IsVisible() {
			t.Fatal("opening the wizard should hide the connect-failed overlay")
		}
	})
}

// TestAddServerAltGGeneratesNotCtrlG locks that Alt+g (not Ctrl+g) is the
// generate shortcut inside the form, complementing the navmode_test.go
// precedence test that Ctrl+g enters server nav.
func TestAddServerAltGGeneratesNotCtrlG(t *testing.T) {
	a := NewAddServer(nil)
	a.Show()
	a.inputs[fieldHost].SetValue("chat.example.com")
	a, _ = a.Update(navAltG())
	if a.mode != addServerGenerate {
		t.Fatalf("Alt+g should open generate mode, mode=%d", a.mode)
	}
}
