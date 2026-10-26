package tui

// A dialog spliced into a row that carries a kitty image placement must
// not re-emit the placement after itself: ansi.Cut keeps the escapes of the
// cells it removes, so without the strip the row carried the placement
// twice and the terminal drew the image to the right of the popup.

import (
	"strings"
	"testing"
)

func TestOverlay_DoesNotDuplicateAGraphicsPlacement(t *testing.T) {
	placement := "\x1b_Ga=T,f=100,i=7;QUJD\x1b\\"
	row := "sidebar " + placement + " box | pane text here"
	out := overlay(row, "[POPUP]", 14, 0, 60, 1)
	if n := strings.Count(out, "\x1b_Ga=T,"); n != 1 {
		t.Fatalf("placements in the row = %d, want exactly one:\n%q", n, out)
	}
	if strings.Index(out, "\x1b_Ga=T,") > strings.Index(out, "[POPUP]") {
		t.Fatalf("the placement must stay where it was, left of the dialog:\n%q", out)
	}
	// A colour that began left of the dialog still carries to the right.
	styled := "\x1b[31mred " + placement + " text that continues past the dialog"
	out = overlay(styled, "[P]", 6, 0, 60, 1)
	if !strings.HasSuffix(strings.TrimRight(out, " "), "dialog") || !strings.Contains(out[strings.Index(out, "[P]"):], "\x1b[31m") {
		t.Fatalf("the SGR style must still carry over to the right part:\n%q", out)
	}
}

func TestStripGraphicsEscapes(t *testing.T) {
	cases := map[string]string{
		"plain":                                 "plain",
		"a\x1b_Gx\x1b\\b":                       "ab",
		"\x1b_Gx\x1b\\\x1b_Gy\x1b\\c":           "c",
		"keep \x1b[1mbold\x1b[0m \x1b_Gz\x1b\\": "keep \x1b[1mbold\x1b[0m ",
		"cut \x1b_Gunterminated":                "cut ",
	}
	for in, want := range cases {
		if got := stripGraphicsEscapes(in); got != want {
			t.Errorf("stripGraphicsEscapes(%q) = %q, want %q", in, got, want)
		}
	}
}
