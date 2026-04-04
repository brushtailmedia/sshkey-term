// Package crypto implements the client-side E2E encryption for sshkey-chat.
//
// Operations:
//   - AES-256-GCM encryption/decryption (messages)
//   - X25519 + HKDF + AES-256-GCM key wrapping/unwrapping (epoch keys and per-message keys)
//   - Ed25519 message signatures
//   - Ed25519 -> X25519 key conversion
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/ssh"

	"filippo.io/edwards25519"
)

// Encrypt encrypts plaintext with AES-256-GCM using the given key.
// Returns base64-encoded nonce + ciphertext.
func Encrypt(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded nonce + ciphertext with AES-256-GCM.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce := data[:gcm.NonceSize()]
	ciphertext := data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// GenerateKey generates a random 256-bit AES key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapKey wraps a symmetric key for a recipient using their Ed25519 public key.
// Returns base64-encoded ephemeral_pub + nonce + ciphertext.
func WrapKey(symmetricKey []byte, recipientPubKey ed25519.PublicKey) (string, error) {
	// Convert Ed25519 public key to X25519
	recipientX25519, err := ed25519PubToX25519(recipientPubKey)
	if err != nil {
		return "", fmt.Errorf("convert pubkey: %w", err)
	}

	// Generate ephemeral X25519 keypair
	ephPriv := make([]byte, 32)
	if _, err := rand.Read(ephPriv); err != nil {
		return "", err
	}
	ephPub, err := curve25519.X25519(ephPriv, curve25519.Basepoint)
	if err != nil {
		return "", err
	}

	// ECDH
	sharedSecret, err := curve25519.X25519(ephPriv, recipientX25519)
	if err != nil {
		return "", err
	}

	// HKDF
	hkdfReader := hkdf.New(sha256.New, sharedSecret, ephPub, []byte("sshkey-chat key wrap"))
	wrappingKey := make([]byte, 32)
	if _, err := hkdfReader.Read(wrappingKey); err != nil {
		return "", err
	}

	// AES-256-GCM wrap
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, symmetricKey, nil)

	// wrapped = ephPub || nonce || ciphertext
	wrapped := make([]byte, 0, len(ephPub)+len(nonce)+len(ciphertext))
	wrapped = append(wrapped, ephPub...)
	wrapped = append(wrapped, nonce...)
	wrapped = append(wrapped, ciphertext...)

	return base64.StdEncoding.EncodeToString(wrapped), nil
}

// UnwrapKey unwraps a symmetric key using the recipient's Ed25519 private key.
func UnwrapKey(wrappedBase64 string, privKey ed25519.PrivateKey) ([]byte, error) {
	wrapped, err := base64.StdEncoding.DecodeString(wrappedBase64)
	if err != nil {
		return nil, err
	}

	// Parse ephPub (32 bytes) + nonce (12 bytes) + ciphertext (rest)
	if len(wrapped) < 32+12 {
		return nil, fmt.Errorf("wrapped key too short")
	}
	ephPub := wrapped[:32]
	nonce := wrapped[32:44]
	ciphertext := wrapped[44:]

	// Convert Ed25519 private key to X25519
	privX25519 := ed25519PrivToX25519(privKey)

	// ECDH
	sharedSecret, err := curve25519.X25519(privX25519, ephPub)
	if err != nil {
		return nil, err
	}

	// HKDF
	hkdfReader := hkdf.New(sha256.New, sharedSecret, ephPub, []byte("sshkey-chat key wrap"))
	wrappingKey := make([]byte, 32)
	if _, err := hkdfReader.Read(wrappingKey); err != nil {
		return nil, err
	}

	// AES-256-GCM unwrap
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return gcm.Open(nil, nonce, ciphertext, nil)
}

// SignRoom signs a room message: Sign(payload_bytes || room_name_utf8 || epoch_as_big_endian_uint64)
func SignRoom(privKey ed25519.PrivateKey, payloadBytes []byte, room string, epoch int64) []byte {
	msg := make([]byte, 0, len(payloadBytes)+len(room)+8)
	msg = append(msg, payloadBytes...)
	msg = append(msg, []byte(room)...)
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	msg = append(msg, epochBytes[:]...)
	return ed25519.Sign(privKey, msg)
}

// VerifyRoom verifies a room message signature.
func VerifyRoom(pubKey ed25519.PublicKey, payloadBytes []byte, room string, epoch int64, sig []byte) bool {
	msg := make([]byte, 0, len(payloadBytes)+len(room)+8)
	msg = append(msg, payloadBytes...)
	msg = append(msg, []byte(room)...)
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	msg = append(msg, epochBytes[:]...)
	return ed25519.Verify(pubKey, msg, sig)
}

// SignDM signs a DM message: Sign(payload_bytes || conversation_id_utf8 || wrapped_keys_canonical)
func SignDM(privKey ed25519.PrivateKey, payloadBytes []byte, conversation string, wrappedKeys map[string]string) []byte {
	msg := make([]byte, 0, len(payloadBytes)+len(conversation)+len(wrappedKeys)*100)
	msg = append(msg, payloadBytes...)
	msg = append(msg, []byte(conversation)...)
	msg = append(msg, wrappedKeysCanonical(wrappedKeys)...)
	return ed25519.Sign(privKey, msg)
}

// VerifyDM verifies a DM message signature.
func VerifyDM(pubKey ed25519.PublicKey, payloadBytes []byte, conversation string, wrappedKeys map[string]string, sig []byte) bool {
	msg := make([]byte, 0, len(payloadBytes)+len(conversation)+len(wrappedKeys)*100)
	msg = append(msg, payloadBytes...)
	msg = append(msg, []byte(conversation)...)
	msg = append(msg, wrappedKeysCanonical(wrappedKeys)...)
	return ed25519.Verify(pubKey, msg, sig)
}

// wrappedKeysCanonical returns wrapped key values concatenated in sorted username order.
func wrappedKeysCanonical(wrappedKeys map[string]string) []byte {
	usernames := make([]string, 0, len(wrappedKeys))
	for u := range wrappedKeys {
		usernames = append(usernames, u)
	}
	sort.Strings(usernames)

	var result []byte
	for _, u := range usernames {
		decoded, err := base64.StdEncoding.DecodeString(wrappedKeys[u])
		if err != nil {
			continue
		}
		result = append(result, decoded...)
	}
	return result
}

// SafetyNumber computes the safety number for two users.
// SHA256(sort(pubkey_a_bytes, pubkey_b_bytes)) -> 24 digits in six groups of four.
func SafetyNumber(pubKeyA, pubKeyB ed25519.PublicKey) string {
	a := []byte(pubKeyA)
	b := []byte(pubKeyB)

	// Sort lexicographically
	if string(a) > string(b) {
		a, b = b, a
	}

	combined := append(a, b...)
	hash := sha256.Sum256(combined)

	// Convert to 24 digits (6 groups of 4)
	digits := ""
	for i := 0; i < 12; i++ {
		val := int(hash[i])%100 + int(hash[i+12])%100
		digits += fmt.Sprintf("%02d", val%100)
	}

	// Format as six groups of four
	return fmt.Sprintf("%s %s %s %s %s %s",
		digits[0:4], digits[4:8], digits[8:12],
		digits[12:16], digits[16:20], digits[20:24])
}

// MemberHash computes SHA256(sort(member_usernames)) for epoch rotation verification.
func MemberHash(members []string) string {
	sorted := make([]string, len(members))
	copy(sorted, members)
	sort.Strings(sorted)

	h := sha256.New()
	for _, m := range sorted {
		h.Write([]byte(m))
	}
	return fmt.Sprintf("SHA256:%x", h.Sum(nil))
}

// ParseSSHPubKey extracts an ed25519.PublicKey from an SSH authorized_key format string.
func ParseSSHPubKey(authorizedKey string) (ed25519.PublicKey, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey))
	if err != nil {
		return nil, err
	}

	cryptoPub, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil, fmt.Errorf("key does not implement CryptoPublicKey")
	}

	edKey, ok := cryptoPub.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 key")
	}

	return edKey, nil
}

// ed25519PubToX25519 converts an Ed25519 public key to X25519.
func ed25519PubToX25519(pub ed25519.PublicKey) ([]byte, error) {
	p, err := new(edwards25519.Point).SetBytes(pub)
	if err != nil {
		return nil, err
	}
	return p.BytesMontgomery(), nil
}

// ed25519PrivToX25519 converts an Ed25519 private key to X25519.
// Ed25519 derives the scalar from SHA-512 of the seed (not SHA-256).
func ed25519PrivToX25519(priv ed25519.PrivateKey) []byte {
	h := sha512.Sum512(priv.Seed())
	// Clamp (standard X25519 clamping)
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	return h[:32]
}
