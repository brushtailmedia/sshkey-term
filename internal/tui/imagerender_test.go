package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

// withRastermProtocol overrides the cached rasterm protocol detection
// for the duration of a test, restoring on cleanup. Used by tests that
// need to exercise the rasterm branch without depending on the host
// terminal's actual capabilities (CI runners typically aren't kitty
// or iTerm2).
func withRastermProtocol(t *testing.T, p rastermProtocol) {
	t.Helper()
	prev := rastermProtocolCache
	rastermProtocolCache = p
	t.Cleanup(func() { rastermProtocolCache = prev })
}

// TestRastermCapable_FollowsCache verifies the public predicate
// reflects rastermProtocolCache. Sanity check before the
// branch-routing tests below assume override semantics work.
func TestRastermCapable_FollowsCache(t *testing.T) {
	withRastermProtocol(t, rastermNone)
	if rastermCapable() {
		t.Error("rastermCapable() should be false when cache is rastermNone")
	}
	withRastermProtocol(t, rastermKitty)
	if !rastermCapable() {
		t.Error("rastermCapable() should be true when cache is rastermKitty")
	}
}

// TestRastermDeleteEscape_ContainsImageID confirms the escape carries
// the placement-targeted delete (`d=I,i=<id>`) rather than the
// "delete every image on screen" form (`d=A`). Hard-coded so a future
// refactor that broadens the delete scope shows up loudly.
func TestRastermDeleteEscape_ContainsImageID(t *testing.T) {
	esc := rastermDeleteEscape()
	if !strings.Contains(esc, "a=d") {
		t.Error("delete escape should set a=d")
	}
	if !strings.Contains(esc, "d=I") {
		t.Error("delete escape should target by image-id (d=I), not all (d=A)")
	}
	if !strings.HasPrefix(esc, "\x1b_G") || !strings.HasSuffix(esc, "\x1b\\") {
		t.Errorf("delete escape should be wrapped in kitty graphics DCS (\\x1b_G ... \\x1b\\), got %q", esc)
	}
}

// writeImageWithThumbs creates a source PNG plus pre-existing
// thumbnail files for both the block-char and rasterm paths. The
// pre-existing thumbnails short-circuit the lazy thumbnail-generation
// goroutines that RenderImageInline normally fires on cache miss —
// without these, the goroutines race t.TempDir cleanup and produce
// flaky "directory not empty" failures. Same PNG bytes for src and
// thumbnails since the test only cares about decode + encode shape,
// not visual fidelity.
func writeImageWithThumbs(t *testing.T, dir string) string {
	t.Helper()
	pngBytes := smallPNGBytes()
	srcPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(srcPath, pngBytes, 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(srcPath+".thumb_v2.png", pngBytes, 0644); err != nil {
		t.Fatalf("write block-char thumb: %v", err)
	}
	if err := os.WriteFile(srcPath+".thumb_rasterm_v1.png", pngBytes, 0644); err != nil {
		t.Fatalf("write rasterm thumb: %v", err)
	}
	return srcPath
}

// withCleanInlineImageEnv unsets SSHKEY_NO_INLINE_IMAGES for the
// duration of a test and restores the original value on cleanup.
// Several rasterm tests need the inline-image path to actually run.
func withCleanInlineImageEnv(t *testing.T) {
	t.Helper()
	prev, set := os.LookupEnv("SSHKEY_NO_INLINE_IMAGES")
	t.Cleanup(func() {
		if set {
			os.Setenv("SSHKEY_NO_INLINE_IMAGES", prev)
		} else {
			os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")
		}
	})
	os.Unsetenv("SSHKEY_NO_INLINE_IMAGES")
}

// TestRenderImageInline_RastermBranchUsedWhenCapable verifies that
// when rastermProtocolCache is set to a kitty/iterm protocol, the
// rendered output contains the rasterm escape header rather than
// block-character cells. The actual encoded bytes vary by image
// content, but the protocol prefix is stable.
func TestRenderImageInline_RastermBranchUsedWhenCapable(t *testing.T) {
	resetInlineImageRenderCache()
	withRastermProtocol(t, rastermKitty)
	withCleanInlineImageEnv(t)

	srcPath := writeImageWithThumbs(t, t.TempDir())

	out := RenderImageInline(srcPath, 20, 12)
	if out == "" {
		t.Fatal("RenderImageInline returned empty output for valid PNG with rasterm capable")
	}
	// Kitty-protocol output starts with `\x1b_G` (the DCS header rasterm
	// emits via KITTY_IMG_HDR). Block-char output uses ANSI SGR sequences
	// (`\x1b[`) plus block runes — disjoint from the kitty header.
	if !strings.HasPrefix(out, "\x1b_G") {
		t.Errorf("rasterm-capable render should produce kitty escape, got prefix %q",
			out[:min(20, len(out))])
	}
	if !strings.Contains(out, "C=1") {
		t.Error("kitty raster output should set C=1 (do not move cursor)")
	}
}

// TestRenderImageInline_FallsBackToBlockCharsWhenNotCapable verifies
// that when rastermProtocolCache is rastermNone, RenderImageInline
// runs the existing block-char path — rendering is non-empty and
// does NOT carry the kitty DCS prefix.
func TestRenderImageInline_FallsBackToBlockCharsWhenNotCapable(t *testing.T) {
	resetInlineImageRenderCache()
	withRastermProtocol(t, rastermNone)
	withCleanInlineImageEnv(t)

	srcPath := writeImageWithThumbs(t, t.TempDir())

	out := RenderImageInline(srcPath, 20, 12)
	if out == "" {
		t.Fatal("RenderImageInline returned empty output for valid PNG with block-char path")
	}
	if strings.HasPrefix(out, "\x1b_G") {
		t.Errorf("rasterm-disabled render should not emit kitty escape, got prefix %q",
			out[:min(20, len(out))])
	}
}

// TestRenderImageInline_CacheKeyHonorsRastermBool verifies that
// switching encoders mid-session doesn't return stale cached output.
// First render under rasterm caches the kitty-encoded bytes; second
// render with the same path/dims/truecolor but rasterm disabled must
// re-render via block-char (different cache slot).
func TestRenderImageInline_CacheKeyHonorsRastermBool(t *testing.T) {
	resetInlineImageRenderCache()
	withCleanInlineImageEnv(t)

	srcPath := writeImageWithThumbs(t, t.TempDir())

	withRastermProtocol(t, rastermKitty)
	rastermOut := RenderImageInline(srcPath, 20, 12)
	if !strings.HasPrefix(rastermOut, "\x1b_G") {
		t.Fatalf("first render: expected kitty prefix, got %q", rastermOut[:min(20, len(rastermOut))])
	}

	withRastermProtocol(t, rastermNone)
	blockOut := RenderImageInline(srcPath, 20, 12)
	if strings.HasPrefix(blockOut, "\x1b_G") {
		t.Errorf("second render with rasterm disabled returned kitty escape — cache key not honoring rasterm bool")
	}
	if blockOut == "" {
		t.Error("second render returned empty — block-char path didn't fire")
	}
}

// TestRenderImageInline_ItermBranchIncludesPaneBounds verifies the
// iTerm/WezTerm rasterm branch encodes explicit width/height in cell
// units, keeping graphics confined to the preview-pane dimensions.
func TestRenderImageInline_ItermBranchIncludesPaneBounds(t *testing.T) {
	resetInlineImageRenderCache()
	withRastermProtocol(t, rastermIterm)
	withCleanInlineImageEnv(t)

	srcPath := writeImageWithThumbs(t, t.TempDir())

	out := RenderImageInline(srcPath, 20, 12)
	if out == "" {
		t.Fatal("RenderImageInline returned empty output for valid PNG with iTerm rasterm capable")
	}
	if !strings.HasPrefix(out, "\x1b]1337;File=") {
		t.Fatalf("expected iTerm inline-image prefix, got %q", out[:min(32, len(out))])
	}
	// Square source image in a 20x12 pane should fit to 20x10 under the
	// 1:2 cell aspect model used by renderer sizing.
	if !strings.Contains(out, "width=20") {
		t.Error("iTerm inline output missing width=20 option")
	}
	if !strings.Contains(out, "height=10") {
		t.Error("iTerm inline output missing aspect-fit height=10 option")
	}
}

// smallPNGBytes returns a valid 4×4 red PNG, encoded via the standard
// image/png path. Used by the rasterm + block-char render tests as a
// minimal real image — small enough that the encode/decode round-trip
// is sub-millisecond, large enough that thumbnail downscale logic
// (which assumes >0 dimensions) doesn't degenerate.
func smallPNGBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
