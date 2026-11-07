package client

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// pngHeader returns a syntactically valid PNG prefix — signature plus an
// IHDR chunk with a correct CRC — declaring w×h at the given bit depth
// and colour type, and nothing after it. image.DecodeConfig reads only
// this much for a non-paletted image, so these are the crafted headers
// the budget must refuse without allocating. Colour type 6 is RGBA
// (NRGBA at 8 bits, NRGBA64 at 16), type 2 is RGB, type 0 is gray.
func pngHeader(w, h uint32, bitDepth, colorType byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], w)
	binary.BigEndian.PutUint32(data[4:8], h)
	data[8] = bitDepth
	data[9] = colorType
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], 13)
	b.Write(n[:])
	b.WriteString("IHDR")
	b.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(data)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc.Sum32())
	b.Write(c[:])
	return b.Bytes()
}

// gifHeader returns a GIF header and logical screen descriptor declaring
// w×h with no global colour table — enough for image.DecodeConfig.
func gifHeader(w, h uint16) []byte {
	var b bytes.Buffer
	b.WriteString("GIF89a")
	var d [7]byte
	binary.LittleEndian.PutUint16(d[0:2], w)
	binary.LittleEndian.PutUint16(d[2:4], h)
	b.Write(d[:])
	return b.Bytes()
}

func smallPNG(t testing.TB) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return b.Bytes()
}

func smallJPEG(t testing.TB) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 5, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 5; x++ {
			img.Set(x, y, color.RGBA{R: byte(x * 50), G: byte(y * 60), B: 90, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return b.Bytes()
}

func TestBytesPerPixel(t *testing.T) {
	cases := map[string]struct {
		m    color.Model
		want int
	}{
		"nrgba64":  {color.NRGBA64Model, 8},
		"rgba64":   {color.RGBA64Model, 8},
		"rgba":     {color.RGBAModel, 4},
		"nrgba":    {color.NRGBAModel, 4},
		"cmyk":     {color.CMYKModel, 4},
		"nycbcra":  {color.NYCbCrAModel, 4},
		"ycbcr":    {color.YCbCrModel, 3},
		"gray16":   {color.Gray16Model, 2},
		"alpha16":  {color.Alpha16Model, 2},
		"gray":     {color.GrayModel, 1},
		"alpha":    {color.AlphaModel, 1},
		"palette":  {color.Palette{color.Black, color.White}, 1},
		"unknown":  {color.ModelFunc(func(c color.Color) color.Color { return c }), 4},
		"nilplate": {color.Palette(nil), 1},
	}
	for name, c := range cases {
		if got := bytesPerPixel(c.m); got != c.want {
			t.Errorf("%s: bytesPerPixel = %d, want %d", name, got, c.want)
		}
	}
}

// TestDecodeConfigBounded_RejectsCraftedHeadersBeforeAllocating is the
// attack the step exists for: a few hundred bytes declaring a 30,000 ×
// 30,000 RGBA image (3.6 GB decoded) must be refused by the header
// check alone — in microseconds, allocating nothing of note.
func TestDecodeConfigBounded_RejectsCraftedHeadersBeforeAllocating(t *testing.T) {
	hdr := pngHeader(30000, 30000, 8, 6)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := DecodeConfigBounded(bytes.NewReader(hdr), DefaultMaxDecodeBytes)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("error = %v, want ErrImageTooLarge", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("header check allocated %d bytes, want well under 1 MiB", grew)
	}
	if _, err := DecodeBounded(bytes.NewReader(hdr), DefaultMaxDecodeBytes); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("DecodeBounded error = %v, want ErrImageTooLarge", err)
	}
}

// TestDecodeConfigBounded_BudgetByBytesNotPixels pins the bytes-per-pixel
// rule at the default 256 MiB budget: 10,000 × 10,000 is 800 MB at 16
// bits and 400 MB at 8 bits (both refused), 7,000 × 7,000 8-bit RGBA is
// 196 MB (accepted), and an 8-bit gray 10,000 × 10,000 is 100 MB
// (accepted) — the same pixel count as a refused image.
func TestDecodeConfigBounded_BudgetByBytesNotPixels(t *testing.T) {
	cases := []struct {
		name string
		hdr  []byte
		ok   bool
	}{
		{"16-bit rgba 10k", pngHeader(10000, 10000, 16, 6), false},
		{"8-bit rgba 10k", pngHeader(10000, 10000, 8, 6), false},
		{"8-bit rgba 7k", pngHeader(7000, 7000, 8, 6), true},
		{"8-bit gray 10k", pngHeader(10000, 10000, 8, 0), true},
		{"16-bit gray 10k", pngHeader(10000, 10000, 16, 0), true},   // 200 MB
		{"8-bit rgb 8192x8192", pngHeader(8192, 8192, 8, 2), false}, // exactly 256 MiB + nothing: 8192² × 4 = 256 MiB, not over
		{"gif 65535x65535", gifHeader(65535, 65535), false},
		{"gif 1000x1000", gifHeader(1000, 1000), true},
	}
	// 8192 × 8192 × 4 is exactly the budget, so it is not over it.
	cases[5].ok = true
	for _, c := range cases {
		cfg, format, err := DecodeConfigBounded(bytes.NewReader(c.hdr), DefaultMaxDecodeBytes)
		if c.ok {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
				continue
			}
			if cfg.Width <= 0 || format == "" {
				t.Errorf("%s: config %+v format %q", c.name, cfg, format)
			}
			continue
		}
		if !errors.Is(err, ErrImageTooLarge) {
			t.Errorf("%s: error = %v, want ErrImageTooLarge", c.name, err)
		}
	}
}

// TestDecodeConfigBounded_FailsClosedOnBadHeaders: anything the decoders
// cannot parse, or a header that makes no sense, is ErrImageHeader and
// never reaches a decode.
func TestDecodeConfigBounded_FailsClosedOnBadHeaders(t *testing.T) {
	good := pngHeader(100, 100, 8, 6)
	cases := map[string][]byte{
		"empty":           {},
		"garbage":         []byte("definitely not an image"),
		"truncated ihdr":  good[:20],
		"signature only":  good[:8],
		"zero width":      pngHeader(0, 100, 8, 6),
		"negative height": pngHeader(100, 0x80000001, 8, 6),
		"bad crc":         append(append([]byte{}, good[:len(good)-1]...), good[len(good)-1]^0xff),
	}
	for name, data := range cases {
		_, _, err := DecodeConfigBounded(bytes.NewReader(data), DefaultMaxDecodeBytes)
		if !errors.Is(err, ErrImageHeader) {
			t.Errorf("%s: error = %v, want ErrImageHeader", name, err)
		}
		if _, err := DecodeBounded(bytes.NewReader(data), DefaultMaxDecodeBytes); !errors.Is(err, ErrImageHeader) {
			t.Errorf("%s: DecodeBounded error = %v, want ErrImageHeader", name, err)
		}
	}
}

// TestDecodeBounded_RealImagesUnchanged: every ordinary photo decodes as
// before — a real PNG and a real JPEG come back with their pixels, and
// the same bytes are refused only when the budget is set below them.
func TestDecodeBounded_RealImagesUnchanged(t *testing.T) {
	for name, data := range map[string][]byte{"png": smallPNG(t), "jpeg": smallJPEG(t)} {
		img, err := DecodeBounded(bytes.NewReader(data), DefaultMaxDecodeBytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Fatalf("%s: empty bounds %v", name, img.Bounds())
		}
		if _, err := DecodeBounded(bytes.NewReader(data), 1); !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("%s with a 1-byte budget: error = %v, want ErrImageTooLarge", name, err)
		}
	}
	// A reader not at offset 0 is rewound by the header check, not left
	// mid-stream for the decode.
	data := smallPNG(t)
	r := bytes.NewReader(data)
	if _, err := r.Seek(3, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBounded(r, DefaultMaxDecodeBytes); !errors.Is(err, ErrImageHeader) {
		// From offset 3 the signature is wrong: fail closed, never decode.
		t.Fatalf("mid-stream reader: error = %v, want ErrImageHeader", err)
	}
}

// TestDecodeBudget_DefaultsAndOverride pins the process-wide budget: the
// default when nothing is set or a non-positive value is given, and the
// value when one is.
func TestDecodeBudget_DefaultsAndOverride(t *testing.T) {
	defer SetDecodeBudget(0)
	SetDecodeBudget(0)
	if got := DecodeBudget(); got != DefaultMaxDecodeBytes {
		t.Fatalf("DecodeBudget() = %d, want default %d", got, DefaultMaxDecodeBytes)
	}
	SetDecodeBudget(-5)
	if got := DecodeBudget(); got != DefaultMaxDecodeBytes {
		t.Fatalf("after -5: DecodeBudget() = %d, want default", got)
	}
	SetDecodeBudget(1 << 20)
	if got := DecodeBudget(); got != 1<<20 {
		t.Fatalf("after 1 MiB: DecodeBudget() = %d", got)
	}
	if _, _, err := DecodeConfigBounded(bytes.NewReader(pngHeader(1000, 1000, 8, 6)), 0); err != nil {
		t.Fatalf("budget 0 should mean the default: %v", err)
	}
}

// TestDecodeBounded_SerialisesDecodes: the semaphore admits one decode
// at a time. Sixteen goroutines decode concurrently while a hook, run
// with the semaphore held, tracks how many are inside at once; the
// maximum must be one. No sleeps — the hook yields to invite overlap.
func TestDecodeBounded_SerialisesDecodes(t *testing.T) {
	var active, maxActive atomic.Int32
	hook := func() {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		runtime.Gosched()
		active.Add(-1)
	}
	decodeHook.Store(&hook)
	defer decodeHook.Store(nil)

	data := smallPNG(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := DecodeBounded(bytes.NewReader(data), DefaultMaxDecodeBytes); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent decodes = %d, want 1", got)
	}
}

// FuzzDecodeConfigBounded: over random byte prefixes the header check
// never panics, returns only the two typed errors, and never accepts an
// image over the budget (so nothing downstream can allocate past it).
func FuzzDecodeConfigBounded(f *testing.F) {
	f.Add(pngHeader(30000, 30000, 8, 6))
	f.Add(pngHeader(7000, 7000, 8, 6))
	f.Add(pngHeader(100, 100, 16, 6))
	f.Add(gifHeader(65535, 65535))
	f.Add(smallPNG(f))
	f.Add(smallJPEG(f))
	f.Add([]byte{})
	f.Add([]byte("GIF89a"))
	f.Fuzz(func(t *testing.T, data []byte) {
		const budget = 1 << 20
		cfg, _, err := DecodeConfigBounded(bytes.NewReader(data), budget)
		if err != nil {
			if !errors.Is(err, ErrImageHeader) && !errors.Is(err, ErrImageTooLarge) {
				t.Fatalf("untyped error %v", err)
			}
			return
		}
		if exceedsBudget(cfg, budget) {
			t.Fatalf("accepted %dx%d over the budget", cfg.Width, cfg.Height)
		}
	})
}
