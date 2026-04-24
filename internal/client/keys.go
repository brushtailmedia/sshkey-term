package client

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// PassphraseFunc is called when a key requires a passphrase.
// It should prompt the user and return the passphrase bytes.
type PassphraseFunc func() ([]byte, error)

// sniffKeyFileFormat inspects the first line of a key file and returns a
// helpful error if the file is not an OpenSSH private key (e.g. public key,
// SSH certificate, RSA/ECDSA key, or unknown format). Returns nil if the
// file looks like it could be an OpenSSH private key (actual validation
// happens when ssh.ParsePrivateKey parses it).
//
// sshkey-chat requires raw Ed25519 identities. SSH certificates are
// rejected because they carry validity windows and expire — identities in
// this system must be permanent (see PROJECT.md "Account Retirement").
func sniffKeyFileFormat(path string, data []byte) error {
	// Read the first line
	firstLine := string(data)
	if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
		firstLine = string(data[:idx])
	}
	firstLine = strings.TrimSpace(firstLine)

	// SSH certificate — has a validity window, not supported
	if strings.HasPrefix(firstLine, "ssh-ed25519-cert-v01@openssh.com") ||
		strings.HasPrefix(firstLine, "ssh-rsa-cert-v01@openssh.com") ||
		strings.HasPrefix(firstLine, "ecdsa-sha2-") && strings.Contains(firstLine, "-cert-v01@openssh.com") {
		return fmt.Errorf("%s is an SSH certificate, not an identity. sshkey-chat requires a raw Ed25519 private key — certificates carry expiry dates and are not supported. Please point to your private key file (usually the path without .pub)", path)
	}

	// Public key file — one-line, starts with the key type
	if strings.HasPrefix(firstLine, "ssh-ed25519 ") ||
		strings.HasPrefix(firstLine, "ssh-rsa ") ||
		strings.HasPrefix(firstLine, "ssh-dss ") ||
		strings.HasPrefix(firstLine, "ecdsa-sha2-") {
		return fmt.Errorf("%s looks like an SSH public key. You need to point to your PRIVATE key instead (typically the same path without the .pub extension)", path)
	}

	// Check for non-OpenSSH private key formats (RSA, DSA, etc.)
	head := string(data)
	if len(head) > 200 {
		head = head[:200]
	}
	if strings.Contains(head, "-----BEGIN RSA PRIVATE KEY-----") ||
		strings.Contains(head, "-----BEGIN DSA PRIVATE KEY-----") ||
		strings.Contains(head, "-----BEGIN EC PRIVATE KEY-----") {
		return fmt.Errorf("%s is a non-Ed25519 private key (RSA/DSA/ECDSA). sshkey-chat only supports Ed25519 — generate a new Ed25519 key via the setup wizard or `ssh-keygen -t ed25519`", path)
	}

	return nil
}

// loadSSHKey reads and parses an SSH private key file.
// If the key is passphrase-protected, calls the passphrase function.
func loadSSHKey(path string, passphraseFn ...PassphraseFunc) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	if err := sniffKeyFileFormat(path, data); err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Check if it's a passphrase error
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			if len(passphraseFn) == 0 || passphraseFn[0] == nil {
				return nil, fmt.Errorf("key is passphrase-protected but no passphrase provided")
			}
			passphrase, err := passphraseFn[0]()
			if err != nil {
				return nil, fmt.Errorf("get passphrase: %w", err)
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, passphrase)
			if err != nil {
				return nil, fmt.Errorf("parse key with passphrase: %w", err)
			}
		} else {
			return nil, fmt.Errorf("parse key: %w", err)
		}
	}

	// Verify it's Ed25519
	if signer.PublicKey().Type() != "ssh-ed25519" {
		return nil, fmt.Errorf("only Ed25519 keys are supported, got %s", signer.PublicKey().Type())
	}

	return signer, nil
}

// ParseRawEd25519Key reads a key file and returns the raw ed25519 private key.
func ParseRawEd25519Key(path string, passphraseFn ...PassphraseFunc) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := sniffKeyFileFormat(path, data); err != nil {
		return nil, err
	}

	rawKey, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			if len(passphraseFn) == 0 || passphraseFn[0] == nil {
				return nil, fmt.Errorf("key is passphrase-protected but no passphrase provided")
			}
			passphrase, err := passphraseFn[0]()
			if err != nil {
				return nil, err
			}
			rawKey, err = ssh.ParseRawPrivateKeyWithPassphrase(data, passphrase)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	edKey, ok := rawKey.(*ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 key")
	}

	return *edKey, nil
}
