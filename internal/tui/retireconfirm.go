package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The confirmation phrase the user must type verbatim to retire their account.
// Uppercase + spaces so it's hard to type by accident.
const retireConfirmPhrase = "RETIRE MY ACCOUNT"

// RetireConfirmModel is a dedicated danger-styled dialog for account retirement.
// Retirement is monotonic and irreversible, so we require typed confirmation
// (not a single keystroke) to prevent accidental invocation.
type RetireConfirmModel struct {
	visible     bool
	reasonIdx   int
	phraseInput textinput.Model
	focused     int // 0 = reason selector, 1 = phrase input
	err         string
}

// RetireReason captures both the protocol value and the human label.
type RetireReason struct {
	Value string // sent to the server in retire_me
	Label string // shown to the user
	Hint  string // brief description shown under the label
}

var retireReasons = []RetireReason{
	{
		Value: "self_compromise",
		Label: "I suspect my key was compromised",
		Hint:  "Someone may have copied or accessed the key file.",
	},
	{
		Value: "switching_key",
		Label: "I'm switching to a new key",
		Hint:  "Moving to a hardware key or starting fresh.",
	},
	{
		Value: "other",
		Label: "Other",
		Hint:  "Some other reason.",
	},
}

// RetireConfirmMsg is emitted when the user completes the retirement flow.
// The app handles this by calling client.SendRetireMe(Reason) and closing.
type RetireConfirmMsg struct {
	Reason string
}

// NewRetireConfirm constructs the retirement confirmation dialog.
func NewRetireConfirm() RetireConfirmModel {
	input := textinput.New()
	input.Placeholder = retireConfirmPhrase
	input.Prompt = ""
	input.CharLimit = len(retireConfirmPhrase)
	return RetireConfirmModel{
		phraseInput: input,
	}
}

func (r *RetireConfirmModel) Show() {
	r.visible = true
	r.reasonIdx = 0
	r.focused = 0
	r.err = ""
	r.phraseInput.SetValue("")
	r.phraseInput.Blur()
}

func (r *RetireConfirmModel) Hide() {
	r.visible = false
	r.phraseInput.Blur()
}

func (r *RetireConfirmModel) IsVisible() bool {
	return r.visible
}

func (r RetireConfirmModel) Update(msg tea.KeyMsg) (RetireConfirmModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		r.Hide()
		return r, nil

	case "tab", "shift+tab":
		// Only 2 focusable fields, so tab and shift+tab both just toggle.
		r.focused = 1 - r.focused
		if r.focused == 1 {
			r.phraseInput.Focus()
		} else {
			r.phraseInput.Blur()
		}
		return r, nil
	}

	// Reason selector navigation (only when focused on it)
	if r.focused == 0 {
		switch msg.String() {
		case "up", "k":
			if r.reasonIdx > 0 {
				r.reasonIdx--
			}
			return r, nil
		case "down", "j":
			if r.reasonIdx < len(retireReasons)-1 {
				r.reasonIdx++
			}
			return r, nil
		case "enter":
			// Move focus to the phrase input
			r.focused = 1
			r.phraseInput.Focus()
			return r, nil
		}
	}

	// Phrase input handling
	if r.focused == 1 {
		if msg.String() == "enter" {
			typed := strings.TrimSpace(r.phraseInput.Value())
			if typed != retireConfirmPhrase {
				r.err = "Please type \"" + retireConfirmPhrase + "\" exactly to confirm."
				return r, nil
			}
			reason := retireReasons[r.reasonIdx].Value
			r.Hide()
			return r, func() tea.Msg {
				return RetireConfirmMsg{Reason: reason}
			}
		}

		var cmd tea.Cmd
		r.phraseInput, cmd = r.phraseInput.Update(msg)
		r.err = ""
		return r, cmd
	}

	return r, nil
}

// HandleMouse lets the user click to select a retirement reason or focus
// the phrase input. Consistent with other dialogs: mouse selects, keyboard
// (typing the phrase + Enter) actually submits.
//
// Layout:
//   Y=0: border top
//   Y=1: padding top
//   Y=2: header " ⚠ Retire Account"
//   Y=3: blank
//   Y=4..9: consequence bullets (6 lines) + blank (Y=10)
//   Y=11: "Reason:" label
//   Y=12..17: 3 reasons × 2 lines (radio + hint) = 6 lines
//   Y=18: blank
//   Y=19: phrase label
//   Y=20: phrase input
func (r RetireConfirmModel) HandleMouse(msg tea.MouseMsg) (RetireConfirmModel, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return r, nil
	}

	// Reason rows: each reason takes 2 lines (radio + hint).
	// First reason radio line is at Y=12.
	const firstReasonY = 12
	if msg.Y >= firstReasonY && msg.Y < firstReasonY+len(retireReasons)*2 {
		idx := (msg.Y - firstReasonY) / 2
		if idx >= 0 && idx < len(retireReasons) {
			r.reasonIdx = idx
			r.focused = 0
			r.phraseInput.Blur()
		}
		return r, nil
	}

	// Clicking on the phrase input area (Y=20) focuses the input.
	const phraseInputY = 20
	if msg.Y == phraseInputY || msg.Y == phraseInputY-1 {
		r.focused = 1
		r.phraseInput.Focus()
		return r, nil
	}

	return r, nil
}

func (r RetireConfirmModel) View(width int) string {
	if !r.visible {
		return ""
	}

	var b strings.Builder

	b.WriteString(errorStyle.Render(" ⚠ Retire Account"))
	b.WriteString("\n\n")
	b.WriteString("  This will " + errorStyle.Render("permanently end your account") + " on this server.\n\n")
	b.WriteString("  After retirement:\n")
	b.WriteString("    • Your key will no longer authenticate\n")
	b.WriteString("    • You will be removed from all rooms and group DMs\n")
	b.WriteString("    • Existing 1:1 DMs become read-only for the other party\n")
	b.WriteString("    • Other members will see your account marked [retired]\n")
	b.WriteString("    • " + errorStyle.Render("This cannot be undone") + " — a new account requires admin approval\n\n")

	// Reason selector
	reasonHeader := "  Reason:"
	if r.focused == 0 {
		reasonHeader = searchHeaderStyle.Render(reasonHeader)
	}
	b.WriteString(reasonHeader + "\n")
	for i, reason := range retireReasons {
		prefix := "    ○ "
		if i == r.reasonIdx {
			prefix = "    ● "
		}
		line := prefix + reason.Label
		if i == r.reasonIdx && r.focused == 0 {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
		if i == r.reasonIdx {
			b.WriteString("      " + helpDescStyle.Render(reason.Hint) + "\n")
		}
	}
	b.WriteString("\n")

	// Confirmation phrase
	phraseHeader := "  Type " + errorStyle.Render(retireConfirmPhrase) + " to confirm:"
	if r.focused == 1 {
		phraseHeader = searchHeaderStyle.Render("  Type ") + errorStyle.Render(retireConfirmPhrase) + searchHeaderStyle.Render(" to confirm:")
	}
	b.WriteString(phraseHeader + "\n")
	b.WriteString("  " + r.phraseInput.View() + "\n")

	if r.err != "" {
		b.WriteString("\n  " + errorStyle.Render(r.err) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  Tab=switch field  ↑/↓=pick reason  Enter=confirm/advance  Esc=cancel"))

	return dialogStyle.Width(width - 4).Render(b.String())
}
