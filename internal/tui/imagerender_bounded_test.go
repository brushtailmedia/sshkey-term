package tui

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// craftedPNGHeader is a valid PNG signature plus IHDR (correct CRC)
// declaring w×h 8-bit RGBA and nothing else — the few hundred bytes a
// hostile sender needs to declare a multi-gigabyte decode. Mirrors the
// helper in the client package's imagedecode tests.
func craftedPNGHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], w)
	binary.BigEndian.PutUint32(data[4:8], h)
	data[8], data[9] = 8, 6
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

// TestRenderImageInline_OversizedImageShowsPlaceholder: an original whose
// header declares more than the decode budget is never decoded; the
// preview slot gets the placeholder line, cached like a render so the
// header is not re-read every frame. The real decode path runs here —
// no stubs — which is the point.
func TestRenderImageInline_OversizedImageShowsPlaceholder(t *testing.T) {
	t.Setenv("SSHKEY_NO_INLINE_IMAGES", "")
	resetInlineImageRenderCache()
	defer resetInlineImageRenderCache()

	path := filepath.Join(t.TempDir(), "file_bigAAAAAAAAAAAAAAAAAA")
	if err := os.WriteFile(path, craftedPNGHeader(30000, 30000), 0o600); err != nil {
		t.Fatal(err)
	}

	got := RenderImageInline(path, 40, 12)
	if !strings.Contains(got, imageTooLargePlaceholder) {
		t.Fatalf("render = %q, want the placeholder %q", got, imageTooLargePlaceholder)
	}

	// Second render is served from the cache: the decode hook is never
	// reached (a stub that fails the test proves it).
	oldDecode := decodeImageFile
	defer func() { decodeImageFile = oldDecode }()
	decodeImageFile = func(string) (image.Image, error) {
		t.Error("cached placeholder re-decoded the file")
		return nil, os.ErrNotExist
	}
	if again := RenderImageInline(path, 40, 12); again != got {
		t.Fatalf("second render = %q, want the cached %q", again, got)
	}
}

// TestDecodeImageFromFile_RealImageUnchanged: an ordinary image decodes
// through the bounded path exactly as before.
func TestDecodeImageFromFile_RealImageUnchanged(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 6, 4))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "file_smallAAAAAAAAAAAAAAAA")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := decodeImageFromFile(path)
	if err != nil {
		t.Fatalf("decodeImageFromFile: %v", err)
	}
	if got.Bounds().Dx() != 6 || got.Bounds().Dy() != 4 {
		t.Fatalf("bounds = %v, want 6x4", got.Bounds())
	}
	// An unreadable file is still an error, not a panic.
	if _, err := decodeImageFromFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file decoded")
	}
}
