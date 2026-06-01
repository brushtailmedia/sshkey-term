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
	"math/big"
	"sort"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/ssh"

	"filippo.io/edwards25519"
)

// keySize is the required AES-256 key length in bytes. Encrypt/Decrypt assert
// the key is exactly this, so a short 16- or 24-byte key can't silently
// downgrade to AES-128/192 — aes.NewCipher accepts all three lengths (audit F5).
const keySize = 32

// Encrypt encrypts plaintext with AES-256-GCM using the given key.
// Returns base64-encoded nonce + ciphertext.
func Encrypt(key, plaintext []byte) (string, error) {
	if len(key) != keySize {
		return "", fmt.Errorf("encrypt: key must be %d bytes (AES-256), got %d", keySize, len(key))
	}
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
	if len(key) != keySize {
		return nil, fmt.Errorf("decrypt: key must be %d bytes (AES-256), got %d", keySize, len(key))
	}
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
	key := make([]byte, keySize)
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

// appendField appends field to out, prefixed by its big-endian uint32 length.
// This is the length-prefixing primitive for the signed canonical forms: it
// makes adjacent variable-length fields unambiguous, so two different field
// tuples can never produce the same signed bytes — without relying on any field
// (room/DM/group ID) being a fixed length (audit F2).
func appendField(out, field []byte) []byte {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(field)))
	out = append(out, l[:]...)
	return append(out, field...)
}

// SignRoom signs a room message. Canonical form (audit F2 — domain-tagged so a
// SignRoom signature can't cross-verify as a DM/edit signature, and
// length-prefixed so the payload/room boundary is unambiguous):
//
//	Sign("room:v1" || u32_be(len(payload)) || payload || u32_be(len(room)) || room || u64_be(epoch))
func SignRoom(privKey ed25519.PrivateKey, payloadBytes []byte, room string, epoch int64) []byte {
	return ed25519.Sign(privKey, buildRoomCanonical(payloadBytes, room, epoch))
}

// VerifyRoom verifies a room message signature against the SignRoom form.
func VerifyRoom(pubKey ed25519.PublicKey, payloadBytes []byte, room string, epoch int64, sig []byte) bool {
	return ed25519.Verify(pubKey, buildRoomCanonical(payloadBytes, room, epoch), sig)
}

// buildRoomCanonical builds the SignRoom/VerifyRoom input. Shared so Sign and
// Verify cannot drift.
func buildRoomCanonical(payloadBytes []byte, room string, epoch int64) []byte {
	const tag = "room:v1"
	out := make([]byte, 0, len(tag)+4+len(payloadBytes)+4+len(room)+8)
	out = append(out, tag...)
	out = appendField(out, payloadBytes)
	out = appendField(out, []byte(room))
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	return append(out, epochBytes[:]...)
}

// SignDM signs a DM / group-DM message. Canonical form (audit F2):
//
//	Sign("dm:v1" || u32_be(len(payload)) || payload || u32_be(len(conversation)) || conversation || wrapped_keys_canonical)
func SignDM(privKey ed25519.PrivateKey, payloadBytes []byte, conversation string, wrappedKeys map[string]string) []byte {
	return ed25519.Sign(privKey, buildDMCanonical(payloadBytes, conversation, wrappedKeys))
}

// VerifyDM verifies a DM / group-DM message signature against the SignDM form.
func VerifyDM(pubKey ed25519.PublicKey, payloadBytes []byte, conversation string, wrappedKeys map[string]string, sig []byte) bool {
	return ed25519.Verify(pubKey, buildDMCanonical(payloadBytes, conversation, wrappedKeys), sig)
}

// buildDMCanonical builds the SignDM/VerifyDM input. Shared so Sign and Verify
// cannot drift. wrappedKeysCanonical is itself self-framing and runs to the end
// of the message, so it needs no outer length prefix.
func buildDMCanonical(payloadBytes []byte, conversation string, wrappedKeys map[string]string) []byte {
	const tag = "dm:v1"
	out := make([]byte, 0, len(tag)+4+len(payloadBytes)+4+len(conversation)+len(wrappedKeys)*120)
	out = append(out, tag...)
	out = appendField(out, payloadBytes)
	out = appendField(out, []byte(conversation))
	return append(out, wrappedKeysCanonical(wrappedKeys)...)
}

// SignRoomEdit signs a room message edit envelope. Distinct from
// SignRoom in two ways: (1) the canonical form binds the signature to a
// specific msg.ID via a length-prefixed msgID field, preventing a
// compromised server from replaying A's past signed `(payload, room,
// epoch)` across different msgIDs to rewrite history via the edit path;
// (2) a domain-separation tag (`"edit_room:"`) guarantees that a
// signature produced by SignRoom can never cross-verify as a SignRoomEdit
// signature regardless of how the attacker constructs inputs.
//
// Canonical form:
//
//	Sign("edit_room:" || u32_be(len(msgID)) || msgID || u32_be(len(payload)) || payload || u32_be(len(room)) || room || u64_be(epoch))
//
// Phase 21 item 3 — defense-in-depth against the substitution attack
// made newly exploitable by Phase 15's `edited` broadcasts overwriting
// existing message rows. See refactor_plan.md Phase 21 scope item 3 for
// the full attack analysis.
func SignRoomEdit(privKey ed25519.PrivateKey, msgID string, payloadBytes []byte, room string, epoch int64) []byte {
	msg := buildRoomEditCanonical(msgID, payloadBytes, room, epoch)
	return ed25519.Sign(privKey, msg)
}

// VerifyRoomEdit verifies a room message edit signature against the
// SignRoomEdit canonical form. Returns false if the signature was
// produced by SignRoom (send-path), bound to a different msgID, or
// otherwise mismatches.
func VerifyRoomEdit(pubKey ed25519.PublicKey, msgID string, payloadBytes []byte, room string, epoch int64, sig []byte) bool {
	msg := buildRoomEditCanonical(msgID, payloadBytes, room, epoch)
	return ed25519.Verify(pubKey, msg, sig)
}

// SignDMEdit signs a DM/group edit envelope with msgID binding. Used
// for both 1:1 DMs and group DMs (same canonical form shape — the
// `conversation` parameter carries either the DM ID or the group ID,
// and wrappedKeys differs by context so signatures don't cross over).
//
// Canonical form:
//
//	Sign("edit_dm:" || u32_be(len(msgID)) || msgID || u32_be(len(payload)) || payload || u32_be(len(conversation)) || conversation || wrapped_keys_canonical)
//
// Phase 21 item 3.
func SignDMEdit(privKey ed25519.PrivateKey, msgID string, payloadBytes []byte, conversation string, wrappedKeys map[string]string) []byte {
	msg := buildDMEditCanonical(msgID, payloadBytes, conversation, wrappedKeys)
	return ed25519.Sign(privKey, msg)
}

// VerifyDMEdit verifies a DM/group edit signature.
func VerifyDMEdit(pubKey ed25519.PublicKey, msgID string, payloadBytes []byte, conversation string, wrappedKeys map[string]string, sig []byte) bool {
	msg := buildDMEditCanonical(msgID, payloadBytes, conversation, wrappedKeys)
	return ed25519.Verify(pubKey, msg, sig)
}

// buildRoomEditCanonical constructs the SignRoomEdit / VerifyRoomEdit
// input bytes. Shared helper so Sign and Verify cannot drift.
func buildRoomEditCanonical(msgID string, payloadBytes []byte, room string, epoch int64) []byte {
	const tag = "edit_room:"
	out := make([]byte, 0, len(tag)+4+len(msgID)+4+len(payloadBytes)+4+len(room)+8)
	out = append(out, tag...)
	out = appendField(out, []byte(msgID))
	out = appendField(out, payloadBytes)
	out = appendField(out, []byte(room))
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	return append(out, epochBytes[:]...)
}

// buildDMEditCanonical constructs the SignDMEdit / VerifyDMEdit input
// bytes. Shared so Sign and Verify cannot drift.
func buildDMEditCanonical(msgID string, payloadBytes []byte, conversation string, wrappedKeys map[string]string) []byte {
	const tag = "edit_dm:"
	out := make([]byte, 0, len(tag)+4+len(msgID)+4+len(payloadBytes)+4+len(conversation)+len(wrappedKeys)*120)
	out = append(out, tag...)
	out = appendField(out, []byte(msgID))
	out = appendField(out, payloadBytes)
	out = appendField(out, []byte(conversation))
	return append(out, wrappedKeysCanonical(wrappedKeys)...)
}

// SignUnreact signs a reaction-removal (un-react) request. Like delete there is
// no encrypted payload — the signature binds the conversation context and the
// server-assigned reaction_id, so a compromised relay can neither forge an
// un-react attributed to a user who never sent one, nor replay a genuine one
// against a different reaction or context (audit F6). The actor is bound by
// verifying against the claimed user's pinned key, as elsewhere — not by living
// in the signed bytes. v2 binds context (rather than resting on reaction-id
// uniqueness) for the same reason delete does, keeping all signed F6 actions
// uniformly context-bound.
//
// Canonical form (kind is one of "room"/"group"/"dm"; contextID is the
// room/group/DM id):
//
//	Sign("unreact:v2" || u32_be(len(kind)) || kind || u32_be(len(contextID)) || contextID || u32_be(len(reactionID)) || reactionID)
func SignUnreact(privKey ed25519.PrivateKey, kind, contextID, reactionID string) []byte {
	return ed25519.Sign(privKey, buildUnreactCanonical(kind, contextID, reactionID))
}

// VerifyUnreact verifies an un-react signature against the SignUnreact form.
func VerifyUnreact(pubKey ed25519.PublicKey, kind, contextID, reactionID string, sig []byte) bool {
	return ed25519.Verify(pubKey, buildUnreactCanonical(kind, contextID, reactionID), sig)
}

// buildUnreactCanonical builds the SignUnreact/VerifyUnreact input. Shared so
// Sign and Verify cannot drift.
func buildUnreactCanonical(kind, contextID, reactionID string) []byte {
	const tag = "unreact:v2"
	out := make([]byte, 0, len(tag)+4+len(kind)+4+len(contextID)+4+len(reactionID))
	out = append(out, tag...)
	out = appendField(out, []byte(kind))
	out = appendField(out, []byte(contextID))
	return appendField(out, []byte(reactionID))
}

// SignDelete signs a message-deletion request. Like un-react there is no
// encrypted payload — the signature binds the conversation context and the
// target message id, so a compromised relay can neither forge a delete
// attributed to a user who never requested one, nor replay a genuine delete
// against a different message or context (audit F6). The actor is bound by
// verifying against the claimed deleter's pinned key, as elsewhere; it is not in
// the signed bytes. Binding the context (rather than relying on the global
// uniqueness of msgIDs) keeps the signature sound regardless of the ID scheme
// and matches every other signed form.
//
// Canonical form (kind is one of "room"/"group"/"dm"; contextID is the
// room/group/DM id):
//
//	Sign("delete:v1" || u32_be(len(kind)) || kind || u32_be(len(contextID)) || contextID || u32_be(len(msgID)) || msgID)
func SignDelete(privKey ed25519.PrivateKey, kind, contextID, msgID string) []byte {
	return ed25519.Sign(privKey, buildDeleteCanonical(kind, contextID, msgID))
}

// VerifyDelete verifies a delete signature against the SignDelete form.
func VerifyDelete(pubKey ed25519.PublicKey, kind, contextID, msgID string, sig []byte) bool {
	return ed25519.Verify(pubKey, buildDeleteCanonical(kind, contextID, msgID), sig)
}

// buildDeleteCanonical builds the SignDelete/VerifyDelete input. Shared so Sign
// and Verify cannot drift.
func buildDeleteCanonical(kind, contextID, msgID string) []byte {
	const tag = "delete:v1"
	out := make([]byte, 0, len(tag)+4+len(kind)+4+len(contextID)+4+len(msgID))
	out = append(out, tag...)
	out = appendField(out, []byte(kind))
	out = appendField(out, []byte(contextID))
	return appendField(out, []byte(msgID))
}

// wrappedKeysCanonical serializes the wrapped-key map into a canonical,
// unambiguous byte string: entries in sorted-username order, each as a
// length-prefixed username followed by its length-prefixed decoded key bytes
// (audit F2). Binding the username — not just sorting by it — and
// length-prefixing both fields means two different maps cannot collide onto the
// same bytes. Shared by SignDM and SignDMEdit.
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
		result = appendField(result, []byte(u))
		result = appendField(result, decoded)
	}
	return result
}

// SafetyNumber computes the out-of-band key-verification number for two users:
// SHA256 over their sorted public-key bytes, rendered as 32 decimal digits in
// eight groups of four. Symmetric in the two keys.
//
// The 32 digits are a *uniform* reduction of the full 256-bit hash —
// bigint(hash) mod 10^32 — using all 32 bytes. The previous form reduced each
// byte with `%100` and summed pairs, which was biased and used only 24 of the
// 32 hash bytes (audit F3). The mod-10^32 reduction bias here is ~2^-150, i.e.
// negligible.
func SafetyNumber(pubKeyA, pubKeyB ed25519.PublicKey) string {
	a := []byte(pubKeyA)
	b := []byte(pubKeyB)

	// Sort lexicographically so the number is symmetric in the two keys.
	if string(a) > string(b) {
		a, b = b, a
	}

	combined := append(a, b...)
	hash := sha256.Sum256(combined)

	// Uniform 32-digit encoding of all 32 hash bytes.
	n := new(big.Int).SetBytes(hash[:])
	mod := new(big.Int).Exp(big.NewInt(10), big.NewInt(32), nil) // 10^32
	digits := fmt.Sprintf("%032d", n.Mod(n, mod))                // exactly 32 digits, zero-padded

	// Format as eight groups of four.
	return fmt.Sprintf("%s %s %s %s %s %s %s %s",
		digits[0:4], digits[4:8], digits[8:12], digits[12:16],
		digits[16:20], digits[20:24], digits[24:28], digits[28:32])
}

// MemberHash computes SHA256 over the sorted member usernames for epoch-rotation
// verification (F7). Each username is length-prefixed (u32_be) before hashing,
// so the result is unambiguous regardless of username length — two different
// member sets can never hash alike via boundary re-splitting (audit F2; the old
// raw concatenation relied on usernames being a fixed-length nanoid). Output:
// "SHA256:" + hex.
func MemberHash(members []string) string {
	sorted := make([]string, len(members))
	copy(sorted, members)
	sort.Strings(sorted)

	h := sha256.New()
	var l [4]byte
	for _, m := range sorted {
		binary.BigEndian.PutUint32(l[:], uint32(len(m)))
		h.Write(l[:])
		h.Write([]byte(m))
	}
	return fmt.Sprintf("SHA256:%x", h.Sum(nil))
}

// SignEpochRoster signs a room epoch's member attestation (F7). The rotating
// client signs the MemberHash it computed over the set it wrapped the epoch key
// for, binding it to (room, epoch). Verifiers check it against the generator's
// pinned key, then compare MemberHash to their own roster — so a relay cannot
// rewrite the hash to match a victim's roster (an unsigned hash is forgeable).
//
// Canonical form (domain-separated + length-prefixed, per the F2 lesson; the
// "v1" tag is the version hook for a future device-set hash under Tier 2):
//
//	Sign("epoch_roster:v1" || uint32_be(len(room)) || room || uint64_be(epoch) || uint32_be(len(memberHash)) || memberHash)
func SignEpochRoster(privKey ed25519.PrivateKey, room string, epoch int64, memberHash string) []byte {
	return ed25519.Sign(privKey, buildEpochRosterCanonical(room, epoch, memberHash))
}

// VerifyEpochRoster verifies a SignEpochRoster signature. Returns false on any
// mismatch (wrong room/epoch/hash, or a signature from a different domain).
func VerifyEpochRoster(pubKey ed25519.PublicKey, room string, epoch int64, memberHash string, sig []byte) bool {
	return ed25519.Verify(pubKey, buildEpochRosterCanonical(room, epoch, memberHash), sig)
}

// buildEpochRosterCanonical constructs the SignEpochRoster / VerifyEpochRoster
// input bytes. Shared so Sign and Verify cannot drift.
func buildEpochRosterCanonical(room string, epoch int64, memberHash string) []byte {
	const tag = "epoch_roster:v1"
	out := make([]byte, 0, len(tag)+4+len(room)+8+4+len(memberHash))
	out = append(out, tag...)
	var roomLen [4]byte
	binary.BigEndian.PutUint32(roomLen[:], uint32(len(room)))
	out = append(out, roomLen[:]...)
	out = append(out, []byte(room)...)
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	out = append(out, epochBytes[:]...)
	var mhLen [4]byte
	binary.BigEndian.PutUint32(mhLen[:], uint32(len(memberHash)))
	out = append(out, mhLen[:]...)
	out = append(out, []byte(memberHash)...)
	return out
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

// ContentHash computes a BLAKE2b-256 hash of the given data and returns it
// in tagged format: "blake2b-256:<hex>". Used to verify file integrity on
// upload and download — hash is computed on the encrypted bytes.
func ContentHash(data []byte) string {
	h := blake2b.Sum256(data)
	return fmt.Sprintf("blake2b-256:%x", h)
}

// VerifyContentHash checks that data matches the expected tagged hash string.
// Returns nil on match, error on mismatch or invalid format.
func VerifyContentHash(data []byte, expected string) error {
	actual := ContentHash(data)
	if actual != expected {
		return fmt.Errorf("content hash mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}
