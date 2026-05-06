package client

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

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

// pendingDownload tracks an in-progress download (file_id-keyed).
type pendingDownload struct {
	started     chan struct{} // signalled when download_start arrives
	err         chan error    // signalled when download_error arrives
	contentHash string        // set by HandleDownloadStart from download_start
}

var (
	downloadsMu sync.Mutex
	downloads   = make(map[string]*pendingDownload)
)

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

	uploadCorrID := protocol.GenerateCorrID()
	envelope := protocol.UploadStart{
		Type:        "upload_start",
		UploadID:    uploadID,
		Size:        int64(len(encBytes)),
		ContentHash: contentHash,
		Room:        room,
		Group:       group,
		DM:          dm,
		CorrID:      uploadCorrID,
	}
	c.sendQueue.EnqueueWithID(uploadCorrID, "upload_start", envelope)
	c.sendQueue.MarkSending(uploadCorrID)
	err = c.enc.Encode(envelope)
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
		// Cache plaintext locally so the sender doesn't re-download
		// their own upload from the server when the message echo
		// arrives — the auto-preview goroutine fired by storeRoomMessage
		// would otherwise pull the same bytes back via Channel 2,
		// hold downloadChanMu for the round-trip, and produce visible
		// freeze "as if it were a re-upload."
		//
		// Best-effort write: if disk is full / permissions are wrong,
		// the auto-preview goroutine will simply do its normal download.
		// Same eager-thumbnail goroutine spawned post-write so the
		// sender's first inline render of their own upload is fast.
		if c.cfg.DataDir != "" {
			localPath := filepath.Join(c.cfg.DataDir, "files", fileID)
			if err := os.MkdirAll(filepath.Dir(localPath), 0700); err == nil {
				if err := os.WriteFile(localPath, data, 0600); err == nil {
					go func() {
						if terr := GenerateThumbnail(localPath, ThumbnailPath(localPath)); terr != nil && c.logger != nil {
							c.logger.Debug("thumbnail generation failed",
								"file_id", fileID, "error", terr)
						}
					}()
				}
			}
		}
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

// HandleDownloadStart is called from handleInternal when download_start
// arrives. Stores the content hash and signals the matching DownloadFile
// call that bytes are inbound on Channel 2.
func HandleDownloadStart(fileID, contentHash string) {
	downloadsMu.Lock()
	p, ok := downloads[fileID]
	if ok {
		p.contentHash = contentHash
	}
	downloadsMu.Unlock()
	if ok {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
}

// HandleDownloadError is called from handleInternal when download_error
// arrives. Signals the matching DownloadFile call to fail fast instead of
// waiting forever for a binary frame that will never arrive.
func HandleDownloadError(fileID string, err error) {
	downloadsMu.Lock()
	p, ok := downloads[fileID]
	downloadsMu.Unlock()
	if ok {
		select {
		case p.err <- err:
		default:
		}
	}
}

// DownloadFile downloads and decrypts a file. Returns the local path.
func (c *Client) DownloadFile(fileID string, decryptKey []byte) (string, error) {
	c.mu.RLock()
	dlChan := c.downloadChan
	c.mu.RUnlock()

	if dlChan == nil {
		return "", fmt.Errorf("no download channel")
	}

	// Determine save directory
	dataDir := c.cfg.DataDir
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	filesDir := filepath.Join(dataDir, "files")
	os.MkdirAll(filesDir, 0700)

	// Register a pending download so Channel 1 can signal download_start
	// or download_error for this specific file_id.
	pending := &pendingDownload{
		started: make(chan struct{}, 1),
		err:     make(chan error, 1),
	}
	downloadsMu.Lock()
	downloads[fileID] = pending
	downloadsMu.Unlock()
	defer func() {
		downloadsMu.Lock()
		delete(downloads, fileID)
		downloadsMu.Unlock()
	}()

	// Serialize downloads on the shared Channel 2: send the request
	// and read the reply under the same lock, otherwise concurrent
	// callers would read each other's binary frames off the same
	// channel. Server writes frames in request order; the client
	// matches them up positionally.
	c.downloadChanMu.Lock()
	defer c.downloadChanMu.Unlock()

	// Send download request
	dlCorrID := protocol.GenerateCorrID()
	envelope := protocol.Download{
		Type:   "download",
		FileID: fileID,
		CorrID: dlCorrID,
	}
	c.sendQueue.EnqueueWithID(dlCorrID, "download", envelope)
	c.sendQueue.MarkSending(dlCorrID)
	err := c.enc.Encode(envelope)
	if err != nil {
		return "", err
	}

	// Wait for download_start (server is sending bytes) or download_error
	// (server rejected the request — fail fast instead of blocking on a
	// binary frame that will never arrive).
	select {
	case <-pending.started:
	case err := <-pending.err:
		return "", err
	case <-c.done:
		return "", fmt.Errorf("disconnected")
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("download timed out waiting for server")
	}

	// Read binary frame from Channel 2
	_, data, err := readBinaryFrame(dlChan)
	if err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}

	// Verify content hash before writing anything to disk (catches
	// truncation, bit rot, and transit corruption). The hash was
	// computed on the encrypted bytes by the uploader.
	downloadsMu.Lock()
	expectedHash := pending.contentHash
	downloadsMu.Unlock()
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

	// Eager thumbnail generation: spawn a goroutine to decode +
	// downscale + persist the inline-display thumbnail. By the time
	// the user scrolls to the message, the thumbnail is usually
	// ready, and RenderImageInline takes the fast (read-thumbnail)
	// path. Falls back to its own lazy generation if we get there
	// first. Non-blocking — fire-and-forget; failure is logged at
	// Debug and otherwise silent (auto-preview is opportunistic).
	go func() {
		if err := GenerateThumbnail(localPath, ThumbnailPath(localPath)); err != nil && c.logger != nil {
			c.logger.Debug("thumbnail generation failed",
				"file_id", fileID, "error", err)
		}
	}()

	return localPath, nil
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
//
// hintName is the original sender-supplied filename (e.g. "screenshot.png").
// It's used ONLY to derive a file extension for the OS launcher — the
// cached file at `path` is named after the file_id (e.g.
// "file_xK9mQ2pR7vT3nW8jL5mZ"), which has no extension, so macOS's
// `open` and Linux's `xdg-open` fall back to opening it as text
// (Launch Services / xdg-mime can't identify the format without the
// extension OR a UTI hint we don't supply).
//
// Strategy: ensure a sibling file exists at `path + ext` that the OS
// launcher will see as having the right extension, then pass that
// path to the launcher. Tries hard link first (cheap; same inode,
// no resolution surprise), falls back to copy if hard link fails
// (cross-device or unsupported FS). Avoids symlinks because macOS
// Launch Services resolves them BEFORE UTI lookup, so the launcher
// sees the extensionless target path and falls back to TextEdit.
//
// hintName itself is never used for path construction beyond
// extracting the extension via filepath.Ext, so a hostile sender
// cannot direct writes outside the cache directory.
func OpenFile(path, hintName string) error {
	ext := filepath.Ext(hintName)
	if ext == "" || ext == "." {
		return openInSystemViewer(path)
	}

	linkPath := path + ext
	// Lstat (not Stat) — we want directory-entry info, not target info,
	// because we need to detect stale symlinks left over from earlier
	// versions of this function (which used os.Symlink before we
	// learned macOS Launch Services resolves symlinks before UTI
	// lookup, defeating the extension hint).
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Stale symlink from previous version — remove and recreate
			// as hard link / copy below.
			_ = os.Remove(linkPath)
		} else {
			// Hard link or regular file copy — already correct shape.
			return openInSystemViewer(linkPath)
		}
	}

	// Hard link first — same inode, but the link's path has the
	// extension, and tools that resolve symlinks (macOS `open`)
	// don't "resolve" hard links the same way: each name is equally
	// first-class. Falls back to copy if hard link fails (cross-device,
	// sandboxed FS, fileystem without hard-link support).
	if err := os.Link(path, linkPath); err != nil {
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if werr := os.WriteFile(linkPath, data, 0600); werr != nil {
			return werr
		}
	}
	return openInSystemViewer(linkPath)
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

// generateNanoID creates a nanoid that matches the server's
// store.GenerateID spec (internal/store/nanoid.go:16):
//   - 21-char body (NOT 16 — server's ValidateNanoID rejects shorter)
//   - 64-char alphabet INCLUDING '_' and '-' (NOT just alphanumeric)
//   - Unbiased rejection sampling via crypto/rand.Int (NOT modulo,
//     which biased the previous implementation slightly)
//
// Pre-fix this function emitted 16+len(prefix)-byte IDs which the
// server rejected as "invalid nanoid length: got 19 bytes, want 24"
// for any "up_"-prefixed upload_id. Causing every upload to fail at
// upload_start with a SignalInvalidNanoID rejection. Reproduced
// 2026-05-07.
func generateNanoID(prefix string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"
	b := make([]byte, 21)
	for i := range b {
		n, _ := crand.Int(crand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return prefix + string(b)
}
