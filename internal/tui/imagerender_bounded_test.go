package tui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/client"
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

// TestRenderImageInline_FailuresAreTypedAndNeverText: the renderer
// returns an empty string and a typed reason for every failure — never
// text of its own, which is what let a 44-cell line grow the sidebar
// past the terminal. The real decode path runs here, no stubs.
func TestRenderImageInline_FailuresAreTypedAndNeverText(t *testing.T) {
	t.Setenv("SSHKEY_NO_INLINE_IMAGES", "")
	resetInlineImageRenderCache()
	defer resetInlineImageRenderCache()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name string
		path string
		want error
	}{
		{"over the budget", write("file_bigAAAAAAAAAAAAAAAAAA", craftedPNGHeader(30000, 30000)), client.ErrImageTooLarge},
		{"unreadable header", write("file_junkAAAAAAAAAAAAAAAAA", []byte("not an image at all")), client.ErrImageHeader},
		{"missing file", filepath.Join(dir, "file_goneAAAAAAAAAAAAAAAAA"), fs.ErrNotExist},
	}
	for _, c := range cases {
		rendered, err := RenderImageInline(c.path, 40, 12)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: error = %v, want %v", c.name, err, c.want)
		}
		if rendered != "" {
			t.Errorf("%s: rendered %q, want nothing", c.name, rendered)
		}
	}

	// Inline images off is a reason too, not a silent blank.
	t.Setenv("SSHKEY_NO_INLINE_IMAGES", "1")
	if rendered, err := RenderImageInline(cases[0].path, 40, 12); !errors.Is(err, errInlineImagesOff) || rendered != "" {
		t.Errorf("images off: rendered %q, error %v; want nothing and errInlineImagesOff", rendered, err)
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
