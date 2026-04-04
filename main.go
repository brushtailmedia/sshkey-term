package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshkey-chat: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configDir := config.DefaultConfigDir()

	// Load or create config
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Ensure device ID
	config.EnsureDeviceID(cfg)

	// Determine which server to connect to
	var server config.ServerConfig

	if len(cfg.Servers) > 0 {
		// Use first server (TODO: server selection UI)
		server = cfg.Servers[0]
	} else {
		// No servers configured — use CLI args or defaults
		host := "localhost"
		port := 2222
		keyPath := os.Getenv("SSHKEY_KEY")
		if keyPath == "" {
			keyPath = os.ExpandEnv("$HOME/.ssh/id_ed25519")
		}
		if len(os.Args) > 1 {
			host = os.Args[1]
		}

		server = config.ServerConfig{
			Name: host,
			Host: host,
			Port: port,
			Key:  keyPath,
		}

		// Save for next time
		cfg.Servers = append(cfg.Servers, server)
		config.Save(configDir, cfg)
	}

	if server.Port == 0 {
		server.Port = 2222
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	dataDir := filepath.Join(configDir, server.Host)

	clientCfg := client.Config{
		Host:     server.Host,
		Port:     server.Port,
		KeyPath:  server.Key,
		DeviceID: cfg.Device.ID,
		DataDir:  dataDir,
		Logger:   logger,
		OnPassphrase: func() ([]byte, error) {
			// TODO: TUI passphrase prompt
			return nil, fmt.Errorf("passphrase-protected keys require interactive input (coming soon)")
		},
	}

	serverIdx := 0
	if len(cfg.Servers) > 1 {
		// TODO: server selection on startup
	}

	app := tui.New(clientCfg, cfg, configDir, serverIdx)

	p := tea.NewProgram(app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = p.Run()
	return err
}
