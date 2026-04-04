package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// PassphraseModel shows a TUI dialog for entering a key passphrase.
type PassphraseModel struct {
	visible bool
	input   textinput.Model
	err     string
}

// PassphraseResultMsg is sent when the user enters a passphrase.
type PassphraseResultMsg struct {
	Passphrase []byte
	Cancelled  bool
}

func NewPassphrase() PassphraseModel {
	ti := textinput.New()
	ti.Placeholder = "passphrase"
	ti.EchoMode = textinput.EchoPassword
	ti.Prompt = ""
	ti.CharLimit = 256
	return PassphraseModel{input: ti}
}

func (p *PassphraseModel) Show(errMsg string) {
	p.visible = true
	p.err = errMsg
	p.input.SetValue("")
	p.input.Focus()
}

func (p *PassphraseModel) Hide() {
	p.visible = false
	p.input.Blur()
}

func (p *PassphraseModel) IsVisible() bool {
	return p.visible
}

func (p PassphraseModel) Update(msg tea.KeyMsg) (PassphraseModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		pass := p.input.Value()
		p.Hide()
		return p, func() tea.Msg {
			return PassphraseResultMsg{Passphrase: []byte(pass)}
		}
	case "esc":
		p.Hide()
		return p, func() tea.Msg {
			return PassphraseResultMsg{Cancelled: true}
		}
	}

	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p PassphraseModel) View(width int) string {
	if !p.visible {
		return ""
	}

	var b strings.Builder
	b.WriteString(searchHeaderStyle.Render(" SSH Key Passphrase"))
	b.WriteString("\n\n")
	b.WriteString("  Your key is passphrase-protected.\n\n")
	b.WriteString("  Passphrase:\n")
	b.WriteString("  " + p.input.View() + "\n\n")
	b.WriteString(helpDescStyle.Render("  Enter=unlock  Esc=cancel"))

	if p.err != "" {
		b.WriteString("\n\n  " + errorStyle.Render(p.err))
	}

	return dialogStyle.Width(width - 4).Render(b.String())
}
