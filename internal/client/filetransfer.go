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
	fileID   chan string // receives file_id on completion
	err      chan error
}

var (
	uploadsMu sync.Mutex
	uploads   = make(map[string]*pendingUpload)
)

// UploadFile encrypts and uploads a file. Returns the server-assigned file_id.
func (c *Client) UploadFile(localPath, room, conversation string) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// Generate encryption key
	var encKey []byte
	if room != "" {
		c.mu.RLock()
		epoch := c.currentEpoch[room]
		encKey = c.epochKeys[room][epoch]
		c.mu.RUnlock()
		if encKey == nil {
			return "", fmt.Errorf("no epoch key for room %s", room)
		}
	} else {
		// DM: generate per-file key (caller wraps it in the message)
		encKey, err = crypto.GenerateKey()
		if err != nil {
			return "", err
		}
	}

	// Encrypt file
	encrypted, err := crypto.Encrypt(encKey, data)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	encBytes := []byte(encrypted)

	uploadID := generateNanoID("up_")

	// Register pending upload
	pending := &pendingUpload{
		uploadID: uploadID,
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

	// Send upload_start
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

	// The server responds with upload_ready, then we send binary data.
	// upload_ready and upload_complete are handled by handleInternal.
	// For simplicity, send the binary data immediately after upload_start —
	// the server queues it until ready.

	c.mu.RLock()
	binChan := c.binChan
	c.mu.RUnlock()

	if binChan == nil {
		return "", fmt.Errorf("no binary channel (Channel 2 not open)")
	}

	if err := writeBinaryFrame(binChan, uploadID, encBytes); err != nil {
		return "", fmt.Errorf("write binary: %w", err)
	}

	// Wait for upload_complete with file_id
	select {
	case fileID := <-pending.fileID:
		return fileID, nil
	case err := <-pending.err:
		return "", err
	case <-c.done:
		return "", fmt.Errorf("disconnected")
	}
}

// HandleUploadComplete is called from handleInternal when upload_complete arrives.
func HandleUploadComplete(uploadID, fileID string) {
	uploadsMu.Lock()
	p, ok := uploads[uploadID]
	uploadsMu.Unlock()
	if ok {
		p.fileID <- fileID
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
