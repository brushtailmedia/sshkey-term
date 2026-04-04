package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// EmojiPickerModel manages the emoji reaction picker overlay.
type EmojiPickerModel struct {
	visible   bool
	input     textinput.Model
	results   []emojiEntry
	cursor    int
	targetMsg DisplayMessage // message being reacted to
}

// EmojiSelectedMsg is sent when the user picks an emoji.
type EmojiSelectedMsg struct {
	Emoji  string
	Target DisplayMessage
}

func NewEmojiPicker() EmojiPickerModel {
	ti := textinput.New()
	ti.Placeholder = "type to search..."
	ti.Prompt = ""
	ti.CharLimit = 20
	return EmojiPickerModel{input: ti}
}

func (e *EmojiPickerModel) Show(target DisplayMessage) {
	e.visible = true
	e.targetMsg = target
	e.input.SetValue("")
	e.input.Focus()
	e.cursor = 0
	e.results = SearchEmoji("", 40)
}

func (e *EmojiPickerModel) Hide() {
	e.visible = false
	e.input.Blur()
}

func (e *EmojiPickerModel) IsVisible() bool {
	return e.visible
}

func (e EmojiPickerModel) Update(msg tea.KeyMsg) (EmojiPickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		e.Hide()
		return e, nil

	case "enter":
		if e.cursor < len(e.results) {
			emoji := e.results[e.cursor].Emoji
			target := e.targetMsg
			e.Hide()
			return e, func() tea.Msg {
				return EmojiSelectedMsg{Emoji: emoji, Target: target}
			}
		}
		return e, nil

	case "1", "2", "3", "4", "5", "6", "7", "8":
		// Quick select from top row
		if e.input.Value() == "" {
			idx := int(msg.String()[0]-'0') - 1
			quick := QuickReactions()
			if idx < len(quick) {
				emoji := quick[idx]
				target := e.targetMsg
				e.Hide()
				return e, func() tea.Msg {
					return EmojiSelectedMsg{Emoji: emoji, Target: target}
				}
			}
		}

	case "up":
		cols := 8
		if e.cursor >= cols {
			e.cursor -= cols
		}
		return e, nil

	case "down":
		cols := 8
		if e.cursor+cols < len(e.results) {
			e.cursor += cols
		}
		return e, nil

	case "left":
		if e.cursor > 0 {
			e.cursor--
		}
		return e, nil

	case "right":
		if e.cursor < len(e.results)-1 {
			e.cursor++
		}
		return e, nil
	}

	// Update search input
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)

	// Update results on search change
	query := e.input.Value()
	e.results = SearchEmoji(query, 40)
	if e.cursor >= len(e.results) {
		e.cursor = 0
	}

	return e, cmd
}

func (e EmojiPickerModel) View() string {
	if !e.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(searchHeaderStyle.Render(" React"))
	b.WriteString("\n\n")

	// Quick reactions row
	quick := QuickReactions()
	b.WriteString(" ")
	for i, emoji := range quick {
		s := " " + emoji + " "
		if e.input.Value() == "" && i == e.cursor {
			s = completionSelectedStyle.Render(s)
		}
		b.WriteString(s)
	}
	b.WriteString("\n\n")

	// Search input
	b.WriteString(" " + e.input.View())
	b.WriteString("\n\n")

	// Search results grid (8 columns)
	if e.input.Value() != "" && len(e.results) > 0 {
		cols := 8
		for i, entry := range e.results {
			if i > 0 && i%cols == 0 {
				b.WriteString("\n")
			}
			s := " " + entry.Emoji + " "
			if i == e.cursor {
				s = completionSelectedStyle.Render(s)
			}
			b.WriteString(s)
		}
		b.WriteString("\n")

		// Show name of currently selected emoji
		if e.cursor < len(e.results) {
			b.WriteString("\n " + helpDescStyle.Render(e.results[e.cursor].Name))
		}
	}

	b.WriteString("\n\n")
	b.WriteString(helpDescStyle.Render(" 1-8=quick  ←→↑↓=navigate  Enter=select  Esc=cancel"))

	return dialogStyle.Render(b.String())
}

// emojiPickerOverlay is used for rendering - wraps the picker in a positioned overlay.
func emojiPickerOverlay(picker EmojiPickerModel) string {
	return picker.View()
}
