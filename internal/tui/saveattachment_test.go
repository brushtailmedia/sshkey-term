package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSaveAttachmentModel_ShowHide verifies the visibility state flips
// and that Hide() clears the in-flight source path / attachment name so
// a subsequent Show() doesn't leak stale state from the previous save.
func TestSaveAttachmentModel_ShowHide(t *testing.T) {
	m := NewSaveAttachment()
	if m.IsVisible() {
		t.Fatal("fresh modal should not be visible")
	}
	m.Show("/tmp/cache/file_abc", "photo.png", "/Users/x/Downloads/photo.png")
	if !m.IsVisible() {
		t.Fatal("Show should make modal visible")
	}
	m.Hide()
	if m.IsVisible() {
		t.Fatal("Hide should make modal invisible")
	}
	if m.sourcePath != "" || m.attachmentName != "" {
		t.Errorf("Hide left stale state: sourcePath=%q attachmentName=%q",
			m.sourcePath, m.attachmentName)
	}
}

// TestSaveAttachmentModel_EscCancels verifies pressing Esc in the edit
// phase closes the modal and emits SaveAttachmentCancelledMsg (so the
// App can surface a "Save cancelled" status-bar message).
func TestSaveAttachmentModel_EscCancels(t *testing.T) {
	m := NewSaveAttachment()
	m.Show("/src", "name", "/dest")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsVisible() {
		t.Error("Esc should hide modal")
	}
	msg := cmd()
	if _, ok := msg.(SaveAttachmentCancelledMsg); !ok {
		t.Errorf("Esc should emit SaveAttachmentCancelledMsg, got %T", msg)
	}
}

// TestSaveAttachmentModel_EnterNonExistentFileSavesDirectly verifies
// the common path: user types a destination where no file currently
// exists, presses Enter, modal closes and emits SaveAttachmentDoMsg
// with the expected source+dest paths. No overwrite prompt interposed.
func TestSaveAttachmentModel_EnterNonExistentFileSavesDirectly(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "photo.png")

	m := NewSaveAttachment()
	m.Show("/src/file_abc", "photo.png", dest)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.IsVisible() {
		t.Error("Enter on non-existent dest should hide modal")
	}
	msg := cmd()
	got, ok := msg.(SaveAttachmentDoMsg)
	if !ok {
		t.Fatalf("Enter should emit SaveAttachmentDoMsg, got %T", msg)
	}
	if got.SourcePath != "/src/file_abc" {
		t.Errorf("SourcePath = %q, want /src/file_abc", got.SourcePath)
	}
	if got.DestPath != dest {
		t.Errorf("DestPath = %q, want %q", got.DestPath, dest)
	}
}

// TestSaveAttachmentModel_EnterExistingFileTransitionsToOverwrite
// verifies that when the user's typed destination resolves to an
// existing file, Enter does NOT fire the save — instead the modal
// transitions to phaseExists with a suggested rename and waits for
// the user's o/r/e/Esc choice.
func TestSaveAttachmentModel_EnterExistingFileTransitionsToOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "photo.png")
	// Seed a pre-existing file at the destination.
	if err := os.WriteFile(dest, []byte("pre-existing content"), 0644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}

	m := NewSaveAttachment()
	m.Show("/src/file_abc", "photo.png", dest)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("Enter on existing dest should not fire a cmd yet (awaiting user choice); got %T", cmd())
	}
	if !m.IsVisible() {
		t.Fatal("Enter on existing dest should keep modal visible for overwrite prompt")
	}
	if m.phase != phaseExists {
		t.Errorf("phase = %d, want phaseExists (%d)", m.phase, phaseExists)
	}
	if m.suggested == "" {
		t.Error("suggested rename should be populated in phaseExists")
	}
	// Rename suggestion should differ from the original dest and not yet
	// exist on disk.
	if m.suggested == dest {
		t.Errorf("suggested = %q should differ from dest", m.suggested)
	}
	if _, err := os.Stat(m.suggested); err == nil {
		t.Errorf("suggested rename %q should not already exist", m.suggested)
	}
}

// TestSaveAttachmentModel_OverwriteChoice verifies the three terminal
// choices in phaseExists: [o] overwrite emits DoMsg with the original
// dest, [r] rename emits DoMsg with the suggested path, [Esc] cancels.
func TestSaveAttachmentModel_OverwriteChoice(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(dest, []byte("pre"), 0644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}

	for _, tc := range []struct {
		name         string
		key          string
		wantDoMsg    bool
		wantCanceled bool
		wantDest     string
	}{
		{name: "overwrite", key: "o", wantDoMsg: true, wantDest: dest},
		{name: "rename", key: "r", wantDoMsg: true, wantDest: "suggested"},
		{name: "cancel", key: "esc", wantCanceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewSaveAttachment()
			m.Show("/src/file_abc", "photo.png", dest)
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // transition to phaseExists
			suggested := m.suggested
			var cmd tea.Cmd
			if tc.key == "esc" {
				m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			} else {
				m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
			}
			if cmd == nil {
				t.Fatal("expected a cmd")
			}
			msg := cmd()
			if tc.wantDoMsg {
				got, ok := msg.(SaveAttachmentDoMsg)
				if !ok {
					t.Fatalf("expected SaveAttachmentDoMsg, got %T", msg)
				}
				wantDest := tc.wantDest
				if wantDest == "suggested" {
					wantDest = suggested
				}
				if got.DestPath != wantDest {
					t.Errorf("DestPath = %q, want %q", got.DestPath, wantDest)
				}
			}
			if tc.wantCanceled {
				if _, ok := msg.(SaveAttachmentCancelledMsg); !ok {
					t.Errorf("expected SaveAttachmentCancelledMsg, got %T", msg)
				}
			}
		})
	}
}

// TestSaveAttachmentModel_EditReturnsToPhaseEdit verifies [e] in the
// overwrite prompt takes the user back to the path-editing phase so
// they can pick a different destination without committing to
// overwrite or rename.
func TestSaveAttachmentModel_EditReturnsToPhaseEdit(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(dest, []byte("pre"), 0644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}

	m := NewSaveAttachment()
	m.Show("/src", "photo.png", dest)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseExists {
		t.Fatalf("setup: want phaseExists, got %d", m.phase)
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil && cmd() != nil {
		t.Errorf("[e] should not emit a tea.Msg; got %T", cmd())
	}
	if m.phase != phaseEdit {
		t.Errorf("[e] should return to phaseEdit (%d), got %d", phaseEdit, m.phase)
	}
	if !m.IsVisible() {
		t.Error("[e] should keep modal visible")
	}
}

// TestSaveAttachmentModel_EmptyPathRejected verifies submitting an
// empty destination path sets the modal's error message and keeps the
// modal open for correction rather than firing a bogus save.
func TestSaveAttachmentModel_EmptyPathRejected(t *testing.T) {
	m := NewSaveAttachment()
	m.Show("/src", "photo.png", "")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil && cmd() != nil {
		t.Errorf("empty-path Enter should not fire a save; got cmd returning %T", cmd())
	}
	if !m.IsVisible() {
		t.Error("empty-path Enter should keep modal visible")
	}
	if m.errMsg == "" {
		t.Error("empty-path Enter should set errMsg")
	}
}

// TestSaveAttachmentModel_EnterDirectoryJoinsAttachmentName verifies
// that when the user types a path pointing at an existing directory,
// the modal appends the attachment's sanitized filename rather than
// trying to write a file where a directory already exists. This
// matches user expectation — "save to ~/Pictures/" should save
// ~/Pictures/photo.png, not overwrite the directory.
func TestSaveAttachmentModel_EnterDirectoryJoinsAttachmentName(t *testing.T) {
	dir := t.TempDir()

	m := NewSaveAttachment()
	m.Show("/src/file_abc", "photo.png", dir) // path is a directory
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a cmd")
	}
	got, ok := cmd().(SaveAttachmentDoMsg)
	if !ok {
		t.Fatalf("expected SaveAttachmentDoMsg, got %T", cmd())
	}
	wantDest := filepath.Join(dir, "photo.png")
	if got.DestPath != wantDest {
		t.Errorf("DestPath = %q, want %q (directory + attachment name)", got.DestPath, wantDest)
	}
}

// TestSaveAttachmentModel_HandleMouseConsumes verifies mouse events are
// silently absorbed — the modal is keyboard-only, and click-through to
// the sidebar / messages pane / compose input underneath would be a UX
// regression. Combined with the keyboard-only overlay early-return in
// App.handleMouse, this guarantees clicks anywhere while the modal is
// up have zero effect on the background.
func TestSaveAttachmentModel_HandleMouseConsumes(t *testing.T) {
	m := NewSaveAttachment()
	m.Show("/src", "photo.png", "/dest")
	for _, btn := range []tea.MouseButton{tea.MouseButtonLeft, tea.MouseButtonRight, tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		_, cmd := m.HandleMouse(tea.MouseMsg{Button: btn, Action: tea.MouseActionPress, X: 10, Y: 10})
		if cmd != nil {
			t.Errorf("HandleMouse(btn=%v) should return nil cmd to absorb the event, got %T", btn, cmd())
		}
	}
	if !m.IsVisible() {
		t.Error("mouse events should not hide modal")
	}
}

// TestUniqueDest verifies the rename-suggestion numbering: photo.png
// exists → photo (1).png; that exists too → photo (2).png; etc. Input
// with no existing file passes through unchanged.
func TestUniqueDest(t *testing.T) {
	dir := t.TempDir()

	// No collision → identity.
	path := filepath.Join(dir, "fresh.png")
	if got := uniqueDest(path); got != path {
		t.Errorf("uniqueDest on missing file = %q, want %q", got, path)
	}

	// Single collision → (1).
	p0 := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(p0, []byte("a"), 0644); err != nil {
		t.Fatalf("seed p0: %v", err)
	}
	got := uniqueDest(p0)
	want := filepath.Join(dir, "photo (1).png")
	if got != want {
		t.Errorf("uniqueDest with 1 collision = %q, want %q", got, want)
	}

	// Double collision → (2).
	p1 := want
	if err := os.WriteFile(p1, []byte("b"), 0644); err != nil {
		t.Fatalf("seed p1: %v", err)
	}
	got = uniqueDest(p0)
	want = filepath.Join(dir, "photo (2).png")
	if got != want {
		t.Errorf("uniqueDest with 2 collisions = %q, want %q", got, want)
	}
}

// TestUniqueDest_NoExtension verifies numbering works on extensionless
// filenames — README → README (1).
func TestUniqueDest_NoExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "README")
	if err := os.WriteFile(p, []byte("a"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := uniqueDest(p)
	want := filepath.Join(dir, "README (1)")
	if got != want {
		t.Errorf("uniqueDest extensionless = %q, want %q", got, want)
	}
}

// TestExpandTilde covers the three interesting inputs: no tilde passes
// through, bare "~" resolves to home, "~/sub" joins under home.
func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir resolvable")
	}
	cases := []struct {
		in   string
		want string
	}{
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
		{"~", home},
		{"~/Downloads", filepath.Join(home, "Downloads")},
		{"~/Downloads/foo.png", filepath.Join(home, "Downloads", "foo.png")},
	}
	for _, tc := range cases {
		if got := expandTilde(tc.in); got != tc.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSanitizeAttachmentName is the path-traversal guard. Sender-
// supplied filenames containing path separators or the dot/dot-dot
// forms must collapse to a plain basename so a hostile filename can't
// steer the save outside the chosen destination directory.
func TestSanitizeAttachmentName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Well-behaved — pass through.
		{"photo.png", "photo.png"},
		{"report.pdf", "report.pdf"},
		{"日本語.png", "日本語.png"},
		{"with space.txt", "with space.txt"},

		// Traversal attempts — all collapse to basename.
		{"../../etc/passwd", "passwd"},
		{"../foo.png", "foo.png"},
		{"/absolute/bad.png", "bad.png"},

		// Dangerous edge cases — fall through to the generic fallback.
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
		{"/", "attachment"}, // filepath.Base("/") = "/", which is rejected
	}
	for _, tc := range cases {
		got := sanitizeAttachmentName(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeAttachmentName(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// A sanitized name must never contain a path separator — the
		// critical invariant that prevents filepath.Join escape.
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("sanitizeAttachmentName(%q) = %q contains path separator", tc.in, got)
		}
	}
}
