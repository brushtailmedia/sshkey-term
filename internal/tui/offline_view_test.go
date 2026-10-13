package tui

// Offline-browse Step 4 (offline-startup-browse R5/R8/R16/R19): the View
// gate keys on a.client (a local-only browse client renders the full
// shell), the footer reports offline mode with a hollow user dot, the
// input blocks sends but keeps typing/navigation, and the slash allow-list
// pins every verb to its D5–D8 default.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newOfflineViewApp is a full-shell App on a seeded local store, landed in
// offline-browse exactly as the offlineWithClient path would leave it.
func newOfflineViewApp(t *testing.T) *App {
	t.Helper()
	a, st := newOfflineHydrateApp(t)
	a.focus = FocusInput
	if err := st.UpsertRoom("room_a", "name-room_a", "", 2); err != nil {
		t.Fatal(err)
	}
	a.hydrateOfflineUI()
	a.setConnState(ConnStateOffline)
	a.statusBar.SetConnected(false)
	return a
}

func TestOfflineView_RendersShellNotConnecting(t *testing.T) {
	a := newOfflineViewApp(t)
	out := a.View()
	if strings.Contains(out, "Connecting...") {
		t.Fatal("offline browse must render the shell, not the Connecting spinner")
	}
	if !strings.Contains(out, "name-room_a") {
		t.Fatalf("offline shell must show the cached sidebar room; got:\n%s", out)
	}
	if !strings.Contains(out, "offline") {
		t.Fatal("offline shell must carry the offline footer")
	}
}

func TestColdStartView_ConnectingThenRawError(t *testing.T) {
	a := App{width: 100, height: 40, statusBar: NewStatusBar()}
	if out := a.View(); !strings.Contains(out, "Connecting...") {
		t.Fatalf("no client yet must render Connecting..., got %q", out)
	}
	a.err = errors.New("dial: boom")
	if out := a.View(); !strings.Contains(out, "Connection error: dial: boom") {
		t.Fatalf("no client + error must render the raw error, got %q", out)
	}
}

func TestStatusBarFooter_OfflineOnlineConnecting(t *testing.T) {
	s := NewStatusBar()
	s.SetUser("alice", false)

	s.SetConnected(false)
	s.SetOffline(true)
	off := s.View(80)
	if !strings.Contains(off, "offline") || !strings.Contains(off, "○") {
		t.Fatalf("offline footer must read 'offline' with a hollow user dot, got %q", off)
	}
	if strings.Contains(off, "E2E encrypted") || strings.Contains(off, "connecting") {
		t.Fatalf("offline footer must not carry online/connecting labels, got %q", off)
	}

	s.SetOffline(false)
	s.SetConnected(true)
	on := s.View(80)
	if !strings.Contains(on, "E2E encrypted") || strings.Contains(on, "○") || strings.Contains(on, "offline") {
		t.Fatalf("online footer regressed, got %q", on)
	}

	s.SetConnected(false)
	connecting := s.View(80)
	if !strings.Contains(connecting, "(connecting...)") || strings.Contains(connecting, "(offline)") {
		t.Fatalf("connecting footer must say (connecting...), never the old (offline) label, got %q", connecting)
	}
}

func TestOfflineInput_EnterBlockedPreservesText(t *testing.T) {
	a := newOfflineViewApp(t)
	a.input.textInput.SetValue("hello while offline")

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	na := model.(App)

	if na.statusBar.errorMsg != "offline - reconnect to send messages" {
		t.Fatalf("offline Enter must set the offline status, got %q", na.statusBar.errorMsg)
	}
	if na.input.Value() != "hello while offline" {
		t.Fatalf("offline Enter must preserve the typed text, got %q", na.input.Value())
	}
	// Local-only client: the send queue must be untouched by the attempt.
	if na.client.SendQueue().Len() != 0 {
		t.Fatal("blocked offline send must not enqueue anything")
	}
}

func TestOfflineInput_AllowListedCommandPasses(t *testing.T) {
	a := newOfflineViewApp(t)
	a.input.textInput.SetValue("/help")

	model, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	na := model.(App)

	if na.statusBar.errorMsg == "offline - reconnect to send messages" {
		t.Fatal("/help is offline-safe and must pass the gate")
	}
	if !na.help.IsVisible() {
		t.Fatal("/help must still open the help panel offline")
	}
}

func TestOfflineInput_NavigationKeysPass(t *testing.T) {
	a := newOfflineViewApp(t)
	for _, k := range []tea.KeyType{tea.KeyTab, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight} {
		model, _ := a.Update(tea.KeyMsg{Type: k})
		na := model.(App)
		if na.statusBar.errorMsg == "offline - reconnect to send messages" {
			t.Fatalf("navigation key %v must not trip the offline send gate", k)
		}
	}
}

// Every verb in handleCommand's switch (input.go) pinned to its D5–D8
// default. Adding a verb to the router without deciding its offline
// policy fails here.
func TestCommandAllowedOffline_TableOverAllVerbs(t *testing.T) {
	allowed := map[string]bool{
		"/help": true, "/?": true, "/search": true, "/mykey": true, "/typing": true,
		"/online": true, "/connect": true, "/offline": true, "/disconnect": true,
	}
	blocked := []string{
		"/leave", "/delete", "/rename", "/mute", "/verify", "/unverify", "/settings",
		"/pending", "/setstatus", "/upload", "/add", "/kick", "/promote", "/demote",
		"/transfer", "/whoami", "/groupinfo", "/info", "/members", "/admins", "/role",
		"/undo", "/groupcreate", "/dmcreate", "/topic",
	}
	for verb := range allowed {
		if !commandAllowedOffline(verb) {
			t.Errorf("%s must be allowed offline", verb)
		}
		if !commandAllowedOffline(verb + " some arg") {
			t.Errorf("%s with an argument must be allowed offline", verb)
		}
	}
	for _, verb := range blocked {
		if commandAllowedOffline(verb) {
			t.Errorf("%s must be blocked offline (D5–D8)", verb)
		}
		if commandAllowedOffline(verb + " arg") {
			t.Errorf("%s with an argument must be blocked offline", verb)
		}
	}
	// D6: /whois only in its typed form.
	if commandAllowedOffline("/whois") {
		t.Error("bare /whois must be blocked offline (picker over a cold cache)")
	}
	if !commandAllowedOffline("/whois @alice") {
		t.Error("typed /whois <arg> must be allowed offline")
	}
	// Plain text and case-folding.
	if commandAllowedOffline("hello") || commandAllowedOffline("") {
		t.Error("non-command text must never pass the offline gate")
	}
	if !commandAllowedOffline("  /HELP  ") {
		t.Error("verb matching must trim and case-fold like the read-only gate")
	}
}

func TestCanUseServerAction_RequiresOnlineAndLiveClient(t *testing.T) {
	a := newOfflineViewApp(t)
	if a.canUseServerAction() {
		t.Fatal("offline browse must not allow server actions")
	}
	a.setConnState(ConnStateOnline)
	// connState says online but the local-only client is not connected.
	if a.canUseServerAction() {
		t.Fatal("a client that is not actually connected must not pass canUseServerAction")
	}
}

func TestOfflineMessageAction_ServerMutationsBlocked(t *testing.T) {
	a := newOfflineViewApp(t)
	for _, action := range []string{"react", "unreact", "delete", "pin", "unpin"} {
		model, _ := a.Update(MessageAction{Action: action})
		na := model.(App)
		if na.statusBar.errorMsg != "offline - this action needs the server" {
			t.Errorf("action %q offline: status = %q, want the needs-server message", action, na.statusBar.errorMsg)
		}
	}
}
