package client

import (
	"os/exec"
	"runtime"
)

// openInSystemViewer opens a file in the OS default application.
func openInSystemViewer(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "linux":
		return exec.Command("xdg-open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
