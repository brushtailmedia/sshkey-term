package tui

import (
	"errors"
	"os"
	"strings"

	"github.com/brushtailmedia/sshkey-term/internal/config"
)

// normalizeUploadPath turns what a user — or a terminal drag-and-drop —
// leaves after `/upload` into something os.Stat can use: one pair of
// surrounding quotes stripped ('…' or "…", macOS drag-drop), backslash-
// escaped spaces unescaped (`My\ File.png`, iTerm/Terminal drag-drop), then
// `~` / `~/…` expanded by the shared config helper. Pure: touches no
// filesystem. `~user/…` and `$VAR` are deliberately left alone.
func normalizeUploadPath(raw string) string {
	p := strings.TrimSpace(raw)
	if len(p) >= 2 {
		first, last := p[0], p[len(p)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			p = p[1 : len(p)-1]
		}
	}
	p = strings.ReplaceAll(p, `\ `, " ")
	return config.ExpandUserPath(p)
}

// validateUploadPath stats the (already normalized) path and rejects
// directories. The not-found message keeps the typed route's wording,
// including the remote-session scp hint.
func validateUploadPath(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		msg := "File not found: " + path
		if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
			msg += " (running remotely — copy the file first with scp)"
		}
		return errors.New(msg)
	}
	if fi.IsDir() {
		return errors.New(path + " is a directory")
	}
	return nil
}

// filePickerDefaultRoot is where the file picker opens on its first use:
// home, else the working directory, else "/" (upload-file-picker GATE G2).
func filePickerDefaultRoot() string {
	if home := config.ExpandUserPath("~"); home != "~" {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return string(os.PathSeparator)
}
