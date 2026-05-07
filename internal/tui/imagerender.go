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
	"strings"
	"sync"

	"github.com/BourgeoisBear/rasterm"
	"golang.org/x/image/draw"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// Inline images render via one of two encoders, picked at startup
// per terminal capability:
//
//   - rasterm (kitty / iTerm2 / WezTerm / Ghostty graphics protocols)
//     — native-resolution placement in the terminal's graphics layer.
//     High visual fidelity. Used when the terminal advertises support
//     via env vars ($KITTY_WINDOW_ID, $TERM_PROGRAM=wezterm/ghostty,
//     etc. — see rastermProtocolCache and rasterm.IsKittyCapable /
//     IsItermCapable). The compositing-layer pitfalls that caused
//     rasterm to be removed in an earlier iteration are addressed
//     locally in the sidebar's preview-pane integration: the preview
//     position is fixed (no scroll drift), modal-state-aware deselect
//     emits a kitty-protocol delete escape on transition (so images
//     don't persist behind modals), and image placements use a
//     fixed image-id so re-placement atomically replaces prior
//     content (no need to clean up before placing again).
//
//   - Block characters (the fallback). Each terminal cell encodes a
//     2×2 pixel region as one of 16 Unicode quadrant block glyphs;
//     foreground = average colour of "lit" pixels (those above the
//     cell's luminance midpoint), background = average colour of
//     unlit pixels. Universal: works on any ANSI-256/truecolor
//     terminal. Coexists cleanly with bubbletea's text-cell
//     rendering (modals overwrite the cells, scrolls reflow them,
//     context switches blank them) — no out-of-band graphics layer
//     to manage. Sized for "recognise the subject" not "appreciate
//     detail" — output runs ~10 KB per image regardless of source
//     resolution, vs the megabytes a full-resolution rasterm
//     placement would cost.
//
// Each encoder has its own thumbnail file (client/thumbnail.go's
// ThumbnailPath / ThumbnailPathRasterm), sized appropriately for
// its rendering model — 80×24 for block-char (matches the 40×12
// cell preview at 2 src px per cell), 256×256 for rasterm (matches
// the preview pane's screen-pixel area on common font densities).
// Cross-terminal use of the same data dir is safe — each encoder
// reads its own thumbnail and ignores the other's.
//
// SSHKEY_NO_INLINE_IMAGES=1 forces both encoders off (placeholder
// path renders); SSHKEY_NO_RASTERM=1 forces just the rasterm
// encoder off, falling through to the block-char path even on
// capable terminals. Useful for diagnostics and for users who
// prefer the block-char aesthetic.

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

// oklabPixel holds a pixel in Oklab perceptual color space (Ottosson
// 2020, https://bottosson.github.io/posts/oklab/). Euclidean distance
// in Oklab space tracks human-perceived color difference far better
// than Euclidean distance in linear-RGB or sRGB space — small steps
// at low luminance read as large to the eye but are tiny in linear
// RGB; large hue changes at the same luminance read as obvious to
// the eye but are small in linear RGB. Oklab normalizes both axes
// so the "max-distance pair" anchor heuristic in quadrantSplit picks
// pairs that are perceptually farthest apart, not light-intensity
// farthest, which is what we actually want for clustering.
//
// Color averaging stays in linear-RGB (see linearPixel) — averaging
// two colors in linear space produces the physically-correct mean
// light intensity, which is the right model for "what color does the
// terminal cell APPROXIMATE these N source pixels with." Oklab is
// the comparison metric, linear is the averaging space, sRGB is the
// emission format.
type oklabPixel struct {
	L, a, b float64
}

// linearToOklab converts linear-RGB → Oklab via the LMS intermediate.
// Matrix coefficients from Ottosson's reference implementation. Per
// pixel: 6 fused-multiply-adds + 3 cube-roots + 6 more FMAs. ~100ns
// on modern hardware; called once per source pixel per cell render
// (4× per quadrant cell), so ~2 μs per cell, ~1ms per 480-cell render.
// Cached per (path, dims, truecolor) so the cost is one-time per
// image.
func linearToOklab(p linearPixel) oklabPixel {
	l := 0.4122214708*p.r + 0.5363325363*p.g + 0.0514459929*p.b
	m := 0.2119034982*p.r + 0.6806995451*p.g + 0.1073969566*p.b
	s := 0.0883024619*p.r + 0.2817188376*p.g + 0.6299787005*p.b
	l_ := math.Cbrt(l)
	m_ := math.Cbrt(m)
	s_ := math.Cbrt(s)
	return oklabPixel{
		L: 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_,
		a: 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_,
		b: 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_,
	}
}

// oklabDistSq returns the squared Euclidean distance between two
// Oklab pixels. Squared to avoid an unnecessary sqrt — only ordering
// matters for the max-of-pairs comparison and the nearer-anchor
// assignment.
func oklabDistSq(p1, p2 oklabPixel) float64 {
	dL := p1.L - p2.L
	da := p1.a - p2.a
	db := p1.b - p2.b
	return dL*dL + da*da + db*db
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
	// rasterm distinguishes cached output produced by the rasterm
	// encoder (kitty / iTerm2 escape sequences) from the block-char
	// encoder (quadrant glyphs + ANSI color). Without this, switching
	// terminals mid-session — or hitting the `SSHKEY_NO_INLINE_IMAGES=1`
	// diagnostic toggle — could return stale block-char output to a
	// rasterm-capable session, or vice versa. Cheap to add to the key;
	// each encoder gets its own cache slot per (path, dims, truecolor).
	rasterm bool
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

// rastermProtocol is the encoder choice when rasterm is in use.
// Detected once at process start (env-var only, no terminal probing —
// see rastermCapable for the rationale). One of:
//
//   - rastermNone   (block-char path)
//   - rastermKitty  (Kitty graphics protocol; also Ghostty + WezTerm
//     when their TERM_PROGRAM matches IsKittyCapable)
//   - rastermIterm  (iTerm2 inline image protocol; also WezTerm when
//     IsItermCapable matches)
//
// Sixel is intentionally absent: rasterm.IsSixelCapable probes the
// terminal via DA1 (write ESC, read response) which is unsafe inside
// a bubbletea program — bubbletea owns stdin/stdout. Sixel-only
// terminals (foot, Contour, mlterm) get the block-char fallback.
type rastermProtocol int

const (
	rastermNone rastermProtocol = iota
	rastermKitty
	rastermIterm
)

var rastermProtocolCache = func() rastermProtocol {
	if os.Getenv("SSHKEY_NO_RASTERM") != "" {
		return rastermNone
	}
	// Kitty wins over iTerm when both env vars look set — the kitty
	// protocol is more capable (placement IDs, deletes, z-index) and
	// terminals that truly support both (notably WezTerm) implement
	// kitty better than iterm.
	if rasterm.IsKittyCapable() {
		return rastermKitty
	}
	if rasterm.IsItermCapable() {
		return rastermIterm
	}
	return rastermNone
}()

// rastermCapable reports whether the active terminal supports a
// rasterm-rendered protocol. Cached once at startup. Wrapped in a
// helper so tests can override behavior via the package-level
// rastermProtocolCache (set in TestMain or per-test).
func rastermCapable() bool {
	return rastermProtocolCache != rastermNone
}

// rastermImagePlacementID is the kitty image-id we attach to every
// preview-pane placement. Stable across renders so the sidebar's
// transition-clear path can issue a targeted delete escape (`a=d,
// d=I,i=<id>`) rather than wiping every kitty image on screen.
//
// Any non-zero uint32 works — we pick a number unlikely to collide
// with images placed by other tools sharing the same terminal.
const rastermImagePlacementID uint32 = 0x73686b79 // 'shky'

// rastermDeleteEscape is the kitty graphics-protocol escape that
// removes our preview placement. No-op for terminals that didn't
// render via rasterm in the first place — the bytes still get
// emitted but the terminal doesn't recognize the sequence and
// silently drops it (kitty escapes are wrapped in `\x1b_G` … `\x1b\\`
// which non-kitty terminals treat as an unknown DCS string).
//
// iTerm2 / WezTerm-iterm-mode don't need an explicit delete — their
// inline images are part of the text scrollback and get overwritten
// by subsequent text cells. The escape is harmless there too.
func rastermDeleteEscape() string {
	// q=2 suppresses kitty's success-and-error response. Without it,
	// kitty replies with `\x1b_Gi=<id>;OK\x1b\\` after processing
	// the delete — that response comes back through stdin, which
	// bubbletea is reading as keyboard input, and gets typed into
	// the focused text field as literal characters. q=2 silences
	// both success and error responses; we don't act on either.
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", rastermImagePlacementID)
}

// rasterFitCells computes an aspect-preserving terminal-cell rectangle
// for raster renderers inside a maxCols x maxRows preview box.
//
// Returns:
//   - cols, rows: chosen display rectangle in terminal cells (>=1)
//   - widthBound: true if width is the limiting dimension, false if height
//
// Uses the same 1:2 terminal-cell aspect assumption as renderBlockImage.
func rasterFitCells(imgW, imgH, maxCols, maxRows int) (cols, rows int) {
	if maxCols < 1 {
		maxCols = 1
	}
	if maxRows < 1 {
		maxRows = 1
	}
	if imgW < 1 {
		imgW = 1
	}
	if imgH < 1 {
		imgH = 1
	}

	scaledRows := (imgH * maxCols) / (imgW * 2)
	if scaledRows < 1 {
		scaledRows = 1
	}
	if scaledRows <= maxRows {
		return maxCols, scaledRows
	}

	scaledCols := (imgW * maxRows * 2) / imgH
	if scaledCols < 1 {
		scaledCols = 1
	}
	if scaledCols > maxCols {
		scaledCols = maxCols
	}
	return scaledCols, maxRows
}

// tryRenderRasterm encodes the image at filePath using the detected
// rasterm protocol, sized to fit (maxCols × maxRows) terminal cells.
// Returns ("", false) when rasterm isn't capable, the file can't be
// decoded, or the encoder errors. Callers fall back to the block-
// char path on the false return.
//
// Uses the rasterm-specific thumbnail (256×256 max) instead of the
// block-char thumbnail (80×24) to give the encoder enough source
// pixels to render crisply at the preview pane's screen-pixel area.
// Lazy thumbnail generation: if the rasterm thumbnail is missing,
// decode the original and fire a fire-and-forget write goroutine.
//
// The encoder output starts with the kitty placement header (which
// includes our image-id) so the sidebar's transition-clear logic can
// issue a matched delete on deselect/modal-open. iTerm2 output uses
// rasterm's default opts (no placement ID needed — the image is
// inline in the text stream and gets cleared by overwriting cells).
func tryRenderRasterm(filePath string, maxCols, maxRows int) (string, bool) {
	thumbPath := client.ThumbnailPathRasterm(filePath)
	img, err := decodeImageFile(thumbPath)
	if err != nil {
		// Lazy fallback: decode original, fire generation goroutine
		// for next render. Mirror the block-char path's pattern so
		// behavior is symmetric across encoders.
		img, err = decodeImageFile(filePath)
		if err != nil {
			return "", false
		}
		go func() {
			_ = client.GenerateRastermThumbnail(filePath, thumbPath)
		}()
	}
	dstCols, dstRows := rasterFitCells(img.Bounds().Dx(), img.Bounds().Dy(), maxCols, maxRows)

	var buf bytes.Buffer
	switch rastermProtocolCache {
	case rastermKitty:
		opts := rasterm.KittyImgOpts{
			DstCols:     uint32(dstCols),
			DstRows:     uint32(dstRows),
			ImageId:     rastermImagePlacementID,
			PlacementId: rastermImagePlacementID,
		}
		if err := rasterm.KittyWriteImage(&buf, img, opts); err != nil {
			return "", false
		}
	case rastermIterm:
		// Constrain iTerm/WezTerm inline images to the preview pane
		// bounds in cell units. Without explicit width/height, iTerm
		// defaults to "auto" and can render larger than the pane.
		opts := rasterm.ItermImgOpts{
			DisplayInline: true,
			Width:         fmt.Sprintf("%d", dstCols),
			Height:        fmt.Sprintf("%d", dstRows),
		}
		if err := rasterm.ItermWriteImageWithOptions(&buf, img, opts); err != nil {
			return "", false
		}
	default:
		return "", false
	}
	out := buf.String()
	if out == "" {
		return "", false
	}
	// Patch the initial placement header for two behaviors rasterm
	// doesn't expose via KittyImgOpts:
	//
	//   - C=1 prevents kitty from moving the text cursor after
	//     placement (otherwise the cursor jumps past the image and
	//     subsequent text rendering lands in the wrong cells).
	//
	//   - q=2 suppresses kitty's `OK` / error response on the wire.
	//     Without it, kitty replies to every placement with
	//     `\x1b_Gi=<id>,p=<placement>;OK\x1b\\`, which comes back
	//     through stdin. Bubbletea reads stdin as keyboard input and
	//     types those response characters into whatever text field
	//     is focused — visible as a literal `_Gi=...,p=...;OK\`
	//     string getting typed into the input bar after clicking on
	//     an image then the input. q=2 silences both success and
	//     error responses; we don't process either anyway.
	if rastermProtocolCache == rastermKitty {
		out = strings.Replace(out, "\x1b_Ga=T,", "\x1b_Ga=T,C=1,q=2,", 1)
	}
	// rasterm's encoders don't emit a trailing newline — the encoded
	// escape sequence sits as a single "logical line" in the output.
	// The block-char path emits one '\n'-separated row per cell row;
	// callers (specifically buildPreviewImageRows) split on '\n' to
	// vertically center. Pad rasterm's output with empty rows so the
	// caller sees the right row count and centers correctly.
	if dstRows > 1 {
		out += strings.Repeat("\n", dstRows-1)
	}
	return out, true
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
//
// Encoder selection: when the active terminal advertises a rasterm-
// supported graphics protocol (kitty / iTerm2 / WezTerm / Ghostty
// per env-var detection), rasterm runs FIRST. On success it returns
// kitty / iTerm2 escape sequences sized to the preview pane. If
// rasterm is not capable or the encode fails, the block-char path
// runs unchanged.
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

	// Try rasterm first when capable. Cache rasterm output under a
	// distinct key (rasterm:true) so swapping encoders mid-session
	// doesn't return stale block-char bytes.
	if rastermCapable() {
		rkey := imageRenderCacheKey{
			path:      filePath,
			maxCols:   maxCols,
			maxRows:   maxRows,
			truecolor: tc,
			rasterm:   true,
		}
		if cached, ok := getCachedInlineImage(rkey, info.ModTime().UnixNano(), info.Size()); ok {
			return cached
		}
		if rendered, ok := tryRenderRasterm(filePath, maxCols, maxRows); ok {
			putCachedInlineImage(rkey, info.ModTime().UnixNano(), info.Size(), rendered)
			return rendered
		}
		// Rasterm capability detected but encode failed (decode error,
		// IO error, etc.). Fall through to block-char path so the
		// preview still renders something.
	}

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
// Three color spaces, three jobs:
//   - Distance comparison (max-distance pair, anchor assignment) →
//     Oklab. Euclidean distance in Oklab tracks perceived color
//     difference; in linear-RGB it tracks light-intensity difference,
//     which mis-weights low-luminance and same-luminance hue changes.
//     Picking anchors by Oklab distance produces clusters that look
//     "different" to the eye, not just to a photometer.
//   - Averaging (fg = mean of lit, bg = mean of unlit) → linear RGB.
//     Linear-space averages are the physically-correct mean light
//     intensity. Averaging in Oklab biases toward perceptual mid-
//     points which can shift hue away from the source colors.
//   - Output → sRGB. The terminal renders the escape's RGB literally
//     as sRGB, so the linear average is gamma-encoded back at emit.
//
// Returns (fg, bg, pattern) where pattern's 4 bits (TL=8 TR=4 BL=2 BR=1)
// encode which positions are assigned to fg's anchor — that pattern
// indexes quadrantBlocks for the matching glyph.
func quadrantSplit(tl, tr, bl, br color.RGBA) (color.RGBA, color.RGBA, int) {
	pixels := [4]linearPixel{toLinear(tl), toLinear(tr), toLinear(bl), toLinear(br)}
	// Convert each pixel to Oklab once for distance comparisons. We
	// keep the linear representation in `pixels` for the averaging
	// step below — Oklab is the comparison metric, linear is the
	// averaging space.
	oklabs := [4]oklabPixel{
		linearToOklab(pixels[0]),
		linearToOklab(pixels[1]),
		linearToOklab(pixels[2]),
		linearToOklab(pixels[3]),
	}

	// Find the maximally-distant pair in Oklab space (6 pairs to
	// check). These become the two cluster anchors.
	var anchorA, anchorB int
	var maxDist float64
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			d := oklabDistSq(oklabs[i], oklabs[j])
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

	// Assign each pixel to the perceptually-nearer anchor (Oklab
	// distance); accumulate group sums in linear space for averaging.
	// Bit positions: TL=8, TR=4, BL=2, BR=1 (matches quadrantBlocks
	// index encoding).
	bits := [4]int{8, 4, 2, 1}
	var fgSum, bgSum linearPixel
	var fgN, bgN int
	var pattern int
	aOk := oklabs[anchorA]
	bOk := oklabs[anchorB]
	for i := 0; i < 4; i++ {
		dA := oklabDistSq(oklabs[i], aOk)
		dB := oklabDistSq(oklabs[i], bOk)
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
