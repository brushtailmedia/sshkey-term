package client

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateThumbnail_DownscalesLargeImage pins the core
// behavior: an oversized source produces a thumbnail at most
// thumbnailMaxPixelW × thumbnailMaxPixelH while preserving aspect
// ratio. This is the load-bearing fix for the
// "scroll-back-with-large-image freezes the TUI" bug — the
// downscaled thumbnail is what RenderImageInline encodes into the
// terminal escape sequence on subsequent renders.
func TestGenerateThumbnail_DownscalesLargeImage(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.png")
	dstPath := filepath.Join(dir, "src.png.thumb_v2.png")

	// 4032x3024 = a typical phone-camera resolution. After downscale
	// to fit the 80×24 cap, expect a 4:3-preserving result that hits
	// the height cap (24) with proportional width (~32), so the
	// output is at most 80×24 and exactly 24 tall.
	src := image.NewRGBA(image.Rect(0, 0, 4032, 3024))
	if err := writePNG(srcPath, src); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := GenerateThumbnail(srcPath, dstPath); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}

	thumb, err := readPNG(dstPath)
	if err != nil {
		t.Fatalf("read thumbnail: %v", err)
	}
	tw := thumb.Bounds().Dx()
	th := thumb.Bounds().Dy()
	if tw > thumbnailMaxPixelW || th > thumbnailMaxPixelH {
		t.Errorf("thumbnail %d×%d exceeds %d×%d cap", tw, th, thumbnailMaxPixelW, thumbnailMaxPixelH)
	}
	// 4:3 aspect → height should hit the cap, width comes out narrower.
	if th != thumbnailMaxPixelH {
		t.Errorf("thumbnail height = %d, want %d (4:3 source should be height-bound)", th, thumbnailMaxPixelH)
	}
}

// TestGenerateThumbnail_PortraitAspectRatio verifies portrait inputs
// produce narrow-tall thumbnails (height-bound).
func TestGenerateThumbnail_PortraitAspectRatio(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "p.png")
	dstPath := filepath.Join(dir, "p.png.thumb_v2.png")

	// 3024x4032 = phone portrait. Aspect 3:4. Downscaled to fit the
	// 80×24 cap → 18×24 (height hits cap, width is height × 3/4).
	src := image.NewRGBA(image.Rect(0, 0, 3024, 4032))
	if err := writePNG(srcPath, src); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := GenerateThumbnail(srcPath, dstPath); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}

	thumb, err := readPNG(dstPath)
	if err != nil {
		t.Fatalf("read thumbnail: %v", err)
	}
	tw := thumb.Bounds().Dx()
	th := thumb.Bounds().Dy()
	if tw > thumbnailMaxPixelW || th > thumbnailMaxPixelH {
		t.Errorf("thumbnail %d×%d exceeds %d×%d cap", tw, th, thumbnailMaxPixelW, thumbnailMaxPixelH)
	}
	if th != thumbnailMaxPixelH {
		t.Errorf("thumbnail height = %d, want %d (portrait should be height-bound)", th, thumbnailMaxPixelH)
	}
	// Portrait should be narrow.
	if tw >= th {
		t.Errorf("portrait thumbnail tw=%d th=%d — expected tw < th", tw, th)
	}
}

// TestGenerateThumbnail_SkipsAlreadyPresent verifies idempotency:
// calling GenerateThumbnail with an existing destination is a no-op
// (no decode, no write). Important because both eager paths
// (DownloadFile + uploadEncrypted) and the lazy path
// (RenderImageInline) can race on the same dstPath; subsequent
// callers must not overwrite or fail.
func TestGenerateThumbnail_SkipsAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "skip.png")
	dstPath := filepath.Join(dir, "preexisting.png")

	src := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	if err := writePNG(srcPath, src); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Pre-write a "thumbnail" with sentinel content.
	sentinel := []byte("PRE-EXISTING")
	if err := os.WriteFile(dstPath, sentinel, 0600); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	if err := GenerateThumbnail(srcPath, dstPath); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(sentinel) {
		t.Errorf("dst was overwritten — got %q, want %q (no-op when present)", got, sentinel)
	}
}

// TestGenerateThumbnail_PassesThroughSmallImage verifies images
// already smaller than the cap are written through without resizing.
// Avoids an extra encode pass and avoids upscaling artifacts.
func TestGenerateThumbnail_PassesThroughSmallImage(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "tiny.png")
	dstPath := filepath.Join(dir, "tiny.png.thumb_v2.png")

	// 16×16 — well below the 80×24 thumbnail cap.
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	if err := writePNG(srcPath, src); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := GenerateThumbnail(srcPath, dstPath); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}

	thumb, err := readPNG(dstPath)
	if err != nil {
		t.Fatalf("read thumb: %v", err)
	}
	if thumb.Bounds().Dx() != 16 || thumb.Bounds().Dy() != 16 {
		t.Errorf("small image was resized: got %d×%d, want 16×16",
			thumb.Bounds().Dx(), thumb.Bounds().Dy())
	}
}

// TestThumbnailPath pins the suffix convention — important because
// both the writer (GenerateThumbnail) and the reader
// (tui.RenderImageInline) must agree on the path.
func TestThumbnailPath(t *testing.T) {
	got := ThumbnailPath("/var/sshkey-chat/files/file_xK9mQ2pR")
	want := "/var/sshkey-chat/files/file_xK9mQ2pR.thumb_v2.png"
	if got != want {
		t.Errorf("ThumbnailPath = %q, want %q", got, want)
	}
}

// helpers

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
