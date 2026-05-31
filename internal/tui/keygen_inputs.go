package tui

import "github.com/charmbracelet/bubbles/textinput"

// keygenInputWidth returns the visible pan-window width for key-generation
// text inputs. The generated views prepend two spaces before each input and
// render inside dialogStyle's border/padding, so keep a conservative margin
// from the available modal width and let textinput handle horizontal panning.
func keygenInputWidth(availableWidth int) int {
	if availableWidth <= 0 {
		return 0
	}
	w := availableWidth - 12
	if w < 8 {
		return 8
	}
	return w
}

func setKeygenInputWidths(width int, inputs ...*textinput.Model) {
	if width <= 0 {
		return
	}
	for _, input := range inputs {
		if input != nil {
			pos := input.Position()
			input.Width = width
			input.SetCursor(len([]rune(input.Value())))
			input.SetCursor(pos)
		}
	}
}
