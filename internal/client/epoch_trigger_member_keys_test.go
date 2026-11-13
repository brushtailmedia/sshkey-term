package client

// A rotating client wraps a new room key only for keys it holds.
//
// The rotation request lists the room's members with a public key each, and
// both come from the server. A listed key has to be the key this client already
// holds for that user; a different one raises the key-change alarm and the
// request is not answered. A member the client has never seen is taken at first
// sight and pinned.
//
// Every case feeds its frames through handleInternal, as the read loop does.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/crypto"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// keyWarning is one call of the key-change alarm.
type keyWarning struct {
	User, Old, New string
}

// captureKeyWarnings records every key-change alarm the client raises from
// here on.
func (h *editVerifyHarness) captureKeyWarnings() *[]keyWarning {
	var got []keyWarning
	h.alice.cfg.OnKeyWarning = func(user, oldFingerprint, newFingerprint string) {
		got = append(got, keyWarning{user, oldFingerprint, newFingerprint})
	}
	return &got
}

// captureSends gives the client a live connection whose frames land in the
// returned buffer.
func (h *editVerifyHarness) captureSends() *bytes.Buffer {
	var sent bytes.Buffer
	h.alice.enc = protocol.NewEncoder(&sent)
	h.alice.connected.Store(true)
	return &sent
}

// rotationsIn returns every epoch_rotate the client has sent.
func rotationsIn(sent *bytes.Buffer) []protocol.EpochRotate {
	var out []protocol.EpochRotate
	for _, line := range bytes.Split(sent.Bytes(), []byte{'\n'}) {
		var frame protocol.EpochRotate
		if json.Unmarshal(line, &frame) == nil && frame.Type == "epoch_rotate" {
			out = append(out, frame)
		}
	}
	return out
}

// rotationAnswer returns the last epoch_rotate the client sent and fails the
// test if there is none.
func rotationAnswer(t *testing.T, sent *bytes.Buffer) protocol.EpochRotate {
	t.Helper()
	all := rotationsIn(sent)
	if len(all) == 0 {
		t.Fatalf("the client sent no epoch_rotate: %s", sent.String())
	}
	return all[len(all)-1]
}

// fingerprintOf is a key's fingerprint in the form profiles carry.
func fingerprintOf(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return ssh.FingerprintSHA256(k)
}

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

// rotationRequest is the frame the server sends to ask for a rotation.
func rotationRequest(epoch int64, members ...protocol.MemberKey) protocol.EpochTrigger {
	return protocol.EpochTrigger{Type: "epoch_trigger", Room: evRoom, NewEpoch: epoch, Members: members}
}

func TestEpochTrigger_DifferentKeyForKnownMemberRefused(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	keyWarnings := h.captureKeyWarnings()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	// Bob's key is pinned and checked out of band, as after a safety-number
	// comparison.
	if err := h.alice.store.PinKey(h.bobID, fingerprintOf(t, h.bobPub), sshPubLine(t, h.bobPub)); err != nil {
		t.Fatal(err)
	}
	if err := h.alice.store.MarkVerified(h.bobID); err != nil {
		t.Fatal(err)
	}

	// The request lists bob under a key the server holds.
	serverPub, _ := newKeyPair(t)
	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch,
		protocol.MemberKey{User: "usr_alice", PubKey: sshPubLine(t, alicePub)},
		protocol.MemberKey{User: h.bobID, PubKey: sshPubLine(t, serverPub)},
	))

	if n := len(rotationsIn(sent)); n != 0 {
		t.Errorf("the client answered a request that lists another key for bob (%d epoch_rotate)", n)
	}
	h.notAdopted(t, "request with a substituted member key")
	want := keyWarning{h.bobID, fingerprintOf(t, h.bobPub), fingerprintOf(t, serverPub)}
	if len(*keyWarnings) != 1 || (*keyWarnings)[0] != want {
		t.Errorf("key warnings = %+v, want one: %+v", *keyWarnings, want)
	}
	if fp, verified, err := h.alice.store.GetPinnedKey(h.bobID); err != nil || verified || fp != fingerprintOf(t, h.bobPub) {
		t.Errorf("after the refusal: pinned fingerprint is bob's = %v, verified = %v (err %v); want bob's key kept and the verified mark cleared", fp == fingerprintOf(t, h.bobPub), verified, err)
	}

	// Control: the same request with bob's own key is answered, and his copy
	// opens to the key the client made.
	raised := len(*keyWarnings)
	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch,
		protocol.MemberKey{User: "usr_alice", PubKey: sshPubLine(t, alicePub)},
		protocol.MemberKey{User: h.bobID, PubKey: sshPubLine(t, h.bobPub)},
	))
	answer := rotationAnswer(t, sent)
	if bobs, err := crypto.UnwrapKey(answer.WrappedKeys[h.bobID], h.bobPriv); err != nil || !bytes.Equal(bobs, h.heldKey(t)) {
		t.Errorf("bob's copy does not open to the key the client made (err %v)", err)
	}
	if len(*keyWarnings) != raised {
		t.Errorf("the honest request raised a key warning: %+v", (*keyWarnings)[raised:])
	}
}

func TestEpochTrigger_DifferentKeyForSelfRefused(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	keyWarnings := h.captureKeyWarnings()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	serverPub, _ := newKeyPair(t)

	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch,
		protocol.MemberKey{User: "usr_alice", PubKey: sshPubLine(t, serverPub)},
		protocol.MemberKey{User: h.bobID, PubKey: sshPubLine(t, h.bobPub)},
	))

	if n := len(rotationsIn(sent)); n != 0 {
		t.Errorf("the client answered a request that lists another key under its own name (%d epoch_rotate)", n)
	}
	h.notAdopted(t, "request with a substituted key for this user")
	want := keyWarning{"usr_alice", fingerprintOf(t, alicePub), fingerprintOf(t, serverPub)}
	if len(*keyWarnings) != 1 || (*keyWarnings)[0] != want {
		t.Errorf("key warnings = %+v, want one: %+v", *keyWarnings, want)
	}
}

func TestEpochTrigger_NeverSeenMemberTakenAtFirstSightAndPinned(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	keyWarnings := h.captureKeyWarnings()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	carolPub, carolPriv := newKeyPair(t)
	if h.alice.pubKeyForUser("usr_carol") != nil {
		t.Fatal("carol must be unknown to the client for this case")
	}
	members := func(carol ed25519.PublicKey) []protocol.MemberKey {
		return []protocol.MemberKey{
			{User: "usr_alice", PubKey: sshPubLine(t, alicePub)},
			{User: h.bobID, PubKey: sshPubLine(t, h.bobPub)},
			{User: "usr_carol", PubKey: sshPubLine(t, carol)},
		}
	}

	// A join: the request is where an existing member first meets the new one.
	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch, members(carolPub)...))

	answer := rotationAnswer(t, sent)
	if carols, err := crypto.UnwrapKey(answer.WrappedKeys["usr_carol"], carolPriv); err != nil || !bytes.Equal(carols, h.heldKey(t)) {
		t.Fatalf("carol's copy does not open to the key the client made (err %v)", err)
	}
	if len(*keyWarnings) != 0 {
		t.Fatalf("a first sight raised a key warning: %+v", *keyWarnings)
	}
	fp, _, pinned := h.alice.store.GetPinnedKeyFull("usr_carol")
	if fp != fingerprintOf(t, carolPub) {
		t.Errorf("carol's pinned fingerprint = %q, want %q", fp, fingerprintOf(t, carolPub))
	}
	if got, err := crypto.ParseSSHPubKey(pinned); err != nil || !bytes.Equal(got, carolPub) {
		t.Errorf("carol's pinned key is not the key the request listed (err %v)", err)
	}
	if !bytes.Equal(h.alice.pubKeyForUser("usr_carol"), carolPub) {
		t.Error("the client does not resolve carol's key after the first sight")
	}

	// The profile the server sends later, with the same key, is accepted quietly.
	h.deliver(t, "profile", protocol.Profile{
		Type: "profile", User: "usr_carol", DisplayName: "Carol",
		PubKey: sshPubLine(t, carolPub), KeyFingerprint: fingerprintOf(t, carolPub),
	})
	if len(*keyWarnings) != 0 {
		t.Errorf("carol's profile with the same key raised a key warning: %+v", *keyWarnings)
	}

	// A later request that lists carol under another key is refused: the first
	// sight was kept.
	otherPub, _ := newKeyPair(t)
	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch+1, members(otherPub)...))
	if n := len(rotationsIn(sent)); n != 1 {
		t.Errorf("epoch_rotate frames sent = %d, want 1: the request with another key for carol must not be answered", n)
	}
	want := keyWarning{"usr_carol", fingerprintOf(t, carolPub), fingerprintOf(t, otherPub)}
	if len(*keyWarnings) != 1 || (*keyWarnings)[0] != want {
		t.Errorf("key warnings = %+v, want one: %+v", *keyWarnings, want)
	}
}

func TestEpochTrigger_NothingPinnedFromARefusedRequest(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	keyWarnings := h.captureKeyWarnings()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	carolPub, _ := newKeyPair(t)
	serverPub, _ := newKeyPair(t)

	// Carol is new to the client and listed first; bob's key is not his.
	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch,
		protocol.MemberKey{User: "usr_carol", PubKey: sshPubLine(t, carolPub)},
		protocol.MemberKey{User: "usr_alice", PubKey: sshPubLine(t, alicePub)},
		protocol.MemberKey{User: h.bobID, PubKey: sshPubLine(t, serverPub)},
	))

	if n := len(rotationsIn(sent)); n != 0 {
		t.Errorf("the client answered the request (%d epoch_rotate)", n)
	}
	if len(*keyWarnings) != 1 || (*keyWarnings)[0].User != h.bobID {
		t.Errorf("key warnings = %+v, want one for %s", *keyWarnings, h.bobID)
	}
	if fp, _, pinned := h.alice.store.GetPinnedKeyFull("usr_carol"); fp != "" || pinned != "" {
		t.Errorf("carol's key was pinned from a request the client refused (fingerprint %q)", fp)
	}
}

// Keep: a member the client knows only from its pinned keys, as after the
// member was removed and added again or retired and unretired, is wrapped for.
func TestEpochTrigger_MemberKnownOnlyFromThePinnedStoreAnswered(t *testing.T) {
	h := newEditVerifyHarness(t)
	sent := h.captureSends()
	keyWarnings := h.captureKeyWarnings()
	alicePub := h.alice.privKey.Public().(ed25519.PublicKey)
	if err := h.alice.store.PinKey(h.bobID, fingerprintOf(t, h.bobPub), sshPubLine(t, h.bobPub)); err != nil {
		t.Fatal(err)
	}
	h.alice.mu.Lock()
	delete(h.alice.profiles, h.bobID) // no live profile this session
	h.alice.mu.Unlock()

	h.deliver(t, "epoch_trigger", rotationRequest(evEpoch,
		protocol.MemberKey{User: "usr_alice", PubKey: sshPubLine(t, alicePub)},
		protocol.MemberKey{User: h.bobID, PubKey: sshPubLine(t, h.bobPub)},
	))

	answer := rotationAnswer(t, sent)
	if bobs, err := crypto.UnwrapKey(answer.WrappedKeys[h.bobID], h.bobPriv); err != nil || !bytes.Equal(bobs, h.heldKey(t)) {
		t.Errorf("bob's copy does not open to the key the client made (err %v)", err)
	}
	if len(*keyWarnings) != 0 {
		t.Errorf("key warnings = %+v, want none", *keyWarnings)
	}
}
