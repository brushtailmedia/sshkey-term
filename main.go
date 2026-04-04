package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshkey-chat: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// TODO: replace with config loading and TUI
	// For now, connect and print messages to stdout
	host := "localhost"
	port := 2222
	keyPath := os.Getenv("SSHKEY_KEY")
	deviceID := "dev_term_test_001"

	if keyPath == "" {
		keyPath = os.ExpandEnv("$HOME/.ssh/id_ed25519")
	}

	if len(os.Args) > 1 {
		host = os.Args[1]
	}

	c := client.New(client.Config{
		Host:     host,
		Port:     port,
		KeyPath:  keyPath,
		DeviceID: deviceID,
		Logger:   logger,
		OnMessage: func(msgType string, raw json.RawMessage) {
			switch msgType {
			case "message":
				var msg protocol.Message
				json.Unmarshal(raw, &msg)
				fmt.Printf("[%s] %s: (encrypted payload)\n", msg.Room, msg.From)
			case "dm":
				var msg protocol.DM
				json.Unmarshal(raw, &msg)
				fmt.Printf("[DM %s] %s: (encrypted payload)\n", msg.Conversation, msg.From)
			case "typing":
				var msg protocol.Typing
				json.Unmarshal(raw, &msg)
				if msg.Room != "" {
					fmt.Printf("[%s] %s is typing...\n", msg.Room, msg.User)
				} else {
					fmt.Printf("[DM] %s is typing...\n", msg.User)
				}
			case "presence":
				var msg protocol.Presence
				json.Unmarshal(raw, &msg)
				fmt.Printf("* %s is %s\n", msg.User, msg.Status)
			case "room_list":
				var msg protocol.RoomList
				json.Unmarshal(raw, &msg)
				fmt.Println("Rooms:")
				for _, r := range msg.Rooms {
					fmt.Printf("  #%s (%d members) - %s\n", r.Name, r.Members, r.Topic)
				}
			case "profile":
				var msg protocol.Profile
				json.Unmarshal(raw, &msg)
				fmt.Printf("  profile: %s (%s)\n", msg.User, msg.DisplayName)
			case "sync_complete":
				fmt.Println("--- sync complete ---")
			case "error":
				var msg protocol.Error
				json.Unmarshal(raw, &msg)
				fmt.Printf("ERROR [%s]: %s\n", msg.Code, msg.Message)
			}
		},
		OnError: func(err error) {
			logger.Error("connection error", "error", err)
		},
	})

	if err := c.Connect(); err != nil {
		return err
	}
	defer c.Close()

	fmt.Printf("\nConnected as %s\n", c.Username())
	if c.IsAdmin() {
		fmt.Println("(admin)")
	}
	fmt.Println("Rooms:", c.Rooms())
	fmt.Println("\nWaiting for messages... (Ctrl+C to quit)")

	// Wait for disconnect
	<-c.Done()
	return nil
}
