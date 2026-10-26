package tui

// Quit always works, and cancel returns you where you were
// (quit-always-works.md): Ctrl+Q opens the dialog over any overlay, drawn
// on the final frame; n, Esc and q put everything back; y quits with
// Ctrl+C's cleanup; a held key only ever shows the dialog; clicks do not
// reach what is under the dialog; the pre-session frames, the bouncer and
// the kitty image layer behave.

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func ctrlQ() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlQ} }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestQuit_WorksOverEveryOverlayAndCancelRestoresIt(t *testing.T) {
	for _, op := range overlayOpeners {
		if op.field == "quitConfirm" {
			continue // the dialog itself
		}
		t.Run(op.field, func(t *testing.T) {
			a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages))
			op.open(t, &a)
			focus, cursor := a.focus, a.messages.cursor

			model, cmd := a.Update(ctrlQ())
			got := model.(App)
			if isQuit(cmd) || !got.quitConfirm.IsVisible() {
				t.Fatalf("Ctrl+Q over %s: quit %v, dialog %v; want the dialog, not a quit", op.field, isQuit(cmd), got.quitConfirm.IsVisible())
			}
			if !strings.Contains(stripANSI(got.View()), "Quit?") {
				t.Fatalf("the dialog must be DRAWN over %s, not only set", op.field)
			}
			if !op.visible(&got) {
				t.Fatalf("%s must stay up under the dialog", op.field)
			}

			model, cmd = got.Update(keyRune('n'))
			got = model.(App)
			if isQuit(cmd) || got.quitConfirm.IsVisible() || !op.visible(&got) {
				t.Fatalf("n over %s: quit %v, dialog %v, overlay %v; want only the dialog gone", op.field, isQuit(cmd), got.quitConfirm.IsVisible(), op.visible(&got))
			}
			if got.focus != focus || got.messages.cursor != cursor {
				t.Fatalf("cancel over %s changed focus %v→%v or cursor %d→%d", op.field, focus, got.focus, cursor, got.messages.cursor)
			}
			if strings.Contains(stripANSI(got.View()), "Quit?") {
				t.Fatalf("the dialog must be gone from the frame over %s", op.field)
			}
		})
	}
}

func TestQuit_YQuitsWithCleanupQAndEscCancelEnterDoesNothing(t *testing.T) {
	a := newTestApp(t)
	model, _ := a.Update(ctrlQ())
	model, cmd := model.(App).Update(keyRune('y'))
	got := model.(App)
	if !isQuit(cmd) || got.client.IsConnected() {
		t.Fatalf("y: quit %v, connected %v; want a quit with the client closed", isQuit(cmd), got.client.IsConnected())
	}

	for _, key := range []string{"q", "esc"} {
		b := newTestApp(t)
		model, _ = b.Update(ctrlQ())
		model, cmd = model.(App).Update(focusKey(key))
		got = model.(App)
		if isQuit(cmd) || got.quitConfirm.IsVisible() || got.focus != FocusInput {
			t.Fatalf("%s: quit %v, dialog %v, focus %v; want the dialog closed and nothing else changed", key, isQuit(cmd), got.quitConfirm.IsVisible(), got.focus)
		}
	}

	c := newTestApp(t)
	model, _ = c.Update(ctrlQ())
	model, cmd = model.(App).Update(focusKey("enter"))
	got = model.(App)
	if isQuit(cmd) || !got.quitConfirm.IsVisible() {
		t.Fatal("Enter must do nothing in the quit dialog")
	}
}

// A held or repeated Ctrl+Q only ever shows the dialog (the old double-press
// bypass is gone).
func TestQuit_HeldKeyOnlyShowsTheDialog(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 5; i++ {
		model, cmd := a.Update(ctrlQ())
		a = model.(App)
		if isQuit(cmd) {
			t.Fatalf("press %d quit without the dialog", i+1)
		}
	}
	if !a.quitConfirm.IsVisible() {
		t.Fatal("the dialog must be up")
	}
}

func TestQuit_PreSessionScreens(t *testing.T) {
	// No client: the short connecting / connection-error frame.
	a := newTestApp(t, withNoClient())
	model, _ := a.Update(ctrlQ())
	got := model.(App)
	frame := stripANSI(got.View())
	if !got.quitConfirm.IsVisible() || !strings.Contains(frame, "Quit?") {
		t.Fatalf("no client: dialog %v, drawn %v", got.quitConfirm.IsVisible(), strings.Contains(frame, "Quit?"))
	}
	if n := len(strings.Split(frame, "\n")); n > got.height {
		t.Fatalf("the short frame grew to %d lines for a %d-row terminal", n, got.height)
	}

	// Connect failed: the dialog over it, n returns to it, its own q still there.
	b := newTestApp(t)
	b.connectFailed.Show("connection refused", "SHA256:fp", "ssh-ed25519 AAAA")
	model, _ = b.Update(ctrlQ())
	got = model.(App)
	if !got.quitConfirm.IsVisible() || !strings.Contains(stripANSI(got.View()), "Quit?") || !got.connectFailed.IsVisible() {
		t.Fatal("connect failed: the dialog must draw over the screen and leave it up")
	}
	model, _ = got.Update(keyRune('n'))
	got = model.(App)
	if got.quitConfirm.IsVisible() || !got.connectFailed.IsVisible() {
		t.Fatal("n must return to the connect-failed screen")
	}

	// Passphrase prompt: the dialog over it, n returns to it.
	c := newTestApp(t)
	c.passphrase.Show("", 1, "/keys/server")
	model, _ = c.Update(ctrlQ())
	got = model.(App)
	if !got.quitConfirm.IsVisible() || !strings.Contains(stripANSI(got.View()), "Quit?") || !got.passphrase.IsVisible() {
		t.Fatal("passphrase: the dialog must draw over the prompt and leave it up")
	}
	model, _ = got.Update(keyRune('n'))
	got = model.(App)
	if got.quitConfirm.IsVisible() || !got.passphrase.IsVisible() {
		t.Fatal("n must return to the passphrase prompt")
	}
}

func TestQuit_SkipsTheBouncerUntilTheTerminalGrows(t *testing.T) {
	a := newTestApp(t, withSize(40, 10))
	model, _ := a.Update(ctrlQ())
	got := model.(App)
	if !got.quitConfirm.IsVisible() {
		t.Fatal("the dialog is set even on the bouncer")
	}
	if strings.Contains(stripANSI(got.View()), "Quit?") {
		t.Fatal("the dialog must not be drawn on the too-small frame")
	}
	got.width, got.height = 100, 30
	if !strings.Contains(stripANSI(got.View()), "Quit?") {
		t.Fatal("the dialog must appear once the terminal grows")
	}
}

// A click while the dialog is up reaches nothing underneath, even a
// mouse-capable dialog like the shared picker.
func TestQuit_DialogConsumesClicksBeforeMouseCapableDialogs(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""))
	a.picker.Show(PickerRequest{Verb: "open_link", ShowFilter: true}, []PickerItem{{ID: "a", Primary: "first"}, {ID: "b", Primary: "second"}})
	model, _ := a.Update(ctrlQ())
	got := model.(App)
	before := got.picker
	model, _ = got.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 30, Y: 12})
	got = model.(App)
	if !got.quitConfirm.IsVisible() || !got.picker.IsVisible() || got.picker.cursor != before.cursor {
		t.Fatal("a click under the quit dialog must change nothing")
	}
}

func TestQuit_KittyFrameUnderTheDialogCarriesNoPlacement(t *testing.T) {
	withRastermProtocol(t, rastermKitty)
	a := newTestApp(t, withContext("general", "", ""))
	model, _ := a.Update(ctrlQ())
	out := model.(App).View()
	if !strings.HasPrefix(out, "\x1b_Ga=d,") || strings.Contains(out, "\x1b_Ga=T,") {
		t.Fatal("the frame under the quit dialog must clear the image layer and carry no placement")
	}
}

// The frame never shrinks: View is always the terminal's height, with the
// dialog up or not, over a short frame (the info panel is 13 lines) and the
// pre-session frame alike. Bubble Tea's alt-screen renderer skips lines
// identical to the last frame's and, after a shrink, erases from the cursor
// to the end of the screen — which wiped the info panel's bottom border
// after a quit dialog drawn below it was cancelled on a tall terminal.
func TestQuit_FrameNeverShrinksSoCancelKeepsTheBottomBorder(t *testing.T) {
	a := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()), withFocus(FocusMessages), withSize(120, 40))
	a.showInfoPanelForContext("general", "", "")
	lines := func(frame string) []string { return strings.Split(frame, "\n") }

	before := lines(a.View())
	model, _ := a.Update(ctrlQ())
	withDialog := lines(model.(App).View())
	model, _ = model.(App).Update(keyRune('n'))
	after := lines(model.(App).View())
	for name, f := range map[string][]string{"before": before, "with the dialog": withDialog, "after": after} {
		if len(f) != 40 {
			t.Fatalf("%s: %d lines on a 40-row terminal; the frame must always be the terminal's height", name, len(f))
		}
	}
	border := lineIndex(before, "╰")
	if border < 0 || !strings.Contains(stripANSI(after[border]), "╰") {
		t.Fatalf("the info panel's bottom border (row %d) must be back after cancel", border)
	}

	for _, op := range overlayOpeners {
		b := newTestApp(t, withContext("general", "", ""), withMessages(nestedMessages()))
		op.open(t, &b)
		if n := len(lines(b.View())); n != b.height {
			t.Fatalf("%s: View is %d lines on a %d-row terminal", op.field, n, b.height)
		}
	}
	c := newTestApp(t, withNoClient())
	if n := len(lines(c.View())); n != c.height {
		t.Fatalf("no client: View is %d lines on a %d-row terminal", n, c.height)
	}
}

// The rule has one home: "ctrl+q" appears in exactly one non-test file of
// the package (the block at the top of the dispatcher).
func TestQuit_OneHomeForCtrlQ(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), `"ctrl+q"`); n > 0 {
			files++
			if n != 1 || name != "app.go" {
				t.Fatalf("%s mentions \"ctrl+q\" %d times; the only home is the one block in app.go", name, n)
			}
		}
	}
	if files != 1 {
		t.Fatalf("\"ctrl+q\" found in %d files, want app.go only", files)
	}
}
