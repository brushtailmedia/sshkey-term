package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"

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
	nameFlag := flag.String("name", "", "local server label (e.g. Home, Work)")
	displayNameFlag := flag.String("display-name", "", "your requested display name on this server (sent as the SSH username hint)")
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
		// CLI bypass — runBypassMode handles both bootstrap-persist
		// (empty config) and ephemeral (existing config) modes per
		// path-centralization.md §"CLI bypass design decision". Flag
		// parsing stays in run() (Option B-lite); only the branch
		// body is extracted for testability.
		var err error
		server, ephemeral, err = runBypassMode(configDir, cfg, *hostFlag, *keyFlag, *nameFlag, *displayNameFlag, *portFlag)
		if err != nil {
			return err
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
			// The wizard's chosen display name (also embedded in the managed
			// .pub comment) becomes the persisted requested-name hint, sent as
			// the SSH username on connect so an unapproved key surfaces in the
			// operator's pending list with a name.
			RequestedDisplayName: result.PreferredName,
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
	//     managed location. The wizard and Add Server flows both
	//     write the key to <configDir>/<host>/keys/id_ed25519 before
	//     reaching this code, so deriving the path from server.Host
	//     gives us the right file without any persisted reference.
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
		// User carries the requested display-name hint as the SSH username.
		// This is the single sink for all three ServerConfig producers
		// (wizard, existing-config, CLI bypass) — server is resolved by the
		// time we reach here, so the field flows through uniformly.
		User: server.RequestedDisplayName,
		// OnPassphrase is handled by the TUI passphrase dialog
	}

	// serverIdx sentinel: -1 if running against an unconfigured
	// (ephemeral) host. Prevents settings/actions from targeting
	// cfg.Servers[0] when there's no genuine "active configured
	// server." See path-centralization.md §"CLI bypass" → "Ephemeral
	// — literal-path runtime override" for the rationale.
	serverIdx := findServerIndex(cfg, server)

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
// When displayName is non-empty, the managed .pub comment is rewritten to match
// the requested display-name hint. This mirrors Add Server and keeps
// `sshkey-ctl approve --key "<copied pubkey>"` aligned with the pending hint.
func copyKeyBytesIntoManaged(configDir, host, srcKeyPath, displayName string) error {
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
	if pubErr == nil {
		var rewriteErr error
		pub, rewriteErr = cliPubLineWithComment(pub, displayName)
		if rewriteErr != nil {
			return rewriteErr
		}
	}

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

func cliPubLineWithComment(pubData []byte, displayName string) ([]byte, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return pubData, nil
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		return nil, fmt.Errorf("parse public key for display-name rewrite: %w", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey))) + " " + displayName + "\n"
	return []byte(line), nil
}

// runBypassMode handles the -host / -key CLI bypass branch. Two
// modes split on whether any servers are already configured:
//
//   - len(cfg.Servers) == 0 → bootstrap-persist. Validate host,
//     copy the source key into <configDir>/<host>/keys/id_ed25519
//     via copyKeyBytesIntoManaged, persist a server entry via
//     config.AddServer. Returns ephemeral=false; subsequent runs
//     pick up the persisted entry naturally.
//   - len(cfg.Servers) > 0 → ephemeral. Validate host, build the
//     in-memory ServerConfig, but skip the copy and skip the save.
//     Returns ephemeral=true; the caller uses the literal -key
//     value (via ExpandUserPath) for this run only.
//
// Both modes share: ValidateHost gate, Name fallback to Host if
// the -name flag was empty, -display-name validation into the requested-name
// hint, and Port carried through verbatim.
//
// Extracted from run() in Phase 4 (Option B-lite) so the bypass
// contract is unit-testable without exercising flag parsing,
// tea.NewProgram wiring, or the wizard branch. See
// path-centralization.md §"Decision — CLI bypass test strategy
// (Option B-lite)" for the rationale.
func runBypassMode(configDir string, cfg *config.Config, host, keyPath, name, displayName string, port int) (config.ServerConfig, bool, error) {
	if err := config.ValidateHost(host); err != nil {
		return config.ServerConfig{}, false, fmt.Errorf("invalid -host: %w", err)
	}
	if name == "" {
		name = host
	}
	// -display-name is the requested display-name hint, distinct from -name
	// (the local label). Validate it with the same policy the wizard and Add
	// Server use so a bad value fails fast rather than being persisted; empty
	// means no hint. Bootstrap-persist saves it onto the ServerConfig; ephemeral
	// returns it in-memory for that run only (never written to config.toml).
	if displayName != "" {
		validated, err := tui.ValidateDisplayName(displayName)
		if err != nil {
			return config.ServerConfig{}, false, fmt.Errorf("invalid -display-name: %w", err)
		}
		displayName = validated
	}
	server := config.ServerConfig{
		Name:                 name,
		Host:                 host,
		Port:                 port,
		RequestedDisplayName: displayName,
	}

	if len(cfg.Servers) == 0 {
		// Bootstrap-persist mode: functionally Add Server via CLI.
		// Copy the source key into the per-server managed location,
		// save the server entry (no Key field — Phase 3e deleted it).
		keySrc := config.ExpandUserPath(keyPath)
		if err := copyKeyBytesIntoManaged(configDir, host, keySrc, displayName); err != nil {
			return config.ServerConfig{}, false, fmt.Errorf("bootstrap-persist copy: %w", err)
		}
		if err := config.AddServer(configDir, cfg, server); err != nil {
			return config.ServerConfig{}, false, fmt.Errorf("save bootstrap server: %w", err)
		}
		return server, false, nil
	}

	// Ephemeral mode: no persistence, use literal -key for this
	// run only. The serverIdx sentinel computed in run() will
	// resolve to -1 since the host isn't in cfg.Servers, so
	// destructive settings actions are correctly gated.
	return server, true, nil
}

// findServerIndex returns the index of the cfg.Servers entry
// matching (Host, Port), or -1 if not found. Sentinel -1 marks
// "running against an unconfigured ephemeral host" and is used by
// the TUI to gate destructive settings actions against
// cfg.Servers[0]. Extracted from run() in Phase 4 (Option B-lite)
// so the sentinel behavior is unit-testable.
func findServerIndex(cfg *config.Config, server config.ServerConfig) int {
	if cfg == nil {
		return -1
	}
	for i, s := range cfg.Servers {
		if s.Host == server.Host && s.Port == server.Port {
			return i
		}
	}
	return -1
}
