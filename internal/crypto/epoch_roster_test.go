package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// F7 — SignEpochRoster / VerifyEpochRoster: the signed room member attestation.
// The signature binds (room, epoch, member_hash) and a commitment to the epoch
// key, so a relay can neither rewrite the hash to match a victim's roster nor
// keep the attestation and swap the key under it, and it is domain-separated so
// it cannot be confused with a room-send signature.

func newEpochKey(t *testing.T) []byte {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestSignEpochRoster_RoundTripAndTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const room = "rm_general"
	const epoch = int64(7)
	mh := MemberHash([]string{"alice", "bob"})
	key := newEpochKey(t)

	sig, err := SignEpochRoster(priv, room, epoch, mh, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !VerifyEpochRoster(pub, room, epoch, mh, key, sig) {
		t.Fatal("valid epoch-roster signature should verify")
	}

	// Each bound field, tampered, must fail verification.
	if VerifyEpochRoster(pub, "rm_other", epoch, mh, key, sig) {
		t.Error("different room must not verify")
	}
	if VerifyEpochRoster(pub, room, epoch+1, mh, key, sig) {
		t.Error("different epoch must not verify")
	}
	if VerifyEpochRoster(pub, room, epoch, MemberHash([]string{"alice", "bob", "eve"}), key, sig) {
		t.Error("different member hash (added 'eve') must not verify")
	}
	if VerifyEpochRoster(pub, room, epoch, mh, newEpochKey(t), sig) {
		t.Error("a different epoch key must not verify")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if VerifyEpochRoster(otherPub, room, epoch, mh, key, sig) {
		t.Error("wrong pubkey must not verify")
	}
}

// Domain separation: an epoch-roster signature and a room-send signature must
// not cross-verify, even over coincidentally-overlapping inputs — so a captured
// SignRoom signature can't be replayed as an attestation, or vice versa.
func TestSignEpochRoster_DomainSeparation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const room = "rm_general"
	const epoch = int64(1)
	mh := MemberHash([]string{"alice"})
	key := newEpochKey(t)

	rosterSig, err := SignEpochRoster(priv, room, epoch, mh, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sendSig := SignRoom(priv, []byte(mh), room, epoch)

	if VerifyEpochRoster(pub, room, epoch, mh, key, sendSig) {
		t.Error("a SignRoom signature must not verify as an epoch-roster attestation")
	}
	if VerifyRoom(pub, []byte(mh), room, epoch, rosterSig) {
		t.Error("an epoch-roster signature must not verify as a room-send signature")
	}
}

// An attestation in the form used before the key was covered ("epoch_roster:v1":
// room, epoch and member hash only) must not verify. There is no fallback.
func TestSignEpochRoster_FormWithoutTheKeyDoesNotVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const room = "rm_general"
	const epoch = int64(3)
	mh := MemberHash([]string{"alice", "bob"})

	old := []byte("epoch_roster:v1")
	old = appendField(old, []byte(room))
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], uint64(epoch))
	old = append(old, epochBytes[:]...)
	old = appendField(old, []byte(mh))
	oldSig := ed25519.Sign(priv, old)

	if VerifyEpochRoster(pub, room, epoch, mh, newEpochKey(t), oldSig) {
		t.Error("an attestation that does not cover the key must not verify")
	}
}

func TestSignEpochRoster_KeyOfTheWrongLength(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	mh := MemberHash([]string{"alice"})
	short := make([]byte, 16)

	if _, err := SignEpochRoster(priv, "rm_general", 1, mh, short); err == nil {
		t.Error("signing over a 16-byte key should fail")
	}
	sig, err := SignEpochRoster(priv, "rm_general", 1, mh, newEpochKey(t))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if VerifyEpochRoster(pub, "rm_general", 1, mh, short, sig) {
		t.Error("a 16-byte key must not verify")
	}
}

// The commitment stands for the key in what is signed. It is the same for the
// same key, room and epoch, differs when any of them differs, and is neither
// the key nor a bare hash of it.
func TestEpochKeyCommitment_BoundToKeyRoomAndEpoch(t *testing.T) {
	key := newEpochKey(t)
	base, err := EpochKeyCommitment(key, "rm_general", 4)
	if err != nil {
		t.Fatalf("commitment: %v", err)
	}
	if len(base) != 32 {
		t.Fatalf("commitment is %d bytes, want 32", len(base))
	}
	again, _ := EpochKeyCommitment(key, "rm_general", 4)
	if !bytes.Equal(base, again) {
		t.Error("the same key, room and epoch must give the same commitment")
	}
	otherKey, _ := EpochKeyCommitment(newEpochKey(t), "rm_general", 4)
	otherRoom, _ := EpochKeyCommitment(key, "rm_other", 4)
	otherEpoch, _ := EpochKeyCommitment(key, "rm_general", 5)
	for name, c := range map[string][]byte{"key": otherKey, "room": otherRoom, "epoch": otherEpoch} {
		if bytes.Equal(base, c) {
			t.Errorf("a different %s gave the same commitment", name)
		}
	}
	bare := sha256.Sum256(key)
	if bytes.Equal(base, key) || bytes.Equal(base, bare[:]) {
		t.Error("the commitment must be neither the key nor its bare hash")
	}
	if _, err := EpochKeyCommitment(key[:16], "rm_general", 4); err == nil {
		t.Error("a 16-byte key should be refused")
	}
}

// Fixed vectors for the commitment, the signed bytes and the signature, for
// any other implementation of this attestation to reproduce. Ed25519 signing is
// deterministic, so the signature is fixed too. Regenerate intentionally if
// either form changes; a silent change locks members out of a room.
func TestEpochRoster_FixedVectors(t *testing.T) {
	key := make([]byte, 32)  // 00 01 02 … 1f
	seed := make([]byte, 32) // 20 21 22 … 3f
	for i := range key {
		key[i] = byte(i)
		seed[i] = byte(0x20 + i)
	}
	const room = "room_0123456789ABCDEFGHIJK"
	const epoch = int64(7)
	members := []string{"usr_BBBBBBBBBBBBBBBBBBBBB", "usr_AAAAAAAAAAAAAAAAAAAAA"}
	priv := ed25519.NewKeyFromSeed(seed)

	const (
		wantMemberHash = "SHA256:5f02c75350ce0bca327c616d922131c86ba5f7aadff4b1bcfc8be91c63738d15"
		wantPublicKey  = "29acbae141bccaf0b22e1a94d34d0bc7361e526d0bfe12c89794bc9322966dd7"
		wantCommitment = "901337551a37178d2bc2b7078b3083c0531d75fb7be74b6d0f49e2b1e52f6269"
		wantSigned     = "65706f63685f726f737465723a7632" + // "epoch_roster:v2"
			"0000001a" + "726f6f6d5f303132333435363738394142434445464748494a4b" + // room
			"0000000000000007" + // epoch
			"00000047" + "5348413235363a35663032633735333530636530626361333237633631366439323231333163383662613566376161646666346231626366633862653931633633373338643135" + // member hash
			"00000020" + "901337551a37178d2bc2b7078b3083c0531d75fb7be74b6d0f49e2b1e52f6269" // commitment
		wantSignature = "4b943e665642131b9300a31e256be879b1174b61b3862196c774c9ce5ae6d027f8ff1402016fcbd4912c9b4591e146c5b72391e0fbcbcf68d486c2cca32e0b0e"
	)

	mh := MemberHash(members)
	if mh != wantMemberHash {
		t.Errorf("member hash = %s, want %s", mh, wantMemberHash)
	}
	if got := hex.EncodeToString(priv.Public().(ed25519.PublicKey)); got != wantPublicKey {
		t.Errorf("public key = %s, want %s", got, wantPublicKey)
	}
	commitment, err := EpochKeyCommitment(key, room, epoch)
	if err != nil {
		t.Fatalf("commitment: %v", err)
	}
	if got := hex.EncodeToString(commitment); got != wantCommitment {
		t.Errorf("commitment = %s, want %s", got, wantCommitment)
	}
	signed, err := buildEpochRosterCanonical(room, epoch, mh, key)
	if err != nil {
		t.Fatalf("signed bytes: %v", err)
	}
	if got := hex.EncodeToString(signed); got != wantSigned {
		t.Errorf("signed bytes = %s, want %s", got, wantSigned)
	}
	sig, err := SignEpochRoster(priv, room, epoch, mh, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got := hex.EncodeToString(sig); got != wantSignature {
		t.Errorf("signature = %s, want %s", got, wantSignature)
	}
	if !VerifyEpochRoster(priv.Public().(ed25519.PublicKey), room, epoch, mh, key, sig) {
		t.Error("the vector's signature does not verify")
	}
}
