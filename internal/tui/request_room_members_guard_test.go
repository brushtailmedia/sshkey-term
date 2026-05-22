package tui

// V8 static guard: exactly ONE production call site of
// Client.RequestRoomMembers may remain — the explicit `r` refresh
// handler. Every automatic UI path (panel open, sidebar movement,
// context switch, mouse selection) must render from the local member
// cache instead of fetching, or the fetch storm regresses.
//
// Modeled on internal/config/path_drift_test.go.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// callRE matches a call expression `.RequestRoomMembers(` but NOT the
// method definition `func (c *Client) RequestRoomMembers(` (no leading dot).
// Keep the leading `\.` — dropping it would match the definition in send.go
// and the test would pass trivially even after a regression.
var requestRoomMembersCallRE = regexp.MustCompile(`\.RequestRoomMembers\s*\(`)

func TestRequestRoomMembers_SingleProductionCallSite(t *testing.T) {
	root := repoRootForGuardTest(t)
	internalDir := filepath.Join(root, "internal")

	type match struct {
		rel  string
		line int
		text string
	}
	var matches []match

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" || name == ".cache" || name == ".gocache" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if requestRoomMembersCallRE.MatchString(line) {
				matches = append(matches, match{rel: rel, line: lineNum, text: strings.TrimSpace(line)})
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}

	if len(matches) != 1 {
		var b strings.Builder
		for _, m := range matches {
			b.WriteString("\n  " + m.rel + ":" + itoa(m.line) + "  " + m.text)
		}
		t.Fatalf("expected exactly 1 production RequestRoomMembers call site, found %d:%s\n\n"+
			"All automatic UI paths must render from the local member cache. Only the explicit "+
			"`r` refresh handler may call RequestRoomMembers.", len(matches), b.String())
	}

	// The single survivor must be the `r` refresh handler in app.go.
	m := matches[0]
	if m.rel != "internal/tui/app.go" {
		t.Fatalf("the surviving RequestRoomMembers call is in %s; expected internal/tui/app.go (the `r` refresh handler)", m.rel)
	}
	assertInsideRoomMembersRefreshCase(t, filepath.Join(root, "internal/tui/app.go"), m.line)
}

// assertInsideRoomMembersRefreshCase checks that the call at callLine is
// preceded (within a small window) by `case "room_members":`, i.e. it lives
// in the RefreshRequestMsg `r` refresh dispatch and not some other path.
func assertInsideRoomMembersRefreshCase(t *testing.T, appPath string, callLine int) {
	t.Helper()
	f, err := os.Open(appPath)
	if err != nil {
		t.Fatalf("open app.go: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNum := 0
	var window []string
	for scanner.Scan() {
		lineNum++
		if lineNum > callLine {
			break
		}
		window = append(window, scanner.Text())
		if len(window) > 20 {
			window = window[1:]
		}
	}
	joined := strings.Join(window, "\n")
	if !strings.Contains(joined, `case "room_members":`) {
		t.Fatalf("the surviving RequestRoomMembers call at app.go:%d is not inside the "+
			"`case \"room_members\":` refresh handler. Window:\n%s", callLine, joined)
	}
}

func repoRootForGuardTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod")
		}
		dir = parent
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
