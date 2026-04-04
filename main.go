package main

import (
	"fmt"
	"log/slog"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshkey-chat: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// TODO: load config from ~/.sshkey-chat/config.toml
	// For now, use env vars and defaults
	host := "localhost"
	port := 2222
	keyPath := os.Getenv("SSHKEY_KEY")
	deviceID := "dev_term_001"

	if keyPath == "" {
		keyPath = os.ExpandEnv("$HOME/.ssh/id_ed25519")
	}

	if len(os.Args) > 1 {
		host = os.Args[1]
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	cfg := client.Config{
		Host:     host,
		Port:     port,
		KeyPath:  keyPath,
		DeviceID: deviceID,
		Logger:   logger,
	}

	app := tui.New(cfg)

	p := tea.NewProgram(app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err := p.Run()
	return err
}
