package config

// Tests for the 13 path helpers + ValidateHost validator added in
// Phase 1 of the path-centralization refactor. See
// path-centralization.md §"Phase 1" and §"Server data dir".
//
// HOME-sensitive tests use t.Setenv("HOME", t.TempDir()) per the
// sshkey-chat CI conventions referenced in the plan.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/testutil"
)

// TestDefaultConfigDir_WithControlledHome verifies the helper
// returns `<HOME>/.sshkey-term` under a controlled HOME — distinct
// from the existing TestDefaultConfigDir in config_test.go which
// just sanity-checks the suffix without isolating HOME.
func TestDefaultConfigDir_WithControlledHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultConfigDir()
	want := filepath.Join(home, ".sshkey-term")
	if got != want {
		t.Errorf("DefaultConfigDir() = %q, want %q", got, want)
	}
}

// TestConfigFilePath verifies the canonical `config.toml` join.
func TestConfigFilePath(t *testing.T) {
	got := ConfigFilePath("/cfg")
	want := filepath.Join("/cfg", "config.toml")
	if got != want {
		t.Errorf("ConfigFilePath = %q, want %q", got, want)
	}
}

// TestExpandUserPath_Tilde verifies the `~/foo` form expands to
// `<HOME>/foo`.
func TestExpandUserPath_Tilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ExpandUserPath("~/foo/bar")
	want := filepath.Join(home, "foo/bar")
	if got != want {
		t.Errorf("ExpandUserPath(~/foo/bar) = %q, want %q", got, want)
	}
}

// TestExpandUserPath_BareTilde verifies a bare `~` expands to HOME
// (no trailing separator).
func TestExpandUserPath_BareTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ExpandUserPath("~")
	if got != home {
		t.Errorf("ExpandUserPath(~) = %q, want %q", got, home)
	}
}

// TestExpandUserPath_AbsolutePassThrough verifies an absolute path
// is returned unchanged.
func TestExpandUserPath_AbsolutePassThrough(t *testing.T) {
	got := ExpandUserPath("/etc/foo")
	if got != "/etc/foo" {
		t.Errorf("ExpandUserPath(/etc/foo) = %q, want unchanged", got)
	}
}

// TestExpandUserPath_RelativePassThrough verifies a relative path is
// returned unchanged.
func TestExpandUserPath_RelativePassThrough(t *testing.T) {
	got := ExpandUserPath("foo/bar")
	if got != "foo/bar" {
		t.Errorf("ExpandUserPath(foo/bar) = %q, want unchanged", got)
	}
}

// TestExpandUserPath_TildeUserNotExpanded verifies `~user/...` is
// NOT expanded — this is the documented out-of-scope form.
func TestExpandUserPath_TildeUserNotExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ExpandUserPath("~someuser/foo")
	if got != "~someuser/foo" {
		t.Errorf("ExpandUserPath(~someuser/foo) = %q, want unchanged", got)
	}
}

// TestExpandUserPath_EmptyString verifies the empty-input case.
func TestExpandUserPath_EmptyString(t *testing.T) {
	got := ExpandUserPath("")
	if got != "" {
		t.Errorf("ExpandUserPath(\"\") = %q, want empty", got)
	}
}

// TestValidateHost_Valid verifies that legitimate host strings
// pass validation.
func TestValidateHost_Valid(t *testing.T) {
	cases := []string{
		"example.com",
		"chat.example.com",
		"sub.host-name.example",
		"127.0.0.1",
		"server01",
		"host_with_underscore",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			if err := ValidateHost(h); err != nil {
				t.Errorf("ValidateHost(%q) = %v, want nil", h, err)
			}
		})
	}
}

// TestValidateHost_RejectsEmpty verifies empty / whitespace-only
// hosts are rejected.
func TestValidateHost_RejectsEmpty(t *testing.T) {
	cases := []string{"", " ", "\t", "   "}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			err := ValidateHost(h)
			if err == nil {
				t.Errorf("ValidateHost(%q) = nil, want error", h)
				return
			}
			if !errors.Is(err, ErrInvalidHost) {
				t.Errorf("ValidateHost(%q) error = %v, want wraps ErrInvalidHost", h, err)
			}
		})
	}
}

// TestValidateHost_RejectsPathSeparators verifies forward and back
// slashes are rejected.
func TestValidateHost_RejectsPathSeparators(t *testing.T) {
	cases := []string{
		"foo/bar",
		"../etc",
		"foo\\bar",
		"/absolute",
		"with/slash/inside",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			err := ValidateHost(h)
			if err == nil {
				t.Errorf("ValidateHost(%q) = nil, want error", h)
				return
			}
			if !errors.Is(err, ErrInvalidHost) {
				t.Errorf("ValidateHost(%q) error = %v, want wraps ErrInvalidHost", h, err)
			}
		})
	}
}

// TestValidateHost_RejectsTraversalSegments verifies `.` and `..`
// are rejected.
func TestValidateHost_RejectsTraversalSegments(t *testing.T) {
	for _, h := range []string{".", ".."} {
		t.Run(h, func(t *testing.T) {
			err := ValidateHost(h)
			if err == nil {
				t.Errorf("ValidateHost(%q) = nil, want error", h)
				return
			}
			if !errors.Is(err, ErrInvalidHost) {
				t.Errorf("ValidateHost(%q) error = %v, want wraps ErrInvalidHost", h, err)
			}
		})
	}
}

// TestValidateHost_RejectsControlBytes verifies NUL and other
// control bytes are rejected.
func TestValidateHost_RejectsControlBytes(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"NUL", "host\x00.com"},
		{"newline", "host\n.com"},
		{"tab inside", "host\t.com"},
		{"CR", "host\r.com"},
		{"DEL", "host\x7f.com"},
		{"BEL", "host\x07.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateHost(c.host)
			if err == nil {
				t.Errorf("ValidateHost(%q) = nil, want error", c.host)
				return
			}
			if !errors.Is(err, ErrInvalidHost) {
				t.Errorf("ValidateHost(%q) error = %v, want wraps ErrInvalidHost", c.host, err)
			}
		})
	}
}

// TestValidateHost_ErrorMessageDescriptive verifies the error
// message includes enough context for the user to understand
// the rejection without reading source.
func TestValidateHost_ErrorMessageDescriptive(t *testing.T) {
	err := ValidateHost("../etc")
	if err == nil {
		t.Fatal("expected error for ../etc")
	}
	msg := err.Error()
	// Should include the offending host and the reason.
	if !strings.Contains(msg, "invalid host") {
		t.Errorf("error message %q should mention 'invalid host'", msg)
	}
	if !strings.Contains(msg, "../etc") {
		t.Errorf("error message %q should include the offending host", msg)
	}
}

// TestServerDirName verifies the host passthrough.
func TestServerDirName(t *testing.T) {
	got := ServerDirName("chat.example.com")
	if got != "chat.example.com" {
		t.Errorf("ServerDirName = %q, want passthrough", got)
	}
}

// TestServerDataDirForHost verifies the `<configDir>/<host>` join.
func TestServerDataDirForHost(t *testing.T) {
	got := ServerDataDirForHost("/cfg", "chat.example.com")
	want := filepath.Join("/cfg", "chat.example.com")
	if got != want {
		t.Errorf("ServerDataDirForHost = %q, want %q", got, want)
	}
}

// TestServerKeysDir verifies the `<configDir>/<host>/keys` join.
func TestServerKeysDir(t *testing.T) {
	got := ServerKeysDir("/cfg", "chat.example.com")
	want := filepath.Join("/cfg", "chat.example.com", "keys")
	if got != want {
		t.Errorf("ServerKeysDir = %q, want %q", got, want)
	}
}

// TestServerKeyPath verifies the canonical
// `<configDir>/<host>/keys/id_ed25519` join.
func TestServerKeyPath(t *testing.T) {
	got := ServerKeyPath("/cfg", "chat.example.com")
	want := filepath.Join("/cfg", "chat.example.com", "keys", "id_ed25519")
	if got != want {
		t.Errorf("ServerKeyPath = %q, want %q", got, want)
	}
}

// TestKnownHostPath verifies the `<dataDir>/known_host` join.
// Note singular `known_host` (not `known_hosts` plural) — see
// internal/client/hostkey.go for the rationale.
func TestKnownHostPath(t *testing.T) {
	got := KnownHostPath("/data")
	want := filepath.Join("/data", "known_host")
	if got != want {
		t.Errorf("KnownHostPath = %q, want %q", got, want)
	}
}

// TestClientLogPath verifies the `<dataDir>/client.log` join.
func TestClientLogPath(t *testing.T) {
	got := ClientLogPath("/data")
	want := filepath.Join("/data", "client.log")
	if got != want {
		t.Errorf("ClientLogPath = %q, want %q", got, want)
	}
}

// TestFilesDir verifies the `<dataDir>/files` join.
func TestFilesDir(t *testing.T) {
	got := FilesDir("/data")
	want := filepath.Join("/data", "files")
	if got != want {
		t.Errorf("FilesDir = %q, want %q", got, want)
	}
}

// TestAttachmentPath verifies the `<dataDir>/files/<fileID>` join.
func TestAttachmentPath(t *testing.T) {
	got := AttachmentPath("/data", testutil.FileID("abc123"))
	want := filepath.Join("/data", "files", testutil.FileID("abc123"))
	if got != want {
		t.Errorf("AttachmentPath = %q, want %q", got, want)
	}
}

// TestMessagesDBPath verifies the `<dataDir>/messages.db` join.
func TestMessagesDBPath(t *testing.T) {
	got := MessagesDBPath("/data")
	want := filepath.Join("/data", "messages.db")
	if got != want {
		t.Errorf("MessagesDBPath = %q, want %q", got, want)
	}
}

// TestHelpers_ComposeCorrectly verifies the helper composition
// chain produces the expected layout. Tests that
// `<configDir>/<host>/keys/id_ed25519` and
// `<configDir>/<host>/files/<fileID>` come out of the composed
// helpers correctly, mirroring real call patterns.
func TestHelpers_ComposeCorrectly(t *testing.T) {
	configDir := "/etc/sshkey-term"
	host := "chat.example.com"
	dataDir := ServerDataDirForHost(configDir, host)

	if want := "/etc/sshkey-term/chat.example.com"; dataDir != want {
		t.Errorf("composed dataDir = %q, want %q", dataDir, want)
	}

	if got, want := ServerKeysDir(configDir, host), "/etc/sshkey-term/chat.example.com/keys"; got != want {
		t.Errorf("ServerKeysDir composed = %q, want %q", got, want)
	}
	if got, want := ServerKeyPath(configDir, host), "/etc/sshkey-term/chat.example.com/keys/id_ed25519"; got != want {
		t.Errorf("ServerKeyPath composed = %q, want %q", got, want)
	}
	if got, want := KnownHostPath(dataDir), "/etc/sshkey-term/chat.example.com/known_host"; got != want {
		t.Errorf("KnownHostPath composed = %q, want %q", got, want)
	}
	if got, want := ClientLogPath(dataDir), "/etc/sshkey-term/chat.example.com/client.log"; got != want {
		t.Errorf("ClientLogPath composed = %q, want %q", got, want)
	}
	if got, want := FilesDir(dataDir), "/etc/sshkey-term/chat.example.com/files"; got != want {
		t.Errorf("FilesDir composed = %q, want %q", got, want)
	}
	if got, want := AttachmentPath(dataDir, testutil.FileID("fid_xyz")), filepath.Join("/etc/sshkey-term/chat.example.com/files", testutil.FileID("fid_xyz")); got != want {
		t.Errorf("AttachmentPath composed = %q, want %q", got, want)
	}
	if got, want := MessagesDBPath(dataDir), "/etc/sshkey-term/chat.example.com/messages.db"; got != want {
		t.Errorf("MessagesDBPath composed = %q, want %q", got, want)
	}
}

// TestValidFileID is the canonical test for the audit-F12 path-safety
// invariant. fileID is server/sender-supplied and gets joined onto FilesDir at
// every read/write/stat/render site, so it must accept exactly the shape the
// server mints ("file_" + 21 nanoid characters over [A-Za-z0-9_-]) and
// nothing else — not merely "a safe single path component" (attachments v2
// PLAN step 3b).
func TestValidFileID(t *testing.T) {
	valid := []string{
		"file_V1StGXR8_Z5jdHi6B-myT", // server format: file_ + 21-char nanoid (incl. _ and -)
		"file_AAAAAAAAAAAAAAAAAAAAA",
		"file_---------------------",
		"file______________________",
		"file_000000000000000000000",
	}
	for _, id := range valid {
		if !ValidFileID(id) {
			t.Errorf("ValidFileID(%q) = false, want true (legitimate id)", id)
		}
	}

	invalid := []string{
		"",                            // malformed: empty ID has no required prefix or suffix
		".",                           // malformed: dot path is not a file ID
		"..",                          // malformed: dot path is not a file ID
		"../etc/passwd",               // malformed: path separators or traversal are not permitted in a file ID
		"../../../../etc/cron.d/evil", // malformed: path separators or traversal are not permitted in a file ID
		"/etc/passwd",                 // malformed: absolute filesystem path is not a file ID
		"/absolute/path",              // malformed: absolute filesystem path is not a file ID
		"sub/dir",                     // malformed: path separators or traversal are not permitted in a file ID
		"foo/",                        // malformed: trailing separator
		"dir\\win",                    // malformed: backslash (cross-platform defense-in-depth)
		"..\\..\\windows",             // malformed: windows-style traversal
		"a/../../b",                   // malformed: path separators or traversal are not permitted in a file ID
		"file_\x00evil",               // malformed: embedded NUL
		// Step 3b: shape, not just safety.
		"file_abc123",                     // malformed: short
		"safename",                        // malformed: no prefix
		"a-b_c.d",                         // malformed: no prefix, dotted
		"FILE_V1StGXR8_Z5jdHi6B-myT",      // malformed: wrong prefix case
		"file_V1StGXR8_Z5jdHi6B-myTX",     // malformed: 22 characters
		"file_V1StGXR8_Z5jdHi6B-my",       // malformed: 20 characters
		"file_V1StGXR8_Z5jdHi6B.myT",      // malformed: dot inside the body
		"file_V1StGXR8_Z5jdHi6B+myT",      // malformed: outside the alphabet
		"file_V1StGXR8_Z5jdHi6B myT",      // malformed: space
		"file_V1StGXR8_Z5jdHi6B-myT.part", // malformed: a derived-file name
		"file_V1StGXR8_Z5jdHi6Bé-my",      // malformed: multibyte, right byte length
	}
	for _, id := range invalid {
		if ValidFileID(id) {
			t.Errorf("ValidFileID(%q) = true, want false", id)
		}
	}
}

// TestValidFileID_CannotSpellADerivedFileName pins the property the delete
// path's closed list relies on: every derived file is `<id>` plus a dotted
// suffix, the alphabet has no dot, so no valid id can name another
// attachment's sidecar (a hostile server cannot make the cache overwrite or
// delete a neighbour's `.part`, `.lthumb` or thumbnail).
func TestValidFileID_CannotSpellADerivedFileName(t *testing.T) {
	suffixes := []string{".part", ".lthumb", ".thumb_v2.png", ".thumb_rasterm_v1.png", ".png"}
	for _, id := range []string{"file_V1StGXR8_Z5jdHi6B-myT", "file_AAAAAAAAAAAAAAAAAAAAA"} {
		if !ValidFileID(id) {
			t.Fatalf("fixture %q is not a valid id", id)
		}
		if strings.Contains(id, ".") {
			t.Errorf("valid id %q contains a dot", id)
		}
		for _, s := range suffixes {
			if ValidFileID(id + s) {
				t.Errorf("ValidFileID(%q) = true, want false (derived-file name)", id+s)
			}
		}
	}
}

// TestAttachmentSidecarPaths pins the two sidecar helpers against the
// original's path: the same directory, the id plus a fixed suffix.
func TestAttachmentSidecarPaths(t *testing.T) {
	dataDir := filepath.Join("some", "data")
	const id = "file_V1StGXR8_Z5jdHi6B-myT"
	orig := AttachmentPath(dataDir, id)
	if got, want := AttachmentPartPath(dataDir, id), orig+".part"; got != want {
		t.Errorf("AttachmentPartPath = %q, want %q", got, want)
	}
	if got, want := AttachmentLocalThumbPath(dataDir, id), orig+".lthumb"; got != want {
		t.Errorf("AttachmentLocalThumbPath = %q, want %q", got, want)
	}
	if filepath.Dir(AttachmentPartPath(dataDir, id)) != FilesDir(dataDir) {
		t.Errorf("sidecar left the files dir: %q", AttachmentPartPath(dataDir, id))
	}
}
