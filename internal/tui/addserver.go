package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// AddServerModel manages the add server dialog.
type AddServerModel struct {
	visible  bool
	inputs   []textinput.Model
	focused  int // 0=name, 1=host, 2=port, 3=key
	labels   []string
}

// AddServerMsg is sent when the user confirms adding a server.
type AddServerMsg struct {
	Name string
	Host string
	Port int
	Key  string
}

func NewAddServer() AddServerModel {
	labels := []string{"Name", "Host", "Port", "SSH key path"}

	inputs := make([]textinput.Model, 4)
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = ""
		inputs[i].CharLimit = 256
	}

	inputs[0].Placeholder = "My Server"
	inputs[1].Placeholder = "chat.example.com"
	inputs[2].Placeholder = "2222"
	inputs[2].SetValue("2222")
	inputs[3].Placeholder = "~/.ssh/id_ed25519"

	return AddServerModel{
		inputs: inputs,
		labels: labels,
	}
}

func (a *AddServerModel) Show() {
	a.visible = true
	a.focused = 0
	for i := range a.inputs {
		if i == 2 {
			a.inputs[i].SetValue("2222")
		} else {
			a.inputs[i].SetValue("")
		}
	}
	a.inputs[0].Focus()
}

func (a *AddServerModel) Hide() {
	a.visible = false
	for i := range a.inputs {
		a.inputs[i].Blur()
	}
}

func (a *AddServerModel) IsVisible() bool {
	return a.visible
}

func (a AddServerModel) Update(msg tea.KeyMsg) (AddServerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.Hide()
		return a, nil

	case "tab", "down":
		a.inputs[a.focused].Blur()
		a.focused = (a.focused + 1) % len(a.inputs)
		a.inputs[a.focused].Focus()
		return a, nil

	case "shift+tab", "up":
		a.inputs[a.focused].Blur()
		a.focused--
		if a.focused < 0 {
			a.focused = len(a.inputs) - 1
		}
		a.inputs[a.focused].Focus()
		return a, nil

	case "enter", "ctrl+enter":
		// Validate and submit
		name := strings.TrimSpace(a.inputs[0].Value())
		host := strings.TrimSpace(a.inputs[1].Value())
		portStr := strings.TrimSpace(a.inputs[2].Value())
		key := strings.TrimSpace(a.inputs[3].Value())

		if host == "" {
			return a, nil
		}
		if name == "" {
			name = host
		}

		port := 2222
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}

		if key == "" {
			key = "~/.ssh/id_ed25519"
		}

		a.Hide()
		return a, func() tea.Msg {
			return AddServerMsg{
				Name: name,
				Host: host,
				Port: port,
				Key:  key,
			}
		}
	}

	var cmd tea.Cmd
	a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
	return a, cmd
}

func (a AddServerModel) View(width int) string {
	if !a.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" Add Server"))
	b.WriteString("\n\n")

	for i, label := range a.labels {
		b.WriteString("  " + label + ": ")
		b.WriteString(a.inputs[i].View())
		b.WriteString("\n\n")
	}

	b.WriteString(helpDescStyle.Render("  Tab=next field  Enter=add  Esc=cancel"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
