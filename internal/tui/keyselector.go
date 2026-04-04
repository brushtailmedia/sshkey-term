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

// scanSSHKeys looks for SSH keys in ~/.ssh/
func scanSSHKeys() []keyEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []keyEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pub") || e.Name() == "known_hosts" || e.Name() == "config" || e.Name() == "authorized_keys" {
			continue
		}

		path := filepath.Join(sshDir, e.Name())

		// Quick check: read first line to determine key type
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		content := string(data)
		keyType := "unknown"
		if strings.Contains(content, "OPENSSH PRIVATE KEY") {
			// Need to check the .pub file for the type
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
				keyType = "openssh" // can't determine without .pub
			}
		}

		// Only show ed25519 keys (others are listed but marked unsupported)
		keys = append(keys, keyEntry{Path: path, Type: keyType})
	}

	// Sort ed25519 keys first
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
