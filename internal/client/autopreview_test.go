package client

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// TestIsAutoPreviewMime verifies the accept-list is narrowly scoped to
// the four image formats supported by the render path. Auto-preview
// fires without user action, so this list must stay tight — any format
// added here becomes an auto-decode surface and must pass the
// crafted-payload threat model.
func TestIsAutoPreviewMime(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},

		// Must NOT auto-preview — either not supported by the renderer,
		// or historically dodgy decoder surface (SVG/TIFF), or not an
		// image at all.
		{"image/svg+xml", false},
		{"image/tiff", false},
		{"image/bmp", false},
		{"image/x-icon", false},
		{"application/octet-stream", false},
		{"text/plain", false},
		{"", false},

		// Capitalization — mime types are case-insensitive per RFC but we
		// only accept the canonical lower form; senders should too.
		{"IMAGE/PNG", false},
	}
	for _, tc := range cases {
		if got := isAutoPreviewMime(tc.mime); got != tc.want {
			t.Errorf("isAutoPreviewMime(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

// TestMaybeAutoPreview_DisabledByZeroCap verifies the feature flag:
// when ImageAutoPreviewMaxBytes is <= 0 the helper short-circuits
// synchronously — no goroutine spawned, so no callback can ever fire.
// We observe this by installing a callback that would signal on any
// fire and asserting the channel stays empty after the helper returns.
func TestMaybeAutoPreview_DisabledByZeroCap(t *testing.T) {
	c, done := newTestAutoPreviewClient(t, 0) // cap=0 => disabled

	att := store.StoredAttachment{
		FileID:     "file_should_skip",
		Name:       "photo.jpg",
		Size:       100,
		Mime:       "image/jpeg",
		DecryptKey: base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")),
	}
	c.maybeAutoPreviewAttachments([]store.StoredAttachment{att})

	// With cap=0 the helper returns before spawning any goroutine, so
	// the channel is drained synchronously — no receive should ever
	// succeed. A non-blocking receive that succeeds means the skip
	// path broke.
	assertNoAttachmentReady(t, done)
}

// TestMaybeAutoPreview_OverCapSkips verifies an image above the size
// threshold is not auto-downloaded. The render path falls through to
// 🖼 placeholder in this case; user still has to press `o` to open.
func TestMaybeAutoPreview_OverCapSkips(t *testing.T) {
	c, done := newTestAutoPreviewClient(t, 2*1024*1024) // cap=2MB

	att := store.StoredAttachment{
		FileID:     "file_too_big",
		Name:       "huge.png",
		Size:       10 * 1024 * 1024, // 10MB — over cap
		Mime:       "image/png",
		DecryptKey: base64.StdEncoding.EncodeToString([]byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")),
	}
	c.maybeAutoPreviewAttachments([]store.StoredAttachment{att})
	assertNoAttachmentReady(t, done)
}

// TestMaybeAutoPreview_NonImageMimeSkips verifies a non-image attachment
// (PDF, zip, etc.) is not auto-downloaded even when under the cap. Same
// reasoning as the mime accept-list: auto-decode surface stays narrow.
func TestMaybeAutoPreview_NonImageMimeSkips(t *testing.T) {
	c, done := newTestAutoPreviewClient(t, 2*1024*1024)

	att := store.StoredAttachment{
		FileID:     "file_pdf",
		Name:       "report.pdf",
		Size:       50 * 1024, // well under cap
		Mime:       "application/pdf",
		DecryptKey: base64.StdEncoding.EncodeToString([]byte("cccccccccccccccccccccccccccccccc")),
	}
	c.maybeAutoPreviewAttachments([]store.StoredAttachment{att})
	assertNoAttachmentReady(t, done)
}

// TestMaybeAutoPreview_AlreadyCached verifies the helper detects an
// attachment that's already in the cache (e.g. from a previous session
// or a manual open) and fires the callback immediately without kicking
// off a download. This makes manual-open history survive across
// context switches without re-fetching.
//
// The callback fires from a goroutine (go cb(fileID)); we block on the
// channel with a generous timeout rather than time.Sleep.
func TestMaybeAutoPreview_AlreadyCached(t *testing.T) {
	dir := t.TempDir()
	c, done := newTestAutoPreviewClient(t, 2*1024*1024)
	c.cfg.DataDir = dir

	// Seed the cache with a file at the deterministic path.
	fileID := "file_already_cached"
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	cachePath := filepath.Join(dir, "files", fileID)
	if err := os.WriteFile(cachePath, []byte("pretend-plaintext"), 0600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	att := store.StoredAttachment{
		FileID:     fileID,
		Name:       "photo.jpg",
		Size:       500,
		Mime:       "image/jpeg",
		DecryptKey: base64.StdEncoding.EncodeToString([]byte("dddddddddddddddddddddddddddddddd")),
	}
	c.maybeAutoPreviewAttachments([]store.StoredAttachment{att})

	select {
	case got := <-done:
		if got != fileID {
			t.Errorf("OnAttachmentReady fired with %q, want %q", got, fileID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnAttachmentReady did not fire for already-cached attachment")
	}
}

// TestMaybeAutoPreview_BadDecryptKeySkips verifies the helper guards
// against a malformed base64 decrypt key — an attachment with broken
// metadata must not cause a panic or a bogus download call.
func TestMaybeAutoPreview_BadDecryptKeySkips(t *testing.T) {
	c, done := newTestAutoPreviewClient(t, 2*1024*1024)

	att := store.StoredAttachment{
		FileID:     "file_bad_key",
		Name:       "photo.jpg",
		Size:       500,
		Mime:       "image/jpeg",
		DecryptKey: "not-valid-base64!!!",
	}
	c.maybeAutoPreviewAttachments([]store.StoredAttachment{att})
	assertNoAttachmentReady(t, done)
}

// newTestAutoPreviewClient builds a minimal *Client usable for
// maybeAutoPreviewAttachments() tests plus a buffered channel that
// receives file_ids on every OnAttachmentReady fire. The channel is
// buffered (1) so a single fire doesn't block even if the test never
// reads it (negative-case tests). Tests that expect a fire block on
// <-ch with a timeout; tests that expect no fire use
// assertNoAttachmentReady to drain and assert empty.
func newTestAutoPreviewClient(t *testing.T, cap int64) (*Client, <-chan string) {
	t.Helper()
	ch := make(chan string, 1)
	c := &Client{
		cfg: Config{
			DataDir:                  t.TempDir(),
			ImageAutoPreviewMaxBytes: cap,
			OnAttachmentReady: func(fileID string) {
				// Non-blocking; if the channel is already full the
				// extra fire is dropped. Tests using this channel
				// should not fire more than once.
				select {
				case ch <- fileID:
				default:
				}
			},
		},
	}
	return c, ch
}

// assertNoAttachmentReady verifies the callback channel is empty right
// after the helper's synchronous return. Uses a non-blocking receive so
// we don't need to Sleep to "wait out" a goroutine that shouldn't have
// been spawned in the first place — the skip paths in
// maybeAutoPreviewAttachments all return without spawning.
func assertNoAttachmentReady(t *testing.T, done <-chan string) {
	t.Helper()
	select {
	case got := <-done:
		t.Errorf("OnAttachmentReady fired unexpectedly with fileID=%q", got)
	default:
		// Expected — no goroutine was spawned, channel is empty.
	}
}
