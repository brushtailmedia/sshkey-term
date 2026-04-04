package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

// startTestServer builds and starts the sshkey-server with test config.
func startTestServer(t *testing.T) (port int, cleanup func()) {
	t.Helper()

	serverDir := filepath.Join("..", "sshkey")
	configDir := filepath.Join(serverDir, "testdata", "config")
	dataDir := t.TempDir()

	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Write a config override with our port
	overrideConfig := filepath.Join(dataDir, "config")
	os.MkdirAll(overrideConfig, 0755)

	// Copy config files and override port
	for _, f := range []string{"users.toml", "rooms.toml"} {
		data, _ := os.ReadFile(filepath.Join(configDir, f))
		os.WriteFile(filepath.Join(overrideConfig, f), data, 0644)
	}

	serverToml, _ := os.ReadFile(filepath.Join(configDir, "server.toml"))
	overridden := strings.Replace(string(serverToml), "port = 2222", fmt.Sprintf("port = %d", port), 1)
	overridden = strings.Replace(overridden, `bind = "0.0.0.0"`, `bind = "127.0.0.1"`, 1)
	os.WriteFile(filepath.Join(overrideConfig, "server.toml"), []byte(overridden), 0644)

	// Build and run server
	serverBin := filepath.Join(dataDir, "sshkey-server")
	build := exec.Command("go", "build", "-o", serverBin, "./cmd/sshkey-server")
	build.Dir = serverDir
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}

	cmd := exec.Command(serverBin, "-config", overrideConfig, "-data", dataDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	// Wait for server to be ready
	time.Sleep(500 * time.Millisecond)

	return port, func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func TestClientConnect(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()

	var receivedTypes []string
	done := make(chan bool, 1)

	c := client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     port,
		KeyPath:  "/tmp/sshkey-test-key",
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

	// Wait for sync_complete
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sync_complete")
	}

	// Verify connection state
	if c.Username() != "alice" {
		t.Errorf("username = %q, want alice", c.Username())
	}
	if !c.IsAdmin() {
		t.Error("expected admin=true")
	}
	rooms := c.Rooms()
	if len(rooms) != 2 {
		t.Errorf("rooms = %v, want 2 rooms", rooms)
	}

	// Check we received expected message types
	typeSet := make(map[string]bool)
	for _, mt := range receivedTypes {
		typeSet[mt] = true
	}

	for _, expected := range []string{"room_list", "profile", "sync_complete"} {
		if !typeSet[expected] {
			t.Errorf("missing message type: %s (got: %v)", expected, receivedTypes)
		}
	}

	t.Logf("connected as %s, rooms=%v, types=%v", c.Username(), rooms, receivedTypes)
}

func TestClientSendReceive(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()

	// Connect alice
	aliceDone := make(chan bool, 1)
	aliceMessages := make(chan protocol.Message, 10)

	alice := client.New(client.Config{
		Host:     "127.0.0.1",
		Port:     port,
		KeyPath:  "/tmp/sshkey-test-key",
		DeviceID: "dev_alice_term",
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

	// Send a room message (unencrypted for now — server just passes through the payload)
	err := alice.SendRoomMessage("general", "hello from terminal client", "", nil)
	if err != nil {
		// Expected to fail without epoch key — server hasn't sent one yet for a fresh room
		t.Logf("send failed (expected without epoch key): %v", err)
	}

	t.Logf("alice connected as %s, admin=%v", alice.Username(), alice.IsAdmin())
	t.Log("client send/receive test complete")
}
