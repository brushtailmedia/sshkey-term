package client

import (
	"os/exec"
	"runtime"
)

// OpenURL opens a URL in the OS default browser: the same launchers as a
// file, except on Windows, where `start` would read the URL as a window
// title and choke on "&"; url.dll's handler takes the URL verbatim.
func OpenURL(url string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	return openInSystemViewer(url)
}

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
