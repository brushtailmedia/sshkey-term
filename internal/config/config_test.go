package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -- Load / Save round-trip --

func TestLoad_MissingFileReturnsDefault(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("should return default config, got nil")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("default config should have no servers, got %d", len(cfg.Servers))
	}
}

func TestSave_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	cfg := &Config{Device: DeviceConfig{ID: "dev_abc"}}
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &Config{
		Device: DeviceConfig{ID: "dev_V1StGXR8_Z5jdHi6B-myT"},
		Servers: []ServerConfig{
			{Name: "Personal", Host: "chat.example.com", Port: 2222, Key: "~/.ssh/id_ed25519"},
			{Name: "Work", Host: "work.example.com", Port: 2223, Key: "~/.ssh/work_key"},
		},
		Notifications: NotificationConfig{
			Desktop:       "mentions",
			Bell:          "dms",
			BellMuteRooms: []string{"noise"},
			BellMuteDMs:   false,
			HelpShown:     true,
		},
	}
	if err := Save(dir, original); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Device.ID != original.Device.ID {
		t.Errorf("device id = %q", loaded.Device.ID)
	}
	if len(loaded.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(loaded.Servers))
	}
	if loaded.Servers[0].Name != "Personal" || loaded.Servers[1].Host != "work.example.com" {
		t.Errorf("server details lost: %+v", loaded.Servers)
	}
	if loaded.Notifications.Desktop != "mentions" {
		t.Errorf("desktop = %q", loaded.Notifications.Desktop)
	}
	if len(loaded.Notifications.BellMuteRooms) != 1 || loaded.Notifications.BellMuteRooms[0] != "noise" {
		t.Errorf("bell_mute_rooms = %v", loaded.Notifications.BellMuteRooms)
	}
	if !loaded.Notifications.HelpShown {
		t.Error("HelpShown should round-trip as true")
	}
}

func TestLoad_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is not valid toml {{"), 0644)
	_, err := Load(dir)
	if err == nil {
		t.Error("malformed TOML should produce an error")
	}
}

// -- DefaultConfigDir --

func TestDefaultConfigDir(t *testing.T) {
	got := DefaultConfigDir()
	if !strings.HasSuffix(got, ".sshkey-chat") {
		t.Errorf("DefaultConfigDir() = %q, want suffix .sshkey-chat", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("DefaultConfigDir() should be absolute path, got %q", got)
	}
}

// -- EnsureDeviceID --

func TestEnsureDeviceID_Generates(t *testing.T) {
	cfg := &Config{}
	EnsureDeviceID(cfg)
	if cfg.Device.ID == "" {
		t.Error("should have generated an ID")
	}
	if !strings.HasPrefix(cfg.Device.ID, "dev_") {
		t.Errorf("ID should have dev_ prefix, got %q", cfg.Device.ID)
	}
	if len(cfg.Device.ID) != len("dev_")+21 {
		t.Errorf("ID should be 'dev_' + 21 chars, got length %d (%q)", len(cfg.Device.ID), cfg.Device.ID)
	}
}

func TestEnsureDeviceID_PreservesExisting(t *testing.T) {
	cfg := &Config{Device: DeviceConfig{ID: "dev_existing_id_xxxxx"}}
	EnsureDeviceID(cfg)
	if cfg.Device.ID != "dev_existing_id_xxxxx" {
		t.Errorf("should preserve existing ID, got %q", cfg.Device.ID)
	}
}

func TestEnsureDeviceID_UniquePerCall(t *testing.T) {
	c1 := &Config{}
	c2 := &Config{}
	EnsureDeviceID(c1)
	EnsureDeviceID(c2)
	if c1.Device.ID == c2.Device.ID {
		t.Error("two fresh IDs should not collide (extremely unlikely)")
	}
}

// -- Server management --

func TestAddServer_FirstServer(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}

	srv := ServerConfig{Name: "Home", Host: "home.example.com", Port: 2222, Key: "~/.ssh/id_ed25519"}
	if err := AddServer(dir, cfg, srv); err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(cfg.Servers))
	}
	// Saved to disk
	loaded, _ := Load(dir)
	if len(loaded.Servers) != 1 {
		t.Errorf("not persisted: %d servers", len(loaded.Servers))
	}
}

func TestAddServer_RejectsDuplicateHostPort(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	AddServer(dir, cfg, ServerConfig{Name: "A", Host: "chat.example.com", Port: 2222})
	err := AddServer(dir, cfg, ServerConfig{Name: "B", Host: "chat.example.com", Port: 2222})
	if err == nil {
		t.Error("duplicate (host, port) should be rejected")
	}
	if len(cfg.Servers) != 1 {
		t.Errorf("rejected add should not mutate list, got %d servers", len(cfg.Servers))
	}
}

func TestAddServer_AllowsSameHostDifferentPort(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	AddServer(dir, cfg, ServerConfig{Name: "A", Host: "chat.example.com", Port: 2222})
	err := AddServer(dir, cfg, ServerConfig{Name: "B", Host: "chat.example.com", Port: 2223})
	if err != nil {
		t.Errorf("different port should be allowed, got: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(cfg.Servers))
	}
}

func TestRemoveServer(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	AddServer(dir, cfg, ServerConfig{Name: "A", Host: "a.example.com", Port: 2222})
	AddServer(dir, cfg, ServerConfig{Name: "B", Host: "b.example.com", Port: 2222})

	if err := RemoveServer(dir, cfg, 0); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "B" {
		t.Errorf("wrong server removed: %q remaining", cfg.Servers[0].Name)
	}
}

func TestRemoveServer_RejectsInvalidIndex(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Servers: []ServerConfig{{Name: "A", Host: "a", Port: 22}}}

	if err := RemoveServer(dir, cfg, -1); err == nil {
		t.Error("negative index should error")
	}
	if err := RemoveServer(dir, cfg, 5); err == nil {
		t.Error("out-of-range index should error")
	}
	if len(cfg.Servers) != 1 {
		t.Error("invalid index should not mutate servers list")
	}
}

func TestRemoveServer_RemovesLocalData(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	srv := ServerConfig{Name: "X", Host: "remove.example.com", Port: 2222}
	AddServer(dir, cfg, srv)

	// Create some fake data for this server
	serverDataDir := filepath.Join(dir, srv.Host)
	os.MkdirAll(serverDataDir, 0700)
	os.WriteFile(filepath.Join(serverDataDir, "messages.db"), []byte("fake"), 0600)

	if err := RemoveServer(dir, cfg, 0); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(serverDataDir); !os.IsNotExist(err) {
		t.Error("server data dir should have been removed")
	}
}

// -- ServerDataDir / Size --

func TestServerDataDir(t *testing.T) {
	got := ServerDataDir("/cfg", ServerConfig{Host: "chat.example.com"})
	if got != "/cfg/chat.example.com" {
		t.Errorf("got %q", got)
	}
}

func TestServerDataSize_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	srv := ServerConfig{Host: "nothing.example.com"}
	size, _ := ServerDataSize(dir, srv)
	if size != 0 {
		t.Errorf("missing dir should report 0 bytes, got %d", size)
	}
}

func TestServerDataSize_WithFiles(t *testing.T) {
	dir := t.TempDir()
	srv := ServerConfig{Host: "has-data.example.com"}
	dataDir := filepath.Join(dir, srv.Host)
	os.MkdirAll(dataDir, 0700)
	os.WriteFile(filepath.Join(dataDir, "a"), []byte("hello"), 0600)
	os.WriteFile(filepath.Join(dataDir, "b"), []byte("world!"), 0600)

	size, err := ServerDataSize(dir, srv)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size != 11 { // 5 + 6
		t.Errorf("expected 11 bytes, got %d", size)
	}
}

// -- ClearServerData --

func TestClearServerData(t *testing.T) {
	dir := t.TempDir()
	srv := ServerConfig{Host: "clear.example.com"}
	dataDir := filepath.Join(dir, srv.Host)
	os.MkdirAll(dataDir, 0700)
	os.WriteFile(filepath.Join(dataDir, "messages.db"), []byte("db"), 0600)
	os.WriteFile(filepath.Join(dataDir, "known_host"), []byte("hostkey"), 0600)

	if err := ClearServerData(dir, srv); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// messages.db removed
	if _, err := os.Stat(filepath.Join(dataDir, "messages.db")); !os.IsNotExist(err) {
		t.Error("messages.db should have been removed")
	}
	// known_host preserved
	if _, err := os.Stat(filepath.Join(dataDir, "known_host")); err != nil {
		t.Errorf("known_host should be preserved: %v", err)
	}
}

// -- MutedMap --

func TestLoadMutedMap(t *testing.T) {
	cfg := &Config{
		Notifications: NotificationConfig{
			MutedRooms:         []string{"general", "engineering"},
			MutedConversations: []string{"conv_xyz"},
		},
	}
	m := LoadMutedMap(cfg)
	if !m["general"] || !m["engineering"] || !m["conv_xyz"] {
		t.Errorf("muted map missing expected entries: %v", m)
	}
	if m["not-muted"] {
		t.Error("unexpected entry")
	}
}

func TestSaveMutedMap_DistinguishesConvsAndRooms(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	muted := map[string]bool{
		"general":     true,
		"engineering": true,
		"conv_abc123": true,
		"off-by-user": false, // should be skipped
	}
	if err := SaveMutedMap(dir, cfg, muted); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify rooms vs conversations were separated correctly
	hasConv := false
	for _, c := range cfg.Notifications.MutedConversations {
		if c == "conv_abc123" {
			hasConv = true
		}
	}
	if !hasConv {
		t.Errorf("conv_abc123 should be in MutedConversations: %v", cfg.Notifications.MutedConversations)
	}
	for _, r := range cfg.Notifications.MutedRooms {
		if strings.HasPrefix(r, "conv_") {
			t.Errorf("MutedRooms contains conversation: %q", r)
		}
	}
	// off-by-user was false, should not appear
	for _, r := range cfg.Notifications.MutedRooms {
		if r == "off-by-user" {
			t.Error("false entries should be skipped")
		}
	}
}

func TestSaveMutedMap_EmptyClearsExistingEntries(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Notifications: NotificationConfig{
			MutedRooms: []string{"stale"},
		},
	}
	if err := SaveMutedMap(dir, cfg, map[string]bool{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(cfg.Notifications.MutedRooms) != 0 {
		t.Errorf("empty map should clear MutedRooms, got %v", cfg.Notifications.MutedRooms)
	}
}

// -- MarkHelpShown --

func TestMarkHelpShown(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	if err := MarkHelpShown(dir, cfg); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if !cfg.Notifications.HelpShown {
		t.Error("HelpShown should be true")
	}
	// Persisted
	loaded, _ := Load(dir)
	if !loaded.Notifications.HelpShown {
		t.Error("HelpShown should persist to disk")
	}
}
