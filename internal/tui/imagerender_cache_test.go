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
	oldDecode := decodeImageFile
	oldRender := renderImageFn
	defer func() {
		decodeImageFile = oldDecode
		renderImageFn = oldRender
		resetInlineImageRenderCache()
	}()

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
// the previous test: cached entries are NOT invalidated when the
// file's mod-time / size changes. Cached file paths are content-
// addressed by file_id (nanoid; same file_id → same path → same
// content), so a rewrite produces identical bytes and identical
// rendered output. The previous mod-time invalidation defeated the
// cache whenever multiple download paths raced to write the same
// file, producing the "freeze every scroll-back against large
// image" pathology.
func TestRenderImageInline_CacheSurvivesFileRewrite(t *testing.T) {
	oldDecode := decodeImageFile
	oldRender := renderImageFn
	defer func() {
		decodeImageFile = oldDecode
		renderImageFn = oldRender
		resetInlineImageRenderCache()
	}()

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

	if err := os.WriteFile(path, []byte("abcd"), 0600); err != nil {
		t.Fatalf("mutate temp image file: %v", err)
	}

	_ = RenderImageInline(path, 80, 15)
	if decodeCalls != 1 {
		t.Fatalf("decode calls after file rewrite = %d, want 1 (cache should survive rewrite)", decodeCalls)
	}
}

// TestRenderImageInline_PrefersThumbnail pins that RenderImageInline
// reads <filePath>.thumb_v2.png in preference to <filePath> when the
// thumbnail exists. Cold-start fast path: a previously-cached
// thumbnail decodes in ~10ms vs the original's multi-second decode +
// downscale, giving "no jarring delay on first render of an image
// you've seen before."
func TestRenderImageInline_PrefersThumbnail(t *testing.T) {
	oldDecode := decodeImageFile
	oldRender := renderImageFn
	defer func() {
		decodeImageFile = oldDecode
		renderImageFn = oldRender
		resetInlineImageRenderCache()
	}()

	resetInlineImageRenderCache()

	dir := t.TempDir()
	srcPath := dir + "/img.bin"
	thumbPath := srcPath + ".thumb_v2.png"

	if err := os.WriteFile(srcPath, []byte("ORIGINAL-DUMMY"), 0600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(thumbPath, []byte("THUMB-DUMMY"), 0600); err != nil {
		t.Fatalf("write thumb: %v", err)
	}

	var seenPaths []string
	decodeImageFile = func(p string) (image.Image, error) {
		seenPaths = append(seenPaths, p)
		return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	renderImageFn = func(_ image.Image, _, _ int) string { return "rendered" }

	_ = RenderImageInline(srcPath, 32, 8)

	if len(seenPaths) == 0 {
		t.Fatal("decodeImageFile was never called")
	}
	if seenPaths[0] != thumbPath {
		t.Errorf("first decode targeted %q, want %q (thumbnail must be preferred)",
			seenPaths[0], thumbPath)
	}
}
