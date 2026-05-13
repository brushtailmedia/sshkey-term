package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// passphrase_flow_test.go covers the App-layer encrypted-key
// connect path — specifically the regression that caused the TUI
// to hang at "Connecting..." with no SSH dial attempted when the
// user's key was passphrase-encrypted (see passphrase-prompt-fix.md
// for the full diagnosis). The fix pre-flights an encryption
// check in App.connect() before calling client.Connect(), so the
// passphrase dialog (passphraseNeededMsg) fires BEFORE the
// goroutine parks inside OnPassphrase's channel receive.
//
// All three tests below wrap the cmd execution in a 200ms timeout
// guard. The bug being regression-tested is a deadlock; without
// the guard a regressed implementation would hang the test
// process forever instead of failing fast.

// Timing budgets for the two test shapes below.
//
// preflightDispatchBudget: the pre-flight check (file read +
// ssh.ParsePrivateKey + type assertion) is synchronous and
// sub-millisecond on any realistic hardware. A regressed deadlock
// would never return; a working dispatch returns essentially
// immediately. 200ms is generous headroom that still fails fast
// on a real hang.
//
// preflightFallthroughBudget: when pre-flight is expected to FALL
// THROUGH (unencrypted key, or encrypted key with cached
// passphrase), the cmd then proceeds into client.New + Connect,
// which does real network I/O (variable latency, especially with
// the extra decrypt step on a cached-encrypted key). We don't
// care whether Connect succeeds or fails — we only care that the
// pre-flight didn't intercept with passphraseNeededMsg. So we
// wait a short window: if the cmd returns in that window AND the
// message is passphraseNeededMsg, the pre-flight wrongly
// intercepted; otherwise (returned with another message OR still
// running past the window) the pre-flight correctly fell through.
const (
	preflightDispatchBudget    = 200 * time.Millisecond
	preflightFallthroughBudget = 50 * time.Millisecond
)

// newAppForPassphraseTest builds the minimal App state that
// App.connect() reads: cfg (with KeyPath), passphraseCache,
// passphraseCh. No client, no appConfig.
func newAppForPassphraseTest(t *testing.T, keyPath string) App {
	t.Helper()
	return App{
		cfg: client.Config{
			KeyPath:  keyPath,
			Host:     "127.0.0.1",
			Port:     1, // intentionally low/unused — Connect will not reach a real server
			DeviceID: "dev_passphrase_test",
		},
		passphraseCache: make(map[string][]byte),
		passphraseCh:    make(chan []byte, 1),
	}
}

// assertPreflightDispatches runs connect() and asserts that the
// pre-flight returns passphraseNeededMsg within
// preflightDispatchBudget. If the cmd doesn't return inside the
// budget, that's the regressed-deadlock signal — fail fast
// instead of hanging the test process.
func assertPreflightDispatches(t *testing.T, a App) {
	t.Helper()
	cmd := a.connect()
	if cmd == nil {
		t.Fatal("connect() returned nil cmd")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if _, ok := msg.(passphraseNeededMsg); !ok {
			t.Errorf("expected passphraseNeededMsg from pre-flight, got %T (%+v)", msg, msg)
		}
	case <-time.After(preflightDispatchBudget):
		t.Fatalf("connect cmd did not return within %s — deadlock regression?", preflightDispatchBudget)
	}
}

// assertPreflightFallsThrough runs connect() and asserts that the
// pre-flight does NOT intercept with passphraseNeededMsg. Uses a
// short window: pre-flight is synchronous and sub-millisecond, so
// if no message arrives in preflightFallthroughBudget the cmd is
// definitively past the pre-flight (parked in Connect's network
// I/O). If a message DOES arrive in that window, just check it
// isn't passphraseNeededMsg. The Connect cmd is left running in
// its goroutine; the buffered done channel means the goroutine
// exits cleanly without blocking when Connect eventually returns.
func assertPreflightFallsThrough(t *testing.T, a App) {
	t.Helper()
	cmd := a.connect()
	if cmd == nil {
		t.Fatal("connect() returned nil cmd")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if _, ok := msg.(passphraseNeededMsg); ok {
			t.Errorf("pre-flight wrongly intercepted with passphraseNeededMsg; should have fallen through to Connect")
		}
	case <-time.After(preflightFallthroughBudget):
		// Cmd still running past the budget — pre-flight is
		// synchronous and would have returned immediately if it
		// had dispatched. Therefore: fell through. Pass.
	}
}

// TestApp_EncryptedKeyDispatchesPassphraseNeeded covers the
// happy path of the fix: with no cached passphrase, an
// encrypted key triggers passphraseNeededMsg from the pre-flight
// rather than parking inside OnPassphrase forever.
func TestApp_EncryptedKeyDispatchesPassphraseNeeded(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "encrypted_id_ed25519")
	if _, err := generateEd25519KeyFile(keyPath, "test-passphrase", "test"); err != nil {
		t.Fatalf("generate encrypted key fixture: %v", err)
	}

	a := newAppForPassphraseTest(t, keyPath)
	assertPreflightDispatches(t, a)
}

// TestApp_UnencryptedKeyProceedsToConnect covers the negative
// case: an unencrypted key must NOT trigger the passphrase
// dialog. The pre-flight returns false; flow continues into
// client.Connect(). The assertion is "no spurious
// passphraseNeededMsg" — we don't care what Connect ultimately
// does since no real server is running.
func TestApp_UnencryptedKeyProceedsToConnect(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "plain_id_ed25519")
	if _, err := generateEd25519KeyFile(keyPath, "", "test"); err != nil {
		t.Fatalf("generate unencrypted key fixture: %v", err)
	}

	a := newAppForPassphraseTest(t, keyPath)
	assertPreflightFallsThrough(t, a)
}

// TestApp_EncryptedKeyWithCachedPassphraseSkipsPrompt covers the
// warm-reconnect path: an encrypted key with a previously-cached
// passphrase must NOT re-prompt. The cache check at the top of
// the pre-flight short-circuits before KeyNeedsPassphrase runs.
func TestApp_EncryptedKeyWithCachedPassphraseSkipsPrompt(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "encrypted_cached_id_ed25519")
	if _, err := generateEd25519KeyFile(keyPath, "test-passphrase", "test"); err != nil {
		t.Fatalf("generate encrypted key fixture: %v", err)
	}

	a := newAppForPassphraseTest(t, keyPath)
	// Pre-populate the cache as if the user had typed the
	// passphrase on a prior connect.
	a.passphraseCache[keyPath] = []byte("test-passphrase")

	assertPreflightFallsThrough(t, a)
}

// TestApp_PassphraseDialogRendersWhenNotConnected covers the
// view-layer bug where the passphrase dialog (visible at the
// model level via Show()) was not actually rendered to the user
// because viewBody() returned its "\n  Connecting...\n" fallback
// BEFORE checking a.passphrase.IsVisible(). The pre-flight fix
// correctly dispatched passphraseNeededMsg, the handler correctly
// called a.passphrase.Show(""), but the user never saw the
// dialog — terminal sat at "Connecting..." forever, with the
// dialog technically visible-but-unrendered behind it.
//
// This test exercises the integration: model state set as if the
// pre-flight had dispatched + handler had run, then call viewBody
// directly and assert the rendered output contains passphrase-
// dialog content rather than the "Connecting..." fallback.
func TestApp_PassphraseDialogRendersWhenNotConnected(t *testing.T) {
	a := App{
		// Width/height must be non-zero or viewBody returns
		// "Loading..."; must be >= appMinWidth/appMinHeight or
		// it returns the too-small bouncer.
		width:           120,
		height:          40,
		connected:       false, // critical: dialog must render even pre-connect
		passphrase:      NewPassphrase(),
		passphraseCache: make(map[string][]byte),
		passphraseCh:    make(chan []byte, 1),
	}
	a.passphrase.Show("")

	out := a.viewBody()

	// The "Connecting..." fallback was the symptom; assert it's
	// not in the output.
	if out == "\n  Connecting...\n" {
		t.Error("viewBody() rendered \"Connecting...\" fallback instead of passphrase dialog while not-connected")
	}
	// Assert the dialog's distinctive rounded-border glyphs are
	// in the output. The PassphraseModel uses dialogStyle which
	// applies lipgloss.RoundedBorder() — `╭` and `╰` are the
	// unambiguous "a dialog rendered here" signal. Plain text
	// markers like "passphrase" get broken up by per-segment ANSI
	// styling, making substring search unreliable.
	if !contains(out, "╭") || !contains(out, "╰") {
		t.Errorf("viewBody() output missing dialog-border glyphs (╭/╰); the passphrase dialog did not render. Got:\n%s", out)
	}
}
