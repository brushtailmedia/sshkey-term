package tui

// Offline-browse Step 6 (offline-startup-browse R9/R10/R20): server-switch
// semantics, and an end-to-end check of the connect fallback — an
// unreachable server with cached history must land in offlineWithClient on
// THAT server's store, while a server without cache keeps today's ErrMsg
// (connect-failed overlay) path.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

func TestSwitch_NilsClientAndResetsLadder(t *testing.T) {
	a := newNavModeAppHarness(t)
	a.initialAttempt = 2
	old := a.client

	cmd := a.switchServerByIndex(1)
	if cmd == nil {
		t.Fatal("switch must return the connect cmd")
	}
	if a.client != nil {
		t.Fatal("switch must nil the closed client so the new server can fall back to ITS store (R9/R10)")
	}
	if a.initialAttempt != 0 || a.connState != ConnStateConnecting {
		t.Fatalf("initialAttempt=%d state=%v, want 0/Connecting", a.initialAttempt, a.connState)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("switch must Close the previous client")
	}
	if a.cfg.Host != "127.0.0.2" || !strings.Contains(a.cfg.DataDir, "127.0.0.2") {
		t.Fatalf("cfg must be repointed to the new server: host=%q dataDir=%q", a.cfg.Host, a.cfg.DataDir)
	}
}

func TestSwitch_StaleOfflineWithClientDroppedAfterSwitch(t *testing.T) {
	a := newNavModeAppHarness(t)
	oldGen := a.connGen
	a.switchServerByIndex(1) // bumps the gen

	stale := client.New(client.Config{})
	na := runUpdate(t, &a, offlineWithClient{client: stale, err: errors.New("dial"), gen: oldGen})
	if na.client != nil {
		t.Fatal("a stale offlineWithClient after a switch must not install its client")
	}
	if na.connectFailed.IsVisible() || na.connState != ConnStateConnecting {
		t.Fatal("a stale offlineWithClient must change nothing")
	}
	select {
	case <-stale.Done():
	default:
		t.Fatal("the stale client must be closed")
	}
}

// seedCachedServer writes an unencrypted OpenSSH Ed25519 key and a
// pre-synced encrypted store (last_synced + a marker) under a fresh DataDir,
// exactly as a previous online session would have left them.
func seedCachedServer(t *testing.T, marker string) (keyPath, dataDir string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir = t.TempDir()
	dbKey, err := store.DeriveDBKey(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(config.MessagesDBPath(dataDir), dbKey)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if err := st.SetState("last_synced", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("marker", marker); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return keyPath, dataDir
}

// closedPort returns a loopback port that was just released, so a dial is
// refused immediately and deterministically.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func runConnectCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case m := <-done:
		return m
	case <-time.After(20 * time.Second):
		t.Fatal("connect cmd did not return")
		return nil
	}
}

func TestConnect_FallbackOpensThatServersStore(t *testing.T) {
	keyPath, dataDir := seedCachedServer(t, "server-B")
	a := newNavModeAppHarness(t)
	a.cfg = client.Config{Host: "127.0.0.1", Port: closedPort(t), KeyPath: keyPath, DataDir: dataDir, DeviceID: "dev_fb"}

	msg := runConnectCmd(t, a.connect(a.connGen, true))
	off, ok := msg.(offlineWithClient)
	if !ok {
		t.Fatalf("unreachable cached server must yield offlineWithClient, got %T (%v)", msg, msg)
	}
	t.Cleanup(func() { off.client.Close() })
	if off.gen != a.connGen || off.err == nil {
		t.Fatalf("offlineWithClient must carry the gen and the dial error: gen=%d err=%v", off.gen, off.err)
	}
	st := off.client.Store()
	if st == nil || !off.client.HasLocalCache() {
		t.Fatal("fallback must open the cached store")
	}
	if v, _ := st.GetState("marker"); v != "server-B" {
		t.Fatalf("fallback opened the wrong store: marker=%q, want server-B (per-server DataDir isolation)", v)
	}
	if off.client.IsConnected() {
		t.Fatal("the fallback client must not report connected")
	}
}

func TestConnect_NoCacheFallsThroughToErrMsg(t *testing.T) {
	// A key but an EMPTY data dir: the store opens, no last_synced → the
	// first-contact / pending-approval path must keep its ErrMsg + overlay.
	keyPath, _ := seedCachedServer(t, "unused")
	a := newNavModeAppHarness(t)
	a.cfg = client.Config{Host: "127.0.0.1", Port: closedPort(t), KeyPath: keyPath, DataDir: t.TempDir(), DeviceID: "dev_nc"}
	msg := runConnectCmd(t, a.connect(a.connGen, true))
	em, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("first-contact server must yield ErrMsg, got %T", msg)
	}
	if em.gen != a.connGen {
		t.Fatalf("ErrMsg gen = %d, want %d", em.gen, a.connGen)
	}

	// With the fallback flag off (mid-session reconnect semantics), even a
	// cached server must yield ErrMsg — never a second store handle (R20).
	keyPath2, dataDir2 := seedCachedServer(t, "server-C")
	a.cfg = client.Config{Host: "127.0.0.1", Port: closedPort(t), KeyPath: keyPath2, DataDir: dataDir2, DeviceID: "dev_nf"}
	if _, ok := runConnectCmd(t, a.connect(a.connGen, false)).(ErrMsg); !ok {
		t.Fatal("allowLocalFallback=false must never produce offlineWithClient")
	}
}
