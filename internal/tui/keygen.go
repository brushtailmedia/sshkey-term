package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// generateEd25519KeyFile creates a new Ed25519 SSH keypair and writes the
// private key (optionally passphrase-protected) to path, with the public key
// to path+".pub". Returns the SHA256 fingerprint of the public key.
//
// Path "~/..." is expanded to the user's home directory. Parent directories
// are created with mode 0700 if missing. Private key is written 0600, public
// key 0644.
//
// Shared by the first-launch wizard and the AddServer dialog. Does not touch
// any server state — callers are responsible for backup prompts and for
// wiring the returned key into config.toml.
func generateEd25519KeyFile(path, passphrase string, comment ...string) (fingerprint string, err error) {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}

	var pemBlock *pem.Block
	if passphrase != "" {
		pemBlock, err = ssh.MarshalPrivateKeyWithPassphrase(privKey, "", []byte(passphrase))
	} else {
		pemBlock, err = ssh.MarshalPrivateKey(privKey, "")
	}
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	privPEM := pem.EncodeToMemory(pemBlock)

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(path, privPEM, 0600); err != nil {
		return "", fmt.Errorf("write private key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	// Append comment (preferred display name) if provided
	if len(comment) > 0 && comment[0] != "" {
		pubLine += " " + comment[0]
	}
	pubLine += "\n"
	if err := os.WriteFile(path+".pub", []byte(pubLine), 0644); err != nil {
		return "", fmt.Errorf("write public key: %w", err)
	}

	return ssh.FingerprintSHA256(sshPub), nil
}
