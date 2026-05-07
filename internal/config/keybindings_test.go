package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultKeybindings_NavModeTimeoutDefault(t *testing.T) {
	kb := DefaultKeybindings()
	if kb.Navigation.NavModeTimeoutMs != 2000 {
		t.Fatalf("default nav_mode_timeout_ms = %d, want 2000", kb.Navigation.NavModeTimeoutMs)
	}
}

func TestLoadKeybindings_NavModeTimeoutZeroOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "keybindings.toml")
	if err := os.WriteFile(userPath, []byte("[navigation]\nnav_mode_timeout_ms = 0\n"), 0600); err != nil {
		t.Fatalf("write keybindings override: %v", err)
	}

	kb := LoadKeybindings(dir)
	if kb.Navigation.NavModeTimeoutMs != 0 {
		t.Fatalf("nav_mode_timeout_ms = %d, want explicit zero override", kb.Navigation.NavModeTimeoutMs)
	}
}

func TestLoadKeybindings_NavModeTimeoutPositiveOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "keybindings.toml")
	if err := os.WriteFile(userPath, []byte("[navigation]\nnav_mode_timeout_ms = 4500\n"), 0600); err != nil {
		t.Fatalf("write keybindings override: %v", err)
	}

	kb := LoadKeybindings(dir)
	if kb.Navigation.NavModeTimeoutMs != 4500 {
		t.Fatalf("nav_mode_timeout_ms = %d, want 4500", kb.Navigation.NavModeTimeoutMs)
	}
}

func TestLoadKeybindings_TemplateIncludesNavModeTimeout(t *testing.T) {
	dir := t.TempDir()
	_ = LoadKeybindings(dir)

	b, err := os.ReadFile(filepath.Join(dir, "keybindings.toml"))
	if err != nil {
		t.Fatalf("read generated keybindings.toml: %v", err)
	}
	if !strings.Contains(string(b), "nav_mode_timeout_ms") {
		t.Fatalf("generated keybindings.toml missing nav_mode_timeout_ms template")
	}
}
