package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAttachmentLocalPath_RejectsTraversal locks in the audit-F12 read-path
// guard. attachmentLocalPath turns a sender-supplied fileID into a local path
// that feeds SelectedImagePath → RenderImageInline (open + decode), so a
// traversal-shaped id must be rejected BEFORE the os.Stat — even when it would
// resolve to a real file *outside* filesDir. Without the guard, a hostile
// `../secret.png` would be stat'd, found, and the recipient's own file
// rendered in their view.
func TestAttachmentLocalPath_RejectsTraversal(t *testing.T) {
	tmp := t.TempDir()
	filesDir := filepath.Join(tmp, "files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A real file OUTSIDE filesDir that the traversal id resolves to:
	// filepath.Join(filesDir, "../secret.png") == <tmp>/secret.png.
	outside := filepath.Join(tmp, "secret.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewMessages()
	m.SetFilesDir(filesDir)

	// The traversal would resolve to an existing file, so a missing guard
	// is observable: attachmentLocalPath would return that path.
	if got := m.attachmentLocalPath("../secret.png"); got != "" {
		t.Errorf("attachmentLocalPath(%q) = %q, want \"\" — F12 guard must reject before stat", "../secret.png", got)
	}

	// Sanity: a legitimate cached id inside filesDir still resolves so the
	// guard hasn't broken normal inline-image rendering.
	good := filepath.Join(filesDir, "file_good")
	if err := os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.attachmentLocalPath("file_good"); got != good {
		t.Errorf("attachmentLocalPath(%q) = %q, want %q (valid cached id must still resolve)", "file_good", got, good)
	}
}
