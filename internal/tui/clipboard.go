package tui

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
)

// CopyToClipboard copies text to the system clipboard.
// Tries OSC 52 first (works over SSH), falls back to platform tools.
func CopyToClipboard(text string) {
	// OSC 52: universal clipboard escape sequence
	// Works in kitty, iTerm2, WezTerm, foot, and most modern terminals.
	// Also works over SSH — the terminal on the local machine handles it.
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	fmt.Printf("\033]52;c;%s\a", encoded)

	// Also try platform clipboard as fallback (only works locally, not over SSH)
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = stringReader(text)
		cmd.Start()
	case "linux":
		// Try xclip, then xsel, then wl-copy (Wayland)
		for _, tool := range []string{"xclip", "xsel", "wl-copy"} {
			if path, err := exec.LookPath(tool); err == nil {
				var cmd *exec.Cmd
				switch tool {
				case "xclip":
					cmd = exec.Command(path, "-selection", "clipboard")
				case "xsel":
					cmd = exec.Command(path, "--clipboard", "--input")
				case "wl-copy":
					cmd = exec.Command(path)
				}
				if cmd != nil {
					cmd.Stdin = stringReader(text)
					cmd.Start()
				}
				break
			}
		}
	}
}

type stringReaderType struct {
	s string
	i int
}

func stringReader(s string) *stringReaderType {
	return &stringReaderType{s: s}
}

func (r *stringReaderType) Read(p []byte) (n int, err error) {
	if r.i >= len(r.s) {
		return 0, fmt.Errorf("EOF")
	}
	n = copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
