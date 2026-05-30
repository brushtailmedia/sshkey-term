package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
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

// The client pins an explicit modern algorithm allowlist (audit S4) that must
// mirror the server: AEAD-only ciphers, ML-KEM-768 + X25519 PQ-hybrid KEX
// preferred, ETM-SHA2 MACs. Guards against silently reverting to the x/crypto
// defaults (which still allow non-AEAD aes-ctr) and against the PQ-hybrid KEX
// disappearing from the preference list.
func TestBuildSSHClientConfig_PinsModernAlgorithms(t *testing.T) {
	c := New(Config{Host: "chat.example.com", DataDir: t.TempDir()})
	cfg := c.buildSSHClientConfig(testSigner(t))

	// KEX: the PQ hybrid must be present AND preferred (first).
	if len(cfg.KeyExchanges) == 0 || cfg.KeyExchanges[0] != ssh.KeyExchangeMLKEM768X25519 {
		t.Errorf("KeyExchanges = %v, want %q first (PQ-hybrid preferred)", cfg.KeyExchanges, ssh.KeyExchangeMLKEM768X25519)
	}

	// Ciphers: AEAD-only — no CBC / CTR / RC4.
	aead := map[string]bool{
		ssh.CipherChaCha20Poly1305: true,
		ssh.CipherAES256GCM:        true,
		ssh.CipherAES128GCM:        true,
	}
	if len(cfg.Ciphers) == 0 {
		t.Fatal("Ciphers must be pinned, got empty (would inherit x/crypto defaults)")
	}
	for _, ci := range cfg.Ciphers {
		if !aead[ci] {
			t.Errorf("non-AEAD cipher %q in pinned list", ci)
		}
	}

	// No known-weak primitive anywhere in the pin.
	for _, list := range [][]string{cfg.Ciphers, cfg.KeyExchanges, cfg.MACs} {
		for _, alg := range list {
			for _, weak := range []string{"cbc", "3des", "arcfour", "rc4", "sha1", "md5"} {
				if strings.Contains(alg, weak) {
					t.Errorf("weak algorithm %q (matches %q) pinned", alg, weak)
				}
			}
		}
	}
}

// S7: the client accepts only an Ed25519 host key. The server's host key is
// always Ed25519 (a single AddHostKey), so pinning HostKeyAlgorithms stops a
// downgraded or rogue server from negotiating a different host-key algorithm.
func TestBuildSSHClientConfig_PinsEd25519HostKeyAlgorithm(t *testing.T) {
	c := New(Config{Host: "chat.example.com", DataDir: t.TempDir()})
	cfg := c.buildSSHClientConfig(testSigner(t))
	if len(cfg.HostKeyAlgorithms) != 1 || cfg.HostKeyAlgorithms[0] != ssh.KeyAlgoED25519 {
		t.Errorf("HostKeyAlgorithms = %v, want [%q]", cfg.HostKeyAlgorithms, ssh.KeyAlgoED25519)
	}
}
