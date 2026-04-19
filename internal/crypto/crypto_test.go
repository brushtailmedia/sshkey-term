package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("hello, encrypted world!")
	encrypted, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := Decrypt(key, encrypted)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	// Generate an Ed25519 keypair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// Generate a symmetric key to wrap
	symKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Wrap
	wrapped, err := WrapKey(symKey, pub)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	// Unwrap
	unwrapped, err := UnwrapKey(wrapped, priv)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}

	if !bytes.Equal(symKey, unwrapped) {
		t.Errorf("unwrapped key doesn't match original")
	}
	t.Logf("wrap/unwrap round-trip: OK (key=%x)", symKey[:8])
}

func TestWrapUnwrapCrossUser(t *testing.T) {
	// Alice wraps a key for Bob, Bob unwraps
	_, alicePriv, _ := ed25519.GenerateKey(rand.Reader)
	bobPub, bobPriv, _ := ed25519.GenerateKey(rand.Reader)

	symKey, _ := GenerateKey()

	// Alice wraps for Bob
	wrapped, err := WrapKey(symKey, bobPub)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	// Bob unwraps
	unwrapped, err := UnwrapKey(wrapped, bobPriv)
	if err != nil {
		t.Fatalf("bob unwrap: %v", err)
	}

	if !bytes.Equal(symKey, unwrapped) {
		t.Error("bob's unwrapped key doesn't match")
	}

	// Alice cannot unwrap (different key)
	_, err = UnwrapKey(wrapped, alicePriv)
	if err == nil {
		t.Error("alice should not be able to unwrap bob's key")
	}

	t.Log("cross-user wrap/unwrap: OK")
}

func TestSignVerifyRoom(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	payload := []byte("encrypted payload bytes")
	room := "general"
	epoch := int64(3)

	sig := SignRoom(priv, payload, room, epoch)

	if !VerifyRoom(pub, payload, room, epoch, sig) {
		t.Error("signature verification failed")
	}

	// Tamper with room name
	if VerifyRoom(pub, payload, "other", epoch, sig) {
		t.Error("signature should fail with different room")
	}

	// Tamper with epoch
	if VerifyRoom(pub, payload, room, epoch+1, sig) {
		t.Error("signature should fail with different epoch")
	}
}

// Phase 21 item 3 — edit signature canonical-form fix. Tests that
// SignRoomEdit / SignDMEdit:
//   (a) round-trip correctly (happy path),
//   (b) bind the signature to msg.ID so a signature for one msgID
//       cannot verify against a different msgID,
//   (c) are domain-separated from SignRoom / SignDM so a send-path
//       signature cannot cross-verify as an edit-path signature.

func TestSignVerifyRoomEdit_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_abc123xyz"
	payload := []byte("encrypted payload bytes")
	room := "general"
	epoch := int64(3)

	sig := SignRoomEdit(priv, msgID, payload, room, epoch)

	if !VerifyRoomEdit(pub, msgID, payload, room, epoch, sig) {
		t.Error("SignRoomEdit / VerifyRoomEdit round-trip failed")
	}
}

func TestSignVerifyRoomEdit_BindsToMsgID(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	payload := []byte("encrypted payload bytes")
	room := "general"
	epoch := int64(3)

	sig := SignRoomEdit(priv, "msg_X", payload, room, epoch)

	// Same signature must NOT verify against a different msgID — this is
	// the core Phase 21 item 3 protection against signature-replay
	// substitution via the edit path.
	if VerifyRoomEdit(pub, "msg_Y", payload, room, epoch, sig) {
		t.Error("signature for msg_X should not verify against msg_Y")
	}
	// Sanity: original msgID still verifies
	if !VerifyRoomEdit(pub, "msg_X", payload, room, epoch, sig) {
		t.Error("signature for msg_X should verify against msg_X")
	}
}

func TestSignVerifyRoomEdit_RejectsReusedSendSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_abc123xyz"
	payload := []byte("encrypted payload bytes")
	room := "general"
	epoch := int64(3)

	// Produce a signature using the SEND canonical form.
	sendSig := SignRoom(priv, payload, room, epoch)

	// Attacker takes the send signature and tries to pass it off as an
	// edit signature. Domain separation (`"edit_room:"` prefix in the
	// edit canonical form) guarantees rejection — without this the
	// canonical forms would be byte-identical whenever msgID=="" and
	// a send signature could be reused to forge edits.
	if VerifyRoomEdit(pub, msgID, payload, room, epoch, sendSig) {
		t.Error("SignRoom signature must not cross-verify as SignRoomEdit — domain separation missing")
	}
	// Sanity: original send signature still verifies via VerifyRoom.
	if !VerifyRoom(pub, payload, room, epoch, sendSig) {
		t.Error("SignRoom round-trip broken")
	}
}

func TestSignVerifyRoomEdit_TamperDetection(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_abc123xyz"
	payload := []byte("encrypted payload bytes")
	room := "general"
	epoch := int64(3)

	sig := SignRoomEdit(priv, msgID, payload, room, epoch)

	if VerifyRoomEdit(pub, msgID, []byte("tampered payload"), room, epoch, sig) {
		t.Error("payload tamper should fail verification")
	}
	if VerifyRoomEdit(pub, msgID, payload, "other", epoch, sig) {
		t.Error("room tamper should fail verification")
	}
	if VerifyRoomEdit(pub, msgID, payload, room, epoch+1, sig) {
		t.Error("epoch tamper should fail verification")
	}
}

func TestSignVerifyDMEdit_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_dm_abc"
	payload := []byte("dm encrypted payload")
	conversation := "dm_12345"
	wrappedKeys := map[string]string{
		"usr_alice": "YWxpY2Vfd3JhcHBlZA==",
		"usr_bob":   "Ym9iX3dyYXBwZWQ=",
	}

	sig := SignDMEdit(priv, msgID, payload, conversation, wrappedKeys)

	if !VerifyDMEdit(pub, msgID, payload, conversation, wrappedKeys, sig) {
		t.Error("SignDMEdit / VerifyDMEdit round-trip failed")
	}
}

func TestSignVerifyDMEdit_BindsToMsgID(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	payload := []byte("dm encrypted payload")
	conversation := "dm_12345"
	wrappedKeys := map[string]string{
		"usr_alice": "YWxpY2Vfd3JhcHBlZA==",
		"usr_bob":   "Ym9iX3dyYXBwZWQ=",
	}

	sig := SignDMEdit(priv, "msg_X", payload, conversation, wrappedKeys)

	if VerifyDMEdit(pub, "msg_Y", payload, conversation, wrappedKeys, sig) {
		t.Error("DM edit signature for msg_X should not verify against msg_Y")
	}
}

func TestSignVerifyDMEdit_RejectsReusedSendSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_dm_abc"
	payload := []byte("dm encrypted payload")
	conversation := "dm_12345"
	wrappedKeys := map[string]string{
		"usr_alice": "YWxpY2Vfd3JhcHBlZA==",
		"usr_bob":   "Ym9iX3dyYXBwZWQ=",
	}

	// Produce a send signature.
	sendSig := SignDM(priv, payload, conversation, wrappedKeys)

	// Must not cross-verify as an edit signature.
	if VerifyDMEdit(pub, msgID, payload, conversation, wrappedKeys, sendSig) {
		t.Error("SignDM signature must not cross-verify as SignDMEdit — domain separation missing")
	}
	// Sanity: send signature still valid for VerifyDM.
	if !VerifyDM(pub, payload, conversation, wrappedKeys, sendSig) {
		t.Error("SignDM round-trip broken")
	}
}

func TestSignVerifyDMEdit_TamperDetection(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	msgID := "msg_dm_abc"
	payload := []byte("dm encrypted payload")
	conversation := "dm_12345"
	wrappedKeys := map[string]string{
		"usr_alice": "YWxpY2Vfd3JhcHBlZA==",
		"usr_bob":   "Ym9iX3dyYXBwZWQ=",
	}

	sig := SignDMEdit(priv, msgID, payload, conversation, wrappedKeys)

	if VerifyDMEdit(pub, msgID, []byte("tampered"), conversation, wrappedKeys, sig) {
		t.Error("payload tamper should fail verification")
	}
	if VerifyDMEdit(pub, msgID, payload, "dm_other", wrappedKeys, sig) {
		t.Error("conversation tamper should fail verification")
	}
	tamperedKeys := map[string]string{
		"usr_alice": "dGFtcGVyZWQ=",
		"usr_bob":   "Ym9iX3dyYXBwZWQ=",
	}
	if VerifyDMEdit(pub, msgID, payload, conversation, tamperedKeys, sig) {
		t.Error("wrapped_keys tamper should fail verification")
	}
}

// TestEditCanonicalForm_MsgIDLengthAmbiguity guards against a subtle
// attack where a length-prefix-free canonical form could let an
// attacker shift bytes between msgID and payload to produce colliding
// inputs. Demonstrates that SignRoomEdit's length-prefixed msgID field
// prevents this — two (msgID, payload) pairs whose concatenation looks
// identical still produce different canonical forms.
func TestEditCanonicalForm_MsgIDLengthAmbiguity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	room := "general"
	epoch := int64(1)

	// Pair A: msgID="msg_a", payload="BBBB"
	// Pair B: msgID="msg_aBBBB", payload=""
	// Without length prefixing, both would produce canonical form
	// "edit_room:msg_aBBBBgeneral<epoch>" and the signatures would
	// interchange — a classic boundary-confusion attack.
	sigA := SignRoomEdit(priv, "msg_a", []byte("BBBB"), room, epoch)

	if VerifyRoomEdit(pub, "msg_aBBBB", []byte(""), room, epoch, sigA) {
		t.Error("boundary confusion: signature for (msg_a, BBBB) must not verify as (msg_aBBBB, empty) — length prefix is load-bearing")
	}
}

func TestSafetyNumber(t *testing.T) {
	pubA, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB, _, _ := ed25519.GenerateKey(rand.Reader)

	sn1 := SafetyNumber(pubA, pubB)
	sn2 := SafetyNumber(pubB, pubA)

	if sn1 != sn2 {
		t.Errorf("safety numbers should be symmetric: %q vs %q", sn1, sn2)
	}

	// Should be 6 groups of 4 digits
	if len(sn1) != 29 { // "1234 5678 9012 3456 7890 1234"
		t.Errorf("safety number length = %d, want 29", len(sn1))
	}

	t.Logf("safety number: %s", sn1)
}

func TestMemberHash(t *testing.T) {
	h1 := MemberHash([]string{"alice", "bob", "carol"})
	h2 := MemberHash([]string{"carol", "alice", "bob"})

	if h1 != h2 {
		t.Error("member hash should be order-independent")
	}

	h3 := MemberHash([]string{"alice", "bob"})
	if h1 == h3 {
		t.Error("different member sets should produce different hashes")
	}
}
