package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateEd25519KeyFile_NoPassphrase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	fingerprint, err := generateEd25519KeyFile(path, "")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q, want SHA256:... prefix", fingerprint)
	}

	// Private key file exists with 0600 perms
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("private key perms = %o, want 0600", info.Mode().Perm())
	}

	// Public key file exists with 0644 perms
	pubInfo, err := os.Stat(path + ".pub")
	if err != nil {
		t.Fatalf("stat .pub: %v", err)
	}
	if pubInfo.Mode().Perm() != 0644 {
		t.Errorf("public key perms = %o, want 0644", pubInfo.Mode().Perm())
	}

	// Private key parses as Ed25519
	data, _ := os.ReadFile(path)
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		t.Fatalf("parse generated private key: %v", err)
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("key type = %q, want ssh-ed25519", signer.PublicKey().Type())
	}

	// Public key matches fingerprint
	if ssh.FingerprintSHA256(signer.PublicKey()) != fingerprint {
		t.Errorf("fingerprint mismatch")
	}

	// .pub starts with "ssh-ed25519"
	pubData, _ := os.ReadFile(path + ".pub")
	if !strings.HasPrefix(string(pubData), "ssh-ed25519") {
		t.Errorf("public key file doesn't start with ssh-ed25519: %q", string(pubData[:40]))
	}
}

func TestGenerateEd25519KeyFile_WithPassphrase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "encrypted_key")
	passphrase := "super-secret-passphrase"

	if _, err := generateEd25519KeyFile(path, passphrase); err != nil {
		t.Fatalf("generate: %v", err)
	}

	data, _ := os.ReadFile(path)

	// Without passphrase — should fail
	if _, err := ssh.ParsePrivateKey(data); err == nil {
		t.Error("parsing encrypted key without passphrase should fail")
	} else if _, ok := err.(*ssh.PassphraseMissingError); !ok {
		t.Errorf("expected PassphraseMissingError, got %T: %v", err, err)
	}

	// With passphrase — should succeed
	signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	if err != nil {
		t.Fatalf("parse with passphrase: %v", err)
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("type = %q", signer.PublicKey().Type())
	}
}

func TestGenerateEd25519KeyFile_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a temp subdir we can clean up
	relPath := ".sshkey-test-tmp-" + t.Name() + "/key"
	tildePath := "~/" + relPath
	absPath := filepath.Join(home, relPath)
	defer os.RemoveAll(filepath.Dir(absPath))

	_, err := generateEd25519KeyFile(tildePath, "")
	if err != nil {
		t.Fatalf("generate with ~: %v", err)
	}

	// File should exist at absolute path
	if _, err := os.Stat(absPath); err != nil {
		t.Errorf("tilde not expanded: file not at %s: %v", absPath, err)
	}
}

func TestGenerateEd25519KeyFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "key")

	if _, err := generateEd25519KeyFile(nested, ""); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("key not created in nested dir: %v", err)
	}

	// Parent dir should be 0700 (os.MkdirAll uses mode 0700 in our helper)
	info, _ := os.Stat(filepath.Dir(nested))
	if info.Mode().Perm() != 0700 {
		t.Errorf("parent perms = %o, want 0700", info.Mode().Perm())
	}
}

func TestGenerateEd25519KeyFile_UniqueKeys(t *testing.T) {
	// Generate two keys, verify fingerprints differ
	dir := t.TempDir()
	fp1, _ := generateEd25519KeyFile(filepath.Join(dir, "k1"), "")
	fp2, _ := generateEd25519KeyFile(filepath.Join(dir, "k2"), "")

	if fp1 == fp2 {
		t.Error("two independent key generations produced identical fingerprints")
	}
	if fp1 == "" || fp2 == "" {
		t.Error("fingerprints should be non-empty")
	}
}
