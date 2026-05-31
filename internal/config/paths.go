package config

// Path centralization — see path-centralization.md for the full plan.
//
// Every path the app constructs against the per-server data
// directory, the managed keys folder, the user's home dir, or the
// shared config file lives in this file. Phases 2/3 of the refactor
// migrate every inline `filepath.Join(home, ".sshkey-term", ...)` /
// `filepath.Join(configDir, server.Host)` / etc. site to call one
// of these helpers. Phase 4's grep gate enforces "no
// `.sshkey-term`/`os.UserHomeDir` construction outside this file
// for managed config/data/key/cache paths" — explicit out-of-scope
// allowlist documented in path-centralization.md §"Scope — Out".
//
// Two helper signature shapes by design:
//
//   - `(configDir, host string)` for top-level derivation. Used by
//     callers that have both the config dir and a server host at
//     hand (main.go startup, switch-server in tui/app.go, every
//     config-package function taking ServerConfig).
//
//   - `(dataDir string)` for in-server sub-paths. Used by callers
//     that already have a derived dataDir (client.go reading
//     messages.db, hostkey.go reading known_host) and don't want
//     to re-derive it from (configDir, host) on every call.
//
// Validation lives in ValidateHost. Path helpers TRUST their input:
// they assume the caller has called ValidateHost upstream (input
// boundary or config-package surface). See §"Server data dir" for
// the validation contract.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Top-level helpers ───────────────────────────────────────────────

// DefaultConfigDir returns the default app config directory under
// the user's home: `~/.sshkey-term`. Used by main.go at startup
// when no `-config` flag is passed.
//
// On macOS / Linux this is `/Users/<name>/.sshkey-term` or
// `/home/<name>/.sshkey-term`. If os.UserHomeDir fails (no HOME
// in the environment, etc.), the function falls back to an empty
// home and returns just `.sshkey-term` — the same fallback the
// historical inline construction in config.go used pre-refactor,
// preserving that behavior so first-launch on a HOME-less shell
// still produces a relative-path config dir rather than panicking.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sshkey-term")
}

// ConfigFilePath returns the path to the top-level `config.toml`
// under the given configDir. Called by Load() and Save() in
// config.go.
func ConfigFilePath(configDir string) string {
	return filepath.Join(configDir, "config.toml")
}

// ExpandUserPath expands a leading `~` or `~/` in path to the
// user's home directory. Other forms pass through unchanged.
//
//   - `~/foo`      → `<HOME>/foo`
//   - `~`          → `<HOME>` (no trailing separator)
//   - `~user/foo`  → unchanged (not supported in this phase)
//   - `/abs/path`  → unchanged
//   - `relative`   → unchanged
//   - `""`         → unchanged
//
// Designed to replace the eight inline `strings.HasPrefix(p, "~/")`
// + `filepath.Join(home, p[2:])` blocks scattered across
// internal/tui/. The legacy `expandTilde` helper in
// `internal/tui/saveattachment.go` gets deleted in Phase 3 once
// all its callers route through here.
//
// If os.UserHomeDir fails, the function returns the input
// unchanged. Caller decides whether that's an error — for save
// destinations, "user typed a bare tilde with no HOME set" failing
// to expand is the right behavior (the OS file open will fail with
// a clearer error than the helper could produce).
func ExpandUserPath(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// ── Input validation ────────────────────────────────────────────────

// ErrInvalidHost is the sentinel error class returned by
// ValidateHost when a host label fails validation. Callers wrap
// the specific reason in a descriptive message for the user.
var ErrInvalidHost = errors.New("invalid host")

// ValidateHost rejects host strings that would be unsafe to use as
// a path segment under `<configDir>/<host>/`. Called at every
// input boundary (Add Server submit, Wizard server-step finalize,
// CLI bypass flag parse, config-driven startup, switch-server) +
// inside every config-package function that takes ServerConfig
// (AddServer, ServerDataSize, ClearServerData, RemoveServer).
//
// See path-centralization.md §"Server data dir" for the full
// validation rule. Rejection cases:
//
//   - empty / whitespace-only
//   - contains path separator `/` or `\`
//   - is the path-traversal segment `.` or `..`
//   - contains a control byte (including NUL)
//
// Returns nil for any host string that passes. Returns an error
// wrapping ErrInvalidHost with a human-readable suffix on
// failure. Errors are intended to be surfaced to the user
// verbatim (e.g. status bar messages); the rejection is never
// silently rewritten.
func ValidateHost(host string) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%w: host is empty or whitespace-only", ErrInvalidHost)
	}
	if strings.ContainsAny(host, "/\\") {
		return fmt.Errorf("%w: host %q contains path separator", ErrInvalidHost, host)
	}
	if host == "." || host == ".." {
		return fmt.Errorf("%w: host %q is a path-traversal segment", ErrInvalidHost, host)
	}
	for i := 0; i < len(host); i++ {
		// Reject ASCII control bytes (0x00–0x1F, 0x7F). These
		// don't appear in valid hostnames and are a common
		// terminal-injection vector if echoed back.
		c := host[i]
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: host contains control byte at position %d", ErrInvalidHost, i)
		}
	}
	return nil
}

// ── Per-server top-level helpers ────────────────────────────────────
//
// These helpers TRUST their input. Caller must have called
// ValidateHost upstream (at an input boundary or inside a
// config-package function that takes ServerConfig). Helpers
// themselves are pure passthroughs — no validation, no error
// return.

// ServerDirName returns the directory-label for a server host
// under the config dir. Today this is just the host string
// itself (no transformation). Exposed as a named helper so a
// future hash-collision or normalization scheme has one place to
// live; today the function is effectively `return host`.
func ServerDirName(host string) string {
	return host
}

// ServerDataDirForHost returns the per-server data directory:
// `<configDir>/<host>`. Replaces the four inline
// `filepath.Join(configDir, server.Host)` sites that the
// path-centralization audit pass 5 surfaced (main.go:96, tui/
// app.go:371, config.go:168/186/203).
//
// Distinct from the existing `ServerDataDir(configDir, server)`
// in config.go which takes a full ServerConfig — that function
// stays as a temporary wrapper during Phase 1 so existing call
// sites compile; Phase 2 migrates them.
func ServerDataDirForHost(configDir, host string) string {
	return filepath.Join(configDir, ServerDirName(host))
}

// ServerKeysDir returns `<configDir>/<host>/keys/`. Under the
// per-server-folder layout this is where every server's
// `id_ed25519` + `.pub` live. Replaces the seven inline
// `filepath.Join(home, ".sshkey-term", "keys", ...)` sites
// scattered across addserver.go, wizard.go, and keyselector.go.
func ServerKeysDir(configDir, host string) string {
	return filepath.Join(ServerDataDirForHost(configDir, host), "keys")
}

// ServerKeyPath returns `<configDir>/<host>/keys/id_ed25519`,
// the canonical managed-key path under the per-server-folder
// layout. Single-key-per-server policy means the filename is
// fixed.
//
// The .pub sibling lives at `ServerKeyPath(...)+".pub"` —
// historically that's the convention every SSH tooling
// follows and the few places we write keys (Add Server,
// Wizard) already do that join inline.
func ServerKeyPath(configDir, host string) string {
	return filepath.Join(ServerKeysDir(configDir, host), "id_ed25519")
}

// ── In-server sub-paths ─────────────────────────────────────────────
//
// These helpers take dataDir directly to keep the call sites in
// client/hostkey.go and main.go's buildClientLogger pure — they
// already have dataDir constructed once at startup and don't want
// to re-derive it from (configDir, host) on every read.

// KnownHostPath returns `<dataDir>/known_host`, the TOFU host-key
// pin file. Note singular `known_host` (not `known_hosts`) — the
// app stores a single host fingerprint per server, not the OpenSSH
// multi-host known_hosts format.
func KnownHostPath(dataDir string) string {
	return filepath.Join(dataDir, "known_host")
}

// ClientLogPath returns `<dataDir>/client.log`, the per-server
// client log file used by `buildClientLogger` in main.go.
func ClientLogPath(dataDir string) string {
	return filepath.Join(dataDir, "client.log")
}

// FilesDir returns `<dataDir>/files`, the attachment-cache root.
// Files inside are named after their server-assigned `FileID`.
func FilesDir(dataDir string) string {
	return filepath.Join(dataDir, "files")
}

// AttachmentPath returns the full path to a single cached
// attachment file: `<dataDir>/files/<fileID>`. Used by every site
// that reads or writes an individual cached attachment
// (filetransfer.go's downloader/saver, app.go's preview
// invalidator, persist.go's `LocalPath` resolution).
func AttachmentPath(dataDir, fileID string) string {
	return filepath.Join(FilesDir(dataDir), fileID)
}

// ValidFileID reports whether fileID is safe to use as a single path
// component under FilesDir — i.e. turning it into a local path cannot escape
// the attachment cache via traversal. fileID is server/sender-supplied (it
// arrives in the E2E attachment metadata), so EVERY site that turns it into a
// local path must gate on this before touching the filesystem — not just the
// write/delete paths but the read/stat/render paths too (audit F12): a
// traversal-shaped id like "../../etc/passwd" must never be treated as a
// cached local file, or the recipient's own files could be stat'd and decoded
// as images in their own view. Rejects empty / "." / ".." / path separators
// (/ and \) / NUL, and requires the id to equal its own filepath.Base.
func ValidFileID(fileID string) bool {
	if fileID == "" || fileID == "." || fileID == ".." {
		return false
	}
	if strings.ContainsAny(fileID, "\x00/\\") {
		return false
	}
	return filepath.Base(fileID) == fileID
}

// MessagesDBPath returns `<dataDir>/messages.db`, the per-server
// SQLite database that holds local message history, attachments
// metadata, epoch keys, and identity state. Single-site drift
// today (`internal/client/client.go:227` + the corresponding test
// fixture in `internal/testutil/fixtures.go:157`) but centralized
// here so Phase 4's grep gate doesn't need a test-fixture
// exception.
func MessagesDBPath(dataDir string) string {
	return filepath.Join(dataDir, "messages.db")
}
