package client

// Bounded image decoding (attachments v2 PLAN step 1b). Every local decode
// of sender-supplied bytes goes through DecodeBounded: the header is read
// first and the image is refused before any pixel allocation when its
// decoded size would exceed a byte budget, an unreadable header fails
// closed, and the decodes that remain run one at a time.
//
// Why: an image arriving in a room is auto-downloaded within the size cap
// and decoded by every member's client to make thumbnails, with no user
// action. A PNG of a few kilobytes can declare dimensions that decode to
// gigabytes (30,000 × 30,000 × 4 bytes is 3.6 GB), so one crafted image
// could make every client in the room allocate until it was killed. The
// pure-Go decoders cannot downscale during decode, so the budget is the
// guard for every full decode (attachments-v2.md §5).

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"sync/atomic"
)

// DefaultMaxDecodeBytes is the decode budget when the config sets none:
// 256 MiB, which admits 64 megapixels of 8-bit RGBA (phones' 48–50 MP
// modes, 61 MP full-frame cameras) and refuses 200 MP phone modes and
// 100 MP medium format, which still open externally.
const DefaultMaxDecodeBytes int64 = 256 << 20

// ErrImageTooLarge is returned when the header declares an image whose
// decoded pixels would exceed the budget. Callers render a placeholder
// and leave the file to an external viewer.
var ErrImageTooLarge = errors.New("image too large to decode within budget")

// ErrImageHeader is returned when the header cannot be read or makes no
// sense (fail closed: no decode happens).
var ErrImageHeader = errors.New("image header unreadable")

// decodeBudget is the process-wide budget set from config at startup; zero
// means DefaultMaxDecodeBytes.
var decodeBudget atomic.Int64

// SetDecodeBudget sets the process-wide decode budget. Values ≤ 0 select
// the default; there is no "unlimited" setting, an operator who wants more
// sets a larger number.
func SetDecodeBudget(n int64) {
	if n <= 0 {
		n = DefaultMaxDecodeBytes
	}
	decodeBudget.Store(n)
}

// DecodeBudget returns the process-wide decode budget.
func DecodeBudget() int64 {
	if v := decodeBudget.Load(); v > 0 {
		return v
	}
	return DefaultMaxDecodeBytes
}

// bytesPerPixel is the conservative in-memory cost of one decoded pixel
// for the colour model a decoder's DecodeConfig reports. Unknown models
// are charged 4.
func bytesPerPixel(m color.Model) int {
	switch m {
	case color.NRGBA64Model, color.RGBA64Model:
		return 8
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel, color.NYCbCrAModel:
		return 4
	case color.YCbCrModel:
		return 3 // conservative: 4:2:0 is 1.5, 4:4:4 is 3
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.GrayModel, color.AlphaModel:
		return 1
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// DecodeConfigBounded reads the image header from r and reports the
// config and format when width × height × bytes-per-pixel fits the
// budget. Any header error is ErrImageHeader (fail closed); an image over
// the budget is ErrImageTooLarge. Nothing beyond the header is read and
// no pixel buffer is allocated. The comparison is overflow-safe: for a
// positive budget and dimensions, w × h × bpp > budget exactly when
// w > budget / (h × bpp) in integer arithmetic.
func DecodeConfigBounded(r io.Reader, budget int64) (image.Config, string, error) {
	if budget <= 0 {
		budget = DefaultMaxDecodeBytes
	}
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return image.Config{}, "", fmt.Errorf("%w: %v", ErrImageHeader, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.ColorModel == nil {
		return image.Config{}, "", fmt.Errorf("%w: %dx%d", ErrImageHeader, cfg.Width, cfg.Height)
	}
	if exceedsBudget(cfg, budget) {
		return image.Config{}, "", fmt.Errorf("%w: %dx%d %s", ErrImageTooLarge, cfg.Width, cfg.Height, format)
	}
	return cfg, format, nil
}

// exceedsBudget reports whether cfg's decoded pixels would exceed budget
// bytes, without computing the product.
func exceedsBudget(cfg image.Config, budget int64) bool {
	w, h := int64(cfg.Width), int64(cfg.Height)
	bpp := int64(bytesPerPixel(cfg.ColorModel))
	if h > budget/bpp { // h × bpp alone would overflow the budget
		return true
	}
	return w > budget/(h*bpp)
}

// decodeSem serialises every full decode in the process (safety-net and
// sender-side thumbnail generation, manual preview): a floor on the
// client's own behaviour under legitimate load, not a defence against
// anyone — the server paces downloads already.
var decodeSem = make(chan struct{}, 1)

// decodeHook, when set by a test, runs while the semaphore is held.
var decodeHook atomic.Pointer[func()]

// DecodeBounded decodes the image in r after DecodeConfigBounded has
// accepted its header, holding the decode semaphore for the duration.
// r must seek: the header read and the decode use the same bytes.
func DecodeBounded(r io.ReadSeeker, budget int64) (image.Image, error) {
	if _, _, err := DecodeConfigBounded(r, budget); err != nil {
		return nil, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	decodeSem <- struct{}{}
	defer func() { <-decodeSem }()
	if hook := decodeHook.Load(); hook != nil {
		(*hook)()
	}
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	return img, nil
}
