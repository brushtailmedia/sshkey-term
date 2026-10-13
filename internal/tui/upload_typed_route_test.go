package tui

// upload-file-picker Step 2: the typed `/upload <path>` route normalizes
// drag-and-drop / tilde input, rejects directories with a friendly message,
// requires a live connection, and hands off to the shared startUpload.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypedUpload_QuotedTildePathUploads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(home, "My Photo.jpg")
	if err := os.WriteFile(file, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := newEditAppHarness(t) // live session: connected client, ConnStateOnline

	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Arg: "'~/My Photo.jpg'", Room: "room_x"})
	if a.statusBar.errorMsg != "Uploading My Photo.jpg..." {
		t.Fatalf("status = %q, want the upload to start on the normalized path", a.statusBar.errorMsg)
	}
}

func TestTypedUpload_DirectoryRejected(t *testing.T) {
	a, _ := newEditAppHarness(t)
	dir := t.TempDir()

	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Arg: dir, Room: "room_x"})
	if !strings.Contains(a.statusBar.errorMsg, "is a directory") {
		t.Fatalf("status = %q, want 'is a directory'", a.statusBar.errorMsg)
	}
}

func TestTypedUpload_MissingTildePathReportsExpandedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, _ := newEditAppHarness(t)

	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Arg: "~/missing.png", Room: "room_x"})
	want := "File not found: " + filepath.Join(home, "missing.png")
	if !strings.HasPrefix(a.statusBar.errorMsg, want) {
		t.Fatalf("status = %q, want prefix %q (expanded, not the literal ~)", a.statusBar.errorMsg, want)
	}
}

func TestTypedUpload_OfflineRefusesBeforeContextCheck(t *testing.T) {
	a, _ := newEditAppHarness(t)
	file := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(file, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.setConnState(ConnStateOffline)

	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Arg: file, Room: "room_x"})
	if a.statusBar.errorMsg != "offline - reconnect to upload files" {
		t.Fatalf("status = %q, want the offline message", a.statusBar.errorMsg)
	}
}

func TestTypedUpload_NoContextRefused(t *testing.T) {
	a, _ := newEditAppHarness(t)
	file := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(file, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Arg: file})
	if a.statusBar.errorMsg != "No active room or group" {
		t.Fatalf("status = %q, want the no-context message", a.statusBar.errorMsg)
	}
}

func TestTypedUpload_BareFormOpensPicker(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the picker's default root
	a, _ := newEditAppHarness(t)
	a.width, a.height = 100, 40
	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload", Room: "room_x"})
	if !a.filePicker.IsVisible() {
		t.Fatalf("bare /upload must open the file picker (was a silent no-op), status = %q", a.statusBar.errorMsg)
	}
	if strings.HasPrefix(a.statusBar.errorMsg, "Usage:") {
		t.Fatal("the usage line must not show for the bare form any more")
	}
}
