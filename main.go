package main

import (
	"flag"
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
	// CLI flags for power users (bypass wizard)
	hostFlag := flag.String("host", "", "server hostname (bypasses wizard)")
	portFlag := flag.Int("port", 2222, "server port")
	keyFlag := flag.String("key", "", "path to Ed25519 SSH key (bypasses wizard)")
	nameFlag := flag.String("name", "", "server display name")
	flag.Parse()

	configDir := config.DefaultConfigDir()

	// Load or create config
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	config.EnsureDeviceID(cfg)

	var server config.ServerConfig

	if *hostFlag != "" && *keyFlag != "" {
		// CLI bypass — skip wizard entirely
		name := *nameFlag
		if name == "" {
			name = *hostFlag
		}
		server = config.ServerConfig{
			Name: name,
			Host: *hostFlag,
			Port: *portFlag,
			Key:  *keyFlag,
		}
		// Save to config
		if len(cfg.Servers) == 0 {
			cfg.Servers = append(cfg.Servers, server)
			config.Save(configDir, cfg)
		}
	} else if len(cfg.Servers) > 0 {
		// Existing config — use first server
		server = cfg.Servers[0]
	} else {
		// No config, no CLI flags — run the wizard
		wizard := tui.NewWizard()
		p := tea.NewProgram(wizard, tea.WithAltScreen())
		model, err := p.Run()
		if err != nil {
			return err
		}

		wiz, ok := model.(tui.WizardModel)
		if !ok || !wiz.IsComplete() {
			return fmt.Errorf("setup cancelled")
		}

		result := wiz.Result()
		server = config.ServerConfig{
			Name: result.ServerName,
			Host: result.ServerHost,
			Port: result.ServerPort,
			Key:  result.KeyPath,
		}

		// Save config
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
		// OnPassphrase is handled by the TUI passphrase dialog
	}

	serverIdx := 0
	for i, s := range cfg.Servers {
		if s.Host == server.Host && s.Port == server.Port {
			serverIdx = i
			break
		}
	}

	app := tui.New(clientCfg, cfg, configDir, serverIdx)

	chatProgram := tea.NewProgram(app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = chatProgram.Run()
	return err
}
