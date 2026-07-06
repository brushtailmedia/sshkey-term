package tui

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"golang.org/x/crypto/ssh"
)

// Regression: the pending-approval overlay must show + copy the CURRENT server's
// public key, sourced from a.cfg.KeyPath — never from a.client. After Add Server
// / a server switch, a.client is the PREVIOUS server's closed-but-not-nilled
// client, and the overlay only renders while NOT connected, so trusting a.client
// surfaced the wrong server's key. This is the "adding a second server copies the
// first server's public key" bug.
func TestConnectFailedKeyInfo_UsesCurrentServerKeyPath(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "id_ed25519")

	// keyInfoFromPubPath reads only "<keyPath>.pub", so that's all we need.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	authLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if err := os.WriteFile(keyPath+".pub", []byte(authLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantFP := ssh.FingerprintSHA256(sshPub)

	// a.client is left nil — the fix makes the overlay independent of it; the key
	// must come from a.cfg.KeyPath regardless of any (stale) client.
	a := App{cfg: client.Config{KeyPath: keyPath}}
	gotFP, gotPub := a.connectFailedKeyInfo()

	if gotFP != wantFP {
		t.Errorf("fingerprint = %q, want %q (must come from a.cfg.KeyPath)", gotFP, wantFP)
	}
	if gotPub != authLine {
		t.Errorf("public key = %q, want %q (current server's key)", gotPub, authLine)
	}
}

// A missing .pub for the current server degrades gracefully: empty fingerprint +
// key (the caller substitutes "unknown" and the overlay omits the key/[c]).
func TestConnectFailedKeyInfo_MissingPubIsGraceful(t *testing.T) {
	a := App{cfg: client.Config{KeyPath: filepath.Join(t.TempDir(), "does_not_exist")}}
	fp, pub := a.connectFailedKeyInfo()
	if fp != "" || pub != "" {
		t.Errorf("missing .pub should yield empty fp/pub, got fp=%q pub=%q", fp, pub)
	}
}
