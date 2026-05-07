package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// StatusPickerModel is the modal that opens on bare `/setstatus` and
// lets the user select Available / Away / Busy via arrow keys instead
// of typing the status name. Less error-prone than the slash-arg
// path (no typos, no need to remember the exact strings) and
// discoverable for new users — the rendered options each show their
// colored dot so the user previews what they'll look like to others.
//
// Locked to the same set the slash command accepts: available, away,
// busy. "Offline" is intentionally NOT a picker option — it's a
// connection-derived state, not a user choice (see /setstatus
// handler comment in app.go for the full rationale).
type StatusPickerModel struct {
	visible bool
	cursor  int
	items   []statusPickerItem
}

type statusPickerItem struct {
	dot    string // colored ● glyph
	label  string // human-readable name
	status string // wire value: StatusAvailable | StatusAway | StatusBusy
}

// StatusSelectMsg is emitted when the user picks a status from the
// picker. App handles it identically to a typed `/setstatus <name>` —
// sends SetStatus to the server + optimistic local sidebar update.
type StatusSelectMsg struct {
	Status string
}

func NewStatusPicker() StatusPickerModel {
	return StatusPickerModel{}
}

// Show opens the picker. current is the user's current status (used
// to position the cursor on the matching row so opening + Enter is
// a no-op rather than a silent reset to Available). Empty current
// means cursor lands on Available — the default for a fresh user.
func (m *StatusPickerModel) Show(current string) {
	m.visible = true
	m.items = []statusPickerItem{
		{dot: onlineDot, label: "Available", status: StatusAvailable},
		{dot: awayDot, label: "Away", status: StatusAway},
		{dot: busyDot, label: "Busy", status: StatusBusy},
	}
	m.cursor = 0
	for i, it := range m.items {
		if it.status == current {
			m.cursor = i
			break
		}
	}
}

func (m *StatusPickerModel) Hide() {
	m.visible = false
}

func (m *StatusPickerModel) IsVisible() bool {
	return m.visible
}

func (m StatusPickerModel) Update(msg tea.KeyMsg) (StatusPickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.Hide()
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < len(m.items) {
			status := m.items[m.cursor].status
			m.Hide()
			return m, func() tea.Msg {
				return StatusSelectMsg{Status: status}
			}
		}
	}
	return m, nil
}

// View renders the picker as a compact auto-sized dialog matching
// the contextMenu / memberMenu visual style — small dropdown with
// the colored dot + label per row, cursor highlighted via the
// selected-style background. Footer with key hints since the
// picker is invoked via slash command (no preceding click that
// would have telegraphed the action) — first-time users benefit
// from explicit guidance.
//
// Returned without a Width() call so dialogStyle auto-sizes to
// content; caller overlays the result onto the screen at a chosen
// anchor (typically just above the input bar).
func (m StatusPickerModel) View() string {
	if !m.visible {
		return ""
	}

	var b strings.Builder
	for i, item := range m.items {
		line := "  " + item.dot + " " + item.label + "  "
		if i == m.cursor {
			line = completionSelectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	// Helper text split across three lines so the dialog stays
	// narrow — the inline single-line form ("↑/↓=navigate
	// Enter=select Esc=cancel") was the widest content row and
	// pushed the dialog visibly wider than its actual options.
	b.WriteString("\n")
	b.WriteString(helpDescStyle.Render("  ↑/↓=navigate") + "\n")
	b.WriteString(helpDescStyle.Render("  Enter=select") + "\n")
	b.WriteString(helpDescStyle.Render("  Esc=cancel"))

	return dialogStyle.Render(b.String())
}
