package tui

import "fmt"

// SetTerminalTitle sets the terminal window/tab title via escape sequence.
// Works in most terminals including over SSH.
func SetTerminalTitle(title string) {
	fmt.Printf("\033]0;%s\007", title)
}

// UpdateTitle sets the terminal title with optional unread count.
func UpdateTitle(serverName string, totalUnread int) {
	if totalUnread > 0 {
		SetTerminalTitle(fmt.Sprintf("sshkey-chat (%d) — %s", totalUnread, serverName))
	} else {
		SetTerminalTitle(fmt.Sprintf("sshkey-chat — %s", serverName))
	}
}
