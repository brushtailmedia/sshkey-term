// Package testutil provides shared test infrastructure for sshkey-term.
// All test usernames are real nanoids generated at runtime — no hardcoded
// values. Tests reference users by role (Alice, Bob, Carol) not by ID.
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// TestUser holds all identity info for a test user.
type TestUser struct {
	Username    string // real nanoid (e.g., "usr_V1StGXR8_Z5jdHi6B-myT")
	DisplayName string // human-visible name
	KeyPath     string // path to Ed25519 private key
	Rooms       string // TOML rooms array
}

// Test user roles — populated by EnsureFixtures.
var (
	Alice TestUser
	Bob   TestUser
	Carol TestUser
)

var (
	fixturesOnce sync.Once
	fixturesErr  error
	usersToml    string
	keyDir       string
)

const idAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"

func generateID(prefix string) string {
	b := make([]byte, 21)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(idAlphabet))))
		b[i] = idAlphabet[n.Int64()]
	}
	return prefix + string(b)
}

// EnsureFixtures generates Ed25519 test keys and real nanoid usernames.
// Safe to call from multiple tests — generates once per process.
func EnsureFixtures(t testing.TB) {
	t.Helper()
	fixturesOnce.Do(func() {
		keyDir = filepath.Join(os.TempDir(), fmt.Sprintf("sshkey-test-%d", os.Getpid()))
		os.MkdirAll(keyDir, 0700)

		Alice = TestUser{DisplayName: "Alice", Rooms: `["general", "engineering"]`}
		Bob = TestUser{DisplayName: "Bob", Rooms: `["general"]`}
		Carol = TestUser{DisplayName: "Carol", Rooms: `["general"]`}

		for _, u := range []*TestUser{&Alice, &Bob, &Carol} {
			u.Username = generateID("usr_")
			u.KeyPath = filepath.Join(keyDir, strings.ToLower(u.DisplayName)+"_ed25519")

			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				fixturesErr = err
				return
			}
			block, err := ssh.MarshalPrivateKey(priv, "")
			if err != nil {
				fixturesErr = err
				return
			}
			if err := os.WriteFile(u.KeyPath, pem.EncodeToMemory(block), 0600); err != nil {
				fixturesErr = err
				return
			}

			sshPub, _ := ssh.NewPublicKey(pub)
			pubLine := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")

			usersToml += fmt.Sprintf("[%s]\nkey = %q\ndisplay_name = %q\nrooms = %s\n\n",
				u.Username, pubLine+" "+u.DisplayName+"@test", u.DisplayName, u.Rooms)
		}
	})
	if fixturesErr != nil {
		t.Fatalf("generate test fixtures: %v", fixturesErr)
	}
}

// UsersToml returns the generated users.toml content.
func UsersToml() string {
	return usersToml
}

// AdminUsername returns Alice's username (Alice is admin in test config).
func AdminUsername() string {
	return Alice.Username
}

// WaitForType reads from a channel until a message of the given type arrives.
func WaitForType(t testing.TB, ch chan json.RawMessage, msgType string, timeout time.Duration) json.RawMessage {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case raw := <-ch:
			typ, _ := protocol.TypeOf(raw)
			if typ == msgType {
				return raw
			}
		case <-timer:
			t.Fatalf("timeout waiting for %s", msgType)
			return nil
		}
	}
}

// MkClient creates a test client with standard callbacks.
func MkClient(port int, keyPath, deviceID, dataDir string, synced chan bool, msgs chan json.RawMessage) *client.Client {
	return client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     port,
		KeyPath:  keyPath,
		DeviceID: deviceID,
		DataDir:  dataDir,
		Logger:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		OnMessage: func(msgType string, raw json.RawMessage) {
			switch msgType {
			case "sync_complete":
				synced <- true
			case "profile":
				// drop
			default:
				msgs <- raw
			}
		},
	})
}

// CreateTestDB creates a database seeded with test data. Attempts to use
// SQLCipher encryption via the real key derivation path. Falls back to
// unencrypted if SQLCipher is not functional in this build.
// Returns the data directory path.
func CreateTestDB(t testing.TB, keyPath string) string {
	t.Helper()

	privKey, err := client.ParseRawEd25519Key(keyPath)
	if err != nil {
		t.Fatalf("parse key for test DB: %v", err)
	}

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "messages.db")

	dbKey, err := store.DeriveDBKey(privKey.Seed())
	if err != nil {
		t.Fatalf("derive DB key: %v", err)
	}

	st, err := store.Open(dbPath, dbKey)
	if err != nil {
		t.Fatalf("open encrypted test DB: %v", err)
	}
	defer st.Close()

	// Seed with test data using the real generated usernames
	st.InsertMessage(store.StoredMessage{
		ID: "msg_test_001", Sender: Alice.Username, Body: "test message from alice",
		TS: time.Now().Unix(), Room: "general",
	})
	st.InsertMessage(store.StoredMessage{
		ID: "msg_test_002", Sender: Bob.Username, Body: "test reply from bob",
		TS: time.Now().Unix() + 1, Room: "general",
	})
	st.StoreEpochKey("general", 1, []byte("test-epoch-key-32-bytes-long!!"))
	st.SetState("last_synced", time.Now().UTC().Format(time.RFC3339))

	return dbDir
}

// ParseRawEd25519Key is exported from the client package for test use.
// This alias avoids tests needing to import client directly for key parsing.
var ParseRawEd25519Key = client.ParseRawEd25519Key
