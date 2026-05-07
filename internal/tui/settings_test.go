package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

func TestSettings_EditNameValid(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("  New Name  ")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("valid name should emit ProfileUpdateMsg")
	}
	msg := cmd()
	pu, ok := msg.(ProfileUpdateMsg)
	if !ok {
		t.Fatalf("expected ProfileUpdateMsg, got %T", msg)
	}
	if pu.DisplayName != "New Name" {
		t.Errorf("name = %q, want trimmed 'New Name'", pu.DisplayName)
	}
	if s.editing {
		t.Error("should exit edit mode on valid name")
	}
}

func TestSettings_EditNameTooShort(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("A")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("invalid name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode on invalid name")
	}
}

func TestSettings_EditNameEmpty(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("   ")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("whitespace-only name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_EditNameTooLong(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("abcdefghijklmnopqrstuvwxyz1234567") // 33 chars

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("too-long name should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_EditNameZeroWidth(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.editing = true
	s.editAction = "edit_name"
	s.editInput.SetValue("test\u200Bname")

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("name with zero-width chars should not emit command")
	}
	if !s.editing {
		t.Error("should stay in edit mode")
	}
}

func TestSettings_CopyPublicKeyAction(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.items = []settingsItem{
		{label: "    [Copy public key]", action: "copy_pubkey"},
	}
	s.cursor = 0

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("copy public key should emit SettingsActionMsg")
	}
	msg := cmd()
	act, ok := msg.(SettingsActionMsg)
	if !ok {
		t.Fatalf("expected SettingsActionMsg, got %T", msg)
	}
	if act.Action != "copy_pubkey" {
		t.Fatalf("action = %q, want copy_pubkey", act.Action)
	}
}

func TestSettings_CopyFingerprintAction(t *testing.T) {
	s := NewSettings()
	s.visible = true
	s.items = []settingsItem{
		{label: "    [Copy fingerprint]", action: "copy_fingerprint"},
	}
	s.cursor = 0

	s, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("copy fingerprint should emit SettingsActionMsg")
	}
	msg := cmd()
	act, ok := msg.(SettingsActionMsg)
	if !ok {
		t.Fatalf("expected SettingsActionMsg, got %T", msg)
	}
	if act.Action != "copy_fingerprint" {
		t.Fatalf("action = %q, want copy_fingerprint", act.Action)
	}
}

// TestSettingsView_AppendsRastermDeleteEscapeWhenCapable verifies
// that settings.View emits the kitty graphics-protocol delete
// escape at the END of its rendered output when rasterm is the
// active encoder.
//
// Why settings does this and other modals don't: the dialog body
// is uniquely tall — server header + Profile + Storage + Keys +
// every configured server + Device + Account sections, often 30+
// lines on a typical 24-row terminal. When the rendered string
// exceeds terminal height, bubbletea's standard renderer drops
// lines from the TOP of the buffer (standard_renderer.go:186-188).
// A kitty escape PREPENDED at the App.View layer therefore gets
// truncated away before reaching the terminal — image visibly
// persists behind the dialog. Appending in settings.View itself
// puts the escape in the rendered string's tail, which bubbletea
// keeps when truncating.
//
// Pinning: the escape must be at the end (so top-truncation
// preserves it), must be the kitty delete form (a=d, d=I, i=<id>),
// and must only fire when rasterm is the active encoder.
func TestSettingsView_AppendsRastermDeleteEscapeWhenCapable(t *testing.T) {
	withRastermProtocol(t, rastermKitty)

	s := NewSettings()
	s.Show(&config.Config{
		Device:  config.DeviceConfig{ID: "dev_test"},
		Servers: []config.ServerConfig{{Name: "Home", Host: "127.0.0.1", Port: 2222, Key: "~/.ssh/id_ed25519"}},
	}, t.TempDir(), "alice", 0)

	out := s.View(80, 24)
	if !strings.HasSuffix(out, rastermDeleteEscape()) {
		t.Errorf("settings.View output should END with kitty delete escape (so top-truncation can't strip it); got tail %q",
			truncateForLog(out, 64))
	}
}

// TestSettingsView_OmitsEscapeWhenRastermNotCapable confirms the
// inverse: when rasterm isn't active, the dialog doesn't emit
// stray bytes to the terminal. Non-rasterm terminals would silently
// drop the unknown DCS sequence, but cleaner to skip outright.
func TestSettingsView_OmitsEscapeWhenRastermNotCapable(t *testing.T) {
	withRastermProtocol(t, rastermNone)

	s := NewSettings()
	s.Show(&config.Config{
		Device:  config.DeviceConfig{ID: "dev_test"},
		Servers: []config.ServerConfig{{Name: "Home", Host: "127.0.0.1", Port: 2222, Key: "~/.ssh/id_ed25519"}},
	}, t.TempDir(), "alice", 0)

	out := s.View(80, 24)
	if strings.Contains(out, "\x1b_Ga=d") {
		t.Errorf("settings.View should NOT emit kitty escape when rasterm isn't active, got")
	}
}

// TestSettingsView_ScrollsToFitTerminalHeight is the regression
// guard for "settings dialog top border missing on small terminals."
//
// Pre-fix, settings.View built its full content (header + items +
// footer) and passed it all to dialogStyle.Render. With many
// configured servers, the rendered output exceeded the terminal
// height. Bubbletea's standard renderer drops lines from the TOP
// of the buffer when content > height (standard_renderer.go:
// `if r.height > 0 && len(newLines) > r.height`), so the dialog's
// top border, the "Settings" header, and the first few items got
// silently truncated.
//
// Post-fix, settings.View slices to a cursor-following scroll
// window when content overflows. The dialog chrome is reapplied
// AFTER slicing, so the top border is always present in the
// rendered output regardless of how many items would fit.
//
// Pinning: with a constructed config that produces more rows than
// fit in the test terminal height, the rendered dialog must still
// have its top border (the rounded-corner `╭` char) and a bottom
// border (`╰`).
func TestSettingsView_ScrollsToFitTerminalHeight(t *testing.T) {
	withRastermProtocol(t, rastermNone) // keep escape out of border check

	s := NewSettings()
	// Lots of servers → forces overflow in a small terminal height.
	servers := make([]config.ServerConfig, 20)
	for i := range servers {
		servers[i] = config.ServerConfig{
			Name: "Server" + string(rune('A'+i)),
			Host: "127.0.0.1",
			Port: 2222 + i,
			Key:  "~/.ssh/id_ed25519",
		}
	}
	s.Show(&config.Config{
		Device:  config.DeviceConfig{ID: "dev_test"},
		Servers: servers,
	}, t.TempDir(), "alice", 0)

	out := s.View(80, 24)
	plain := stripANSI(out)
	if !strings.Contains(plain, "╭") {
		t.Errorf("settings.View should keep its top border (╭) intact even when content overflows; pre-fix the top border was truncated by bubbletea's renderer. Got:\n%s", plain)
	}
	if !strings.Contains(plain, "╰") {
		t.Errorf("settings.View should keep its bottom border (╰) intact. Got:\n%s", plain)
	}

	// Sanity: the rendered output (split on newlines) must fit
	// within the terminal height. If our slicing math is off, we'd
	// emit more rows than the terminal can show.
	rows := strings.Count(out, "\n") + 1
	if rows > 24 {
		t.Errorf("rendered settings exceeded terminal height: got %d rows, want <= 24", rows)
	}
}

// TestSettingsView_ScrollKeepsCursorVisible verifies that the
// cursor's row stays inside the rendered window when settings
// overflows. Cursor selects items 2..items[s.cursor] in the
// rendered output (after header + blank); after scrolling, that
// same row must still appear in the sliced visible.
func TestSettingsView_ScrollKeepsCursorVisible(t *testing.T) {
	withRastermProtocol(t, rastermNone)

	s := NewSettings()
	servers := make([]config.ServerConfig, 20)
	for i := range servers {
		servers[i] = config.ServerConfig{
			Name: "Srv" + string(rune('A'+i)),
			Host: "127.0.0.1",
			Port: 2222 + i,
			Key:  "~/.ssh/id_ed25519",
		}
	}
	s.Show(&config.Config{
		Device:  config.DeviceConfig{ID: "dev_test"},
		Servers: servers,
	}, t.TempDir(), "alice", 0)

	// Move cursor far enough into the items list that we'd be
	// off-screen if scroll didn't follow. Pick the last-actionable
	// index.
	s.cursor = len(s.items) - 1

	out := s.View(80, 24)
	plain := stripANSI(out)

	// The last actionable item's label should appear in the rendered
	// view (otherwise the cursor row would be off-screen).
	wantLabel := s.items[len(s.items)-1].label
	if !strings.Contains(plain, strings.TrimSpace(wantLabel)) {
		t.Errorf("cursor row's label %q should be in the rendered window after scroll-follows-cursor. Got:\n%s",
			strings.TrimSpace(wantLabel), plain)
	}
}
