package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

// buildSSHClientConfig is the single source for both dial sites (Connect and
// doConnect/reconnect), so the requested-display-name hint (cfg.User) cannot
// drift between initial connect and reconnect. These assertions stand in for a
// live SSH server.
func TestBuildSSHClientConfig_CarriesUserHint(t *testing.T) {
	c := New(Config{User: "Alice Smith", Host: "chat.example.com", DataDir: t.TempDir()})
	cfg := c.buildSSHClientConfig(testSigner(t))

	if cfg.User != "Alice Smith" {
		t.Errorf("User = %q, want %q", cfg.User, "Alice Smith")
	}
	if len(cfg.Auth) != 1 {
		t.Errorf("expected exactly one auth method, got %d", len(cfg.Auth))
	}
	if cfg.HostKeyCallback == nil {
		t.Error("HostKeyCallback must be set (TOFU)")
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", cfg.Timeout)
	}
}

// An empty User means "no hint" — the server treats absent/empty SSH usernames
// as no requested name, so we must send empty rather than a placeholder.
func TestBuildSSHClientConfig_EmptyUserWhenNoHint(t *testing.T) {
	c := New(Config{Host: "chat.example.com", DataDir: t.TempDir()})
	cfg := c.buildSSHClientConfig(testSigner(t))
	if cfg.User != "" {
		t.Errorf("User = %q, want empty when no hint configured", cfg.User)
	}
}
