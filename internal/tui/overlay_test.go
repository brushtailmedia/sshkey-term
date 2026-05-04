package tui

import (
	"strings"
	"testing"
)

// stripANSIForOverlay removes ANSI SGR sequences so visual position can
// be asserted in test expectations. We only need the strip-by-CSI form
// because overlay() itself doesn't emit any non-SGR escapes.
func stripANSIForOverlay(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestOverlay_PlacesDialogAtPosition is the basic contract: a dialog
// drawn at (col, row) must land exactly at that cell of the screen,
// without shifting any of the surrounding lines. This is the property
// that the prior `screen + "\n" + menu.View()` approach violated —
// every dialog appended to the bottom and pushed the layout.
func TestOverlay_PlacesDialogAtPosition(t *testing.T) {
	screen := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
		"dddddddddd",
		"eeeeeeeeee",
	}, "\n")
	dialog := "XX\nYY"

	got := stripANSIForOverlay(overlay(screen, dialog, 3, 1, 10, 5))
	want := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbXXbbbbb",
		"cccYYccccc",
		"dddddddddd",
		"eeeeeeeeee",
	}, "\n")

	if got != want {
		t.Errorf("overlay placement wrong\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestOverlay_ClampsRightOverflow verifies that a dialog whose right
// edge would extend past the terminal width shifts left to fit. Without
// this the user-supplied click coordinate near the right edge would
// produce a partially-clipped menu.
func TestOverlay_ClampsRightOverflow(t *testing.T) {
	screen := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 10)
	dialog := "XXXX\nYYYY"

	// Request col=8 with width=4 in a 10-wide terminal — would overflow
	// to col=12. Should clamp to col=6 (10 - 4).
	got := stripANSIForOverlay(overlay(screen, dialog, 8, 0, 10, 2))
	want := "aaaaaaXXXX\nbbbbbbYYYY"

	if got != want {
		t.Errorf("right-overflow clamp wrong\ngot:  %q\nwant: %q", got, want)
	}
}

// TestOverlay_ClampsBottomOverflow mirrors the right-overflow test for
// the row axis: a dialog requested too low shifts up to keep all rows
// visible.
func TestOverlay_ClampsBottomOverflow(t *testing.T) {
	screen := "aaaa\nbbbb\ncccc\ndddd"
	dialog := "XX\nYY"

	// Request row=3 with height=2 in a 4-tall terminal — would
	// overflow at row=4. Should clamp to row=2 (4 - 2).
	got := stripANSIForOverlay(overlay(screen, dialog, 0, 3, 4, 4))
	want := "aaaa\nbbbb\nXXcc\nYYdd"

	if got != want {
		t.Errorf("bottom-overflow clamp wrong\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestOverlay_PadsShortScreenLine verifies that when the screen line is
// shorter than the dialog's left edge, the gap is filled with spaces
// rather than producing a truncated splice. This matters for terminals
// that emit lines of unequal length (common — trailing trim is normal).
func TestOverlay_PadsShortScreenLine(t *testing.T) {
	screen := "abc\ndef"
	dialog := "XY\nZW"

	// col=5 means we need 2 cells of padding on each line ("abc  XY",
	// "def  ZW").
	got := stripANSIForOverlay(overlay(screen, dialog, 5, 0, 10, 2))
	want := "abc  XY\ndef  ZW"

	if got != want {
		t.Errorf("short-screen-line padding wrong\ngot:  %q\nwant: %q", got, want)
	}
}

// TestOverlay_PadsShortDialogLine verifies that variable-width dialog
// lines get padded to the dialog's max width, so the right-side splice
// of screen content joins on a consistent column boundary.
func TestOverlay_PadsShortDialogLine(t *testing.T) {
	screen := "aaaaaaaaaa\nbbbbbbbbbb"
	dialog := "XXX\nY"

	// Dialog width = 3 (max of "XXX"=3 and "Y"=1). Second line should be
	// padded to "Y  " before splicing, so the result is "bbbY  bbbb".
	got := stripANSIForOverlay(overlay(screen, dialog, 3, 0, 10, 2))
	want := "aaaXXXaaaa\nbbbY  bbbb"

	if got != want {
		t.Errorf("short-dialog-line padding wrong\ngot:  %q\nwant: %q", got, want)
	}
}

// TestOverlay_EmptyDialogReturnsScreen verifies the trivial case: an
// empty dialog is a no-op (the menu's Hide() path produces an empty
// View()).
func TestOverlay_EmptyDialogReturnsScreen(t *testing.T) {
	screen := "abc\ndef"
	got := overlay(screen, "", 1, 1, 10, 10)
	if got != screen {
		t.Errorf("empty dialog should pass-through screen, got %q want %q", got, screen)
	}
}

// TestOverlay_PreservesUntouchedRows checks that rows above and below
// the dialog's span are byte-identical to the original — no surprise
// styling resets or whitespace changes leak into rows we didn't draw.
func TestOverlay_PreservesUntouchedRows(t *testing.T) {
	row0 := "first row content"
	row1 := "second row content"
	row2 := "third row will be touched"
	row3 := "fourth row content"
	screen := strings.Join([]string{row0, row1, row2, row3}, "\n")

	got := overlay(screen, "MENU", 0, 2, 80, 4)
	gotLines := strings.Split(got, "\n")

	if gotLines[0] != row0 {
		t.Errorf("row 0 changed: got %q want %q", gotLines[0], row0)
	}
	if gotLines[1] != row1 {
		t.Errorf("row 1 changed: got %q want %q", gotLines[1], row1)
	}
	if gotLines[3] != row3 {
		t.Errorf("row 3 changed: got %q want %q", gotLines[3], row3)
	}
	if !strings.HasPrefix(stripANSIForOverlay(gotLines[2]), "MENU") {
		t.Errorf("row 2 should start with MENU, got %q", gotLines[2])
	}
}

// TestOverlay_DoesNotShiftLayoutHeight pins the central regression
// guard: prior to the overlay() refactor, the App View built the menu
// via `screen + "\n" + menu` which made the rendered height (line
// count) grow by however many lines the menu had. That shifted the
// terminal output upward and made every dialog appear at the bottom
// regardless of click position. overlay() must produce output with
// the same line count as the input screen.
func TestOverlay_DoesNotShiftLayoutHeight(t *testing.T) {
	screen := strings.Repeat("row\n", 19) + "row" // 20 rows, no trailing newline
	dialog := "X\nY\nZ"

	got := overlay(screen, dialog, 0, 5, 80, 20)
	gotRows := strings.Count(got, "\n") + 1
	if gotRows != 20 {
		t.Errorf("overlay should preserve screen height (20 rows), got %d", gotRows)
	}
}
