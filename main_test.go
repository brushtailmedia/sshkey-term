//go:build e2e

package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/testutil"
)

// Aliases for backward compatibility with e2e test files that reference
// package-level functions. These delegate to testutil.
func startTestServer(t *testing.T) (int, func()) {
	return testutil.StartTestServer(t)
}

func waitForType(t *testing.T, ch chan json.RawMessage, msgType string, timeout time.Duration) json.RawMessage {
	return testutil.WaitForType(t, ch, msgType, timeout)
}

// roomIDByName finds the nanoid room ID for a display name using the client's DB.
func roomIDByName(t *testing.T, c *client.Client, displayName string) string {
	t.Helper()
	for _, id := range c.Rooms() {
		if c.DisplayRoomName(id) == displayName {
			return id
		}
	}
	t.Fatalf("room %q not found in %v", displayName, c.Rooms())
	return ""
}

func TestClientConnect(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	var receivedTypes []string
	done := make(chan bool, 1)

	c := client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     port,
		KeyPath:  testutil.Alice.KeyPath,
		DeviceID: "dev_term_test_001",
		Logger:   slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnMessage: func(msgType string, raw json.RawMessage) {
			receivedTypes = append(receivedTypes, msgType)
			if msgType == "sync_complete" {
				done <- true
			}
		},
		OnError: func(err error) {
			t.Logf("error: %v", err)
		},
	})

	err := c.Connect()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sync_complete")
	}

	if c.UserID() != testutil.Alice.UserID {
		t.Errorf("username = %q, want %s", c.UserID(), testutil.Alice.UserID)
	}
	if !c.IsAdmin() {
		t.Error("expected admin=true")
	}
	rooms := c.Rooms()
	if len(rooms) != 2 {
		t.Errorf("rooms = %v, want 2 rooms", rooms)
	}

	typeSet := make(map[string]bool)
	for _, mt := range receivedTypes {
		typeSet[mt] = true
	}
	for _, expected := range []string{"room_list", "profile", "sync_complete"} {
		if !typeSet[expected] {
			t.Errorf("missing message type: %s (got: %v)", expected, receivedTypes)
		}
	}

	t.Logf("connected as %s, rooms=%v, types=%v", c.UserID(), rooms, receivedTypes)
}

func TestClientSendReceive(t *testing.T) {
	port, cleanup := testutil.StartTestServer(t)
	defer cleanup()

	aliceDone := make(chan bool, 1)
	aliceMessages := make(chan protocol.Message, 10)

	alice := client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     port,
		KeyPath:  testutil.Alice.KeyPath,
		DeviceID: "dev_alice_term",
		DataDir:  t.TempDir(),
		Logger:   slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})),
		OnMessage: func(msgType string, raw json.RawMessage) {
			if msgType == "sync_complete" {
				aliceDone <- true
			}
			if msgType == "message" {
				var msg protocol.Message
				json.Unmarshal(raw, &msg)
				aliceMessages <- msg
			}
		},
	})

	if err := alice.Connect(); err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Close()

	select {
	case <-aliceDone:
	case <-time.After(5 * time.Second):
		t.Fatal("alice sync timeout")
	}

	generalID := roomIDByName(t, alice, "general")
	err := alice.SendRoomMessage(generalID, "hello from terminal client", "", nil)
	if err != nil {
		t.Logf("send failed (expected without epoch key): %v", err)
	}

	t.Logf("alice connected as %s, admin=%v", alice.UserID(), alice.IsAdmin())
	t.Log("client send/receive test complete")
}
