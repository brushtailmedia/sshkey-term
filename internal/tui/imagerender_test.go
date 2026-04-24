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
	// Random bytes that claim to be a PNG but aren't — image.Decode
	// should return an error, which RenderImageInline treats as a
	// render miss. If a future decoder bug panics instead, the
	// deferred recover catches it and we still return "".
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

	got := RenderImageInline(path, 80, 40)
	if got != "" {
		// A terminal without image capability also returns "" — either
		// outcome is acceptable; the important invariant is no panic.
		t.Logf("RenderImageInline returned non-empty on malformed bytes (len=%d) — acceptable if tests run under a capable terminal", len(got))
	}
}

// withTermCapability temporarily overrides the package-level
// termImageCapability cache for the duration of a test, restoring the
// original via t.Cleanup. Tests MUST NOT run in parallel when using
// this helper because the cache is package-scope global state — any
// t.Parallel() call in the same package's tests could race with the
// override. None of the existing imagerender tests use t.Parallel, and
// the helper intentionally doesn't call it either.
func withTermCapability(t *testing.T, cap string) {
	t.Helper()
	prev := termImageCapability
	termImageCapability = cap
	t.Cleanup(func() { termImageCapability = prev })
}

// TestRenderImageInline_NoCapabilityReturnsEmpty pins the core "no
// terminal image protocol = placeholder" invariant: if the terminal
// can't render images, RenderImageInline must return "" regardless of
// the file's state (missing, corrupt, valid, zero-byte). This is what
// makes Terminal.app / Windows Terminal / vanilla xterm / piped output
// all degrade cleanly to the 🖼 placeholder.
//
// The test forces termImageCapability = "" to simulate no-capability,
// which also forces CanRenderImages() to attempt its lazy sixel probe.
// The probe internally guards on os.Stdout being a TTY, and `go test`
// captures stdout to a buffer (not a TTY), so the probe returns false
// quickly without sending escape bytes anywhere. No real terminal
// interaction happens.
func TestRenderImageInline_NoCapabilityReturnsEmpty(t *testing.T) {
	withTermCapability(t, "")

	dir := t.TempDir()

	cases := []struct {
		name      string
		setup     func() string
	}{
		{
			name: "missing file",
			setup: func() string {
				return filepath.Join(dir, "does_not_exist.png")
			},
		},
		{
			name: "zero-byte file",
			setup: func() string {
				p := filepath.Join(dir, "empty.png")
				if err := os.WriteFile(p, nil, 0600); err != nil {
					t.Fatalf("seed empty: %v", err)
				}
				return p
			},
		},
		{
			name: "corrupt bytes",
			setup: func() string {
				p := filepath.Join(dir, "corrupt.png")
				if err := os.WriteFile(p, []byte{0x00, 0x01, 0x02, 0x03}, 0600); err != nil {
					t.Fatalf("seed corrupt: %v", err)
				}
				return p
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup()
			got := RenderImageInline(path, 80, 40)
			if got != "" {
				t.Errorf("RenderImageInline with no capability returned %d bytes, want \"\"", len(got))
			}
		})
	}
}

// TestRenderImageInline_MissingFileWithCapability verifies the file-
// missing failure mode with a capability forced on. The Open error at
// imagerender.go:63-66 must collapse to "" so the caller renders the
// placeholder instead of spewing garbage or panicking. Without forcing
// the capability we couldn't distinguish "no capability short-circuit"
// from "capability present but file missing."
//
// We pick "kitty" as the forced capability because it's env-var-driven
// in rasterm (no escape-code interaction) so overriding it is inert.
func TestRenderImageInline_MissingFileWithCapability(t *testing.T) {
	withTermCapability(t, "kitty")

	got := RenderImageInline("/definitely/does/not/exist/photo.png", 80, 40)
	if got != "" {
		t.Errorf("RenderImageInline on missing file = %d bytes, want \"\"", len(got))
	}
}

// TestCanRenderImages_CachedCapabilitySkipsProbe verifies the
// short-circuit at imagerender.go:33-35: once termImageCapability is
// set (by the init() detector or a previous lazy probe), subsequent
// CanRenderImages calls must return true without re-probing. Probe
// re-entry inside a Bubble Tea alt-screen frame is the exact scenario
// where a stray terminal response could interleave with keyboard
// input; the cache prevents it.
func TestCanRenderImages_CachedCapabilitySkipsProbe(t *testing.T) {
	for _, cap := range []string{"kitty", "iterm", "sixel"} {
		t.Run(cap, func(t *testing.T) {
			withTermCapability(t, cap)
			if !CanRenderImages() {
				t.Errorf("CanRenderImages with cached %q capability returned false", cap)
			}
		})
	}
}
