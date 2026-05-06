package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"

	"github.com/BourgeoisBear/rasterm"
)

// termImageCapability caches the detected terminal image protocol.
var termImageCapability string

// termImageCapabilityChecked tracks whether we've already completed
// capability detection (positive or negative). This prevents repeated
// sixel probes on every render frame when the terminal cannot render
// inline images.
var termImageCapabilityChecked bool

// decodeImageFile and renderImageFn are function vars so tests can stub
// expensive decode/encode paths without depending on terminal capabilities.
var (
	decodeImageFile    = decodeImageFromFile
	renderImageFn      = renderImage
	detectSixelCapable = rasterm.IsSixelCapable
)

type imageRenderCacheKey struct {
	path       string
	maxCols    int
	maxRows    int
	capability string
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

func init() {
	// Detect once at startup — these check env vars and terminal responses.
	// Safe to call during init.
	if rasterm.IsKittyCapable() {
		termImageCapability = "kitty"
		termImageCapabilityChecked = true
	} else if rasterm.IsItermCapable() {
		termImageCapability = "iterm"
		termImageCapabilityChecked = true
	} else {
		// Sixel detection requires terminal interaction — skip in init,
		// check lazily on first use.
	}
}

// CanRenderImages returns true if the terminal supports inline images.
//
// Diagnostic toggle: set SSHKEY_NO_INLINE_IMAGES=1 in the environment
// to force the placeholder path regardless of terminal capability.
// Useful for narrowing down whether a perceived freeze is in the
// image-decode/encode path (RenderImageInline) or elsewhere. Just
// `unset SSHKEY_NO_INLINE_IMAGES` to re-enable inline rendering.
func CanRenderImages() bool {
	if os.Getenv("SSHKEY_NO_INLINE_IMAGES") != "" {
		return false
	}
	if termImageCapability != "" {
		return true
	}
	if termImageCapabilityChecked {
		return false
	}
	// Lazy sixel check
	termImageCapabilityChecked = true
	if ok, _ := detectSixelCapable(); ok {
		termImageCapability = "sixel"
		return true
	}
	return false
}

// RenderImageInline renders an image file to a string of terminal escape sequences.
// maxCols/maxRows define the bounding box in terminal cells.
// Aspect ratio is preserved — the image fits within the box.
//
// A deferred recover wraps decode + encode so a malformed or crafted image
// (untrusted sender-supplied bytes) that trips a decoder panic does not
// crash the TUI — we fall back to the empty-string result which makes the
// caller render the 🖼 placeholder instead.
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

	key := imageRenderCacheKey{
		path:       filePath,
		maxCols:    maxCols,
		maxRows:    maxRows,
		capability: termImageCapability,
	}
	if cached, ok := getCachedInlineImage(key, info.ModTime().UnixNano(), info.Size()); ok {
		return cached
	}

	img, err := decodeImageFile(filePath)
	if err != nil {
		return ""
	}

	rendered := renderImageFn(img, maxCols, maxRows)
	if rendered != "" {
		putCachedInlineImage(key, info.ModTime().UnixNano(), info.Size(), rendered)
	}
	return rendered
}

// RenderImageFromBytes renders an image from raw bytes.
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
	// Note on staleness: we deliberately ignore mod-time and size on
	// cache hit. Cached files are content-addressed by file_id (a
	// nanoid; same content → same name → same path). Even if a
	// concurrent download path rewrites the file, the BYTES are
	// identical, so the rendered escape sequence is identical too.
	// The previous mod-time/size check defeated the cache whenever
	// two paths raced to download the same file (both wrote to disk,
	// each rewrite changed mod-time, every render saw a "stale"
	// cache and re-decoded the multi-MB image — the hot loop that
	// caused the user-reported "freeze every scroll-back" against
	// an 8.5 MB PNG).
	//
	// modUnixNano and size are kept in the entry record only for
	// possible future debugging / introspection; nothing reads them.
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

func renderImage(img image.Image, maxCols, maxRows int) string {
	// Calculate display size preserving aspect ratio.
	// Terminal cells are roughly 2:1 (height:width in pixels),
	// so 1 row ≈ 2 cols in visual space.
	imgW := img.Bounds().Dx()
	imgH := img.Bounds().Dy()

	if imgW == 0 || imgH == 0 {
		return ""
	}

	// Target: fit within maxCols x maxRows cells
	// Assume cell aspect ratio ~2:1 (each cell is taller than wide)
	displayCols := maxCols
	displayRows := maxRows

	// Scale to fit width
	if imgW > 0 {
		scaledRows := (imgH * maxCols) / (imgW * 2) // /2 for cell aspect
		if scaledRows < displayRows {
			displayRows = scaledRows
		}
	}

	// Scale to fit height
	if imgH > 0 {
		scaledCols := (imgW * displayRows * 2) / imgH
		if scaledCols < displayCols {
			displayCols = scaledCols
		}
	}

	// Minimum size
	if displayCols < 4 {
		displayCols = 4
	}
	if displayRows < 2 {
		displayRows = 2
	}

	var buf bytes.Buffer

	switch termImageCapability {
	case "kitty":
		opts := rasterm.KittyImgOpts{
			DstCols: uint32(displayCols),
			DstRows: uint32(displayRows),
		}
		if err := rasterm.KittyWriteImage(&buf, img, opts); err != nil {
			return ""
		}

	case "iterm":
		opts := rasterm.ItermImgOpts{
			Width:         fmt.Sprintf("%d", displayCols),
			Height:        fmt.Sprintf("%d", displayRows),
			DisplayInline: true,
		}
		if err := rasterm.ItermWriteImageWithOptions(&buf, img, opts); err != nil {
			return ""
		}

	case "sixel":
		if paletted, ok := img.(*image.Paletted); ok {
			if err := rasterm.SixelWriteImage(&buf, paletted); err != nil {
				return ""
			}
		} else {
			// Non-paletted image — sixel can't render without quantization.
			// Fall back to text placeholder.
			return ""
		}
	}

	return buf.String()
}
