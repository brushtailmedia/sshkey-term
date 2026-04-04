package client

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// FileTransfer handles file upload and download via SSH Channel 2.
type FileTransfer struct {
	client  *Client
	binChan ssh.Channel // Channel 2
}

// OpenBinaryChannel opens SSH Channel 2 for file transfers.
func (c *Client) OpenBinaryChannel() error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	ch, reqs, err := c.conn.OpenChannel("session", nil)
	if err != nil {
		return fmt.Errorf("open binary channel: %w", err)
	}
	go ssh.DiscardRequests(reqs)

	c.mu.Lock()
	c.binChan = ch
	c.mu.Unlock()

	return nil
}

// UploadFile encrypts and uploads a file, returns the file_id.
func (c *Client) UploadFile(localPath, room, conversation string) (string, error) {
	// Read the file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// Generate encryption key
	var encKey []byte
	if room != "" {
		// Room: use current epoch key
		c.mu.RLock()
		epoch := c.currentEpoch[room]
		encKey = c.epochKeys[room][epoch]
		c.mu.RUnlock()
		if encKey == nil {
			return "", fmt.Errorf("no epoch key for room %s", room)
		}
	} else {
		// DM: generate a per-file key (same as per-message key)
		encKey, err = crypto.GenerateKey()
		if err != nil {
			return "", err
		}
	}

	// Encrypt file content
	encrypted, err := crypto.Encrypt(encKey, data)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}

	encBytes := []byte(encrypted)

	// Generate upload ID
	uploadID := generateNanoID("up_")

	// Send upload_start on Channel 1
	err = c.enc.Encode(protocol.UploadStart{
		Type:         "upload_start",
		UploadID:     uploadID,
		Size:         int64(len(encBytes)),
		Room:         room,
		Conversation: conversation,
	})
	if err != nil {
		return "", err
	}

	// Wait for upload_ready
	// The response comes through the normal message channel
	// TODO: proper synchronization — for now, proceed immediately

	// Send binary frame on Channel 2
	c.mu.RLock()
	binChan := c.binChan
	c.mu.RUnlock()

	if binChan != nil {
		if err := writeBinaryFrame(binChan, uploadID, encBytes); err != nil {
			return "", fmt.Errorf("write binary: %w", err)
		}
	}

	// upload_complete comes through the message channel with file_id
	// Return empty for now — the caller should wait for upload_complete
	return "", nil
}

// DownloadFile downloads and decrypts a file from the server.
func (c *Client) DownloadFile(fileID, localDir string, decryptKey []byte) (string, error) {
	// Send download request on Channel 1
	err := c.enc.Encode(protocol.Download{
		Type:   "download",
		FileID: fileID,
	})
	if err != nil {
		return "", err
	}

	// Read binary frame from Channel 2
	c.mu.RLock()
	binChan := c.binChan
	c.mu.RUnlock()

	if binChan == nil {
		return "", fmt.Errorf("no binary channel open")
	}

	id, data, err := readBinaryFrame(binChan)
	if err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}
	_ = id

	// Decrypt
	plaintext, err := crypto.Decrypt(decryptKey, string(data))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	// Save to disk
	localPath := filepath.Join(localDir, fileID)
	if err := os.MkdirAll(localDir, 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(localPath, plaintext, 0600); err != nil {
		return "", err
	}

	return localPath, nil
}

// writeBinaryFrame writes a Channel 2 binary frame.
func writeBinaryFrame(w io.Writer, id string, data []byte) error {
	// id_len (1 byte)
	if _, err := w.Write([]byte{byte(len(id))}); err != nil {
		return err
	}
	// id (variable)
	if _, err := w.Write([]byte(id)); err != nil {
		return err
	}
	// data_len (8 bytes, big-endian)
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	// data
	_, err := w.Write(data)
	return err
}

// readBinaryFrame reads a Channel 2 binary frame.
func readBinaryFrame(r io.Reader) (string, []byte, error) {
	// id_len (1 byte)
	var idLen [1]byte
	if _, err := io.ReadFull(r, idLen[:]); err != nil {
		return "", nil, err
	}
	// id
	idBuf := make([]byte, idLen[0])
	if _, err := io.ReadFull(r, idBuf); err != nil {
		return "", nil, err
	}
	// data_len (8 bytes)
	var lenBuf [8]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", nil, err
	}
	dataLen := binary.BigEndian.Uint64(lenBuf[:])
	// data
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", nil, err
	}

	return string(idBuf), data, nil
}

// generateNanoID creates a simple ID with a prefix.
func generateNanoID(prefix string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 16)
	cryptoRandRead(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + string(b)
}

func cryptoRandRead(b []byte) {
	crand.Read(b)
}
