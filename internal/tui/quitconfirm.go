package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// QuitConfirmModel shows a quit confirmation dialog.
type QuitConfirmModel struct {
	visible    bool
	serverName string
}

func (q *QuitConfirmModel) Show(serverName string) {
	q.visible = true
	q.serverName = serverName
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
	b.WriteString("  Disconnect from " + q.serverName + "?\n\n")
	b.WriteString("  [y] Quit  [n] Cancel\n")

	return dialogStyle.Width(width - 4).Render(b.String())
}
