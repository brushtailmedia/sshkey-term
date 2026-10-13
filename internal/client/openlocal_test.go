package client

// Tests for OpenLocal — the store-open path extracted from Connect so the
// offline-browse flow can open the encrypted local DB without a dial
// (offline-startup-browse Step 1, risk R1: double-open / idempotency).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// writeTestKeyFile writes a fresh unencrypted OpenSSH Ed25519 private key to
// a temp dir and returns its path. Same marshaling as testutil's fixtures,
// replicated here because testutil imports this package (import cycle).
func writeTestKeyFile(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

// newLocalOnlyTestClient builds a client with a real key file and a temp
// DataDir that is never dialed.
func newLocalOnlyTestClient(t *testing.T) *Client {
	t.Helper()
	c := New(Config{KeyPath: writeTestKeyFile(t), DataDir: t.TempDir()})
	t.Cleanup(func() {
		if c.store != nil {
			c.store.Close()
		}
	})
	return c
}

func TestOpenLocal_WithoutConnectOpensStore(t *testing.T) {
	c := newLocalOnlyTestClient(t)
	if c.Store() != nil {
		t.Fatal("store must be nil before OpenLocal")
	}
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	if c.Store() == nil {
		t.Fatal("OpenLocal must open the store without Connect")
	}
	if c.privKey == nil {
		t.Fatal("OpenLocal must parse the key itself when privKey is unset")
	}
}

func TestOpenLocal_Idempotent(t *testing.T) {
	c := newLocalOnlyTestClient(t)
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("first OpenLocal: %v", err)
	}
	first := c.Store()
	if first == nil {
		t.Fatal("first OpenLocal must open the store")
	}
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("second OpenLocal: %v", err)
	}
	if c.Store() != first {
		t.Fatal("second OpenLocal must not reopen the store (R1 double-open)")
	}
}

func TestOpenLocal_EmptyDataDirIsNoop(t *testing.T) {
	c := New(Config{KeyPath: writeTestKeyFile(t)})
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("OpenLocal with empty DataDir must return nil, got %v", err)
	}
	if c.Store() != nil {
		t.Fatal("no store expected with empty DataDir")
	}
}
