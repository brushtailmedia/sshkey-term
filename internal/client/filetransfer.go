package client

import (
	"bufio"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// downloadChannelType is the SSH channel subtype the server accepts for
// per-request download streams (Phase 17 Step 4.f). Must match the
// server's DownloadChannelType constant exactly — any mismatch results
// in the server rejecting the channel open with UnknownChannelType.
const downloadChannelType = "sshkey-chat-download"

// pendingUpload tracks an in-progress upload.
type pendingUpload struct {
	uploadID string
	ready    chan struct{} // signalled when server sends upload_ready
	fileID   chan string   // receives file_id on completion
	err      chan error
}

var (
	uploadsMu sync.Mutex
	uploads   = make(map[string]*pendingUpload)
)

// Phase 17 Step 4.f retired the pending-download map and the
// HandleDownloadStart / HandleDownloadError callbacks. Downloads now
// open their own SSH channel per request; server replies with
// download_start / download_error inline on that same channel. No
// cross-goroutine correlation state is needed.

// UploadFile encrypts and uploads a file using the room's current epoch key.
// For group DM uploads, use UploadGroupFile instead — it takes a per-file
// key from the caller which is stored in the Attachment struct inside the
// encrypted message payload.
//
// Returns the server-assigned file_id and the epoch used for encryption, so
// the caller can stamp FileEpoch on the attachment metadata correctly (avoids
// a race if the epoch rotates between upload and send).
func (c *Client) UploadFile(localPath, room, group string) (fileID string, epoch int64, err error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", 0, fmt.Errorf("read file: %w", err)
	}

	if room == "" {
		return "", 0, fmt.Errorf("UploadFile requires a room; for group DM attachments use UploadGroupFile")
	}

	c.mu.RLock()
	epoch = c.currentEpoch[room]
	encKey := c.epochKeys[room][epoch]
	c.mu.RUnlock()
	if encKey == nil {
		return "", 0, fmt.Errorf("no epoch key for room %s", room)
	}

	fileID, err = c.uploadEncrypted(data, encKey, room, group, "")
	return fileID, epoch, err
}

// UploadDMFile encrypts a file with a per-file key and uploads it for a 1:1 DM.
// Same Design A pattern as UploadGroupFile — each attachment carries its own
// key in the encrypted payload.
func (c *Client) UploadDMFile(localPath, dmID string, fileKey []byte) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if len(fileKey) == 0 {
		return "", fmt.Errorf("UploadDMFile: fileKey is required")
	}
	return c.uploadEncrypted(data, fileKey, "", "", dmID)
}

// UploadGroupFile encrypts a file with the given per-file key (K_file) and
// uploads it. The caller stores K_file inside the Attachment's FileKey
// field when sending the group DM message that references this file_id, so
// recipients can decrypt the file after decrypting the message payload.
//
// This is Design A: each attachment carries its own key in the encrypted
// payload, decoupling upload from message send. See PROTOCOL.md "DM
// attachments".
func (c *Client) UploadGroupFile(localPath, group string, fileKey []byte) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if len(fileKey) == 0 {
		return "", fmt.Errorf("UploadGroupFile: fileKey is required")
	}
	return c.uploadEncrypted(data, fileKey, "", group, "")
}

// uploadEncrypted is the shared transport: encrypts bytes with encKey, runs
// the upload_start → binary frame → upload_complete round-trip, and returns
// the server-assigned file_id.
func (c *Client) uploadEncrypted(data, encKey []byte, room, group, dm string) (string, error) {
	encrypted, err := crypto.Encrypt(encKey, data)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	encBytes := []byte(encrypted)

	// Compute content hash of encrypted bytes for integrity verification
	contentHash := crypto.ContentHash(encBytes)

	uploadID := generateNanoID("up_")

	pending := &pendingUpload{
		uploadID: uploadID,
		ready:    make(chan struct{}, 1),
		fileID:   make(chan string, 1),
		err:      make(chan error, 1),
	}
	uploadsMu.Lock()
	uploads[uploadID] = pending
	uploadsMu.Unlock()

	defer func() {
		uploadsMu.Lock()
		delete(uploads, uploadID)
		uploadsMu.Unlock()
	}()

	err = c.enc.Encode(protocol.UploadStart{
		Type:        "upload_start",
		UploadID:    uploadID,
		Size:        int64(len(encBytes)),
		ContentHash: contentHash,
		Room:        room,
		Group:       group,
		DM:          dm,
	})
	if err != nil {
		return "", fmt.Errorf("send upload_start: %w", err)
	}

	// Wait for upload_ready before writing binary data — avoids a race where
	// the binary frame arrives before the server has registered the pending
	// upload, causing the server to discard the bytes (see handleBinaryChannel).
	select {
	case <-pending.ready:
	case err := <-pending.err:
		return "", err
	case <-c.done:
		return "", fmt.Errorf("disconnected")
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("upload timed out waiting for server")
	}

	c.mu.RLock()
	ulChan := c.uploadChan
	c.mu.RUnlock()

	if ulChan == nil {
		return "", fmt.Errorf("no upload channel (Channel 3 not open)")
	}

	// Hold uploadChanMu across the whole frame write so concurrent uploads
	// don't interleave bytes within a frame (id_len|id|data_len|data).
	c.uploadChanMu.Lock()
	err = writeBinaryFrame(ulChan, uploadID, encBytes)
	c.uploadChanMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("write binary: %w", err)
	}

	select {
	case fileID := <-pending.fileID:
		return fileID, nil
	case err := <-pending.err:
		return "", err
	case <-c.done:
		return "", fmt.Errorf("disconnected")
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("upload timed out waiting for completion")
	}
}

// HandleUploadReady is called from handleInternal when upload_ready arrives.
func HandleUploadReady(uploadID string) {
	uploadsMu.Lock()
	p, ok := uploads[uploadID]
	uploadsMu.Unlock()
	if ok {
		select {
		case p.ready <- struct{}{}:
		default:
		}
	}
}

// HandleUploadComplete is called from handleInternal when upload_complete arrives.
func HandleUploadComplete(uploadID, fileID string) {
	uploadsMu.Lock()
	p, ok := uploads[uploadID]
	uploadsMu.Unlock()
	if ok {
		select {
		case p.fileID <- fileID:
		default:
		}
	}
}

// HandleUploadError is called from handleInternal when upload_error arrives.
// Signals the matching pending upload's err channel so the caller fails fast
// instead of waiting forever for upload_ready or upload_complete.
func HandleUploadError(uploadID string, err error) {
	uploadsMu.Lock()
	p, ok := uploads[uploadID]
	uploadsMu.Unlock()
	if ok {
		select {
		case p.err <- err:
		default:
		}
	}
}

// DownloadFile downloads and decrypts a file by opening a dedicated
// `sshkey-chat-download` SSH channel for this request. Returns the
// local path on success. Phase 17 Step 4.f — one channel per
// download, streamed and closed inline.
//
// Protocol on the new channel:
//
//	Client: write  {"type":"download","file_id":"..."}\n
//	Server: reply  {"type":"download_start","file_id":...,"size":...,"content_hash":"..."}\n
//	                OR  {"type":"download_error","file_id":...,"code":...,"message":...}\n (fatal)
//	Server: stream <binary frame — id_len|id|data_len|data>  (only on success)
//	Server: reply  {"type":"download_complete","file_id":...}\n (only on success)
//	Server: close channel
//
// Content hash verification is unchanged from the pre-Phase-17-Step-4.f
// flow — the hash was the end-to-end integrity backstop against bit
// rot / truncation / transit corruption, and it remains so on the new
// per-request channel. The hash is computed on the encrypted bytes by
// the uploader and re-verified by the downloader before anything hits
// disk.
//
// Concurrency: each DownloadFile call opens its own SSH channel, so
// multiple DownloadFile invocations proceed in parallel (up to the
// server's per-connection cap — default 3). No client-side mutex
// needed.
func (c *Client) DownloadFile(fileID string, decryptKey []byte) (string, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return "", fmt.Errorf("not connected")
	}

	// Determine save directory
	dataDir := c.cfg.DataDir
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	filesDir := filepath.Join(dataDir, "files")
	os.MkdirAll(filesDir, 0700)

	// Open a fresh SSH channel for this download. Server rejects with
	// ResourceShortage if we're at the per-connection cap — propagate as
	// a typed-ish error so callers can surface a "try again" UX.
	dlCh, reqs, err := conn.OpenChannel(downloadChannelType, nil)
	if err != nil {
		return "", fmt.Errorf("open download channel: %w", err)
	}
	go ssh.DiscardRequests(reqs)
	defer dlCh.Close()

	// Send the download request inline on the new channel.
	reqLine, err := json.Marshal(protocol.Download{
		Type:   "download",
		FileID: fileID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	reqLine = append(reqLine, '\n')
	if _, err := dlCh.Write(reqLine); err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}

	// Read the first JSON line — either download_start (success, bytes
	// follow) or download_error (fatal, channel closes after).
	reader := bufio.NewReaderSize(dlCh, 4096)
	firstLine, err := readLineWithDeadline(reader, 30*time.Second, c.done)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var typeProbe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(firstLine, &typeProbe); err != nil {
		return "", fmt.Errorf("parse response type: %w", err)
	}

	switch typeProbe.Type {
	case "download_error":
		var de protocol.DownloadError
		if err := json.Unmarshal(firstLine, &de); err != nil {
			return "", fmt.Errorf("parse download_error: %w", err)
		}
		return "", fmt.Errorf("%s: %s", de.Code, de.Message)

	case "download_start":
		// Fall through to stream read below.
	default:
		return "", fmt.Errorf("unexpected response type %q", typeProbe.Type)
	}

	var ds protocol.DownloadStart
	if err := json.Unmarshal(firstLine, &ds); err != nil {
		return "", fmt.Errorf("parse download_start: %w", err)
	}
	expectedHash := ds.ContentHash

	// Read the binary frame. The bufio.Reader may have buffered past the
	// newline; pass it as the reader so no bytes are lost.
	_, data, err := readBinaryFrame(reader)
	if err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}

	// Read the trailing download_complete line. This is the server's
	// clean end-of-stream signal; if we don't see it, we consider the
	// download aborted and abandon the bytes. Skipping verification on
	// an aborted stream keeps corrupt-but-hash-matching data from ever
	// reaching disk.
	completeLine, err := readLineWithDeadline(reader, 30*time.Second, c.done)
	if err != nil {
		return "", fmt.Errorf("read completion: %w", err)
	}
	if err := json.Unmarshal(completeLine, &typeProbe); err != nil || typeProbe.Type != "download_complete" {
		return "", fmt.Errorf("unexpected trailer: %s", completeLine)
	}

	// Verify content hash before writing anything to disk (catches
	// truncation, bit rot, and transit corruption). The hash was
	// computed on the encrypted bytes by the uploader.
	if err := crypto.VerifyContentHash(data, expectedHash); err != nil {
		return "", fmt.Errorf("download integrity check failed: %w", err)
	}

	// Decrypt (GCM tag provides a second integrity check)
	plaintext, err := crypto.Decrypt(decryptKey, string(data))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	// Save — only reached if both hash and GCM verification passed
	localPath := filepath.Join(filesDir, fileID)
	if err := os.WriteFile(localPath, plaintext, 0600); err != nil {
		return "", err
	}

	return localPath, nil
}

// readLineWithDeadline reads one newline-terminated line from r,
// aborting if done fires or the deadline elapses. Used to read JSON
// lines off the download channel with a bounded wait so a dead
// connection / stuck server doesn't hang DownloadFile forever.
func readLineWithDeadline(r *bufio.Reader, timeout time.Duration, done <-chan struct{}) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-done:
		return nil, fmt.Errorf("disconnected")
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout")
	}
}

// SaveFileAs copies a downloaded file to a user-chosen path.
func SaveFileAs(srcPath, dstPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, data, 0644)
}

// OpenFile opens a file in the system's default viewer.
func OpenFile(path string) error {
	return openInSystemViewer(path)
}

func writeBinaryFrame(w io.Writer, id string, data []byte) error {
	if _, err := w.Write([]byte{byte(len(id))}); err != nil {
		return err
	}
	if _, err := w.Write([]byte(id)); err != nil {
		return err
	}
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readBinaryFrame(r io.Reader) (string, []byte, error) {
	var idLen [1]byte
	if _, err := io.ReadFull(r, idLen[:]); err != nil {
		return "", nil, err
	}
	idBuf := make([]byte, idLen[0])
	if _, err := io.ReadFull(r, idBuf); err != nil {
		return "", nil, err
	}
	var lenBuf [8]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", nil, err
	}
	dataLen := binary.BigEndian.Uint64(lenBuf[:])
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", nil, err
	}
	return string(idBuf), data, nil
}

func generateNanoID(prefix string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 16)
	crand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + string(b)
}
