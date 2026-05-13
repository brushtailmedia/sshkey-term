// Package config handles the client configuration file (~/.sshkey-term/config.toml).
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the client configuration.
type Config struct {
	Device        DeviceConfig       `toml:"device"`
	Servers       []ServerConfig     `toml:"servers"`
	Notifications NotificationConfig `toml:"notifications"`
	Attachments   AttachmentsConfig  `toml:"attachments"`
}

// AttachmentsConfig controls inline attachment behavior.
type AttachmentsConfig struct {
	// ImageAutoPreviewMaxBytes is the upper size cap for auto-downloading
	// image attachments on receive so they render inline in the chat
	// stream. Images above this threshold still require an explicit
	// `p` (preview), `o` (open), or `s` (save) keypress to download.
	// A NEGATIVE value disables auto-preview entirely — all images
	// stay as 🖼 placeholders until the user explicitly downloads.
	// Zero (the toml zero value, what fresh configs serialize to)
	// gets defaulted by Load, NOT treated as "off."
	//
	// Default when unset or zero: 20 MiB. Picked to comfortably cover
	// typical phone photos (8–12 MB) and DSLR JPGs (4–15 MB) so most
	// images preview without explicit action. The cap is still the
	// primary defense against crafted-image decoder exploits —
	// anything above it cannot auto-fire the decoder; decoder panics
	// are additionally recovered in RenderImageInline. Operators on
	// slow links or who want a tighter security posture can lower
	// this in config.toml.
	ImageAutoPreviewMaxBytes int64 `toml:"image_auto_preview_max_bytes"`
}

// DefaultImageAutoPreviewMaxBytes is applied in Load when the config
// either omits the attachments section entirely or sets the cap to
// zero without being explicit about disabling auto-preview. Operators
// who want auto-preview fully off should set a negative value.
const DefaultImageAutoPreviewMaxBytes int64 = 20 * 1024 * 1024

type NotificationConfig struct {
	Desktop          string   `toml:"desktop"`            // "all", "mentions", "off" (default: "all")
	Bell             string   `toml:"bell"`               // "all", "mentions", "dms", "off" (default: "mentions")
	BellMuteRooms    []string `toml:"bell_mute_rooms"`    // room names where bell is silenced
	BellMuteDMs      bool     `toml:"bell_mute_dms"`      // silence bell for all group DMs
	BellMuteMentions bool     `toml:"bell_mute_mentions"` // silence bell for @mentions
	MutedRooms       []string `toml:"muted_rooms"`        // rooms muted via info panel
	MutedGroups      []string `toml:"muted_groups"`       // group DMs muted via info panel
	HelpShown        bool     `toml:"help_shown"`         // true after first-time help hint dismissed
}

type DeviceConfig struct {
	ID string `toml:"id"`
}

// ServerConfig is a single server entry in the user's config.toml.
//
// No `Key` field: under per-server folder layout, every server's
// SSH key lives at <configDir>/<host>/keys/id_ed25519 (canonical,
// implicit). Runtime derives the key path via ServerKeyPath(configDir,
// server.Host); persisting an explicit reference would just create a
// second source of truth that could drift from the layout.
//
// The Add Server / Wizard flows always copy the source key into the
// per-server managed location before saving the server entry, so the
// canonical file always exists by the time downstream consumers read
// from it. CLI bypass `-host`/`-key` ephemeral mode is the one
// exception — it overrides KeyPath at runtime without persisting.
type ServerConfig struct {
	Name string `toml:"name"`
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

// Load reads the config file. Returns a default config if the file doesn't exist.
func Load(dir string) (*Config, error) {
	path := ConfigFilePath(dir)

	cfg := &Config{}
	// Decode the config file if present; otherwise fall through with
	// an empty cfg so the default-apply below still runs. The earlier
	// early-return-on-not-exists path skipped the default-apply,
	// leaving fresh-install sessions with
	// ImageAutoPreviewMaxBytes = 0 (auto-preview disabled) until the
	// next restart — first-run users would never see auto-preview
	// on the session where they completed the wizard.
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// Apply default for image auto-preview cap if the operator didn't
	// set one. A negative value is the explicit "off" switch. Runs
	// for both existing configs (where 0 was written historically
	// before this default-apply existed) and fresh installs (no file
	// yet), so first-run sessions get auto-preview immediately
	// instead of having to wait for a restart.
	if cfg.Attachments.ImageAutoPreviewMaxBytes == 0 {
		cfg.Attachments.ImageAutoPreviewMaxBytes = DefaultImageAutoPreviewMaxBytes
	}

	return cfg, nil
}

// Save writes the config file.
func Save(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	path := ConfigFilePath(dir)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(cfg)
}

// EnsureDeviceID generates a device ID if one doesn't exist.
func EnsureDeviceID(cfg *Config) {
	if cfg.Device.ID != "" {
		return
	}
	cfg.Device.ID = generateDeviceID()
}

// generateDeviceID creates a Nano ID with dev_ prefix.
func generateDeviceID() string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"
	b := make([]byte, 21)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed")
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "dev_" + string(b)
}

// AddServer adds a server to the config. Saves to disk.
//
// Phase 2 of the path-centralization refactor: validates
// `server.Host` via ValidateHost before mutation, so a bad host
// value (e.g. from a malformed wizard result or a future
// programmer mistake) never gets persisted to config.toml. See
// paths.go ValidateHost + path-centralization.md §"Server data
// dir" validation rule.
func AddServer(dir string, cfg *Config, server ServerConfig) error {
	if err := ValidateHost(server.Host); err != nil {
		return err
	}
	// Check for duplicate
	for _, s := range cfg.Servers {
		if s.Host == server.Host && s.Port == server.Port {
			return fmt.Errorf("server %s:%d already exists", server.Host, server.Port)
		}
	}
	cfg.Servers = append(cfg.Servers, server)
	return Save(dir, cfg)
}

// RemoveServer removes a server from the config by index. Saves to disk.
// Also removes the server's local data directory.
//
// Phase 2 of the path-centralization refactor: validates
// `cfg.Servers[index].Host` via ValidateHost AFTER the existing
// index-bounds check and BEFORE deriving `dataDir`. Bad host
// values from hand-edited config.toml fail closed — both the
// filesystem removal and the config-entry removal share one
// validation gate, keeping the two in sync.
func RemoveServer(dir string, cfg *Config, index int) error {
	if index < 0 || index >= len(cfg.Servers) {
		return fmt.Errorf("invalid server index: %d", index)
	}

	server := cfg.Servers[index]

	if err := ValidateHost(server.Host); err != nil {
		return err
	}

	// Remove local data for this server. Under the per-server-
	// folder layout, this single os.RemoveAll cleans up every-
	// thing the server owns: messages.db, known_host, keys/,
	// files/, client.log.
	dataDir := ServerDataDirForHost(dir, server.Host)
	os.RemoveAll(dataDir)

	// Remove from config
	cfg.Servers = append(cfg.Servers[:index], cfg.Servers[index+1:]...)
	return Save(dir, cfg)
}

// ServerDataDir returns the local data directory for a server.
//
// Phase 1 legacy wrapper — delegates to ServerDataDirForHost so
// existing call sites in this file and in tests continue to
// compile during the path-centralization migration. New code
// should call ServerDataDirForHost(configDir, host string)
// directly. This wrapper will be removed once all callers have
// migrated (planned for a follow-up cleanup after Phase 4).
func ServerDataDir(configDir string, server ServerConfig) string {
	return ServerDataDirForHost(configDir, server.Host)
}

// ServerDataSize returns the total size of a server's local data in bytes.
//
// Phase 2 of the path-centralization refactor: validates
// `server.Host` before deriving `dataDir`. Read-only walk, but
// without validation a malformed Host (e.g. from hand-edited
// config) would walk the WRONG directory and report misleading
// byte counts to the settings UI. ValidateHost closes that
// silent-UI-bug surface.
func ServerDataSize(configDir string, server ServerConfig) (int64, error) {
	if err := ValidateHost(server.Host); err != nil {
		return 0, err
	}
	dataDir := ServerDataDirForHost(configDir, server.Host)
	var total int64
	err := filepath.Walk(dataDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ClearServerData removes the chat history for a server but
// preserves identity state (known_host TOFU pin + keys/).
//
// Phase 2 of the path-centralization refactor: validates
// `server.Host` as the first action, then routes the join
// through ServerDataDirForHost and replaces the inline
// `files/` join with FilesDir. The partial-clear semantics
// (messages.db* + files/ removed; known_host + keys/ retained)
// are UNCHANGED.
func ClearServerData(configDir string, server ServerConfig) error {
	if err := ValidateHost(server.Host); err != nil {
		return err
	}
	dataDir := ServerDataDirForHost(configDir, server.Host)
	// Remove messages.db but keep known_host and keys/.
	os.Remove(filepath.Join(dataDir, "messages.db"))
	os.Remove(filepath.Join(dataDir, "messages.db-wal"))
	os.Remove(filepath.Join(dataDir, "messages.db-shm"))
	os.RemoveAll(FilesDir(dataDir))
	return nil
}

// LoadMutedMap returns a map of muted rooms and group DMs from config.
func LoadMutedMap(cfg *Config) map[string]bool {
	muted := make(map[string]bool)
	for _, r := range cfg.Notifications.MutedRooms {
		muted[r] = true
	}
	for _, g := range cfg.Notifications.MutedGroups {
		muted[g] = true
	}
	return muted
}

// SaveMutedMap writes the mute state back to config. Targets are bucketed
// by ID prefix: room_xxx → MutedRooms, group_xxx → MutedGroups. 1:1 DMs
// (Phase C) will get their own bucket when they land.
func SaveMutedMap(dir string, cfg *Config, muted map[string]bool) error {
	cfg.Notifications.MutedRooms = nil
	cfg.Notifications.MutedGroups = nil
	for target, isMuted := range muted {
		if !isMuted {
			continue
		}
		switch {
		case strings.HasPrefix(target, "group_"):
			cfg.Notifications.MutedGroups = append(cfg.Notifications.MutedGroups, target)
		case strings.HasPrefix(target, "room_"):
			cfg.Notifications.MutedRooms = append(cfg.Notifications.MutedRooms, target)
		default:
			// Unknown prefix — drop it. Better than misclassifying.
		}
	}
	return Save(dir, cfg)
}

// MarkHelpShown marks the first-time help hint as shown.
func MarkHelpShown(dir string, cfg *Config) error {
	cfg.Notifications.HelpShown = true
	return Save(dir, cfg)
}
