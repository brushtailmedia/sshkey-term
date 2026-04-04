package client

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hostKeyCallback returns an ssh.HostKeyCallback that implements TOFU.
// On first connect: stores the host key fingerprint.
// On subsequent connects: verifies the fingerprint matches.
func hostKeyCallback(dataDir, host string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)

		if dataDir == "" {
			// No data dir — accept all (testing)
			return nil
		}

		knownHostsPath := filepath.Join(dataDir, "known_host")

		// Try to read stored fingerprint
		stored, err := os.ReadFile(knownHostsPath)
		if err != nil {
			// First connect — store and accept
			if os.IsNotExist(err) {
				os.MkdirAll(filepath.Dir(knownHostsPath), 0700)
				content := fmt.Sprintf("%s %s %s\n", host, key.Type(), fingerprint)
				if err := os.WriteFile(knownHostsPath, []byte(content), 0600); err != nil {
					return fmt.Errorf("store host key: %w", err)
				}
				return nil
			}
			return fmt.Errorf("read known_host: %w", err)
		}

		// Verify against stored fingerprint
		storedStr := strings.TrimSpace(string(stored))
		parts := strings.Fields(storedStr)
		if len(parts) < 3 {
			// Malformed file — overwrite
			content := fmt.Sprintf("%s %s %s\n", host, key.Type(), fingerprint)
			os.WriteFile(knownHostsPath, []byte(content), 0600)
			return nil
		}

		storedFingerprint := parts[2]
		if storedFingerprint != fingerprint {
			return &HostKeyChangedError{
				Host:           host,
				OldFingerprint: storedFingerprint,
				NewFingerprint: fingerprint,
				Path:           knownHostsPath,
			}
		}

		return nil
	}
}

// HostKeyChangedError indicates the server's host key has changed since last connect.
type HostKeyChangedError struct {
	Host           string
	OldFingerprint string
	NewFingerprint string
	Path           string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf(
		"HOST KEY CHANGED for %s\n"+
			"  Old: %s\n"+
			"  New: %s\n"+
			"  This could indicate a man-in-the-middle attack.\n"+
			"  If the server key was legitimately changed, delete:\n"+
			"  %s",
		e.Host, e.OldFingerprint, e.NewFingerprint, e.Path,
	)
}
