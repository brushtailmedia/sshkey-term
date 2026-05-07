package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KeySelectorModel shows available SSH keys on first launch.
type KeySelectorModel struct {
	visible bool
	keys    []keyEntry
	cursor  int
}

type keyEntry struct {
	Path string
	Type string // "ed25519", "rsa", etc.
}

// KeySelectedMsg is sent when the user selects a key.
type KeySelectedMsg struct {
	Path string
}

func NewKeySelector() KeySelectorModel {
	return KeySelectorModel{}
}

func (k *KeySelectorModel) Show() {
	k.visible = true
	k.cursor = 0
	k.keys = scanSSHKeys()
}

func (k *KeySelectorModel) Hide() {
	k.visible = false
}

func (k *KeySelectorModel) IsVisible() bool {
	return k.visible
}

func (k KeySelectorModel) Update(msg tea.KeyMsg) (KeySelectorModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		k.Hide()
		return k, tea.Quit
	case "up", "k":
		if k.cursor > 0 {
			k.cursor--
		}
	case "down", "j":
		if k.cursor < len(k.keys)-1 {
			k.cursor++
		}
	case "enter":
		if k.cursor < len(k.keys) {
			entry := k.keys[k.cursor]
			k.Hide()
			return k, func() tea.Msg {
				return KeySelectedMsg{Path: entry.Path}
			}
		}
	}
	return k, nil
}

func (k KeySelectorModel) View(width int) string {
	if !k.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Welcome to sshkey-chat"))
	b.WriteString("\n\n")
	b.WriteString("  Select your SSH key:\n\n")

	if len(k.keys) == 0 {
		b.WriteString("  No Ed25519 keys found in ~/.ssh/\n\n")
		b.WriteString("  Generate one with:\n")
		b.WriteString("  " + helpKeyStyle.Render("ssh-keygen -t ed25519") + "\n")
	} else {
		for i, key := range k.keys {
			line := "  " + key.Path
			if key.Type != "ed25519" {
				line += helpDescStyle.Render(fmt.Sprintf(" (%s — not supported)", key.Type))
			}

			if i == k.cursor {
				line = completionSelectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  ↑/↓=navigate  Enter=select  Esc=quit"))

	return dialogStyle.Width(width - 4).Render(b.String())
}

// scanSSHKeys returns SSH keys discovered in two locations,
// concatenated in this order:
//
//  1. ~/.sshkey-term/keys/  — app-managed (listed first; these are
//     keys generated via the wizard or add-server flow, most likely
//     candidates for reuse with another sshkey-chat server)
//  2. ~/.ssh/               — system SSH directory (listed second)
//
// Within each source ed25519 keys come before other types (the
// protocol only accepts ed25519, but we surface unsupported types
// too so users see why a key they expected isn't usable).
//
// Missing directories are silently skipped — most users won't have
// `~/.sshkey-term/keys/` until they've gone through the wizard, and
// some users won't have `~/.ssh/` at all. Per-file read errors are
// also silently skipped (the file is omitted from results); a
// permission error on one key shouldn't hide all the others.
//
// Paths are deduplicated by absolute string match: if the same path
// appears in both directories (e.g. via symlink), the managed-folder
// occurrence wins.
func scanSSHKeys() []keyEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var all []keyEntry

	// Managed folder first — app-generated keys.
	managedDir := filepath.Join(home, ".sshkey-term", "keys")
	for _, k := range scanKeyDir(managedDir) {
		if seen[k.Path] {
			continue
		}
		seen[k.Path] = true
		all = append(all, k)
	}

	// System ~/.ssh second.
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
