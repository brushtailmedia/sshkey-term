package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// CopyToClipboard copies text to the system clipboard.
//
// Strategy:
//
//  1. OSC 52 first — the only path that works over SSH (the user's
//     local terminal is what receives the escape and writes the
//     clipboard). When running inside tmux, the OSC 52 sequence
//     must be wrapped in tmux's passthrough escape (DCS tmux;...ST)
//     or tmux will consume it and the outer terminal never sees
//     anything. Without the wrapper, what reaches the outer terminal
//     is at best a partial sequence — the symptom users see is
//     "the clipboard got truncated content" because tmux ate the
//     ESC ] header but let the rest through as literal text.
//  2. Platform clipboard tool as a local-only fallback (pbcopy on
//     macOS, xclip/xsel/wl-copy on Linux). Useful when running the
//     binary directly on a desktop with a terminal that doesn't
//     speak OSC 52, but irrelevant over SSH — pbcopy on the SSH
//     server writes to the server's pasteboard, not the user's.
//     We Wait() on the subprocess so it actually finishes draining
//     stdin before we return; the previous Start()-and-forget
//     pattern raced and could leave pbcopy with a partial buffer.
func CopyToClipboard(text string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))

	// Inner OSC 52: ESC ] 52 ; c ; <base64> ST.
	// Use ST (ESC \) rather than BEL — more terminals parse it
	// reliably for long payloads.
	osc52 := fmt.Sprintf("\033]52;c;%s\033\\", encoded)

	if os.Getenv("TMUX") != "" {
		// tmux passthrough: DCS tmux ; ESC <inner-with-doubled-ESC> ST.
		// Each ESC inside the inner sequence must be doubled so tmux's
		// DCS parser doesn't terminate early.
		var wrapped []byte
		wrapped = append(wrapped, '\033', 'P', 't', 'm', 'u', 'x', ';')
		for i := 0; i < len(osc52); i++ {
			if osc52[i] == '\033' {
				wrapped = append(wrapped, '\033', '\033')
			} else {
				wrapped = append(wrapped, osc52[i])
			}
		}
		wrapped = append(wrapped, '\033', '\\')
		fmt.Print(string(wrapped))
	} else {
		fmt.Print(osc52)
	}

	// Local platform fallback. Skipped over SSH (SSH_CONNECTION set)
	// because pbcopy/xclip on the remote host writes to the wrong
	// pasteboard and just wastes a fork.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return
	}

	switch runtime.GOOS {
	case "darwin":
		runClipboardCmd(text, "pbcopy")
	case "linux":
		// Try xclip, then xsel, then wl-copy (Wayland).
		for _, tool := range []string{"xclip", "xsel", "wl-copy"} {
			if path, err := exec.LookPath(tool); err == nil {
				var args []string
				switch tool {
				case "xclip":
					args = []string{"-selection", "clipboard"}
				case "xsel":
					args = []string{"--clipboard", "--input"}
				case "wl-copy":
					args = nil
				}
				runClipboardCmd(text, path, args...)
				break
			}
		}
	}
}

// runClipboardCmd invokes a clipboard tool and waits for it to
// finish reading its stdin. Wait() is critical — Start()-and-return
// races with the subprocess's stdin drain; on small payloads it
// usually wins, on larger ones it leaves the clipboard with whatever
// bytes happened to flush before the goroutine got cancelled.
func runClipboardCmd(text, name string, args ...string) {
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	_, _ = stdin.Write([]byte(text))
	_ = stdin.Close()
	_ = cmd.Wait()
}
