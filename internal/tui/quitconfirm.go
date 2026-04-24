package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// QuitConfirmModel shows a quit confirmation dialog.
type QuitConfirmModel struct {
	visible     bool
	serverName  string
	pendingSend int // Phase 17c Step 5: unacked in-flight sends
}

func (q *QuitConfirmModel) Show(serverName string) {
	q.visible = true
	q.serverName = serverName
}

// ShowWithPending is Show + a pending-send count. When pendingSend > 0
// the dialog additionally warns about unflushed messages.
func (q *QuitConfirmModel) ShowWithPending(serverName string, pendingSend int) {
	q.visible = true
	q.serverName = serverName
	q.pendingSend = pendingSend
}

func (q *QuitConfirmModel) Hide() {
	q.visible = false
}

func (q *QuitConfirmModel) IsVisible() bool {
	return q.visible
}

func (q QuitConfirmModel) Update(msg tea.KeyMsg) (QuitConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		q.Hide()
		return q, tea.Quit
	case "n", "esc":
		q.Hide()
		return q, nil
	}
	return q, nil
}

func (q QuitConfirmModel) View(width int) string {
	if !q.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Quit?"))
	b.WriteString("\n\n")
	b.WriteString("  Disconnect from " + q.serverName + "?\n")
	// Phase 17c Step 5: warn about unflushed sends if any. User can
	// still choose [y] to abandon them; message body is lost.
	if q.pendingSend > 0 {
		var noun string
		if q.pendingSend == 1 {
			noun = "message"
		} else {
			noun = "messages"
		}
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("  ⚠ " + fmtInt(q.pendingSend) + " " + noun + " still sending — quit will lose them."))
		b.WriteString("\n")
	}
	b.WriteString("\n  [y] Quit  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}

// fmtInt is a tiny helper — strconv.Itoa without importing strconv.
// Used once by quitconfirm for the pending-send count.
func fmtInt(n int) string {
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
