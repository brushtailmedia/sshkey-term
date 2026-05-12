package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
	"github.com/brushtailmedia/sshkey-term/internal/config"
	"github.com/brushtailmedia/sshkey-term/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sshkey-term: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// CLI flags for power users (bypass wizard)
	hostFlag := flag.String("host", "", "server hostname (bypasses wizard)")
	portFlag := flag.Int("port", 2222, "server port")
	keyFlag := flag.String("key", "", "path to Ed25519 SSH key (bypasses wizard)")
	nameFlag := flag.String("name", "", "server display name")
	debugFlag := flag.Bool("debug", false, "enable verbose client logs in terminal")
	flag.Parse()

	configDir := config.DefaultConfigDir()

	// Load or create config
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	config.EnsureDeviceID(cfg)

	var server config.ServerConfig
	// ephemeral is set to true when the CLI bypass is used in
	// "ephemeral mode" — `-host`/`-key` supplied but the host
	// doesn't get persisted to config.toml. See path-
	// centralization.md §"CLI bypass design decision" for the
	// two-mode split. Drives both `cfg.KeyPath` derivation and
	// `serverIdx` sentinel below.
	ephemeral := false

	if *hostFlag != "" && *keyFlag != "" {
		// CLI bypass — input boundary. Validate host before any
		// path derivation downstream.
		if err := config.ValidateHost(*hostFlag); err != nil {
			return fmt.Errorf("invalid -host: %w", err)
		}
		name := *nameFlag
		if name == "" {
			name = *hostFlag
		}
		server = config.ServerConfig{
			Name: name,
			Host: *hostFlag,
			Port: *portFlag,
			// Key intentionally omitted — see the two-mode split:
			// bootstrap-persist copies the key into a managed
			// location and persists no Key reference (managed-
			// derived); ephemeral mode uses the literal -key value
			// at runtime via ExpandUserPath, also no persisted Key.
		}
		if len(cfg.Servers) == 0 {
			// Bootstrap-persist mode: functionally Add Server via
			// CLI. Copy the source key into the per-server managed
			// location, save the server entry (no Key field), and
			// fall through to the standard ServerKeyPath-derived
			// runtime read.
			keySrc := config.ExpandUserPath(*keyFlag)
			if err := copyKeyBytesIntoManaged(configDir, *hostFlag, keySrc); err != nil {
				return fmt.Errorf("bootstrap-persist copy: %w", err)
			}
			if err := config.AddServer(configDir, cfg, server); err != nil {
				return fmt.Errorf("save bootstrap server: %w", err)
			}
		} else {
			// Ephemeral mode: no persistence, use literal -key for
			// this run only. The serverIdx sentinel below distin-
			// guishes "running against a configured server" from
			// "running against an ephemeral host" so settings/
			// actions don't accidentally target cfg.Servers[0].
			ephemeral = true
		}
	} else if len(cfg.Servers) > 0 {
		// Existing config — use first server. Validate the
		// persisted host (defense against hand-edited config.toml).
		server = cfg.Servers[0]
		if err := config.ValidateHost(server.Host); err != nil {
			return fmt.Errorf("invalid host in config: %w", err)
		}
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
		if err := config.ValidateHost(result.ServerHost); err != nil {
			return fmt.Errorf("invalid wizard host: %w", err)
		}
		server = config.ServerConfig{
			Name: result.ServerName,
			Host: result.ServerHost,
			Port: result.ServerPort,
			// Key intentionally retained on the ServerConfig struct
			// during the Phase 2 transition — Phase 3 of the path
			// centralization will (a) rewrite the wizard's key-copy
			// destination to `<configDir>/<host>/keys/id_ed25519`
			// and (b) delete the `Key` field from `ServerConfig`
			// entirely. Until then the field stays declared and
			// receives result.KeyPath as a historical record; the
			// runtime derives cfg.KeyPath via ServerKeyPath below
			// regardless.
			Key: result.KeyPath,
		}

		// Save config (uses AddServer which validates again as
		// defense-in-depth).
		if err := config.AddServer(configDir, cfg, server); err != nil {
			return fmt.Errorf("save wizard server: %w", err)
		}
	}

	if server.Port == 0 {
		server.Port = 2222
	}

	dataDir := config.ServerDataDirForHost(configDir, server.Host)
	logger, closeLogger := buildClientLogger(dataDir, *debugFlag)
	defer closeLogger()

	// cfg.KeyPath derivation:
	//   - Ephemeral CLI bypass: literal -key value (tilde-expanded
	//     only). No copy was performed; runtime reads from wherever
	//     the user pointed.
	//   - All other flows: ServerKeyPath under the per-server
	//     managed location. Phase 3 will retire the wizard branch's
	//     reliance on `server.Key` once the wizard's copy
	//     destination is rewritten and the field is dropped from
	//     ServerConfig; until then the wizard branch still writes
	//     to the legacy shared keys dir and Phase 2's transition
	//     window depends on a Phase 3 follow-up commit.
	var keyPath string
	if ephemeral {
		keyPath = config.ExpandUserPath(*keyFlag)
	} else {
		keyPath = config.ServerKeyPath(configDir, server.Host)
	}

	clientCfg := client.Config{
		Host:                     server.Host,
		Port:                     server.Port,
		KeyPath:                  keyPath,
		DeviceID:                 cfg.Device.ID,
		DataDir:                  dataDir,
		Logger:                   logger,
		ImageAutoPreviewMaxBytes: cfg.Attachments.ImageAutoPreviewMaxBytes,
		// OnPassphrase is handled by the TUI passphrase dialog
	}

	// serverIdx sentinel: -1 if running against an unconfigured
	// (ephemeral) host. Prevents settings/actions from targeting
	// cfg.Servers[0] when there's no genuine "active configured
	// server." See path-centralization.md §"CLI bypass" → "Ephemeral
	// — literal-path runtime override" for the rationale.
	serverIdx := -1
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

func buildClientLogger(dataDir string, debug bool) (*slog.Logger, func()) {
	if debug {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})), func() {}
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}
	}

	// Note: the MkdirAll above bootstraps the per-server data dir on
	// first run; the helper substitution below MUST stay paired
	// with it. Failure to MkdirAll falls through to io.Discard
	// silently, which would mask a real disk-permission issue —
	// the MkdirAll is the only place that surfaces it.
	logPath := config.ClientLogPath(dataDir)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}
	}

	logger := slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	return logger, func() {
		_ = f.Close()
	}
}

// copyKeyBytesIntoManaged copies a source key file (and its .pub
// sibling if present) into the per-server managed key location:
// <configDir>/<host>/keys/id_ed25519. Used by the CLI bypass
// bootstrap-persist mode to convert a one-off `-key /external/path`
// invocation into a persistent server entry whose key bytes are
// owned by the new server's folder.
//
// Phase 2 inline implementation; mirrors what Phase 3's wizard
// rewrite will do for the wizard flow.
func copyKeyBytesIntoManaged(configDir, host, srcKeyPath string) error {
	if srcKeyPath == "" {
		return fmt.Errorf("source key path is empty")
	}
	// Read the private key.
	priv, err := os.ReadFile(srcKeyPath)
	if err != nil {
		return fmt.Errorf("read source key %q: %w", srcKeyPath, err)
	}
	// Read the .pub sibling if present (optional — some users
	// might point -key at a private-only file).
	pub, pubErr := os.ReadFile(srcKeyPath + ".pub")

	// Prepare destination paths.
	keysDir := config.ServerKeysDir(configDir, host)
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("mkdir managed keys dir %q: %w", keysDir, err)
	}
	dst := config.ServerKeyPath(configDir, host)
	if err := os.WriteFile(dst, priv, 0o600); err != nil {
		return fmt.Errorf("write managed key %q: %w", dst, err)
	}
	if pubErr == nil {
		if err := os.WriteFile(dst+".pub", pub, 0o644); err != nil {
			return fmt.Errorf("write managed .pub %q: %w", dst+".pub", err)
		}
	}
	return nil
}
