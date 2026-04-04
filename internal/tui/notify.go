package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// SendDesktopNotification sends an OS-level notification.
// On macOS: osascript. On Linux: notify-send.
func SendDesktopNotification(title, body string) {
	// Truncate body
	if len(body) > 100 {
		body = body[:97] + "..."
	}

	// Escape special characters
	title = strings.ReplaceAll(title, `"`, `\"`)
	body = strings.ReplaceAll(body, `"`, `\"`)

	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, body, title)
		exec.Command("osascript", "-e", script).Start()
	case "linux":
		exec.Command("notify-send", "-a", "sshkey-chat", title, body).Start()
	}
	// Windows: not supported yet, fail silently
}
