package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// First-connect failures happen before App receives ConnectedMsg, so a.client
// is still nil. The pending-approval overlay must still have a copyable public
// key by reading KeyPath+".pub" directly.
func TestApp_ErrMsg_FirstConnectFailureLoadsPublicKeyFromDisk(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	wantFP, err := generateEd25519KeyFile(keyPath, "", "wizarduser")
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	a := App{
		cfg:       client.Config{KeyPath: keyPath},
		statusBar: NewStatusBar(),
	}

	model, _ := a.Update(ErrMsg{Err: fmt.Errorf("key not authorized")})
	got := model.(App)

	if !got.connectFailed.IsVisible() {
		t.Fatal("connect-failed overlay should be visible")
	}
	if got.connectFailed.pubKey == "" {
		t.Fatal("connect-failed overlay missing public key fallback from disk")
	}
	if !strings.HasPrefix(got.connectFailed.pubKey, "ssh-ed25519 ") {
		t.Fatalf("pubKey = %q, want ssh-ed25519 line", got.connectFailed.pubKey)
	}
	if got.connectFailed.fingerprint != wantFP {
		t.Fatalf("fingerprint = %q, want %q", got.connectFailed.fingerprint, wantFP)
	}

	// [c] should now be actionable on this screen.
	cf, _ := got.connectFailed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !cf.copied {
		t.Fatal("pressing c should mark key as copied")
	}
}
