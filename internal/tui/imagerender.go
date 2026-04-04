package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"github.com/BourgeoisBear/rasterm"
)

// termImageCapability caches the detected terminal image protocol.
var termImageCapability string

func init() {
	// Detect once at startup — these check env vars and terminal responses.
	// Safe to call during init.
	if rasterm.IsKittyCapable() {
		termImageCapability = "kitty"
	} else if rasterm.IsItermCapable() {
		termImageCapability = "iterm"
	} else {
		// Sixel detection requires terminal interaction — skip in init,
		// check lazily on first use.
	}
}

// CanRenderImages returns true if the terminal supports inline images.
func CanRenderImages() bool {
	if termImageCapability != "" {
		return true
	}
	// Lazy sixel check
	if ok, _ := rasterm.IsSixelCapable(); ok {
		termImageCapability = "sixel"
		return true
	}
	return false
}

// RenderImageInline renders an image file to a string of terminal escape sequences.
// maxCols/maxRows define the bounding box in terminal cells.
// Aspect ratio is preserved — the image fits within the box.
func RenderImageInline(filePath string, maxCols, maxRows int) string {
	if !CanRenderImages() {
		return ""
	}

	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return ""
	}

	return renderImage(img, maxCols, maxRows)
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

	return renderImage(img, maxCols, maxRows)
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
