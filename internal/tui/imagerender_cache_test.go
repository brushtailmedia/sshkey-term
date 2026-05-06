package tui

import (
	"image"
	"os"
	"testing"
)

func resetInlineImageRenderCache() {
	imageRenderCacheMu.Lock()
	defer imageRenderCacheMu.Unlock()
	imageRenderCache = make(map[imageRenderCacheKey]imageRenderCacheEntry)
	imageRenderTick = 0
}

func TestRenderImageInline_CachesStableFile(t *testing.T) {
	oldCap := termImageCapability
	oldChecked := termImageCapabilityChecked
	oldDecode := decodeImageFile
	oldRender := renderImageFn
	defer func() {
		termImageCapability = oldCap
		termImageCapabilityChecked = oldChecked
		decodeImageFile = oldDecode
		renderImageFn = oldRender
		resetInlineImageRenderCache()
	}()

	termImageCapability = "kitty"
	termImageCapabilityChecked = true
	resetInlineImageRenderCache()

	path := t.TempDir() + "/img.bin"
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatalf("write temp image file: %v", err)
	}

	decodeCalls := 0
	decodeImageFile = func(string) (image.Image, error) {
		decodeCalls++
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	renderImageFn = func(_ image.Image, _, _ int) string { return "rendered" }

	got1 := RenderImageInline(path, 80, 15)
	got2 := RenderImageInline(path, 80, 15)

	if got1 != "rendered" || got2 != "rendered" {
		t.Fatalf("unexpected render outputs: %q / %q", got1, got2)
	}
	if decodeCalls != 1 {
		t.Fatalf("decode calls = %d, want 1 (second call should hit cache)", decodeCalls)
	}
}

// TestRenderImageInline_CacheSurvivesFileRewrite pins the inverse of
// the previous test (whose name was a misnomer for the production
// design): cached entries are NOT invalidated when the file's
// mod-time / size changes. Since cached file paths are content-
// addressed by file_id (nanoid; same file_id → same path → same
// content), a rewrite of the same file produces identical bytes
// and identical rendered escape sequences. The previous mod-time
// invalidation defeated the cache whenever multiple download paths
// raced to write the same file, causing the user-visible "freeze
// every scroll-back" against multi-MB images. This test pins the
// post-fix behavior: re-rendering after a file change re-uses the
// cached result.
func TestRenderImageInline_CacheSurvivesFileRewrite(t *testing.T) {
	oldCap := termImageCapability
	oldChecked := termImageCapabilityChecked
	oldDecode := decodeImageFile
	oldRender := renderImageFn
	defer func() {
		termImageCapability = oldCap
		termImageCapabilityChecked = oldChecked
		decodeImageFile = oldDecode
		renderImageFn = oldRender
		resetInlineImageRenderCache()
	}()

	termImageCapability = "kitty"
	termImageCapabilityChecked = true
	resetInlineImageRenderCache()

	path := t.TempDir() + "/img.bin"
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatalf("write temp image file: %v", err)
	}

	decodeCalls := 0
	decodeImageFile = func(string) (image.Image, error) {
		decodeCalls++
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	renderImageFn = func(_ image.Image, _, _ int) string { return "rendered" }

	_ = RenderImageInline(path, 80, 15)
	if decodeCalls != 1 {
		t.Fatalf("decode calls after first render = %d, want 1", decodeCalls)
	}

	// Rewrite the file with different content. Under the prior
	// (mod-time-aware) cache, this would invalidate. Under the new
	// content-addressed-trust cache, the cached escape sequence is
	// re-used; no re-decode.
	if err := os.WriteFile(path, []byte("abcd"), 0600); err != nil {
		t.Fatalf("mutate temp image file: %v", err)
	}

	_ = RenderImageInline(path, 80, 15)
	if decodeCalls != 1 {
		t.Fatalf("decode calls after file rewrite = %d, want 1 (cache should survive rewrite)", decodeCalls)
	}
}
