package client

import (
	"crypto/ed25519"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

// PassphraseFunc is called when a key requires a passphrase.
// It should prompt the user and return the passphrase bytes.
type PassphraseFunc func() ([]byte, error)

// loadSSHKey reads and parses an SSH private key file.
// If the key is passphrase-protected, calls the passphrase function.
func loadSSHKey(path string, passphraseFn ...PassphraseFunc) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
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

// extractEd25519Key extracts the raw ed25519.PrivateKey from an ssh.Signer.
func extractEd25519Key(signer ssh.Signer) (ed25519.PrivateKey, error) {
	// ssh.Signer wraps the key — we need the underlying crypto key
	type cryptoSigner interface {
		Sign(rand interface{}, data []byte) ([]byte, error)
	}

	// Try to get the public key and work backwards
	pub := signer.PublicKey()
	cryptoPub, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil, fmt.Errorf("key does not implement CryptoPublicKey")
	}

	edPub, ok := cryptoPub.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 key")
	}

	// The signer should also give us access to the private key
	// Use the AlgorithmSigner interface to sign a test message and verify
	// Actually, we need the raw private key for crypto operations.
	// Parse the key file directly to get it.
	_ = edPub

	// Re-read and parse to get the raw key
	// This is a limitation — we need the raw ed25519.PrivateKey for X25519 conversion
	return nil, fmt.Errorf("direct key extraction not available — use parseRawKey")
}

// ParseRawEd25519Key reads a key file and returns the raw ed25519 private key.
func ParseRawEd25519Key(path string, passphraseFn ...PassphraseFunc) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
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
