package tui

// The one App builder for tests (test-harness-consolidation.md): a COMPLETE
// App built the way production builds it — via New, so every sub-model has
// its constructor and a model added to New tomorrow is in every test App
// automatically — narrowed by options. Presets open each overlay; the
// coverage guard below requires a preset (or a reasoned allow-list entry)
// for every App field that can be visible, so the opener table the quit,
// focus and q designs share cannot drift from the real set of overlays.
//
// Server frames for the App come from App.serverMsg, which stamps the live
// connection generation the way the read loop does; a hand-built ServerMsg
// has gen 0 and Update drops it as stale (testapp_ratchet_test.go keeps
// that rule).

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// testAppSpec is what newTestApp hands to New — its four parameters — plus
// the steps that run on the built App. Options edit it.
type testAppSpec struct {
	clientCfg client.Config
	appCfg    *config.Config
	configDir string
	serverIdx int
	after     []func(t *testing.T, a *App)
}

type testAppOption func(t *testing.T, s *testAppSpec)

// then queues a step to run on the built App, in option order.
func then(f func(t *testing.T, a *App)) testAppOption {
	return func(t *testing.T, s *testAppSpec) { s.after = append(s.after, f) }
}

// newTestApp returns a fully constructed App in a quiet, usable state: every
// sub-model built by New, a local-only client marked connected as usr_self,
// ConnStateOnline, focus on the input, 100×30, no conversation selected.
func newTestApp(t *testing.T, opts ...testAppOption) App {
	t.Helper()
	spec := &testAppSpec{
		clientCfg: client.Config{DeviceID: "dev_test"},
		appCfg:    &config.Config{Device: config.DeviceConfig{ID: "dev_test"}},
		configDir: t.TempDir(),
	}
	for _, o := range opts {
		o(t, spec)
	}
	a := New(spec.clientCfg, spec.appCfg, spec.configDir, spec.serverIdx)
	// New does not dial (Init does); the client arrives with
	// connectedWithClient in production. Tests get a local-only one.
	c := client.New(client.Config{DeviceID: spec.clientCfg.DeviceID})
	client.SetUserIDForTesting(c, "usr_self")
	client.SetConnectedForTesting(c, true)
	a.client = c
	a.width, a.height = 100, 30
	a.setConnState(ConnStateOnline)
	a.focus = FocusInput
	for _, f := range spec.after {
		f(t, &a)
	}
	return a
}

// serverMsg builds a frame for the App stamped with the live connection
// generation, as the read loop stamps them, so Update's stale-drop keeps
// it. Only gen_token_test.go builds ServerMsg by hand — to test the stamp.
func (a *App) serverMsg(typ string, raw json.RawMessage) ServerMsg {
	return ServerMsg{Type: typ, Raw: raw, gen: a.connGen}
}

// withConfig passes New's own four parameters: the client config, the app
// config (servers, device), the config dir and the selected server.
func withConfig(cfg client.Config, appCfg *config.Config, configDir string, serverIdx int) testAppOption {
	return func(t *testing.T, s *testAppSpec) {
		s.clientCfg, s.appCfg, s.configDir, s.serverIdx = cfg, appCfg, configDir, serverIdx
	}
}

// withStore opens a real (unencrypted) temp store on the client, closed by
// t.Cleanup; read it back with a.client.Store(). Opt-in: it costs tens of
// milliseconds and most tests need no database.
func withStore() testAppOption {
	return then(func(t *testing.T, a *App) {
		st, err := store.OpenUnencrypted(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		client.SetStoreForTesting(a.client, st)
	})
}

// withOffline models a cold offline start: client not connected,
// ConnStateOffline.
func withOffline() testAppOption {
	return then(func(t *testing.T, a *App) {
		client.SetConnectedForTesting(a.client, false)
		a.setConnState(ConnStateOffline)
	})
}

// withConnecting models the dial in progress.
func withConnecting() testAppOption {
	return then(func(t *testing.T, a *App) {
		client.SetConnectedForTesting(a.client, false)
		a.setConnState(ConnStateConnecting)
	})
}

// withNoClient models the pre-session screens (nothing dialed yet).
func withNoClient() testAppOption {
	return then(func(t *testing.T, a *App) { a.client = nil })
}

func withSize(w, h int) testAppOption {
	return then(func(t *testing.T, a *App) { a.width, a.height = w, h })
}

func withFocus(f Focus) testAppOption {
	return then(func(t *testing.T, a *App) { a.focus = f })
}

// withContext selects a conversation in the pane.
func withContext(room, group, dm string) testAppOption {
	return then(func(t *testing.T, a *App) { selectContext(a, room, group, dm) })
}

// withMessages loads messages into the pane with the cursor on the first.
func withMessages(msgs []DisplayMessage) testAppOption {
	return then(func(t *testing.T, a *App) { loadPane(a, msgs) })
}

// withSelfUser sets the local user's ID everywhere the connected handler
// sets it.
func withSelfUser(id string) testAppOption {
	return then(func(t *testing.T, a *App) {
		client.SetUserIDForTesting(a.client, id)
		a.sidebar.SetSelfUserID(id)
		a.messages.SetSelfUserID(id)
		a.threadPanel.SetSelfUserID(id)
		a.pinnedBar.SetSelfUserID(id)
	})
}

// selectContext selects a conversation in the pane and syncs the pane's
// reachability from the connection state. A room is also listed in the
// sidebar with the sidebar's selection on it: a live session keeps the
// two in step, and a key routed to the sidebar compares them — a sidebar
// that lists nothing would read as a switch to no conversation.
func selectContext(a *App, room, group, dm string) {
	if room != "" {
		idx := -1
		for i, r := range a.sidebar.rooms {
			if r == room {
				idx = i
				break
			}
		}
		if idx < 0 {
			a.sidebar.SetRooms(append(a.sidebar.rooms, room))
			idx = len(a.sidebar.rooms) - 1
		}
		a.sidebar.cursor = idx
		a.sidebar.updateSelection()
	}
	a.messages.SetContext(room, group, dm)
	a.syncPaneReachability()
}

// loadPane puts msgs in the pane and renders once so the row map and
// viewport are live, with the cursor on the first message.
func loadPane(a *App, msgs []DisplayMessage) {
	a.messages.messages = msgs
	_ = a.messages.View(80, 16, true)
	a.messages.cursor = 0
}

// overlayOpener puts a default App into one overlay state. field is the
// App field's name (the coverage guard matches on it); visible reports the
// overlay's own IsVisible, since reflection cannot call methods on
// unexported fields.
type overlayOpener struct {
	field   string
	open    func(t *testing.T, a *App)
	visible func(a *App) bool
}

var overlayOpeners = []overlayOpener{
	{"threadPanel", func(t *testing.T, a *App) {
		selectContext(a, "general", "", "")
		loadPane(a, nestedMessages())
		a.threadPanel.Show("top", a.messages.messages)
	}, func(a *App) bool { return a.threadPanel.IsVisible() }},
	{"emojiPicker", func(t *testing.T, a *App) {
		a.emojiPicker.Show(DisplayMessage{ID: "m1"}, "usr_self")
	}, func(a *App) bool { return a.emojiPicker.IsVisible() }},
	{"contextMenu", func(t *testing.T, a *App) {
		a.contextMenu.Show(DisplayMessage{ID: "m1", Body: "hi"}, 5, 5, ContextMenuOptions{})
	}, func(a *App) bool { return a.contextMenu.IsVisible() }},
	{"picker", func(t *testing.T, a *App) {
		a.picker.Show(PickerRequest{Verb: "open_link", ShowFilter: true}, []PickerItem{{ID: "a", Primary: "a"}})
	}, func(a *App) bool { return a.picker.IsVisible() }},
	{"quitConfirm", func(t *testing.T, a *App) {
		a.quitConfirm.ShowWithPending("server", 0)
	}, func(a *App) bool { return a.quitConfirm.IsVisible() }},
	{"help", func(t *testing.T, a *App) { a.help.Toggle() }, func(a *App) bool { return a.help.IsVisible() }},
	{"settings", func(t *testing.T, a *App) { a.openSettingsPanel() }, func(a *App) bool { return a.settings.IsVisible() }},
	{"search", func(t *testing.T, a *App) { a.search.Show() }, func(a *App) bool { return a.search.IsVisible() }},
	{"quickSwitch", func(t *testing.T, a *App) { a.openQuickSwitch() }, func(a *App) bool { return a.quickSwitch.IsVisible() }},
	{"newConv", func(t *testing.T, a *App) { a.openNewConversation() }, func(a *App) bool { return a.newConv.IsVisible() }},
	{"infoPanel", func(t *testing.T, a *App) {
		selectContext(a, "general", "", "")
		a.showInfoPanelForContext("general", "", "")
	}, func(a *App) bool { return a.infoPanel.IsVisible() }},
	{"pendingPanel", func(t *testing.T, a *App) { a.pendingPanel.Show(nil) }, func(a *App) bool { return a.pendingPanel.IsVisible() }},
	{"keyWarning", func(t *testing.T, a *App) { a.keyWarning.Show("alice", "aa", "bb") }, func(a *App) bool { return a.keyWarning.IsVisible() }},
	{"leaveConfirm", func(t *testing.T, a *App) { a.leaveConfirm.Show("grp", "crew") }, func(a *App) bool { return a.leaveConfirm.IsVisible() }},
	{"memberMenu", func(t *testing.T, a *App) {
		a.memberMenu.Show("usr_a", []ContextMenuItem{{Label: "x", Action: "x"}}, 5, 5)
	}, func(a *App) bool { return a.memberMenu.IsVisible() }},
	{"verify", func(t *testing.T, a *App) { a.verify.Show("usr_a", a.client) }, func(a *App) bool { return a.verify.IsVisible() }},
	{"addServer", func(t *testing.T, a *App) { a.openAddServerFromNav() }, func(a *App) bool { return a.addServer.IsVisible() }},
	{"deviceMgr", func(t *testing.T, a *App) { a.openDeviceManager() }, func(a *App) bool { return a.deviceMgr.IsVisible() }},
	{"memberPanel", func(t *testing.T, a *App) {
		selectContext(a, "general", "", "")
		a.toggleMemberPanel()
	}, func(a *App) bool { return a.memberPanel.IsVisible() }},
}

// overlayAllowList names App fields with IsVisible that have no preset yet,
// each with the reason. Nothing here is exempt for good: the quit, focus
// and q designs add presets as they need them.
var overlayAllowList = map[string]string{
	"connectFailed":      "pre-session screen; needs the connect-failed flow",
	"passphrase":         "pre-session prompt; needs the passphrase flow",
	"retireConfirm":      "typed-phrase dialog; preset pending",
	"leaveRoomConfirm":   "preset pending",
	"deleteDMConfirm":    "preset pending",
	"deleteGroupConfirm": "preset pending",
	"deleteRoomConfirm":  "preset pending",
	"addConfirm":         "preset pending",
	"kickConfirm":        "preset pending",
	"promoteConfirm":     "preset pending",
	"demoteConfirm":      "preset pending",
	"transferConfirm":    "preset pending",
	"unverifyConfirm":    "preset pending",
	"saveAttachment":     "needs a cached attachment path; preset pending",
	"auditOverlay":       "preset pending",
	"membersOverlay":     "preset pending",
	"lastAdminPicker":    "preset pending",
	"deviceRevoked":      "preset pending",
	"newDeviceAlert":     "preset pending",
	"roomAttestation":    "preset pending",
	"statusPicker":       "preset pending",
	"filePicker":         "needs a readable root and a live upload path; preset pending",
}

// Every preset opens its overlay on a default App, and the App still
// renders.
func TestTestApp_PresetsOpen(t *testing.T) {
	for _, op := range overlayOpeners {
		t.Run(op.field, func(t *testing.T) {
			a := newTestApp(t)
			op.open(t, &a)
			if !op.visible(&a) {
				t.Fatalf("preset %q did not make its overlay visible", op.field)
			}
			if a.View() == "" {
				t.Fatalf("preset %q: View rendered nothing", op.field)
			}
		})
	}
}

// Coverage guard: every App field whose type has IsVisible() bool is either
// in the opener table or in the allow-list with a reason. Adding a modal
// without adding it here fails this test — the "add a new modal? add it
// here too" rule, made mechanical.
func TestTestApp_EveryVisibleFieldHasOpenerOrReason(t *testing.T) {
	openers := map[string]bool{}
	for _, op := range overlayOpeners {
		openers[op.field] = true
	}
	var missing, stale []string
	typ := reflect.TypeOf(App{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if _, ok := reflect.PointerTo(f.Type).MethodByName("IsVisible"); !ok {
			continue
		}
		if !openers[f.Name] && overlayAllowList[f.Name] == "" {
			missing = append(missing, f.Name)
		}
		if openers[f.Name] && overlayAllowList[f.Name] != "" {
			stale = append(stale, f.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("App fields with IsVisible but no opener and no allow-list reason: %s", strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("allow-listed fields that now have an opener (remove the entry): %s", strings.Join(stale, ", "))
	}
	for name := range overlayAllowList {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("allow-list names a field App does not have: %s", name)
		}
	}
}

// The builder's defaults and options hold: the production seed (gen 1) is
// kept, so a frame from serverMsg passes Update's stale-drop and a
// hand-built one does not; withConfig reaches New; withStore is readable
// back through the client.
func TestTestApp_DefaultsAndOptions(t *testing.T) {
	a := newTestApp(t)
	if a.connGen != 1 {
		t.Fatalf("connGen = %d, want New's seed of 1", a.connGen)
	}
	if a.client == nil || !a.client.IsConnected() || a.connState != ConnStateOnline || a.focus != FocusInput {
		t.Fatal("default App is not a quiet online session with focus on the input")
	}
	if a.serverMsg("profile", nil).gen != a.connGen {
		t.Fatal("serverMsg must stamp the live generation")
	}

	appCfg := &config.Config{Servers: []config.ServerConfig{{Name: "Home", Host: "127.0.0.1", Port: 2222}}}
	dir := t.TempDir()
	b := newTestApp(t, withConfig(client.Config{DeviceID: "dev_cfg", Host: "127.0.0.1"}, appCfg, dir, 0), withStore(), withOffline())
	if b.appConfig != appCfg || b.configDir != dir || b.cfg.DeviceID != "dev_cfg" {
		t.Fatal("withConfig must reach New")
	}
	if b.client.Store() == nil {
		t.Fatal("withStore must be readable back through the client")
	}
	if b.client.IsConnected() || b.connState != ConnStateOffline || b.messages.serverReachable {
		t.Fatal("withOffline must leave the client, App and pane all offline")
	}

	c := newTestApp(t, withConnecting(), withSize(40, 12), withFocus(FocusMessages))
	if c.client.IsConnected() || c.connState != ConnStateConnecting {
		t.Fatal("withConnecting must model the dial in progress")
	}
	if c.width != 40 || c.height != 12 || c.focus != FocusMessages {
		t.Fatalf("size %dx%d focus %v; want 40x12 on the pane", c.width, c.height, c.focus)
	}

	d := newTestApp(t, withNoClient())
	if d.client != nil {
		t.Fatal("withNoClient must model the pre-session screens")
	}
}
