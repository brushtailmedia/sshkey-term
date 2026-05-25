package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// main_test.go covers Phase 4's CLI bypass regression suite
// (Option B-lite): targeted tests of the extracted runBypassMode +
// findServerIndex helpers. The full run() function is not exercised
// here — flag parsing, tea.NewProgram, and the wizard branch stay
// inside run() per the Option B-lite scope. Integration coverage of
// the chained run() flow would require a subprocess test (rejected
// for cost reasons) and is documented as a gap in
// path-centralization.md §"Decision — CLI bypass test strategy
// (Option B-lite)".

// writeKeyFixture writes a private/public key pair to a temp dir
// and returns the private-key path (the .pub sibling is at <path>.pub).
// The bytes are fixed so tests can assert on round-trip content.
func writeKeyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	priv := filepath.Join(dir, "src_id_ed25519")
	if err := os.WriteFile(priv, []byte("FAKE PRIVATE KEY BYTES\n"), 0o600); err != nil {
		t.Fatalf("seed private: %v", err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate public fixture: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("public fixture signer: %v", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " old-comment\n"
	if err := os.WriteFile(priv+".pub", []byte(pubLine), 0o644); err != nil {
		t.Fatalf("seed public: %v", err)
	}
	return priv
}

// -- runBypassMode tests --

func TestRunBypassMode_BootstrapPersist(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{Device: config.DeviceConfig{ID: "dev_bootstrap"}}
	srcKey := writeKeyFixture(t)

	server, ephemeral, err := runBypassMode(configDir, cfg, "first.example.com", srcKey, "First", "First User", 2222)
	if err != nil {
		t.Fatalf("runBypassMode: %v", err)
	}
	if ephemeral {
		t.Errorf("bootstrap-persist should report ephemeral=false")
	}
	if server.Name != "First" || server.Host != "first.example.com" || server.Port != 2222 {
		t.Errorf("server fields = %+v", server)
	}
	if server.RequestedDisplayName != "First User" {
		t.Errorf("RequestedDisplayName = %q, want %q", server.RequestedDisplayName, "First User")
	}

	// Managed key file exists at the canonical location with the
	// source bytes.
	managedKey := config.ServerKeyPath(configDir, "first.example.com")
	got, err := os.ReadFile(managedKey)
	if err != nil {
		t.Fatalf("read managed key: %v", err)
	}
	if string(got) != "FAKE PRIVATE KEY BYTES\n" {
		t.Errorf("managed key bytes = %q", string(got))
	}
	// .pub sibling also copied.
	pubBytes, err := os.ReadFile(managedKey + ".pub")
	if err != nil {
		t.Errorf("managed .pub missing: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(pubBytes)), " First User") {
		t.Errorf("managed .pub comment = %q, want requested display name", string(pubBytes))
	}
	if strings.Contains(string(pubBytes), "old-comment") {
		t.Errorf("managed .pub should not keep source comment when -display-name is set: %q", string(pubBytes))
	}

	// cfg.Servers gained the new entry; config.toml persisted.
	if len(cfg.Servers) != 1 {
		t.Fatalf("cfg.Servers = %d entries, want 1", len(cfg.Servers))
	}
	if cfg.Servers[0].Host != "first.example.com" {
		t.Errorf("persisted host = %q", cfg.Servers[0].Host)
	}
	// Confirm config.toml on disk loads back to the same shape.
	loaded, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Servers) != 1 || loaded.Servers[0].Host != "first.example.com" {
		t.Errorf("reloaded config = %+v", loaded.Servers)
	}
	if loaded.Servers[0].RequestedDisplayName != "First User" {
		t.Errorf("persisted requested_display_name = %q, want %q", loaded.Servers[0].RequestedDisplayName, "First User")
	}
}

func TestRunBypassMode_Ephemeral(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{
		Device: config.DeviceConfig{ID: "dev_ephemeral"},
		Servers: []config.ServerConfig{
			{Name: "Existing", Host: "existing.example.com", Port: 2222},
		},
	}
	// Seed config.toml on disk to match cfg so we can assert
	// post-call that it's UNCHANGED (no spurious save from the
	// ephemeral path).
	if err := config.Save(configDir, cfg); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	server, ephemeral, err := runBypassMode(configDir, cfg, "new.example.com", "/external/key", "", "Ghost", 2223)
	if err != nil {
		t.Fatalf("runBypassMode: %v", err)
	}
	if !ephemeral {
		t.Errorf("ephemeral mode should report ephemeral=true")
	}
	// Name fallback: empty -name → Host.
	if server.Name != "new.example.com" {
		t.Errorf("name fallback failed, got %q", server.Name)
	}
	if server.Host != "new.example.com" || server.Port != 2223 {
		t.Errorf("server fields = %+v", server)
	}
	// The -display-name hint is carried on the in-memory server for THIS run.
	if server.RequestedDisplayName != "Ghost" {
		t.Errorf("ephemeral RequestedDisplayName = %q, want %q (runtime-only)", server.RequestedDisplayName, "Ghost")
	}

	// CRITICAL: no managed copy created for the ephemeral host.
	managedKey := config.ServerKeyPath(configDir, "new.example.com")
	if _, err := os.Stat(managedKey); err == nil {
		t.Errorf("ephemeral mode must NOT create managed key at %q", managedKey)
	}

	// cfg.Servers untouched.
	if len(cfg.Servers) != 1 {
		t.Errorf("cfg.Servers length should be 1 after ephemeral run, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Host != "existing.example.com" {
		t.Errorf("existing entry mutated: %+v", cfg.Servers[0])
	}
	// ...and is NOT persisted to config.toml: the on-disk config still holds
	// only the seeded entry, with no "Ghost" requested name anywhere.
	loaded, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Servers) != 1 || loaded.Servers[0].Host != "existing.example.com" {
		t.Errorf("ephemeral mutated config.toml: %+v", loaded.Servers)
	}
	if loaded.Servers[0].RequestedDisplayName != "" {
		t.Errorf("ephemeral hint leaked to disk: %q", loaded.Servers[0].RequestedDisplayName)
	}
}

func TestRunBypassMode_InvalidHost(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{Device: config.DeviceConfig{ID: "dev_invalid"}}

	_, ephemeral, err := runBypassMode(configDir, cfg, "../etc", "/some/key", "", "", 2222)
	if err == nil {
		t.Fatal("expected error for invalid -host")
	}
	if !strings.Contains(err.Error(), "invalid -host") {
		t.Errorf("error message should mention -host, got %q", err)
	}
	if ephemeral {
		t.Error("error path should not report ephemeral=true")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("cfg.Servers should be untouched on reject, got %d entries", len(cfg.Servers))
	}
	// No managed key created on disk.
	if _, err := os.Stat(filepath.Join(configDir, "..", "etc")); err == nil {
		t.Error("invalid host must NOT result in any path being created")
	}
}

func TestRunBypassMode_NameFallback(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{Device: config.DeviceConfig{ID: "dev_name"}}
	srcKey := writeKeyFixture(t)

	server, _, err := runBypassMode(configDir, cfg, "host.example.com", srcKey, "", "", 2222)
	if err != nil {
		t.Fatalf("runBypassMode: %v", err)
	}
	if server.Name != "host.example.com" {
		t.Errorf("name fallback: got %q, want host.example.com", server.Name)
	}
}

func TestRunBypassMode_InvalidDisplayName(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{Device: config.DeviceConfig{ID: "dev_baddn"}}
	srcKey := writeKeyFixture(t)

	// DP9: '+' is banned. A bad -display-name must fail fast — before any
	// managed copy or persisted entry is created.
	_, _, err := runBypassMode(configDir, cfg, "host.example.com", srcKey, "Label", "bad+name", 2222)
	if err == nil {
		t.Fatal("expected error for invalid -display-name")
	}
	if !strings.Contains(err.Error(), "-display-name") {
		t.Errorf("error should mention -display-name, got %q", err)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("nothing should be persisted on a bad -display-name, got %d servers", len(cfg.Servers))
	}
	if _, statErr := os.Stat(config.ServerKeyPath(configDir, "host.example.com")); statErr == nil {
		t.Error("no managed key should be created when -display-name is rejected")
	}
}

// -- findServerIndex / ephemeral-index sentinel tests --

func TestFindServerIndex_Match(t *testing.T) {
	cfg := &config.Config{
		Servers: []config.ServerConfig{
			{Host: "a", Port: 2222},
			{Host: "b", Port: 2223},
			{Host: "c", Port: 2224},
		},
	}
	got := findServerIndex(cfg, config.ServerConfig{Host: "b", Port: 2223})
	if got != 1 {
		t.Errorf("expected idx 1 for matching (b, 2223), got %d", got)
	}
}

func TestFindServerIndex_UnconfiguredEphemeralHost(t *testing.T) {
	cfg := &config.Config{
		Servers: []config.ServerConfig{
			{Host: "a", Port: 2222},
		},
	}
	// Ephemeral case: bypass host not in cfg.Servers.
	got := findServerIndex(cfg, config.ServerConfig{Host: "ephemeral.example.com", Port: 2222})
	if got != -1 {
		t.Errorf("expected sentinel -1 for unconfigured host, got %d", got)
	}
}

func TestFindServerIndex_PortDiscriminates(t *testing.T) {
	// Same Host but different Port should NOT match — the lookup
	// uses (Host, Port) jointly. This is what makes the same-host-
	// different-port collision the known limitation documented in
	// the plan: at the index level we DO discriminate, but the
	// path layout (<configDir>/<host>/) doesn't.
	cfg := &config.Config{
		Servers: []config.ServerConfig{
			{Host: "shared.example.com", Port: 2222},
		},
	}
	got := findServerIndex(cfg, config.ServerConfig{Host: "shared.example.com", Port: 9999})
	if got != -1 {
		t.Errorf("expected -1 for matching-host-different-port, got %d", got)
	}
}

func TestFindServerIndex_NilCfg(t *testing.T) {
	got := findServerIndex(nil, config.ServerConfig{Host: "x", Port: 2222})
	if got != -1 {
		t.Errorf("nil cfg should return -1 sentinel, got %d", got)
	}
}
