package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// assertNoHoles fails if, on any line, visible text follows an SGR reset
// without the band sequence being re-armed first — i.e. if the terminal's
// own background could show through inside a selected row.
func assertNoHoles(t *testing.T, rendered, seq string) {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		pieces := strings.Split(line, sgrReset)
		for i, p := range pieces[1:] {
			if p == "" {
				continue // reset at end of line
			}
			if !strings.HasPrefix(p, seq) {
				t.Errorf("hole after reset %d: band not re-armed before %q\nline: %q", i+1, p, line)
			}
		}
		if line != "" && !strings.HasSuffix(line, sgrReset) {
			t.Errorf("line does not end with a reset (band could leak): %q", line)
		}
	}
}

func withTrueColor(t *testing.T, fn func()) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	fn()
}

// The band survives every nested segment: timestamp, bold chip, gray names.
func TestRenderSelected_ReArmsBandAfterInnerResets(t *testing.T) {
	withTrueColor(t, func() {
		seq := styleSequence(selectedMsgStyle)
		if seq == "" {
			t.Fatalf("expected a background sequence under TrueColor")
		}
		content := " " + usernameStyle.Render("chicks") + "  " + timestampStyle.Render("5:17 PM") +
			"\n" + " body" +
			"\n   " + reactionStyle.Bold(true).Render("👍 1") + " " + timestampStyle.Render("you") + timestampStyle.Render("  t=thread")

		plain := selectedMsgStyle.Width(40).Render(content)
		// Prove the plain lipgloss render has the hole this fixes.
		hole := false
		for _, line := range strings.Split(plain, "\n") {
			for _, p := range strings.Split(line, sgrReset)[1:] {
				if p != "" && !strings.HasPrefix(p, seq) {
					hole = true
				}
			}
		}
		if !hole {
			t.Fatalf("plain render unexpectedly had no hole; this test's premise changed")
		}

		assertNoHoles(t, renderSelected(selectedMsgStyle, 40, content), seq)
	})
}

// A band with a foreground too (the pinned bar) re-arms both.
func TestRenderSelected_WidthZeroReArmsForegroundAndBackground(t *testing.T) {
	withTrueColor(t, func() {
		seq := styleSequence(pinnedSelectedStyle)
		if seq == "" || !strings.Contains(seq, "48;") || !strings.Contains(seq, "38;") {
			t.Fatalf("pinned band sequence should carry fg and bg: %q", seq)
		}
		content := " 📌 " + usernameStyle.Render("chicks") + ": pinned body"
		out := renderSelected(pinnedSelectedStyle, 0, content)
		assertNoHoles(t, out, seq)
		if strings.Contains(out, "\n") {
			t.Errorf("width 0 must not wrap: %q", out)
		}
	})
}

// With no color sequences (the default under test) the helper is exactly
// lipgloss's own render.
func TestRenderSelected_PlainUnderAscii(t *testing.T) {
	content := " " + usernameStyle.Render("chicks") + "  " + timestampStyle.Render("5:17 PM")
	if got, want := renderSelected(selectedMsgStyle, 40, content), selectedMsgStyle.Width(40).Render(content); got != want {
		t.Errorf("ascii render differs:\n got %q\nwant %q", got, want)
	}
	if styleSequence(selectedMsgStyle) != "" {
		t.Errorf("no sequence expected under the Ascii profile")
	}
}

// Styles whose prefix is not pure SGR (padding) are left alone rather than
// re-armed with spaces.
func TestStyleSequence_RejectsNonSGRPrefix(t *testing.T) {
	withTrueColor(t, func() {
		padded := lipgloss.NewStyle().Background(lipgloss.Color("#1E1E2E")).PaddingLeft(2)
		if seq := styleSequence(padded); seq != "" {
			t.Errorf("padded style must yield no sequence, got %q", seq)
		}
		if !isSGROnly("\x1b[48;2;30;30;46m") || !isSGROnly("\x1b[1m\x1b[38;5;12m") {
			t.Errorf("valid SGR prefixes rejected")
		}
		if isSGROnly("  \x1b[48;2;30;30;46m") || isSGROnly("\x1b[48;2;30;30;46m ") || isSGROnly("\x1b]0;title\x07") {
			t.Errorf("non-SGR prefixes accepted")
		}
	})
}

// End to end: the pane's selected row with reactions and a hint, and the
// thread view's selected row, carry no holes.
func TestSelectedRows_NoHolesInPaneAndThread(t *testing.T) {
	withTrueColor(t, func() {
		seq := styleSequence(selectedMsgStyle)
		root := DisplayMessage{ID: "root", From: "chicks", FromID: "usr_self", Body: "t", TS: 1000,
			ReactionsByUser: map[string]map[string][]string{"usr_self": {"👍": {"r1"}}}}
		reply := DisplayMessage{ID: "reply", From: "chicks", FromID: "usr_self", Body: "y", TS: 1001, ReplyTo: "root"}

		m := NewMessages()
		m.SetSelfUserID("usr_self")
		m.messages = []DisplayMessage{root, reply}
		for cursor := 0; cursor < 2; cursor++ {
			m.cursor = cursor
			content, _ := m.buildContent(40)
			for _, line := range strings.Split(content, "\n") {
				if strings.HasPrefix(line, seq) { // selected rows start with the band
					assertNoHoles(t, line, seq)
				}
			}
		}

		var p ThreadPanelModel
		p.SetSelfUserID("usr_self")
		p.Show("root", []DisplayMessage{root, reply})
		// The panel wraps each row in its own padding and border, which sit
		// outside the band by design. Check only the row: from the first band
		// sequence to the closing reset renderSelected appends (the pattern
		// reset+band+reset).
		rowEnd := sgrReset + seq + sgrReset
		for _, line := range strings.Split(p.View(40, 12), "\n") {
			start := strings.Index(line, seq)
			end := strings.LastIndex(line, rowEnd)
			if start < 0 || end < start {
				continue
			}
			assertNoHoles(t, line[start:end+len(rowEnd)], seq)
		}
	})
}
