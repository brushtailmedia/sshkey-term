// Package config handles the client configuration file (~/.sshkey-chat/config.toml).
package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config represents the client configuration.
type Config struct {
	Device  DeviceConfig   `toml:"device"`
	Servers []ServerConfig `toml:"servers"`
}

type DeviceConfig struct {
	ID string `toml:"id"`
}

type ServerConfig struct {
	Name string `toml:"name"`
	Host string `toml:"host"`
	Port int    `toml:"port"`
	Key  string `toml:"key"`
}

// DefaultConfigDir returns the default config directory path.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sshkey-chat")
}

// Load reads the config file. Returns a default config if the file doesn't exist.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.toml")

	cfg := &Config{}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// Save writes the config file.
func Save(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	path := filepath.Join(dir, "config.toml")
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
func AddServer(dir string, cfg *Config, server ServerConfig) error {
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
func RemoveServer(dir string, cfg *Config, index int) error {
	if index < 0 || index >= len(cfg.Servers) {
		return fmt.Errorf("invalid server index: %d", index)
	}

	server := cfg.Servers[index]

	// Remove local data for this server
	dataDir := filepath.Join(dir, server.Host)
	os.RemoveAll(dataDir)

	// Remove known_host
	os.Remove(filepath.Join(dataDir, "known_host"))

	// Remove from config
	cfg.Servers = append(cfg.Servers[:index], cfg.Servers[index+1:]...)
	return Save(dir, cfg)
}

// ServerDataDir returns the local data directory for a server.
func ServerDataDir(configDir string, server ServerConfig) string {
	return filepath.Join(configDir, server.Host)
}

// ServerDataSize returns the total size of a server's local data in bytes.
func ServerDataSize(configDir string, server ServerConfig) (int64, error) {
	dataDir := filepath.Join(configDir, server.Host)
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

// ClearServerData removes all local data for a server (messages, keys, etc.)
// but keeps the server in the config.
func ClearServerData(configDir string, server ServerConfig) error {
	dataDir := filepath.Join(configDir, server.Host)
	// Remove messages.db but keep known_host
	os.Remove(filepath.Join(dataDir, "messages.db"))
	os.Remove(filepath.Join(dataDir, "messages.db-wal"))
	os.Remove(filepath.Join(dataDir, "messages.db-shm"))
	os.RemoveAll(filepath.Join(dataDir, "files"))
	return nil
}

// GenerateSSHKey generates a new Ed25519 SSH key pair and saves to disk.
func GenerateSSHKey(path string) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	// Write private key in OpenSSH format
	// Use ssh.MarshalPrivateKey for proper format
	// For now, write raw PEM — TODO: use proper OpenSSH format
	_ = priv
	return fmt.Errorf("key generation not yet implemented — use ssh-keygen -t ed25519")
}
