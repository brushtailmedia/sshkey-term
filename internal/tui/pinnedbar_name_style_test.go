package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The pinned bar follows the same sender-name rule as the pane and the
// thread view: your own pinned messages show your name green and bold.
func TestPinnedBar_OwnNameIsGreen(t *testing.T) {
	var p PinnedBarModel
	p.SetSelfUserID("usr_self")

	own := p.nameStyleFor("usr_self")
	if got := own.GetForeground(); got != lipgloss.Color("#22C55E") {
		t.Errorf("own name foreground = %v, want #22C55E", got)
	}
	if !own.GetBold() {
		t.Errorf("own name must stay bold")
	}
	if got := p.nameStyleFor("usr_other").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("other name foreground = %v, want none", got)
	}
	// A pin whose FromID is unknown (legacy row) never turns green.
	if got := p.nameStyleFor("").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("empty sender id must never be green: %v", got)
	}
}
