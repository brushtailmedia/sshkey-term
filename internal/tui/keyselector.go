// Package tui — keyselector.go scans SSH key directories for the
// Add Server and Wizard flows. The standalone KeySelector dialog
// model that previously lived here was never wired into the live
// UI (the actual key-listing UIs live inside AddServerModel and
// WizardModel directly), so it was removed during Phase 4 dead-
// code cleanup. What remains is the shared scan infrastructure:
// scanSSHKeys (the entry point), scanKeyDir (the per-directory
// walker), and the keyEntry result type.
package tui

import (
	"os"
	"path/filepath"
	"strings"
)

type keyEntry struct {
	Path string
	Type string // "ed25519", "rsa", etc.
}

// scanSSHKeys returns SSH keys discovered across two source kinds:
//
//  1. extraDirs — typically the per-server keys folders of each
//     already-configured server (`<configDir>/<host>/keys/`). Listed
//     first because these are the user's own previously-managed
//     keys, the most likely candidates for re-use when adding
//     another server. Caller computes the slice; the scanner just
//     walks what it's given.
//  2. ~/.ssh/   — the system SSH directory, listed second. Always
//     scanned. External input source — not an app-managed path.
//
// Within each source ed25519 keys come before other types (the
// protocol only accepts ed25519, but we surface unsupported types
// too so users see why a key they expected isn't usable).
//
// Missing directories are silently skipped — a fresh user won't have
// any per-server keys folders, and some users won't have `~/.ssh/`
// at all. Per-file read errors are also silently skipped (the file
// is omitted from results); a permission error on one key shouldn't
// hide all the others.
//
// Paths are deduplicated by absolute string match. Earlier entries
// (extraDirs first, then ~/.ssh) win in collisions — i.e. an app-
// managed copy of a system key reads as the managed entry.
func scanSSHKeys(extraDirs []string) []keyEntry {
	seen := make(map[string]bool)
	var all []keyEntry

	// Caller-provided managed dirs first — typically the per-server
	// keys folders of each already-configured server.
	for _, dir := range extraDirs {
		for _, k := range scanKeyDir(dir) {
			if seen[k.Path] {
				continue
			}
			seen[k.Path] = true
			all = append(all, k)
		}
	}

	// System ~/.ssh/ second. External input source (out of scope for
	// path centralization), so home resolution stays inline here.
	home, err := os.UserHomeDir()
	if err != nil {
		return all
	}
	sshDir := filepath.Join(home, ".ssh")
	for _, k := range scanKeyDir(sshDir) {
		if seen[k.Path] {
			continue
		}
		seen[k.Path] = true
		all = append(all, k)
	}

	return all
}

// scanKeyDir scans a single directory for SSH private key files,
// returning entries with ed25519 keys ordered before other types.
// Returns nil for missing directories — callers (and scanSSHKeys)
// silently skip; not having a directory is the expected case for
// fresh users, not an error condition.
//
// Filters out: subdirectories, .pub files (paired with their
// private counterparts), known_hosts, config, authorized_keys —
// the standard non-key files commonly found in ~/.ssh.
func scanKeyDir(dir string) []keyEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Includes the "directory doesn't exist" case (os.PathError
		// from ReadDir on a missing path). Silent skip.
		return nil
	}

	var keys []keyEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pub") || e.Name() == "known_hosts" || e.Name() == "config" || e.Name() == "authorized_keys" {
			continue
		}

		path := filepath.Join(dir, e.Name())

		// Quick check: read the file to verify it's a private key.
		// Read errors (permission denied, vanished file) skip the
		// entry rather than failing the whole scan.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		content := string(data)
		keyType := "unknown"
		if strings.Contains(content, "OPENSSH PRIVATE KEY") {
			// Determine the algorithm from the paired .pub file.
			// If .pub is missing/unreadable we still list the key
			// as "openssh" so the user sees it.
			pubData, err := os.ReadFile(path + ".pub")
			if err == nil {
				pub := string(pubData)
				if strings.HasPrefix(pub, "ssh-ed25519") {
					keyType = "ed25519"
				} else if strings.HasPrefix(pub, "ssh-rsa") {
					keyType = "rsa"
				} else if strings.HasPrefix(pub, "ecdsa") {
					keyType = "ecdsa"
				}
			} else {
				keyType = "openssh"
			}
		}

		keys = append(keys, keyEntry{Path: path, Type: keyType})
	}

	// Sort ed25519 first within this directory; preserves caller's
	// expectations from the previous single-directory implementation.
	sorted := make([]keyEntry, 0, len(keys))
	for _, k := range keys {
		if k.Type == "ed25519" {
			sorted = append(sorted, k)
		}
	}
	for _, k := range keys {
		if k.Type != "ed25519" {
			sorted = append(sorted, k)
		}
	}
	return sorted
}
