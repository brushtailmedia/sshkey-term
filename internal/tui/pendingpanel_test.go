package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func samplePendingKeys() []protocol.PendingKeyEntry {
	return []protocol.PendingKeyEntry{
		{
			Fingerprint:       "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			Attempts:          3,
			FirstSeen:         "2026-05-25T10:00:00Z",
			LastSeen:          "2026-05-25T11:30:00Z",
			PubKey:            "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITESTKEYBYTESBYTESBYTES alice",
			RequestedUsername: "Alice",
		},
		{
			Fingerprint: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			Attempts:    1,
			FirstSeen:   "2026-05-25T09:00:00Z",
			LastSeen:    "2026-05-25T09:00:00Z",
			// No PubKey / RequestedUsername — legacy/empty entry.
		},
	}
}

func TestPendingPanel_RendersSelectedDetail(t *testing.T) {
	var p PendingPanelModel
	p.Show(samplePendingKeys())

	out := p.View(80, 30)
	// Requested name, full (untruncated) fingerprint, and the public key all
	// appear in the selected-row detail block.
	if !strings.Contains(out, "Alice") {
		t.Error("view should show the requested display name")
	}
	if !strings.Contains(out, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Error("detail block should show the full fingerprint")
	}
	if !strings.Contains(out, "ssh-ed25519") {
		t.Error("detail block should show the public key")
	}
}

func TestPendingPanel_CopyKeySetsNotice(t *testing.T) {
	var p PendingPanelModel
	p.Show(samplePendingKeys())

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if p.notice != "Public key copied to clipboard." {
		t.Errorf("notice = %q, want copy confirmation", p.notice)
	}
}

func TestPendingPanel_CopyNoKeyReportsUnavailable(t *testing.T) {
	var p PendingPanelModel
	p.Show(samplePendingKeys())

	// Move to the second entry, which has no stored public key.
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !strings.Contains(p.notice, "No public key") {
		t.Errorf("notice = %q, want 'no public key' message", p.notice)
	}
}

func TestPendingPanel_NavigationClearsNotice(t *testing.T) {
	var p PendingPanelModel
	p.Show(samplePendingKeys())

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if p.notice == "" {
		t.Fatal("precondition: notice should be set after copy")
	}
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.notice != "" {
		t.Errorf("notice should clear on navigation, got %q", p.notice)
	}
}

func TestPendingPanel_EmptyList(t *testing.T) {
	var p PendingPanelModel
	p.Show(nil)
	out := p.View(80, 30)
	if !strings.Contains(out, "No pending keys") {
		t.Error("empty list should render the no-keys message")
	}
}

func TestPendingPanel_ShowResetsCursor(t *testing.T) {
	var p PendingPanelModel
	p.Show(samplePendingKeys())
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != 1 {
		t.Fatalf("precondition: cursor should be 1, got %d", p.cursor)
	}
	// A refresh with a shorter list must reset the cursor so we never index
	// past the end.
	p.Show(samplePendingKeys()[:1])
	if p.cursor != 0 {
		t.Errorf("Show should reset cursor to 0, got %d", p.cursor)
	}
	// View must not panic with the reset cursor.
	_ = p.View(80, 30)
}

func TestPendingPanel_ViewClampsToHeight(t *testing.T) {
	keys := samplePendingKeys()
	for i := 0; i < 30; i++ {
		keys = append(keys, protocol.PendingKeyEntry{
			Fingerprint:       "SHA256:LONGENTRYLONGENTRYLONGENTRYLONGENTRY",
			Attempts:          i + 1,
			FirstSeen:         "2026-05-25T09:00:00Z",
			LastSeen:          "2026-05-25T10:00:00Z",
			PubKey:            "ssh-ed25519 " + strings.Repeat("A", 300) + " alice",
			RequestedUsername: "Alice",
		})
	}
	var p PendingPanelModel
	p.Show(keys)

	out := p.View(80, 12)
	if rows := strings.Count(out, "\n") + 1; rows > 12 {
		t.Fatalf("pending panel rows = %d, want <= 12\n%s", rows, out)
	}
	if !strings.Contains(out, "more below") {
		t.Fatalf("clamped list should show continuation marker:\n%s", out)
	}
}
