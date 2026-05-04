package tui

import "testing"

// TestHelpView_DoesNotPanic captures the regression for the /help
// crash: HelpModel.View must not panic at any reasonable terminal
// size. The original failure was a strings.Repeat with a negative
// count when the column-1 padding arithmetic underflowed (28 -
// visibleWidth(line) went negative because some descriptions exceeded
// the budget). The fix has to clamp the pad count to >= 0.
func TestHelpView_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("HelpModel.View panicked: %v", r)
		}
	}()

	var h HelpModel
	h.SetContext(true)
	h.Toggle() // visible
	_ = h.View(120, 40)
	_ = h.View(80, 24)
	_ = h.View(40, 20)
	_ = h.View(10, 5)
}
