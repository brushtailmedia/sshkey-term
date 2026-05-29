package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultKeybindings_NavModePopupDefaults(t *testing.T) {
	kb := DefaultKeybindings()
	if kb.Navigation.NavModePopupDelayMs != 300 {
		t.Fatalf("default nav_mode_popup_delay_ms = %d, want 300", kb.Navigation.NavModePopupDelayMs)
	}
	if !kb.Navigation.NavModePopup {
		t.Fatalf("default nav_mode_popup = false, want true (popup enabled)")
	}
}

func TestLoadKeybindings_NavModePopupDelayZeroOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "keybindings.toml")
	if err := os.WriteFile(userPath, []byte("[navigation]\nnav_mode_popup_delay_ms = 0\n"), 0600); err != nil {
		t.Fatalf("write keybindings override: %v", err)
	}

	kb := LoadKeybindings(dir)
	if kb.Navigation.NavModePopupDelayMs != 0 {
		t.Fatalf("nav_mode_popup_delay_ms = %d, want explicit zero override", kb.Navigation.NavModePopupDelayMs)
	}
}

func TestLoadKeybindings_NavModePopupDelayPositiveOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "keybindings.toml")
	if err := os.WriteFile(userPath, []byte("[navigation]\nnav_mode_popup_delay_ms = 450\n"), 0600); err != nil {
		t.Fatalf("write keybindings override: %v", err)
	}

	kb := LoadKeybindings(dir)
	if kb.Navigation.NavModePopupDelayMs != 450 {
		t.Fatalf("nav_mode_popup_delay_ms = %d, want 450", kb.Navigation.NavModePopupDelayMs)
	}
}

func TestLoadKeybindings_NavModePopupDisableOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "keybindings.toml")
	// Default is true; an explicit false must override (the kill switch).
	if err := os.WriteFile(userPath, []byte("[navigation]\nnav_mode_popup = false\n"), 0600); err != nil {
		t.Fatalf("write keybindings override: %v", err)
	}

	kb := LoadKeybindings(dir)
	if kb.Navigation.NavModePopup {
		t.Fatalf("nav_mode_popup = true, want explicit false override (kill switch)")
	}
}

func TestLoadKeybindings_TemplateIncludesNavModePopup(t *testing.T) {
	dir := t.TempDir()
	_ = LoadKeybindings(dir)

	b, err := os.ReadFile(filepath.Join(dir, "keybindings.toml"))
	if err != nil {
		t.Fatalf("read generated keybindings.toml: %v", err)
	}
	if !strings.Contains(string(b), "nav_mode_popup_delay_ms") {
		t.Fatalf("generated keybindings.toml missing nav_mode_popup_delay_ms template")
	}
}
