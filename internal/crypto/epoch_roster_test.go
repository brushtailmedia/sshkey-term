package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// F7 — SignEpochRoster / VerifyEpochRoster: the signed room member attestation.
// The signature binds (room, epoch, member_hash) so a relay cannot rewrite the
// hash to match a victim's roster, and is domain-separated so it cannot be
// confused with a room-send signature.

func TestSignEpochRoster_RoundTripAndTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const room = "rm_general"
	const epoch = int64(7)
	mh := MemberHash([]string{"alice", "bob"})

	sig := SignEpochRoster(priv, room, epoch, mh)
	if !VerifyEpochRoster(pub, room, epoch, mh, sig) {
		t.Fatal("valid epoch-roster signature should verify")
	}

	// Each bound field, tampered, must fail verification.
	if VerifyEpochRoster(pub, "rm_other", epoch, mh, sig) {
		t.Error("different room must not verify")
	}
	if VerifyEpochRoster(pub, room, epoch+1, mh, sig) {
		t.Error("different epoch must not verify")
	}
	if VerifyEpochRoster(pub, room, epoch, MemberHash([]string{"alice", "bob", "eve"}), sig) {
		t.Error("different member hash (added 'eve') must not verify")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if VerifyEpochRoster(otherPub, room, epoch, mh, sig) {
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

	rosterSig := SignEpochRoster(priv, room, epoch, mh)
	sendSig := SignRoom(priv, []byte(mh), room, epoch)

	if VerifyEpochRoster(pub, room, epoch, mh, sendSig) {
		t.Error("a SignRoom signature must not verify as an epoch-roster attestation")
	}
	if VerifyRoom(pub, []byte(mh), room, epoch, rosterSig) {
		t.Error("an epoch-roster signature must not verify as a room-send signature")
	}
}
