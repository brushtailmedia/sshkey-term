package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The thread view uses the same sender-name rule as the pane: your own
// replies render green and bold, everyone else plain bold. Colors are
// stripped under test, so assert on the chosen style.
func TestThreadPanel_OwnNameIsGreen(t *testing.T) {
	var p ThreadPanelModel
	p.SetSelfUserID("usr_self")

	if got := p.nameStyleFor("usr_self").GetForeground(); got != lipgloss.Color("#22C55E") {
		t.Errorf("own name foreground = %v, want #22C55E", got)
	}
	if got := p.nameStyleFor("usr_other").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("other name foreground = %v, want none", got)
	}
	if got := p.nameStyleFor("").GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("empty sender id must never be green: %v", got)
	}
}

// Retired senders carry the pane's "[retired]" marker in the thread view,
// and only they do.
func TestThreadPanel_RetiredMarkerParity(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Me", FromID: "usr_self", Body: "root body", TS: 1000}
	reply := DisplayMessage{ID: "reply", From: "Bob", FromID: "usr_bob", Body: "a reply", TS: 1001, ReplyTo: "root"}

	var p ThreadPanelModel
	p.SetSelfUserID("usr_self")
	p.SetRetired(map[string]bool{"usr_bob": true})
	p.Show("root", []DisplayMessage{root, reply})

	view := p.View(80, 30)
	var bobLine, meLine string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "Bob") {
			bobLine = l
		}
		if strings.Contains(l, "Me") && strings.Contains(l, "AM") || strings.Contains(l, "Me") && strings.Contains(l, "PM") {
			meLine = l
		}
	}
	if !strings.Contains(bobLine, "[retired]") {
		t.Errorf("retired sender line lacks the marker: %q\n%s", bobLine, view)
	}
	if strings.Contains(meLine, "[retired]") {
		t.Errorf("non-retired sender line carries the marker: %q", meLine)
	}
	if strings.Count(view, "[retired]") != 1 {
		t.Errorf("want exactly one marker, got %d:\n%s", strings.Count(view, "[retired]"), view)
	}
}

// A shared retired map stays live: a user retired after the panel opened is
// marked on the next render without re-setting the map.
func TestThreadPanel_RetiredMapIsShared(t *testing.T) {
	root := DisplayMessage{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root body", TS: 1000}
	retired := map[string]bool{}

	var p ThreadPanelModel
	p.SetRetired(retired)
	p.Show("root", []DisplayMessage{root})
	if strings.Contains(p.View(80, 30), "[retired]") {
		t.Fatalf("nobody is retired yet")
	}
	retired["usr_alice"] = true // the pane's MarkRetired mutates in place
	if !strings.Contains(p.View(80, 30), "[retired]") {
		t.Errorf("marker must follow the shared map without a new SetRetired")
	}
}
