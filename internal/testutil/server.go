package testutil

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// StartTestServer builds and starts sshkey-server with test config.
// Returns the port and a cleanup function. Uses the calling module's
// testdata/config/ for static configs (rooms.toml, server.toml) and
// generates users.toml from the test fixtures.
func StartTestServer(t testing.TB) (port int, cleanup func()) {
	t.Helper()
	EnsureFixtures(t)

	// Find project paths
	serverDir := findServerDir()
	testdataDir := findTestdataDir()
	dataDir := t.TempDir()

	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Write config override
	overrideConfig := filepath.Join(dataDir, "config")
	os.MkdirAll(overrideConfig, 0755)

	os.WriteFile(filepath.Join(overrideConfig, "users.toml"), []byte(UsersToml()), 0644)

	roomsData, _ := os.ReadFile(filepath.Join(testdataDir, "rooms.toml"))
	os.WriteFile(filepath.Join(overrideConfig, "rooms.toml"), roomsData, 0644)

	serverToml, _ := os.ReadFile(filepath.Join(testdataDir, "server.toml"))
	overridden := strings.Replace(string(serverToml), "port = 2222", fmt.Sprintf("port = %d", port), 1)
	overridden = strings.Replace(overridden, `bind = "0.0.0.0"`, `bind = "127.0.0.1"`, 1)
	// Replace admin placeholder with the real generated nanoid
	overridden = strings.Replace(overridden, `admins = ["usr_alice_test"]`, fmt.Sprintf(`admins = ["%s"]`, AdminUsername()), 1)
	os.WriteFile(filepath.Join(overrideConfig, "server.toml"), []byte(overridden), 0644)

	// Build and run server
	serverBin := filepath.Join(dataDir, "sshkey-server")
	build := exec.Command("go", "build", "-o", serverBin, "./cmd/sshkey-server")
	build.Dir = serverDir
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}

	cmd := exec.Command(serverBin, "-config", overrideConfig, "-data", dataDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	return port, func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// findServerDir locates the sshkey-chat directory relative to the test binary.
func findServerDir() string {
	// Try relative paths from common test locations
	for _, rel := range []string{
		"../sshkey-chat",
		"../../sshkey-chat",
		"../../../sshkey-chat",
	} {
		if info, err := os.Stat(filepath.Join(rel, "cmd", "sshkey-server")); err == nil && info.IsDir() {
			abs, _ := filepath.Abs(rel)
			return abs
		}
	}
	// Fallback: assume workspace layout
	return filepath.Join("..", "sshkey-chat")
}

// findTestdataDir locates the testdata/config directory.
func findTestdataDir() string {
	// Try sshkey-term's own testdata first
	for _, rel := range []string{
		"testdata/config",
		"../testdata/config",
		"../../testdata/config",
	} {
		if _, err := os.Stat(filepath.Join(rel, "server.toml")); err == nil {
			abs, _ := filepath.Abs(rel)
			return abs
		}
	}
	// Fallback: sshkey-chat's testdata
	return filepath.Join(findServerDir(), "testdata", "config")
}
