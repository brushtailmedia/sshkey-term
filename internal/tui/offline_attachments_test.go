package tui

// Offline-browse post-landing fixes (R14): a cached attachment must preview
// and open without any server round-trip, an offline cache miss must say so
// in plain words (never the raw "not connected"), and online a cache miss
// must still download exactly as before.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// newOfflineAttachmentApp is an offline-browse App with a real per-server
// DataDir, so the files dir (and therefore attachmentCached) resolves.
func newOfflineAttachmentApp(t *testing.T) *App {
	t.Helper()
	a := newOfflineViewApp(t)
	a.cfg.DataDir = t.TempDir()
	a.hydrateOfflineUI() // re-run: messages.filesDir derives from cfg.DataDir
	return a
}

// cacheBlob drops a fake decrypted blob where DownloadFile would have
// written it, and returns that path.
func cacheBlob(t *testing.T, a *App, fileID string) string {
	t.Helper()
	path := config.AttachmentPath(a.cfg.DataDir, fileID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("decrypted-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func imageMessage(fileID string) DisplayMessage {
	return DisplayMessage{
		ID: "msg_img",
		Attachments: []DisplayAttachment{
			{FileID: fileID, Name: "pic.png", Mime: "image/png", IsImage: true},
		},
	}
}

func TestOfflinePreview_CachedBlobNeedsNoServer(t *testing.T) {
	a := newOfflineAttachmentApp(t)
	cacheBlob(t, a, "file_cached01")

	model, _ := a.Update(MessageAction{Action: "preview_attachment", Msg: imageMessage("file_cached01")})
	na := model.(App)
	if na.statusBar.errorMsg != "Preview ready: pic.png" {
		t.Fatalf("cached preview offline: status = %q, want Preview ready", na.statusBar.errorMsg)
	}
}

func TestOfflinePreview_UncachedBlobSaysReconnect(t *testing.T) {
	a := newOfflineAttachmentApp(t)

	model, _ := a.Update(MessageAction{Action: "preview_attachment", Msg: imageMessage("file_missing01")})
	na := model.(App)
	if !strings.Contains(na.statusBar.errorMsg, "not cached") || !strings.Contains(na.statusBar.errorMsg, "reconnect") {
		t.Fatalf("uncached preview offline: status = %q, want the not-cached / reconnect message", na.statusBar.errorMsg)
	}
	if strings.Contains(na.statusBar.errorMsg, "not connected") {
		t.Fatal("the raw ErrNotConnected text must not reach the user")
	}
}

func TestOfflineOpen_CachedBlobOpensWithoutServer(t *testing.T) {
	a := newOfflineAttachmentApp(t)
	want := cacheBlob(t, a, "file_cached02")

	opened := make(chan [2]string, 1)
	orig := openCachedAttachment
	openCachedAttachment = func(path, hint string) error {
		opened <- [2]string{path, hint}
		return nil
	}
	t.Cleanup(func() { openCachedAttachment = orig })

	model, _ := a.Update(MessageAction{Action: "open_attachment", Msg: imageMessage("file_cached02")})
	na := model.(App)
	select {
	case got := <-opened:
		if got[0] != want || got[1] != "pic.png" {
			t.Fatalf("opened %v, want path %q with hint pic.png", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cached open offline must hand the cached path to the OS open")
	}
	if strings.Contains(na.statusBar.errorMsg, "Downloading") || strings.Contains(na.statusBar.errorMsg, "not cached") {
		t.Fatalf("cached open must neither download nor complain: status = %q", na.statusBar.errorMsg)
	}
}

func TestOfflineOpen_UncachedBlobSaysReconnect(t *testing.T) {
	a := newOfflineAttachmentApp(t)
	called := false
	orig := openCachedAttachment
	openCachedAttachment = func(path, hint string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { openCachedAttachment = orig })

	model, _ := a.Update(MessageAction{Action: "open_attachment", Msg: imageMessage("file_missing02")})
	na := model.(App)
	if !strings.Contains(na.statusBar.errorMsg, "not cached") {
		t.Fatalf("uncached open offline: status = %q, want the not-cached message", na.statusBar.errorMsg)
	}
	if called {
		t.Fatal("nothing must be opened on a cache miss")
	}
}

// Online, a cache miss must still start the download exactly as before.
func TestOnlinePreview_UncachedStillDownloads(t *testing.T) {
	a, _ := newEditAppHarness(t) // live session: connected client, ConnStateOnline
	a.cfg.DataDir = t.TempDir()

	model, _ := a.Update(MessageAction{Action: "preview_attachment", Msg: imageMessage("file_missing03")})
	na := model.(App)
	if na.statusBar.errorMsg != "Loading preview of pic.png..." {
		t.Fatalf("online uncached preview must start a download: status = %q", na.statusBar.errorMsg)
	}
}
