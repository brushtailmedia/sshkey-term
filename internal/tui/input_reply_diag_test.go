package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInputReplyAfterSend_NoExtraBox simulates the "two input boxes
// after reply + send" scenario to capture what input.View actually
// renders. The user reports a transient stale-render where, between
// pressing Enter (send) and the next event, two bordered boxes appear
// stacked. This test makes the symptom visible by inspecting the
// rendered string row count + border presence.
func TestInputReplyAfterSend_NoExtraBox(t *testing.T) {
	i := NewInput()
	i.SetReply("msg_42", "alice: hello there")

	// Type some reply text and press enter. nil client = no actual
	// network send; the state-clearing path still runs (textinput
	// reset, clearReply, etc).
	for _, r := range "thanks" {
		i, _ = i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, nil, "general", "", "")
	}

	// Snapshot view BEFORE enter — should have reply preview INSIDE
	// the bordered box.
	beforeView := i.View(80, true)
	beforeRows := strings.Split(beforeView, "\n")

	// Press Enter — clears reply, resets textinput.
	i, _ = i.Update(tea.KeyMsg{Type: tea.KeyEnter}, nil, "general", "", "")

	afterView := i.View(80, true)
	afterRows := strings.Split(afterView, "\n")

	t.Logf("BEFORE ENTER: %d rows", len(beforeRows))
	for idx, r := range beforeRows {
		t.Logf("  [%d] %q", idx, stripANSIForOverlay(r))
	}
	t.Logf("AFTER ENTER: %d rows", len(afterRows))
	for idx, r := range afterRows {
		t.Logf("  [%d] %q", idx, stripANSIForOverlay(r))
	}

	// Verify reply state is actually cleared.
	if i.replyTo != "" {
		t.Errorf("replyTo not cleared after send: %q", i.replyTo)
	}
	if i.replyText != "" {
		t.Errorf("replyText not cleared after send: %q", i.replyText)
	}

	// After-view should be exactly 3 rows (top border + content + bottom
	// border). If there's an extra "empty box" appearing, we'd see 5+
	// rows here.
	if len(afterRows) > 3 {
		t.Errorf("input.View after send rendered %d rows, want 3 — possible stale-render bug", len(afterRows))
	}

	// Each row should contain at most ONE rounded-corner top-left "╭"
	// or bottom-left "╰" — finding two would indicate two bordered
	// boxes in the output.
	corners := strings.Count(stripANSIForOverlay(afterView), "╭")
	if corners > 1 {
		t.Errorf("after send, found %d top-left corners ╭ — two-boxes regression", corners)
	}
}
