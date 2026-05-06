package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAttachmentLocalPath_EmptyFilesDir verifies the render helper returns
// "" when the model hasn't been wired to a files directory — e.g. before
// SetFilesDir is called during the handshake. Must never return a raw
// filesystem path from the process working directory in this state.
func TestAttachmentLocalPath_EmptyFilesDir(t *testing.T) {
	m := NewMessages()
	got := m.attachmentLocalPath("file_anything")
	if got != "" {
		t.Errorf("attachmentLocalPath with empty filesDir = %q, want \"\"", got)
	}
}

// TestAttachmentLocalPath_EmptyFileID verifies the render helper short-
// circuits on an empty file_id without any filesystem lookup.
func TestAttachmentLocalPath_EmptyFileID(t *testing.T) {
	m := NewMessages()
	m.SetFilesDir(t.TempDir())
	got := m.attachmentLocalPath("")
	if got != "" {
		t.Errorf("attachmentLocalPath with empty fileID = %q, want \"\"", got)
	}
}

// TestAttachmentLocalPath_CacheMiss verifies the helper returns "" when
// the file_id has no corresponding file in the cache directory. This is
// the pre-download / post-eviction state — render must fall through to
// the 🖼 placeholder path.
func TestAttachmentLocalPath_CacheMiss(t *testing.T) {
	m := NewMessages()
	m.SetFilesDir(t.TempDir())
	got := m.attachmentLocalPath("file_not_downloaded")
	if got != "" {
		t.Errorf("attachmentLocalPath on cache miss = %q, want \"\"", got)
	}
}

// TestAttachmentLocalPath_CacheHit verifies the helper returns the
// deterministic path <filesDir>/<fileID> when the file exists on disk.
// This is the post-download state that unlocks the inline-render branch
// in the View function.
func TestAttachmentLocalPath_CacheHit(t *testing.T) {
	dir := t.TempDir()
	m := NewMessages()
	m.SetFilesDir(dir)

	fileID := "file_cached_locally"
	cached := filepath.Join(dir, fileID)
	if err := os.WriteFile(cached, []byte("pretend-decrypted-image-bytes"), 0600); err != nil {
		t.Fatalf("seed cache file: %v", err)
	}

	got := m.attachmentLocalPath(fileID)
	if got != cached {
		t.Errorf("attachmentLocalPath cache hit = %q, want %q", got, cached)
	}
}

// TestRenderImageInline_MalformedBytesDoesNotPanic verifies the defer-
// recover wrapper in RenderImageInline catches decoder panics from
// crafted-image payloads and returns "" rather than crashing the TUI.
// The size threshold is the primary defense against crafted images
// reaching the decoder, but this is the belt-and-braces layer in case
// a user manually opens one.
func TestRenderImageInline_MalformedBytesDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.png")
	malformed := []byte{0x89, 'P', 'N', 'G', 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF}
	if err := os.WriteFile(path, malformed, 0600); err != nil {
		t.Fatalf("write malformed fixture: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderImageInline panicked on malformed bytes: %v", r)
		}
	}()

	got := RenderImageInline(path, 40, 12)
	if got != "" {
		t.Logf("RenderImageInline returned non-empty on malformed bytes (len=%d) — acceptable provided no panic", len(got))
	}
}

// TestCanRenderImages_DiagnosticDisable pins the env-var override.
// SSHKEY_NO_INLINE_IMAGES=1 must force the placeholder path so users
// (and us in triage) can isolate inline-image-rendering behavior
// without rebuilding.
func TestCanRenderImages_DiagnosticDisable(t *testing.T) {
	prev, set := os.LookupEnv("SSHKEY_NO_INLINE_IMAGES")
	defer func() {
		if set {
			os.Setenv("SSHKEY_NO_INLINE_IMAGES", prev)
		} else {
			os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")
		}
	}()

	os.Setenv("SSHKEY_NO_INLINE_IMAGES", "1")
	if CanRenderImages() {
		t.Error("CanRenderImages with SSHKEY_NO_INLINE_IMAGES=1 returned true, want false")
	}

	os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")
	if !CanRenderImages() {
		t.Error("CanRenderImages with env var unset returned false, want true (block-cell rendering uses universal ANSI escapes)")
	}
}

// TestUseTrueColor_DetectsCOLORTERM pins the truecolor detection logic.
// Modern terminals set $COLORTERM=truecolor or =24bit; we use truecolor
// escapes for them and 256-color escapes for everything else.
func TestUseTrueColor_DetectsCOLORTERM(t *testing.T) {
	prev, set := os.LookupEnv("COLORTERM")
	defer func() {
		if set {
			os.Setenv("COLORTERM", prev)
		} else {
			os.Unsetenv("COLORTERM")
		}
	}()

	cases := []struct {
		val  string
		want bool
	}{
		{"truecolor", true},
		{"24bit", true},
		{"", false},
		{"256color", false},
		{"unknown", false},
	}

	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			if tc.val == "" {
				os.Unsetenv("COLORTERM")
			} else {
				os.Setenv("COLORTERM", tc.val)
			}
			got := useTrueColor()
			if got != tc.want {
				t.Errorf("useTrueColor with COLORTERM=%q = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestRenderImageInline_MissingFile pins the file-missing failure mode.
// The os.Stat error in RenderImageInline must collapse to "" so the
// caller renders the placeholder instead of spewing garbage or panicking.
func TestRenderImageInline_MissingFile(t *testing.T) {
	prev, set := os.LookupEnv("SSHKEY_NO_INLINE_IMAGES")
	defer func() {
		if set {
			os.Setenv("SSHKEY_NO_INLINE_IMAGES", prev)
		} else {
			os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")
		}
	}()
	os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")

	got := RenderImageInline("/definitely/does/not/exist/photo.png", 40, 12)
	if got != "" {
		t.Errorf("RenderImageInline on missing file = %d bytes, want \"\"", len(got))
	}
}
