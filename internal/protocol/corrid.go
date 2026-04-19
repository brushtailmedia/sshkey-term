package protocol

// Phase 17c Step 5 — correlation ID generation + validation.
//
// Client generates a corr_id for each outbound request that supports it
// (15 verbs per the server-side plan); the server echoes the value back
// in both the error response and the authoritative success broadcast,
// giving the client an unambiguous way to correlate each sent frame to
// its server-acknowledged outcome.
//
// Format mirrors sshkey-chat's server side: `corr_` prefix + 21
// characters from the nanoid alphabet. 126 bits of entropy in the
// random body. Empty (omitempty) is valid on the wire and means "the
// client isn't tracking this request".

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	corrIDPrefix   = "corr_"
	corrIDBodyLen  = 21
	corrIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"
)

var (
	ErrInvalidCorrIDLength   = errors.New("invalid corr_id length")
	ErrInvalidCorrIDPrefix   = errors.New("invalid corr_id prefix")
	ErrInvalidCorrIDAlphabet = errors.New("invalid corr_id alphabet")
)

// GenerateCorrID returns a fresh correlation ID. Uses crypto/rand for
// each character so consecutive calls are unpredictable by other
// observers.
func GenerateCorrID() string {
	body := make([]byte, corrIDBodyLen)
	for i := range body {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(corrIDAlphabet))))
		body[i] = corrIDAlphabet[n.Int64()]
	}
	return corrIDPrefix + string(body)
}

// ValidateCorrID reports whether id is a well-formed corr_xxx value.
// Empty id is valid (absent-from-wire convention). Non-empty must be
// exactly `corr_` + 21 body characters from the nanoid alphabet.
func ValidateCorrID(id string) error {
	if id == "" {
		return nil
	}
	want := len(corrIDPrefix) + corrIDBodyLen
	if len(id) != want {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidCorrIDLength, len(id), want)
	}
	if !strings.HasPrefix(id, corrIDPrefix) {
		return fmt.Errorf("%w: id does not start with %q", ErrInvalidCorrIDPrefix, corrIDPrefix)
	}
	for i := len(corrIDPrefix); i < len(id); i++ {
		if strings.IndexByte(corrIDAlphabet, id[i]) < 0 {
			return fmt.Errorf("%w: byte outside alphabet at position %d", ErrInvalidCorrIDAlphabet, i)
		}
	}
	return nil
}
