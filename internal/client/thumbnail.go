package client

import (
	"fmt"
	"image"
	_ "image/gif"  // decoder registration
	_ "image/jpeg" // decoder registration
	"image/png"
	"os"

	"golang.org/x/image/draw"
)

// Inline image thumbnail dimensions, kernel, and format.
//
// Target: 80×24 pixels max (= 40 cols × 2 quadrant-pixels-per-cell
// for width, 12 rows × 2 quadrant-pixels-per-cell for height). The
// inline-display path (internal/tui/imagerender.go) renders these
// at 1 quadrant-block character per 2×2 source pixels, giving a
// 40×12 cell thumbnail in the message pane.
//
// Sized for "recognize the subject" not "appreciate detail" — `o`
// opens the original at full resolution from the cached file. Typical
// downscale ratio for an 8.5 MB photo: 4032×3024 → 80×24 (50:1 pixel
// reduction per dimension; ~2500× pixel-count reduction). The dim
// constraint trades raw pixel count for layout discipline — 40×12
// stays comfortably thumbnail-sized vs message text without
// dominating the pane on smaller terminals. Per-cell legibility at
// this density comes from the kernel choice (see GenerateThumbnail).
//
// Versioned filename suffix lets format / dimension changes land
// without colliding with stale on-disk thumbnails — bumping the
// version makes old `.thumb_vN.png` files into orphans (silently
// ignored, never read), and the lazy path regenerates with the new
// suffix on next view. v1 → v2: kernel change ApproxBiLinear →
// NearestNeighbor. Dimensions unchanged; on-disk format is the same
// PNG container, but the per-pixel sampling rule is different enough
// that v1 thumbnails would render as a different aesthetic and need
// to be replaced.
const (
	thumbnailMaxPixelW = 80
	thumbnailMaxPixelH = 24
	thumbnailSuffix    = ".thumb_v2.png"
)

// ThumbnailPath returns the canonical thumbnail path for a cached
// original. Format: `<srcPath>.thumb_v2.png` (sibling to original).
// See thumbnailSuffix for the version history.
func ThumbnailPath(srcPath string) string {
	return srcPath + thumbnailSuffix
}

// GenerateThumbnail decodes the original at srcPath, downscales it
// preserving aspect ratio to fit within the thumbnail target, and
// writes the result as a PNG to dstPath.
//
// No-op (returns nil) if dstPath already exists. Atomic via
// temp-file + rename — concurrent goroutines writing the same path
// can't produce a partially-written file (whoever wins the rename
// wins; same content from same source so semantics are
// indistinguishable).
//
// Images already smaller than the thumbnail target are written
// through as-is (re-encoded to PNG; no quality loss for small
// inputs but normalizes the format so RenderImageInline's path is
// uniform).
//
// Returns nil errors are not flushed to logs by this function — the
// caller decides whether failure is worth surfacing. Auto-preview
// callers fire-and-forget in a goroutine; lazy callers in
// RenderImageInline don't propagate errors either since the lazy
// path is "cache for next time," not load-bearing.
func GenerateThumbnail(srcPath, dstPath string) error {
	if _, err := os.Stat(dstPath); err == nil {
		return nil
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}

	imgW := img.Bounds().Dx()
	imgH := img.Bounds().Dy()
	if imgW == 0 || imgH == 0 {
		return fmt.Errorf("zero-dimensional image")
	}

	targetW, targetH := scaleToFit(imgW, imgH, thumbnailMaxPixelW, thumbnailMaxPixelH)

	var thumb image.Image = img
	if targetW < imgW || targetH < imgH {
		// NearestNeighbor (not BiLinear / bicubic): the inline display
		// is rendered as Unicode block-cell pixel art at terminal-cell
		// scale — each "pixel" is many monitor pixels wide. At that
		// output scale, smoothing kernels produce muddy low-contrast
		// cells (every output pixel is the average of ~2500 source
		// pixels, so adjacent cells share most of their input and look
		// alike). NearestNeighbor preserves the contrast of whichever
		// source pixel it samples, producing high-contrast adjacent
		// cells that read as "pixelated" in a way that improves
		// subject visibility — closer to a deliberate pixel-art
		// abstraction than a blurred photo.
		//
		// Yes, NearestNeighbor at 50:1 reduction skips a lot of source
		// pixels. That's the point: at this output density the choice
		// isn't "preserve detail" vs "lose detail" (we lose ~99.96%
		// either way), it's "show the detail of one specific pixel
		// crisply" vs "show the average of 2500 pixels muddily."
		// Crisp wins for recognition.
		scaled := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		draw.NearestNeighbor.Scale(scaled, scaled.Rect, img, img.Bounds(), draw.Over, nil)
		thumb = scaled
	}

	return writeThumbnailPNGAtomic(dstPath, thumb)
}

// scaleToFit returns target dimensions for downscaling (imgW, imgH)
// to fit within (maxW, maxH) while preserving aspect ratio. Returns
// the original dimensions if they already fit.
func scaleToFit(imgW, imgH, maxW, maxH int) (int, int) {
	if imgW <= maxW && imgH <= maxH {
		return imgW, imgH
	}
	wRatio := float64(maxW) / float64(imgW)
	hRatio := float64(maxH) / float64(imgH)
	ratio := wRatio
	if hRatio < ratio {
		ratio = hRatio
	}
	targetW := int(float64(imgW) * ratio)
	targetH := int(float64(imgH) * ratio)
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	return targetW, targetH
}

// writeThumbnailPNGAtomic encodes img as PNG and writes to dstPath.
// Atomic via temp-file + rename: the destination either doesn't
// exist or contains a fully-written PNG — never a partially-written
// file. Concurrent writers don't corrupt each other (last
// successful rename wins; identical content makes the race
// observably benign).
func writeThumbnailPNGAtomic(dstPath string, img image.Image) error {
	tmpPath := dstPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dstPath)
}
