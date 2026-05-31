package client

// F7 — client verify-or-fail-closed for room epoch keys (the "no shadow reader"
// guarantee). These lock in the §6.5 decision tree in epoch_verify.go: a
// current-epoch key is adopted only if the rotator's signed attestation
// verifies against the rotator's pinned key AND the attested member set matches
// the local roster; otherwise the key is NOT adopted (fail-closed). A lagging
// roster gets one room_members_list refresh before alarming.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// discardSends gives the harness client a no-op encoder so the internal
// RequestRoomMembers (fired on a roster mismatch/absent) doesn't nil-panic
// without a live connection. The test drives the "response" via
// drainPendingEpochAttestation directly.
func (h *editVerifyHarness) discardSends() {
	h.alice.enc = protocol.NewEncoder(io.Discard)
}

const evRoom = "rm_general"
const evEpoch = int64(5)

// buildAttestedEpochKey wraps a fresh epoch key for Alice and attaches an
// attestation signed by `signer` over MemberHash(hashRoster). generator labels
// who supposedly signed it. The caller can then mutate fields for negatives.
func (h *editVerifyHarness) buildAttestedEpochKey(t *testing.T, generator string, signer ed25519.PrivateKey, hashRoster []string) protocol.EpochKey {
	t.Helper()
	epochKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen epoch key: %v", err)
	}
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	wrapped, err := crypto.WrapKey(epochKey, alicePub)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	mh := crypto.MemberHash(hashRoster)
	sig := base64.StdEncoding.EncodeToString(crypto.SignEpochRoster(signer, evRoom, evEpoch, mh))
	return protocol.EpochKey{
		Type:       "epoch_key",
		Room:       evRoom,
		Epoch:      evEpoch,
		WrappedKey: wrapped,
		Generator:  generator,
		MemberHash: mh,
		MemberSig:  sig,
	}
}

func (h *editVerifyHarness) adopted(t *testing.T) bool {
	t.Helper()
	return h.alice.EpochKeyBase64(evRoom, evEpoch) != ""
}

func (h *editVerifyHarness) stashed() bool {
	h.alice.mu.RLock()
	defer h.alice.mu.RUnlock()
	_, ok := h.alice.pendingEpochAtt[evRoom]
	return ok
}

func TestVerifyEpochKey_ValidAttestationAdopted(t *testing.T) {
	h := newEditVerifyHarness(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)

	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster) // bob signs over the real roster
	h.alice.verifyAndAdoptEpochKey(ek)

	if !h.adopted(t) {
		t.Fatal("valid attestation should be adopted")
	}
	if h.alice.CurrentEpoch(evRoom) != evEpoch {
		t.Errorf("currentEpoch = %d, want %d", h.alice.CurrentEpoch(evRoom), evEpoch)
	}
}

func TestVerifyEpochKey_ForgedSignatureFailsClosed(t *testing.T) {
	h := newEditVerifyHarness(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)

	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)
	ek.MemberSig = base64.StdEncoding.EncodeToString([]byte("not a real signature"))
	h.alice.verifyAndAdoptEpochKey(ek)

	if h.adopted(t) {
		t.Error("forged attestation signature must fail closed (key not adopted)")
	}
}

func TestVerifyEpochKey_MissingAttestationFailsClosed(t *testing.T) {
	h := newEditVerifyHarness(t)
	h.alice.setRoomMembers(evRoom, []string{"usr_alice", "usr_bob"})

	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, []string{"usr_alice", "usr_bob"})
	ek.MemberSig = "" // no attestation
	h.alice.verifyAndAdoptEpochKey(ek)

	if h.adopted(t) {
		t.Error("missing attestation must fail closed")
	}
}

func TestVerifyEpochKey_UnknownGeneratorFailsClosed(t *testing.T) {
	h := newEditVerifyHarness(t)
	roster := []string{"usr_alice", "usr_charlie"}
	h.alice.setRoomMembers(evRoom, roster)

	// Signed by an unknown key, claiming a generator Alice can't resolve
	// (no profile, no pin). ed25519.GenerateKey(nil) defaults to crypto/rand.
	_, ghostPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("gen ghost: %v", err)
	}
	ek := h.buildAttestedEpochKey(t, "usr_charlie", ghostPriv, roster)
	h.alice.verifyAndAdoptEpochKey(ek)

	if h.adopted(t) {
		t.Error("unresolvable generator must fail closed")
	}
}

// Mismatch: a sig-verified attestation over a member set that differs from the
// local roster → after one refresh that still disagrees → fail-closed.
func TestVerifyEpochKey_RosterMismatchFailsClosedAfterRefresh(t *testing.T) {
	h := newEditVerifyHarness(t)
	h.discardSends()
	h.alice.setRoomMembers(evRoom, []string{"usr_alice", "usr_bob"})

	// Bob genuinely signs an attestation over {alice, bob, eve} — a shadow
	// reader. Alice's roster is {alice, bob}, so the hashes differ.
	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, []string{"usr_alice", "usr_bob", "usr_eve"})
	h.alice.verifyAndAdoptEpochKey(ek)

	if h.adopted(t) {
		t.Fatal("mismatch must not adopt on first sight")
	}
	if !h.stashed() {
		t.Fatal("mismatch on first sight should stash + request a roster refresh")
	}
	// Simulate the room_members_list arriving with the same (eve-free) roster.
	h.alice.drainPendingEpochAttestation(evRoom)
	if h.adopted(t) {
		t.Error("persistent mismatch after refresh must fail closed")
	}
	if h.stashed() {
		t.Error("stash should be cleared after the terminal drain")
	}
}

// Absent roster at arrival → stash + refresh (no alarm); a matching roster on
// refresh → adopt.
func TestVerifyEpochKey_AbsentRosterThenAdoptOnRefresh(t *testing.T) {
	h := newEditVerifyHarness(t)
	h.discardSends()
	// No roster set yet.
	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, []string{"usr_alice", "usr_bob"})
	h.alice.verifyAndAdoptEpochKey(ek)

	if h.adopted(t) {
		t.Fatal("absent roster must not adopt yet")
	}
	if !h.stashed() {
		t.Fatal("absent roster should stash + request a refresh")
	}
	// Roster arrives and matches the attested set.
	h.alice.setRoomMembers(evRoom, []string{"usr_alice", "usr_bob"})
	h.alice.drainPendingEpochAttestation(evRoom)
	if !h.adopted(t) {
		t.Error("matching roster on refresh should adopt")
	}
}

// epoch_confirmed must NOT advance currentEpoch for a non-rotator — otherwise a
// malicious server could deliver the current epoch's key via the skip-verified
// sync path and then advance the victim onto it with epoch_confirmed, bypassing
// the F7 member-attestation check.
func TestEpochConfirmed_NonRotatorDoesNotAdvance(t *testing.T) {
	h := newEditVerifyHarness(t)
	// Alice did NOT rotate (rotatedEpoch empty). A stray/forged epoch_confirmed:
	raw, _ := json.Marshal(protocol.EpochConfirmed{Type: "epoch_confirmed", Room: evRoom, Epoch: evEpoch})
	h.alice.handleEpochConfirmed(raw)
	if h.alice.CurrentEpoch(evRoom) == evEpoch {
		t.Errorf("non-rotator must NOT advance currentEpoch via epoch_confirmed; got %d", h.alice.CurrentEpoch(evRoom))
	}
}

// The rotator (the client that generated the epoch) DOES advance on its own
// epoch_confirmed — it trusts its own member set.
func TestEpochConfirmed_RotatorAdvances(t *testing.T) {
	h := newEditVerifyHarness(t)
	h.alice.mu.Lock()
	h.alice.rotatedEpoch[evRoom] = evEpoch // as set by handleEpochTrigger
	h.alice.mu.Unlock()
	raw, _ := json.Marshal(protocol.EpochConfirmed{Type: "epoch_confirmed", Room: evRoom, Epoch: evEpoch})
	h.alice.handleEpochConfirmed(raw)
	if h.alice.CurrentEpoch(evRoom) != evEpoch {
		t.Errorf("rotator should advance currentEpoch on its own epoch_confirmed; got %d", h.alice.CurrentEpoch(evRoom))
	}
}
