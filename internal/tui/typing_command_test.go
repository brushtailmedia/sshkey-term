package tui

import (
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// The `/typing [on|off]` command is a LOCAL UX toggle, never a
// security control: the sshkey-chat server independently authorizes
// every typing relay (typing-relay-authz-hardening). These tests pin
// the client behavior: bare toggles, on/off set explicitly, the input
// model's send-gate flag stays in sync, the preference is persisted to
// config.toml (survives restart), and a bad arg is a no-op usage error.

// TestInputParser_TypingRoutesToAppInAnyContext is the regression test
// for the wiring gap: the InputModel.handleCommand switch (no default)
// must produce a pendingCmd for `/typing`, otherwise the typed command
// is swallowed by the input box and app.go's handler is never reached.
// This exercises the REAL dispatch path (handleCommand → PendingCommand)
// — the app-handler-in-isolation tests below do NOT, which is exactly
// why the original gap slipped through. `/typing` mirrors `/setstatus`:
// routes in every context, bare + on/off arg forms, no context gate.
func TestInputParser_TypingRoutesToAppInAnyContext(t *testing.T) {
	cases := []struct {
		text    string
		wantArg string
		room    string
		group   string
		dm      string
	}{
		{"/typing", "", "", "", ""},
		{"/typing on", "on", "", "", ""},
		{"/typing off", "off", "", "", ""},
		{"/typing", "", "", "group_x", ""}, // still routes with a context set
		{"/typing on", "on", "room_x", "", ""},
		{"/typing off", "off", "", "", "dm_x"},
	}
	for _, tc := range cases {
		i := &InputModel{}
		i.handleCommand(tc.text, nil, tc.room, tc.group, tc.dm)
		sc := i.PendingCommand()
		if sc == nil {
			t.Fatalf("%q produced no pendingCmd — /typing not wired through the input router", tc.text)
		}
		if sc.Command != "/typing" || sc.Arg != tc.wantArg {
			t.Errorf("%q routed as {Command:%q Arg:%q}, want {/typing %q}", tc.text, sc.Command, sc.Arg, tc.wantArg)
		}
	}
}

func newTypingCmdApp(t *testing.T) App {
	t.Helper()
	return App{
		appConfig: &config.Config{},
		configDir: t.TempDir(),
		input:     NewInput(),
		statusBar: NewStatusBar(),
		focus:     FocusInput,
	}
}

// persistedTypingDisabled re-reads config.toml from disk so the test
// asserts the actual persisted value, not just the in-memory struct.
func persistedTypingDisabled(t *testing.T, dir string) bool {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	return cfg.Notifications.TypingDisabled
}

func TestTypingCommand_BareToggles(t *testing.T) {
	a := newTypingCmdApp(t)

	// Default = enabled (TypingDisabled false). Bare /typing flips it
	// to disabled.
	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing"})
	if !a.appConfig.Notifications.TypingDisabled {
		t.Fatal("bare /typing from default should disable typing")
	}
	if !a.input.typingDisabled {
		t.Fatal("input send-gate flag must mirror the toggle (disabled)")
	}
	if !persistedTypingDisabled(t, a.configDir) {
		t.Fatal("disabled state must be persisted to config.toml")
	}

	// Bare again flips back to enabled.
	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing"})
	if a.appConfig.Notifications.TypingDisabled {
		t.Fatal("second bare /typing should re-enable typing")
	}
	if a.input.typingDisabled {
		t.Fatal("input send-gate flag must mirror the toggle (enabled)")
	}
	if persistedTypingDisabled(t, a.configDir) {
		t.Fatal("re-enabled state must be persisted to config.toml")
	}
}

func TestTypingCommand_OnOffExplicit(t *testing.T) {
	a := newTypingCmdApp(t)

	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing", Arg: "off"})
	if !a.appConfig.Notifications.TypingDisabled || !a.input.typingDisabled {
		t.Fatal("/typing off must disable (config + input flag)")
	}
	if !persistedTypingDisabled(t, a.configDir) {
		t.Fatal("/typing off must persist disabled")
	}
	if a.statusBar.errorMsg != "Typing indicators off" {
		t.Fatalf("status = %q, want %q", a.statusBar.errorMsg, "Typing indicators off")
	}

	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing", Arg: "ON"}) // case-insensitive
	if a.appConfig.Notifications.TypingDisabled || a.input.typingDisabled {
		t.Fatal("/typing ON must enable (config + input flag)")
	}
	if persistedTypingDisabled(t, a.configDir) {
		t.Fatal("/typing on must persist enabled")
	}
	if a.statusBar.errorMsg != "Typing indicators on" {
		t.Fatalf("status = %q, want %q", a.statusBar.errorMsg, "Typing indicators on")
	}
}

func TestTypingCommand_InvalidArg_UsageErrorNoStateChange(t *testing.T) {
	a := newTypingCmdApp(t)
	// Seed a known state so we can assert it is untouched.
	a.appConfig.Notifications.TypingDisabled = true
	a.input.SetTypingDisabled(true)

	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing", Arg: "bogus"})

	if !a.appConfig.Notifications.TypingDisabled || !a.input.typingDisabled {
		t.Fatal("invalid arg must NOT mutate typing state")
	}
	if a.statusBar.errorMsg != "Usage: /typing [on|off]" {
		t.Fatalf("status = %q, want usage error", a.statusBar.errorMsg)
	}
}

func TestTypingCommand_NilConfig_GuardedNoPanic(t *testing.T) {
	a := App{
		input:     NewInput(),
		statusBar: NewStatusBar(),
		focus:     FocusInput,
	} // appConfig deliberately nil
	a.handleSlashCommand(&SlashCommandMsg{Command: "/typing"})
	if a.statusBar.errorMsg == "" {
		t.Fatal("nil appConfig should surface an error, not panic/silently no-op")
	}
}
