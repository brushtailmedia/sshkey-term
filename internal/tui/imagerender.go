package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sync"

	"golang.org/x/image/draw"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// Inline images are rendered as truecolor (or 256-color fallback)
// quadrant block characters. Each terminal cell encodes a 2×2 pixel
// region of the image: foreground = average color of "lit" pixels
// (those above the cell's luminance midpoint), background = average
// color of unlit pixels, character = one of 16 Unicode block glyphs
// representing which quadrants are lit.
//
// Why this approach (vs the kitty / iterm graphics protocols rasterm
// previously provided): graphics escapes paint into a separate
// terminal compositing layer that bubbletea has no concept of, which
// caused images to persist behind modals, drift on scroll, and
// linger after context switches. Block-character rendering lives in
// the text-cell layer — bubbletea's diffing and lipgloss's layout
// see them as ordinary cells, so modal overlays clear them, scrolls
// reflow them, and context switches blank them.
//
// Output size: ~10 KB per cell-rendered image regardless of source
// resolution (vs ~11 MB for an 8 MP image through kitty graphics
// protocol). The thumbnail PNG persistence layer (client/thumbnail.go)
// keeps the input pixel grid small so the downscale cost is one-time
// per file, not per render.

// quadrantBlocks indexes the 16 quadrant block glyphs by their
// 4-bit pattern (TL=8, TR=4, BL=2, BR=1). Empty (0) and full (15)
// patterns get space and full-block; the 14 partial patterns each
// have a dedicated U+2580–U+259F glyph.
//
// All 16 chars are Unicode 1.1 (1991). Universal monospace-font
// support including Apple Terminal's defaults — no glyph fallback
// concerns.
var quadrantBlocks = [16]rune{
	' ', // 0000
	'▗', // 0001 BR
	'▖', // 0010 BL
	'▄', // 0011 BL+BR
	'▝', // 0100 TR
	'▐', // 0101 TR+BR (right half)
	'▞', // 0110 TR+BL (anti-diagonal)
	'▟', // 0111 TR+BL+BR
	'▘', // 1000 TL
	'▚', // 1001 TL+BR (diagonal)
	'▌', // 1010 TL+BL (left half)
	'▙', // 1011 TL+BL+BR
	'▀', // 1100 TL+TR (top half)
	'▜', // 1101 TL+TR+BR
	'▛', // 1110 TL+TR+BL
	'█', // 1111 all
}

// sRGB ↔ linear-RGB conversion lookup tables. Per-cell color math
// (clustering distances, fg/bg averaging) operates in linear space
// for perceptually-correct results — averaging two colors in
// gamma-encoded sRGB produces a darker mid-tone than physically
// correct, which makes photos look "muddy" at thumbnail scale.
//
// 256-entry forward table indexed by sRGB byte → linear float.
// 4096-entry reverse table for sub-byte linear precision when
// converting back. Initialized once in init().
var (
	srgbToLinear [256]float64
	linearToSRGB [4096]uint8
)

func init() {
	for i := 0; i < 256; i++ {
		v := float64(i) / 255
		if v <= 0.04045 {
			srgbToLinear[i] = v / 12.92
		} else {
			srgbToLinear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	for i := 0; i < 4096; i++ {
		v := float64(i) / 4095
		var sv float64
		if v <= 0.0031308 {
			sv = v * 12.92
		} else {
			sv = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		linearToSRGB[i] = uint8(math.Round(sv * 255))
	}
}

// linearPixel holds an RGB pixel decoded from gamma-encoded sRGB
// to linear light intensity. Color math (averaging, distance) is
// physically accurate in this space.
type linearPixel struct {
	r, g, b float64
}

func toLinear(c color.RGBA) linearPixel {
	return linearPixel{
		r: srgbToLinear[c.R],
		g: srgbToLinear[c.G],
		b: srgbToLinear[c.B],
	}
}

func toSRGB(p linearPixel) color.RGBA {
	return color.RGBA{
		R: linearToSRGB[clampLinearIdx(p.r)],
		G: linearToSRGB[clampLinearIdx(p.g)],
		B: linearToSRGB[clampLinearIdx(p.b)],
		A: 255,
	}
}

func clampLinearIdx(v float64) int {
	i := int(v * 4095)
	if i < 0 {
		return 0
	}
	if i > 4095 {
		return 4095
	}
	return i
}

// linearDistSq returns the squared Euclidean distance between two
// linear-space pixels. Squared (not square-rooted) because the only
// caller is comparison (max-of-pairs); avoiding the sqrt is a
// per-cell hot-path optimization.
func linearDistSq(p1, p2 linearPixel) float64 {
	dr := p1.r - p2.r
	dg := p1.g - p2.g
	db := p1.b - p2.b
	return dr*dr + dg*dg + db*db
}

// decodeImageFile and renderImageFn are function vars so tests can stub
// expensive decode/encode paths without depending on terminal capabilities.
var (
	decodeImageFile = decodeImageFromFile
	renderImageFn   = renderBlockImage
)

type imageRenderCacheKey struct {
	path      string
	maxCols   int
	maxRows   int
	truecolor bool
}

type imageRenderCacheEntry struct {
	modUnixNano int64
	size        int64
	rendered    string
	lastUse     uint64
}

var (
	imageRenderCacheMu sync.Mutex
	imageRenderCache   = make(map[imageRenderCacheKey]imageRenderCacheEntry)
	imageRenderTick    uint64
)

const maxImageRenderCacheEntries = 32

// CanRenderImages returns true if inline image rendering should be
// attempted. With the move to block-cell rendering (vs the prior
// graphics protocols that needed kitty/iterm/sixel detection), the
// answer is essentially always yes: ANSI 256-color is universal,
// truecolor support is detected per-render via $COLORTERM. The
// remaining false-result is the diagnostic env var.
//
// SSHKEY_NO_INLINE_IMAGES=1 forces the placeholder path. Useful for
// triage when isolating whether a perceived freeze is in the
// image-render path or elsewhere; also a clean opt-out for users
// who prefer text-only display.
func CanRenderImages() bool {
	if os.Getenv("SSHKEY_NO_INLINE_IMAGES") != "" {
		return false
	}
	return true
}

// useTrueColor returns true if the active terminal advertises
// truecolor (24-bit) support via the conventional $COLORTERM env
// var. Falls back to 256-color when this is unset or unrecognized.
//
// Modern terminals (iTerm2, kitty, alacritty, gnome-terminal,
// Apple Terminal recent versions, Windows Terminal, etc.) all set
// $COLORTERM=truecolor. Legacy terminals don't set it and get the
// 256-color quantized path.
func useTrueColor() bool {
	c := os.Getenv("COLORTERM")
	return c == "truecolor" || c == "24bit"
}

// RenderImageInline renders an image file as a string of cell-aligned
// terminal escape sequences. maxCols/maxRows define the bounding box
// in terminal cells; aspect ratio is preserved within those bounds
// using the standard ~1:2 width:height cell aspect.
//
// A deferred recover wraps decode + encode so a malformed or crafted
// image (untrusted sender-supplied bytes) that trips a decoder panic
// does not crash the TUI — we fall back to the empty-string result
// which makes the caller render the 🖼 placeholder instead.
func RenderImageInline(filePath string, maxCols, maxRows int) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = ""
		}
	}()

	if !CanRenderImages() {
		return ""
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return ""
	}

	tc := useTrueColor()
	key := imageRenderCacheKey{
		path:      filePath,
		maxCols:   maxCols,
		maxRows:   maxRows,
		truecolor: tc,
	}
	if cached, ok := getCachedInlineImage(key, info.ModTime().UnixNano(), info.Size()); ok {
		return cached
	}

	// Prefer a persisted thumbnail (~50 KB PNG, fast decode) over the
	// original (potentially many MB; multi-second decode + downscale).
	// On thumbnail miss, decode the original via decodeImageFile and
	// fire-and-forget a thumbnail-generation goroutine so the next
	// render — this session or any future cold start — takes the
	// fast path. See client/thumbnail.go for the persistence format.
	thumbPath := client.ThumbnailPath(filePath)
	img, err := decodeImageFile(thumbPath)
	if err != nil {
		img, err = decodeImageFile(filePath)
		if err != nil {
			return ""
		}
		// Thumbnail missing for this file. Generate it asynchronously
		// for next time. Eager paths in DownloadFile / uploadEncrypted
		// normally cover this; the lazy fallback here handles files
		// that landed before the eager path existed (cold-start of a
		// pre-existing session) AND files persisted by some path we
		// don't control (manual `o`, future restore/import paths).
		go func() {
			_ = client.GenerateThumbnail(filePath, thumbPath)
		}()
	}

	rendered := renderImageFn(img, maxCols, maxRows)
	if rendered != "" {
		putCachedInlineImage(key, info.ModTime().UnixNano(), info.Size(), rendered)
	}
	return rendered
}

// RenderImageFromBytes renders an image from raw bytes. Used by paths
// that have an in-memory image without a backing file (e.g. share
// screen previews). Bypasses the persistence cache; suitable for
// one-off rendering.
func RenderImageFromBytes(data []byte, maxCols, maxRows int) string {
	if !CanRenderImages() {
		return ""
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}

	return renderImageFn(img, maxCols, maxRows)
}

func decodeImageFromFile(filePath string) (image.Image, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return img, nil
}

func getCachedInlineImage(key imageRenderCacheKey, modUnixNano, size int64) (string, bool) {
	imageRenderCacheMu.Lock()
	defer imageRenderCacheMu.Unlock()
	entry, ok := imageRenderCache[key]
	if !ok {
		return "", false
	}
	// Cached files are content-addressed by file_id (nanoid; same
	// path → same content). Mod-time changes from concurrent download
	// rewrites don't imply content changes, so we deliberately don't
	// invalidate on those — the previous mod-time check produced the
	// "freeze every scroll-back against large image" pathology where
	// rewrites kept defeating the cache.
	_ = modUnixNano
	_ = size
	imageRenderTick++
	entry.lastUse = imageRenderTick
	imageRenderCache[key] = entry
	return entry.rendered, true
}

func putCachedInlineImage(key imageRenderCacheKey, modUnixNano, size int64, rendered string) {
	imageRenderCacheMu.Lock()
	defer imageRenderCacheMu.Unlock()
	imageRenderTick++
	imageRenderCache[key] = imageRenderCacheEntry{
		modUnixNano: modUnixNano,
		size:        size,
		rendered:    rendered,
		lastUse:     imageRenderTick,
	}
	if len(imageRenderCache) <= maxImageRenderCacheEntries {
		return
	}

	// Evict one least-recently-used entry.
	var oldestKey imageRenderCacheKey
	var oldestUse uint64
	first := true
	for k, v := range imageRenderCache {
		if first || v.lastUse < oldestUse {
			oldestKey = k
			oldestUse = v.lastUse
			first = false
		}
	}
	delete(imageRenderCache, oldestKey)
}

// renderBlockImage produces a string of cell-aligned ANSI-colored
// quadrant block characters representing img scaled to fit within
// maxCols × maxRows terminal cells, preserving aspect ratio.
//
// Algorithm (per cell, 2×2 source pixels):
//  1. Compute luminance midpoint of the four pixels.
//  2. Threshold each pixel: above midpoint = "lit" (1), below = "unlit" (0).
//  3. Build a 4-bit pattern (TL=8, TR=4, BL=2, BR=1) → quadrantBlocks index.
//  4. fg = average of lit pixels, bg = average of unlit pixels.
//  5. Emit fg + bg + glyph.
//
// Truecolor escapes when useTrueColor() is true (modern terminals);
// 256-color quantized escapes otherwise. Output is cell-bounded —
// bubbletea/lipgloss treat the resulting string as ordinary text:
// modals overwrite cleanly, scrolls reflow, context switches clear.
func renderBlockImage(img image.Image, maxCols, maxRows int) string {
	imgW := img.Bounds().Dx()
	imgH := img.Bounds().Dy()
	if imgW == 0 || imgH == 0 {
		return ""
	}

	// Aspect-correct cell dimensions. Terminal cells are roughly 1:2
	// width:height physically, so a square source image fills 1 cell
	// row per 2 cell columns (the *2 in the math below).
	displayCols := maxCols
	displayRows := maxRows
	if imgW > 0 {
		scaledRows := (imgH * maxCols) / (imgW * 2)
		if scaledRows < displayRows {
			displayRows = scaledRows
		}
	}
	if imgH > 0 {
		scaledCols := (imgW * displayRows * 2) / imgH
		if scaledCols < displayCols {
			displayCols = scaledCols
		}
	}
	if displayCols < 4 {
		displayCols = 4
	}
	if displayRows < 2 {
		displayRows = 2
	}

	// Quadrant block sampling: 2×2 source pixels per output cell.
	// Pre-resize to exactly the target pixel grid so the per-cell
	// loop below is a simple lookup, not a resampling step.
	//
	// BiLinear (not CatmullRom or ApproxBiLinear): bicubic kernels
	// sharpen edges but introduce ringing/overshoot, which the 2×2
	// quadrant clustering then quantizes into mis-classified speckle
	// pixels — a sharp source edge becomes a noisy cell. Bilinear's
	// smoother output averages cleanly into the fg/bg color split,
	// which preserves more of the visible structure at this output
	// size. Tested against CatmullRom: subjects were harder to read
	// despite the higher per-pixel sharpness.
	scaledW := displayCols * 2
	scaledH := displayRows * 2
	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	draw.BiLinear.Scale(scaled, scaled.Rect, img, img.Bounds(), draw.Over, nil)

	tc := useTrueColor()
	var b bytes.Buffer
	// Pre-size: ~38 bytes/cell truecolor or ~22 bytes/cell 256-color,
	// plus reset + newline per row. Bound prevents repeated grow-and-copy.
	perCell := 38
	if !tc {
		perCell = 22
	}
	b.Grow(displayCols*displayRows*perCell + displayRows*8)

	for cy := 0; cy < displayRows; cy++ {
		for cx := 0; cx < displayCols; cx++ {
			tl := scaled.RGBAAt(cx*2, cy*2)
			tr := scaled.RGBAAt(cx*2+1, cy*2)
			bl := scaled.RGBAAt(cx*2, cy*2+1)
			br := scaled.RGBAAt(cx*2+1, cy*2+1)

			fg, bg, pattern := quadrantSplit(tl, tr, bl, br)
			if tc {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm%c",
					fg.R, fg.G, fg.B, bg.R, bg.G, bg.B, quadrantBlocks[pattern])
			} else {
				fmt.Fprintf(&b, "\x1b[38;5;%dm\x1b[48;5;%dm%c",
					quantize256(fg.R, fg.G, fg.B), quantize256(bg.R, bg.G, bg.B), quadrantBlocks[pattern])
			}
		}
		b.WriteString("\x1b[0m")
		if cy < displayRows-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// quadrantSplit picks the optimal fg/bg color split for a 2×2 pixel
// block by treating it as a 2-means clustering problem with anchors
// chosen as the maximally-distant pair.
//
// Why this beats luminance thresholding (the previous approach):
// luminance threshold mis-groups pixels that have similar brightness
// but different hues — e.g. a green leaf next to a brown twig at the
// same luminance both end up in the same group, losing all chromatic
// distinction. Photos with subtle hue variation (foliage, skin, sky)
// looked muddy. Max-distance pair correctly separates pixels by their
// dominant color difference regardless of luminance — the result has
// noticeably more chromatic vibrancy at thumbnail scale.
//
// Linear-space color math (vs sRGB): averaging in gamma-encoded sRGB
// produces darker mid-tones than physically correct because the
// nonlinear encoding skews the arithmetic mean. Linear-space averages
// give accurate light blending. Combined with max-distance clustering,
// the result is closer to what a photo-aware renderer would produce.
//
// Returns (fg, bg, pattern) where pattern's 4 bits (TL=8 TR=4 BL=2 BR=1)
// encode which positions are assigned to fg's anchor — that pattern
// indexes quadrantBlocks for the matching glyph.
func quadrantSplit(tl, tr, bl, br color.RGBA) (color.RGBA, color.RGBA, int) {
	pixels := [4]linearPixel{toLinear(tl), toLinear(tr), toLinear(bl), toLinear(br)}

	// Find the maximally-distant pair (6 pairs to check). These become
	// the two cluster anchors.
	var anchorA, anchorB int
	var maxDist float64
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			d := linearDistSq(pixels[i], pixels[j])
			if d > maxDist {
				maxDist = d
				anchorA = i
				anchorB = j
			}
		}
	}

	// Edge: all 4 pixels identical. Emit full block with single color.
	if maxDist == 0 {
		c := toSRGB(pixels[0])
		return c, c, 15
	}

	// Assign each pixel to the nearer anchor; accumulate group sums
	// for averaging in linear space. Bit positions: TL=8, TR=4, BL=2,
	// BR=1 (matches quadrantBlocks index encoding).
	bits := [4]int{8, 4, 2, 1}
	var fgSum, bgSum linearPixel
	var fgN, bgN int
	var pattern int
	a := pixels[anchorA]
	b2 := pixels[anchorB]
	for i := 0; i < 4; i++ {
		dA := linearDistSq(pixels[i], a)
		dB := linearDistSq(pixels[i], b2)
		if dA <= dB {
			pattern |= bits[i]
			fgSum.r += pixels[i].r
			fgSum.g += pixels[i].g
			fgSum.b += pixels[i].b
			fgN++
		} else {
			bgSum.r += pixels[i].r
			bgSum.g += pixels[i].g
			bgSum.b += pixels[i].b
			bgN++
		}
	}

	var fg, bg color.RGBA
	if fgN > 0 {
		fgSum.r /= float64(fgN)
		fgSum.g /= float64(fgN)
		fgSum.b /= float64(fgN)
		fg = toSRGB(fgSum)
	}
	if bgN > 0 {
		bgSum.r /= float64(bgN)
		bgSum.g /= float64(bgN)
		bgSum.b /= float64(bgN)
		bg = toSRGB(bgSum)
	}
	if fgN == 0 {
		fg = bg
	}
	if bgN == 0 {
		bg = fg
	}
	return fg, bg, pattern
}

// quantize256 maps an RGB triplet to the nearest entry in the 256-color
// palette (16 + 6×6×6 cube + 24 grayscale). Used for the 256-color
// fallback when the terminal doesn't advertise truecolor via $COLORTERM.
//
// Considers both the 6×6×6 cube (indices 16–231) and the grayscale
// ramp (232–255), picking whichever is closer in Euclidean RGB space.
// The first 16 indices (terminal theme colors) are not used because
// their RGB values are theme-defined and not reliable as a target.
func quantize256(r, g, b uint8) byte {
	rN := int(r) * 5 / 255
	gN := int(g) * 5 / 255
	bN := int(b) * 5 / 255
	cubeIdx := byte(16 + 36*rN + 6*gN + bN)

	// Near-grayscale pixels often map closer to the grayscale ramp
	// than to the cube. Check both, pick whichever's closer in
	// Euclidean RGB distance.
	maxC := max3(int(r), int(g), int(b))
	minC := min3(int(r), int(g), int(b))
	if maxC-minC < 16 {
		gray := (int(r) + int(g) + int(b)) / 3
		grayIdx := 232 + gray*23/255
		if grayIdx > 255 {
			grayIdx = 255
		}
		// The cube palette's actual RGB for cubeIdx is (rN*51, gN*51, bN*51).
		// Compare distances.
		cubeR := rN * 51
		cubeG := gN * 51
		cubeB := bN * 51
		cubeDist := dist3(int(r), int(g), int(b), cubeR, cubeG, cubeB)
		grayDist := dist3(int(r), int(g), int(b), gray, gray, gray)
		if grayDist < cubeDist {
			return byte(grayIdx)
		}
	}
	return cubeIdx
}

func dist3(r1, g1, b1, r2, g2, b2 int) int {
	dr := r1 - r2
	dg := g1 - g2
	db := b1 - b2
	return int(math.Sqrt(float64(dr*dr + dg*dg + db*db)))
}

func max3(a, b, c int) int {
	if a > b && a > c {
		return a
	}
	if b > c {
		return b
	}
	return c
}

func min3(a, b, c int) int {
	if a < b && a < c {
		return a
	}
	if b < c {
		return b
	}
	return c
}
