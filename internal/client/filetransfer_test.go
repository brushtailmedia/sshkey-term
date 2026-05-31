package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

type stubChannel struct {
	r io.Reader
}

func (s *stubChannel) Read(p []byte) (int, error)                     { return s.r.Read(p) }
func (s *stubChannel) Write(p []byte) (int, error)                    { return len(p), nil }
func (s *stubChannel) Close() error                                   { return nil }
func (s *stubChannel) CloseWrite() error                              { return nil }
func (s *stubChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (s *stubChannel) Stderr() io.ReadWriter                          { return &bytes.Buffer{} }

func waitForPendingDownload(t *testing.T, fileID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		downloadsMu.Lock()
		_, ok := downloads[fileID]
		downloadsMu.Unlock()
		if ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending download %s was never registered", fileID)
}

func decodeSingleDownloadRequest(t *testing.T, data []byte) protocol.Download {
	t.Helper()
	dec := protocol.NewDecoder(bytes.NewReader(data))
	var got protocol.Download
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return got
}

func TestDownloadFile_HappyPath(t *testing.T) {
	const fileID = "file_dl_happy"

	plaintext := []byte("hello from download path")
	decryptKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	encrypted, err := crypto.Encrypt(decryptKey, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	encryptedBytes := []byte(encrypted)
	contentHash := crypto.ContentHash(encryptedBytes)

	var frame bytes.Buffer
	if err := writeBinaryFrame(&frame, fileID, encryptedBytes); err != nil {
		t.Fatalf("writeBinaryFrame: %v", err)
	}

	reqSink := &bytes.Buffer{}
	c := New(Config{
		DeviceID: "dev_test_dl",
		DataDir:  t.TempDir(),
	})
	c.enc = protocol.NewEncoder(reqSink)
	c.downloadChan = &stubChannel{r: bytes.NewReader(frame.Bytes())}

	go func() {
		waitForPendingDownload(t, fileID)
		HandleDownloadStart(fileID, contentHash)
	}()

	localPath, err := c.DownloadFile(fileID, decryptKey, contentHash)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	if want := filepath.Join(c.cfg.DataDir, "files", fileID); localPath != want {
		t.Fatalf("download path = %q, want %q", localPath, want)
	}
	gotBytes, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", localPath, err)
	}
	if !bytes.Equal(gotBytes, plaintext) {
		t.Fatalf("downloaded plaintext mismatch: got %q want %q", string(gotBytes), string(plaintext))
	}

	req := decodeSingleDownloadRequest(t, reqSink.Bytes())
	if req.Type != "download" {
		t.Fatalf("request type = %q, want download", req.Type)
	}
	if req.FileID != fileID {
		t.Fatalf("request file_id = %q, want %q", req.FileID, fileID)
	}
	if req.CorrID == "" {
		t.Fatal("request corr_id should be set")
	}

	downloadsMu.Lock()
	_, stillPending := downloads[fileID]
	downloadsMu.Unlock()
	if stillPending {
		t.Fatalf("pending download %s should be cleaned up", fileID)
	}
}

// F11: the E2E-committed ContentHash detects a malicious server substituting
// another same-key (same-room/same-epoch) blob — even though the server also
// supplies the *swapped* blob's hash via download_start, so the server-relayed
// check would pass and the blob decrypts cleanly under the shared key. Only the
// sender-authenticated hash catches the swap.
func TestDownloadFile_RejectsServerSubstitutedBlob(t *testing.T) {
	const fileID = "file_dl_swap"

	key, err := crypto.GenerateKey() // the shared room epoch key
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	// The original file the sender committed to — its hash goes in the E2E
	// Attachment metadata (sender-authenticated).
	origEnc, err := crypto.Encrypt(key, []byte("the real attachment"))
	if err != nil {
		t.Fatal(err)
	}
	e2eContentHash := crypto.ContentHash([]byte(origEnc))

	// The malicious server serves a DIFFERENT blob encrypted under the same key
	// (so it decrypts cleanly) and vouches for it with its own hash.
	swapEnc, err := crypto.Encrypt(key, []byte("attacker swapped this in"))
	if err != nil {
		t.Fatal(err)
	}
	swapBytes := []byte(swapEnc)
	swapHash := crypto.ContentHash(swapBytes)

	var frame bytes.Buffer
	if err := writeBinaryFrame(&frame, fileID, swapBytes); err != nil {
		t.Fatal(err)
	}

	c := New(Config{DeviceID: "dev_test_swap", DataDir: t.TempDir()})
	c.enc = protocol.NewEncoder(&bytes.Buffer{})
	c.downloadChan = &stubChannel{r: bytes.NewReader(frame.Bytes())}

	go func() {
		waitForPendingDownload(t, fileID)
		HandleDownloadStart(fileID, swapHash) // server vouches for the swapped blob
	}()

	// The recipient verifies against the E2E hash → mismatch → rejected, even
	// though the server-relayed hash matches the swap and decrypt would succeed.
	if _, err := c.DownloadFile(fileID, key, e2eContentHash); err == nil {
		t.Fatal("DownloadFile accepted a server-substituted blob — F11 check failed")
	} else if !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("error = %q, want an integrity-check failure", err)
	}
}

func TestDownloadFile_DownloadErrorFailsFast(t *testing.T) {
	const fileID = "file_dl_error"

	decryptKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	reqSink := &bytes.Buffer{}
	c := New(Config{
		DeviceID: "dev_test_dl_err",
		DataDir:  t.TempDir(),
	})
	c.enc = protocol.NewEncoder(reqSink)
	c.downloadChan = &stubChannel{r: bytes.NewReader(nil)}

	wantErr := errors.New("not_found: file not found")
	go func() {
		waitForPendingDownload(t, fileID)
		HandleDownloadError(fileID, wantErr)
	}()

	_, err = c.DownloadFile(fileID, decryptKey, "")
	if err == nil {
		t.Fatal("DownloadFile should fail on download_error")
	}
	if !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("error = %q, want not_found", err)
	}

	downloadsMu.Lock()
	_, stillPending := downloads[fileID]
	downloadsMu.Unlock()
	if stillPending {
		t.Fatalf("pending download %s should be cleaned up after error", fileID)
	}

	// Ensure exactly one download request frame was sent on Channel 1.
	dec := protocol.NewDecoder(bytes.NewReader(reqSink.Bytes()))
	var req protocol.Download
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.FileID != fileID {
		t.Fatalf("request file_id = %q, want %q", req.FileID, fileID)
	}
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != io.EOF {
		t.Fatalf("expected exactly one request frame, extra decode err=%v raw=%s", err, string(raw))
	}
}

var _ ssh.Channel = (*stubChannel)(nil)
