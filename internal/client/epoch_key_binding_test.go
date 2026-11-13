package client

// The attestation of a room key covers the key itself.
//
// A member opens its copy of the key first and checks the generator's
// signature against that key. A server that keeps a genuine attestation and
// hands a member a key of its own is refused; so is a copy that does not open;
// and when a verified key differs from the one already held for that epoch, the
// verified one replaces it.
//
// Every case feeds its frames through handleInternal, as the read loop does.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

const (
	reasonSignature = "attestation signature verification failed"
	reasonUnwrap    = "epoch key could not be unwrapped"
)

// wrapForClient wraps key for the client under test, which needs only its
// public key: anyone can do it.
func (h *editVerifyHarness) wrapForClient(t *testing.T, key []byte) string {
	t.Helper()
	wrapped, err := crypto.WrapKey(key, h.alice.privKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	return wrapped
}

// heldKey is the key the client holds as adopted for evRoom/evEpoch, or nil.
func (h *editVerifyHarness) heldKey(t *testing.T) []byte {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(h.alice.EpochKeyBase64(evRoom, evEpoch))
	if err != nil {
		t.Fatalf("held key: %v", err)
	}
	if len(key) == 0 {
		return nil
	}
	return key
}

// keyIn opens the client's copy in an epoch_key frame.
func (h *editVerifyHarness) keyIn(t *testing.T, ek protocol.EpochKey) []byte {
	t.Helper()
	key, err := crypto.UnwrapKey(ek.WrappedKey, h.alice.privKey)
	if err != nil {
		t.Fatalf("open the frame's key: %v", err)
	}
	return key
}

// sshPubLine is a public key in the authorized_keys form the server sends.
func sshPubLine(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestEpochKey_SwappedKeyUnderGenuineAttestationRefused(t *testing.T) {
	h := newEditVerifyHarness(t)
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)

	// Bob rotates honestly: his key, his attestation over the roster.
	genuine := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)
	bobsKey := h.keyIn(t, genuine)

	// The server keeps bob's attestation and puts a key of its own in the frame.
	serverKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	swapped := genuine
	swapped.WrappedKey = h.wrapForClient(t, serverKey)
	h.deliver(t, "epoch_key", swapped)

	h.notAdopted(t, "key swapped under a genuine attestation")
	if len(*warnings) != 1 || (*warnings)[0].Generator != h.bobID || (*warnings)[0].Reason != reasonSignature {
		t.Errorf("warnings = %+v, want one naming %s with reason %q", *warnings, h.bobID, reasonSignature)
	}

	// Control: the frame as bob made it is adopted, and the key held is his.
	raised := len(*warnings)
	h.deliver(t, "epoch_key", genuine)
	if !bytes.Equal(h.heldKey(t), bobsKey) || h.alice.CurrentEpoch(evRoom) != evEpoch {
		t.Errorf("bob's own frame: held key is his = %v, current epoch %d", bytes.Equal(h.heldKey(t), bobsKey), h.alice.CurrentEpoch(evRoom))
	}
	if len(*warnings) != raised {
		t.Errorf("bob's own frame raised a warning: %+v", (*warnings)[raised:])
	}
}

func TestEpochKey_CopyThatDoesNotOpenRaisesTheWarning(t *testing.T) {
	h := newEditVerifyHarness(t)
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)
	genuine := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)

	// The copy in the frame is wrapped for somebody else's key.
	someoneElse, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notOurs, err := crypto.WrapKey(h.keyIn(t, genuine), someoneElse)
	if err != nil {
		t.Fatal(err)
	}
	unopenable := genuine
	unopenable.WrappedKey = notOurs
	h.deliver(t, "epoch_key", unopenable)

	h.notAdopted(t, "copy that does not open")
	if len(*warnings) != 1 || (*warnings)[0].Reason != reasonUnwrap {
		t.Errorf("warnings = %+v, want one with reason %q", *warnings, reasonUnwrap)
	}

	// Control: the copy made for this client is adopted.
	h.deliver(t, "epoch_key", genuine)
	if !h.adopted(t) {
		t.Error("the copy wrapped for this client was not adopted")
	}
}

func TestEpochKey_VerifiedKeyReplacesADifferentHeldKey(t *testing.T) {
	h := newEditVerifyHarness(t)
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)

	// The client already holds a key for this epoch, in memory and on disk: one
	// it generated itself for a rotation that another member's answer won.
	own := h.seedRoomEpochKey(t, evRoom, evEpoch)
	if err := h.alice.store.StoreEpochKey(evRoom, evEpoch, own); err != nil {
		t.Fatal(err)
	}

	genuine := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)
	bobsKey := h.keyIn(t, genuine)
	h.deliver(t, "epoch_key", genuine)

	if !bytes.Equal(h.heldKey(t), bobsKey) {
		t.Errorf("the verified key did not replace the held one (still the old key: %v)", bytes.Equal(h.heldKey(t), own))
	}
	if stored, err := h.alice.store.GetEpochKey(evRoom, evEpoch); err != nil || !bytes.Equal(stored, bobsKey) {
		t.Errorf("the stored key is not the verified one (err %v, still the old key: %v)", err, bytes.Equal(stored, own))
	}
	if h.alice.CurrentEpoch(evRoom) != evEpoch {
		t.Errorf("current epoch = %d, want %d", h.alice.CurrentEpoch(evRoom), evEpoch)
	}
	if len(*warnings) != 0 {
		t.Errorf("warnings = %+v, want none: replacing with a verified key is not an alarm", *warnings)
	}
}

// Keep: the server sends the current key again on every connect.
func TestEpochKey_SameKeyDeliveredAgainChangesNothing(t *testing.T) {
	h := newEditVerifyHarness(t)
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)
	genuine := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)
	bobsKey := h.keyIn(t, genuine)

	h.deliver(t, "epoch_key", genuine)
	h.deliver(t, "epoch_key", genuine)

	if !bytes.Equal(h.heldKey(t), bobsKey) || h.alice.CurrentEpoch(evRoom) != evEpoch {
		t.Errorf("after two deliveries: held key is bob's = %v, current epoch %d", bytes.Equal(h.heldKey(t), bobsKey), h.alice.CurrentEpoch(evRoom))
	}
	if len(*warnings) != 0 {
		t.Errorf("warnings = %+v, want none", *warnings)
	}
}

// An attestation in the earlier form, over room, epoch and member list without
// the key, is refused. There is no fallback to it.
func TestEpochKey_AttestationWithoutTheKeyRefused(t *testing.T) {
	h := newEditVerifyHarness(t)
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)
	ek := h.buildAttestedEpochKey(t, h.bobID, h.bobPriv, roster)

	// Bob's genuine signature over the earlier form.
	earlier := []byte("epoch_roster:v1")
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(evRoom)))
	earlier = append(append(earlier, n[:]...), evRoom...)
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], uint64(evEpoch))
	earlier = append(earlier, e[:]...)
	binary.BigEndian.PutUint32(n[:], uint32(len(ek.MemberHash)))
	earlier = append(append(earlier, n[:]...), ek.MemberHash...)
	withoutKey := ek
	withoutKey.MemberSig = base64.StdEncoding.EncodeToString(ed25519.Sign(h.bobPriv, earlier))
	h.deliver(t, "epoch_key", withoutKey)

	h.notAdopted(t, "attestation that does not cover the key")
	if len(*warnings) != 1 || (*warnings)[0].Reason != reasonSignature {
		t.Errorf("warnings = %+v, want one with reason %q", *warnings, reasonSignature)
	}

	// Control: the same frame with bob's signature in the current form is adopted.
	h.deliver(t, "epoch_key", ek)
	if !h.adopted(t) {
		t.Error("the frame with the current form was not adopted")
	}
}

// The rotating client signs the key it wrapped: its answer, handed back as the
// server would hand it to the user's other device, verifies; the same
// attestation over a different key does not.
func TestEpochTrigger_AnswerSignsTheKeyItWrapped(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	warnings := h.captureAttestationWarnings(t)
	roster := []string{"usr_alice", "usr_bob"}
	h.alice.setRoomMembers(evRoom, roster)

	h.deliver(t, "epoch_trigger", protocol.EpochTrigger{
		Type: "epoch_trigger", Room: evRoom, NewEpoch: evEpoch,
		Members: []protocol.MemberKey{
			{User: "usr_alice", PubKey: sshPubLine(t, h.alice.privKey.Public().(ed25519.PublicKey))},
			{User: h.bobID, PubKey: sshPubLine(t, h.bobPub)},
		},
	})
	answer := rotationAnswer(t, sent)
	made := h.heldKey(t)
	if made == nil {
		t.Fatal("the client holds no key after answering")
	}
	if bobs, err := crypto.UnwrapKey(answer.WrappedKeys[h.bobID], h.bobPriv); err != nil || !bytes.Equal(bobs, made) {
		t.Fatalf("bob's copy does not open to the key the client made (err %v)", err)
	}

	back := protocol.EpochKey{
		Type: "epoch_key", Room: evRoom, Epoch: evEpoch,
		WrappedKey: answer.WrappedKeys["usr_alice"],
		Generator:  "usr_alice", MemberHash: answer.MemberHash, MemberSig: answer.MemberSig,
	}
	h.deliver(t, "epoch_key", back)
	if len(*warnings) != 0 || !bytes.Equal(h.heldKey(t), made) {
		t.Fatalf("the client's own answer did not verify: warnings %+v", *warnings)
	}

	other, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	back.WrappedKey = h.wrapForClient(t, other)
	h.deliver(t, "epoch_key", back)
	if len(*warnings) != 1 || (*warnings)[0].Reason != reasonSignature {
		t.Errorf("warnings = %+v, want one with reason %q: the answer's signature must not cover another key", *warnings, reasonSignature)
	}
	if !bytes.Equal(h.heldKey(t), made) {
		t.Error("the key the client made was replaced by a key its attestation does not cover")
	}
}
