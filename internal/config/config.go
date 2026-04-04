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
