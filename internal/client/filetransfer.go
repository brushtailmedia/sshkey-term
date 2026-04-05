package client

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

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

// UploadFile encrypts and uploads a file using the room's current epoch key.
// For DM uploads, use UploadDMFile instead — it takes a per-file key from
// the caller which is stored in the Attachment struct inside the encrypted
// message payload.
//
// Returns the server-assigned file_id.
func (c *Client) UploadFile(localPath, room, conversation string) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	if room == "" {
		return "", fmt.Errorf("UploadFile requires a room; for DM attachments use UploadDMFile")
	}

	c.mu.RLock()
	epoch := c.currentEpoch[room]
	encKey := c.epochKeys[room][epoch]
	c.mu.RUnlock()
	if encKey == nil {
		return "", fmt.Errorf("no epoch key for room %s", room)
	}

	return c.uploadEncrypted(data, encKey, room, conversation)
}

// UploadDMFile encrypts a file with the given per-file key (K_file) and
// uploads it. The caller stores K_file inside the Attachment's FileKey
// field when sending the DM message that references this file_id, so
// recipients can decrypt the file after decrypting the message payload.
//
// This is Design A: each attachment carries its own key in the encrypted
// payload, decoupling upload from message send. See PROTOCOL.md "DM
// attachments".
func (c *Client) UploadDMFile(localPath, conversation string, fileKey []byte) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if len(fileKey) == 0 {
		return "", fmt.Errorf("UploadDMFile: fileKey is required")
	}
	return c.uploadEncrypted(data, fileKey, "", conversation)
}

// uploadEncrypted is the shared transport: encrypts bytes with encKey, runs
// the upload_start → binary frame → upload_complete round-trip, and returns
// the server-assigned file_id.
func (c *Client) uploadEncrypted(data, encKey []byte, room, conversation string) (string, error) {
	encrypted, err := crypto.Encrypt(encKey, data)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	encBytes := []byte(encrypted)

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
		Type:         "upload_start",
		UploadID:     uploadID,
		Size:         int64(len(encBytes)),
		Room:         room,
		Conversation: conversation,
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
	}

	c.mu.RLock()
	binChan := c.binChan
	c.mu.RUnlock()

	if binChan == nil {
		return "", fmt.Errorf("no binary channel (Channel 2 not open)")
	}

	// Hold binChanMu across the whole frame write so concurrent uploads
	// don't interleave bytes within a frame (id_len|id|data_len|data).
	c.binChanMu.Lock()
	err = writeBinaryFrame(binChan, uploadID, encBytes)
	c.binChanMu.Unlock()
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

// DownloadFile downloads and decrypts a file. Returns the local path.
func (c *Client) DownloadFile(fileID string, decryptKey []byte) (string, error) {
	c.mu.RLock()
	binChan := c.binChan
	c.mu.RUnlock()

	if binChan == nil {
		return "", fmt.Errorf("no binary channel")
	}

	// Determine save directory
	dataDir := c.cfg.DataDir
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	filesDir := filepath.Join(dataDir, "files")
	os.MkdirAll(filesDir, 0700)

	// Serialize downloads: we must send the request AND read the reply
	// under the same lock, otherwise two concurrent callers could read
	// each other's frames (server sends frames in request order, but the
	// client has no per-request demux here).
	c.binChanMu.Lock()
	defer c.binChanMu.Unlock()

	// Send download request
	err := c.enc.Encode(protocol.Download{
		Type:   "download",
		FileID: fileID,
	})
	if err != nil {
		return "", err
	}

	// Read binary frame from Channel 2
	_, data, err := readBinaryFrame(binChan)
	if err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}

	// Decrypt
	plaintext, err := crypto.Decrypt(decryptKey, string(data))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	// Save
	localPath := filepath.Join(filesDir, fileID)
	if err := os.WriteFile(localPath, plaintext, 0600); err != nil {
		return "", err
	}

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
