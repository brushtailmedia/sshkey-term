package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTmp_InfoPanelAdminKeysDontPanic(t *testing.T) {
	a := App{}
	a.statusBar = NewStatusBar()
	a.width, a.height = 120, 40
	a.infoPanel.visible = true
	a.infoPanel.group = "group_1"
	a.infoPanel.isGroup = true
	a.infoPanel.members = []memberInfo{{User: "u1", DisplayName: "u1"}}
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'a'}},
		{Type: tea.KeyRunes, Runes: []rune{'K'}},
		{Type: tea.KeyRunes, Runes: []rune{'p'}},
		{Type: tea.KeyRunes, Runes: []rune{'x'}},
	}
	for _, k := range keys {
		m, cmd := a.Update(k)
		if m == nil {
			t.Fatalf("nil model")
		}
		if cmd != nil {
			msg := cmd()
			if msg != nil {
				m2, _ := a.Update(msg)
				if m2 == nil {
					t.Fatalf("nil model after msg")
				}
			}
		}
	}
}
