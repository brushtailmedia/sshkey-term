package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPathDrift_NoUnmanagedJoins is the Phase 4 grep gate. It walks
// every Go source file in the repo (production code only — skips
// _test.go), greps for forbidden patterns that would re-introduce
// the inline `.sshkey-term` or `os.UserHomeDir` joins the
// path-centralization refactor eliminated, and reports any match
// that isn't on the explicit allowlist.
//
// Forbidden patterns:
//
//   - `filepath.Join(..., ".sshkey-term", ...)` — direct construction
//     of the layout root outside paths.go.
//   - `os.UserHomeDir()` — should go through helpers (DefaultConfigDir,
//     ExpandUserPath) unless one of the explicit Scope—Out exceptions.
//
// Allowlist mirrors §"Scope — Out" in path-centralization.md.
// Adding a new entry is intentional architectural decision; the
// reviewer should check the plan.
//
// Comment lines (`//`-prefixed) are skipped so that doc references
// to `.sshkey-term` (e.g. in a struct field comment) don't trip the
// gate. The gate is about runtime path construction, not prose.
//
// Coverage gap: file content is scanned line-by-line; multi-line
// expressions that span more than one line could in theory hide
// from the regex. Acceptable today — `filepath.Join` calls are
// almost always single-line, and a future cross-line construction
// would still be caught by code review.
func TestPathDrift_NoUnmanagedJoins(t *testing.T) {
	// Patterns to forbid. Each entry is (regex, human-readable
	// description used in failure messages).
	patterns := []struct {
		re   *regexp.Regexp
		desc string
	}{
		{
			re:   regexp.MustCompile(`filepath\.Join\([^)]*"\.sshkey-term"`),
			desc: `filepath.Join with the ".sshkey-term" literal`,
		},
		{
			re:   regexp.MustCompile(`os\.UserHomeDir\(\)`),
			desc: `os.UserHomeDir() call`,
		},
	}

	// Allowlist: file path → set of allowed line numbers. The file
	// path is relative to the module root. Each allowlisted line is
	// documented in §"Scope — Out" with rationale.
	//
	// Use line ranges only when a single conceptual exception spans
	// multiple lines (e.g. a multi-line export-default setup).
	allowed := map[string]map[int]string{
		// paths.go owns the canonical path derivation — every
		// helper here is the one place these primitives should
		// live.
		"internal/config/paths.go": nil, // nil = all lines allowed
		// defaultSaveDir() resolves home for the user's Downloads
		// fallback. Save-destination concern, not managed-path.
		"internal/tui/saveattachment.go": {320: "defaultSaveDir home resolution"},
		// Wizard's Documents/sshkey-backup default. Save destination.
		"internal/tui/wizard.go": {124: "Documents/sshkey-backup export default"},
		// keyselector's ~/.ssh/ scanner input — external read source.
		"internal/tui/keyselector.go": {64: "external ~/.ssh/ scanner input"},
		// Reverse-tilde display normalization (single-site UI helper).
		"internal/tui/addserver.go": {878: "reverse-tilde display normalization"},
	}

	root := repoRootForTest(t)

	var failures []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip vendor/, node_modules/, hidden dirs, build output.
			name := info.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" || name == ".cache" || name == ".gocache" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The grep gate test itself contains the forbidden regex
		// patterns as string literals; skip ourselves.
		if strings.HasSuffix(path, "path_drift_test.go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		// Normalize path separator for cross-platform allowlist keys.
		rel = filepath.ToSlash(rel)

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // large lines OK
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			// Skip comment lines — content there is documentation.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, p := range patterns {
				if !p.re.MatchString(line) {
					continue
				}
				// Match found — check allowlist.
				if allowedLines, fileOK := allowed[rel]; fileOK {
					if allowedLines == nil {
						// nil = entire file is allowed
						continue
					}
					if _, lineOK := allowedLines[lineNum]; lineOK {
						continue
					}
				}
				failures = append(failures, formatDriftFailure(rel, lineNum, p.desc, line))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}

	if len(failures) > 0 {
		t.Errorf("path drift detected (%d unallowlisted matches):\n\n%s\n\nIf an addition is intentional, update the allowlist in this file AND §\"Scope — Out\" in path-centralization.md.",
			len(failures), strings.Join(failures, "\n"))
	}
}

func formatDriftFailure(file string, line int, desc, content string) string {
	return "  " + file + ":" + itoa(line) + " — " + desc + "\n        " + strings.TrimSpace(content)
}

// itoa is a tiny strconv.Itoa replacement to avoid the import here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// repoRootForTest walks up from the current working directory until
// it finds the go.mod file (or fails the test). Test runner sets
// CWD to the package dir, so the module root is some number of
// parents up.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod")
		}
		dir = parent
	}
}
