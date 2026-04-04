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
