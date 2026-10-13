package tui

// upload-file-picker Step 1: the typed-route path normalizer and validator.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeUploadPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		in, want string
	}{
		{"'~/My File.png'", filepath.Join(home, "My File.png")},
		{`"~/x"`, filepath.Join(home, "x")},
		{"~", home},
		{"~/x", filepath.Join(home, "x")},
		{`My\ File.png`, "My File.png"},
		{`~/Pictures/My\ File.png`, filepath.Join(home, "Pictures", "My File.png")},
		{"~bob/x", "~bob/x"},
		{"$HOME/x", "$HOME/x"},
		{"/abs/x", "/abs/x"},
		{"  /abs/x  ", "/abs/x"},
		{"'", "'"}, // a lone quote is not a pair
	}
	for _, tc := range cases {
		if got := normalizeUploadPath(tc.in); got != tc.want {
			t.Errorf("normalizeUploadPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateUploadPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(file, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateUploadPath(file); err != nil {
		t.Fatalf("regular file must validate, got %v", err)
	}
	if err := validateUploadPath(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("directory must be rejected with 'is a directory', got %v", err)
	}

	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	missing := filepath.Join(dir, "missing.bin")
	err := validateUploadPath(missing)
	if err == nil || !strings.Contains(err.Error(), "File not found: "+missing) {
		t.Fatalf("missing file: got %v", err)
	}
	if strings.Contains(err.Error(), "scp") {
		t.Fatal("scp hint must not appear in a local session")
	}

	t.Setenv("SSH_TTY", "/dev/ttys001")
	err = validateUploadPath(missing)
	if err == nil || !strings.Contains(err.Error(), "scp") {
		t.Fatalf("remote session must carry the scp hint, got %v", err)
	}
}

func TestFilePickerDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := filePickerDefaultRoot(); got != home {
		t.Fatalf("with HOME set, root = %q, want %q", got, home)
	}

	t.Setenv("HOME", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := filePickerDefaultRoot(); got != wd {
		t.Fatalf("with HOME unset, root = %q, want cwd %q", got, wd)
	}
}
