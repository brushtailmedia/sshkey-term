package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// captureLinkSeams swaps the clipboard and browser seams for recorders and
// restores them when the test ends.
func captureLinkSeams(t *testing.T) (copied, opened *[]string) {
	t.Helper()
	var c, o []string
	prevCopy, prevOpen := copyText, openURL
	copyText = func(s string) { c = append(c, s) }
	openURL = func(u string) error { o = append(o, u); return nil }
	t.Cleanup(func() { copyText, openURL = prevCopy, prevOpen })
	return &c, &o
}

func linkMessages() []DisplayMessage {
	return []DisplayMessage{
		{ID: "plain", From: "Alice", FromID: "usr_alice", Body: "no links here", TS: 1000},
		{ID: "one", From: "Bob", FromID: "usr_bob", Body: "read https://example.com/post today", TS: 1001},
		{ID: "two", From: "Carol", FromID: "usr_carol", Body: "https://a.example/1 or https://b.example/2", TS: 1002},
		{ID: "file", From: "Dave", FromID: "usr_dave", Body: "notes.pdf https://c.example/3", TS: 1003,
			Attachments: []DisplayAttachment{{FileID: "f1", Name: "notes.pdf", Size: 2048}}},
	}
}

// Pane keys: o opens the attachment first, else the link; C/Y copy the
// link; c/y copy the text; nothing fires on a message without links.
func TestMessages_LinkKeys(t *testing.T) {
	m := NewMessages()
	m.messages = linkMessages()
	act := func(cursor int, key tea.KeyMsg) string {
		m.cursor = cursor
		_, cmd := m.Update(key)
		if cmd == nil {
			return ""
		}
		if a, ok := cmd().(MessageAction); ok {
			return a.Action
		}
		return "?"
	}
	o := keyRune('o')
	if got := act(0, o); got != "" {
		t.Errorf("o on a plain message = %q, want nothing", got)
	}
	if got := act(1, o); got != "open_link" {
		t.Errorf("o on a message with a link = %q, want open_link", got)
	}
	if got := act(3, o); got != "open_attachment" {
		t.Errorf("o with an attachment present = %q, want open_attachment (attachment wins)", got)
	}
	for _, k := range []rune{'C', 'Y'} {
		if got := act(1, keyRune(k)); got != "copy_link" {
			t.Errorf("%c on a message with a link = %q, want copy_link", k, got)
		}
		if got := act(0, keyRune(k)); got != "" {
			t.Errorf("%c on a plain message = %q, want nothing", k, got)
		}
	}
	for _, k := range []rune{'c', 'y'} {
		if got := act(1, keyRune(k)); got != "copy" {
			t.Errorf("%c = %q, want copy", k, got)
		}
	}
}

// Hints: one key each, only on the selected message; o=open link only
// when there is no attachment for o to open.
func TestBuildContent_LinkHints(t *testing.T) {
	m := NewMessages()
	m.messages = linkMessages()
	// Wide enough that the selected row's band does not wrap the hints onto
	// a second line (the band wraps like any pane row).
	wide := func() string { c, _ := m.buildContent(100); return c }

	m.cursor = 1
	content := wide()
	if l := lineContaining(content, "C=copy link"); !strings.Contains(l, "o=open link") {
		t.Errorf("selected link message must show both hints on one line: %q", l)
	}
	m.cursor = 3
	content = wide()
	if strings.Contains(content, "o=open link") {
		t.Errorf("with an attachment, o already means open attachment; no o=open link hint:\n%s", content)
	}
	if !strings.Contains(content, "C=copy link") || !strings.Contains(content, "o=open  s=save") {
		t.Errorf("attachment hints plus the copy-link hint expected:\n%s", content)
	}
	m.cursor = 0
	if c := wide(); strings.Contains(c, "copy link") || strings.Contains(c, "open link") {
		t.Errorf("no link hints on a message without links")
	}
}

// App: one link acts at once; copy goes to the clipboard, open to the
// browser, with a status line.
func TestApp_LinkActions_SingleLink(t *testing.T) {
	copied, opened := captureLinkSeams(t)
	a := newPaneThreadApp(t)
	a.messages.messages = linkMessages()
	one := a.messages.messages[1]

	model, _ := a.Update(MessageAction{Action: "copy_link", Msg: one})
	a = model.(App)
	if len(*copied) != 1 || (*copied)[0] != "https://example.com/post" {
		t.Fatalf("copied = %v, want the link", *copied)
	}
	model, _ = a.Update(MessageAction{Action: "open_link", Msg: one})
	a = model.(App)
	if len(*opened) != 1 || (*opened)[0] != "https://example.com/post" {
		t.Fatalf("opened = %v, want the link", *opened)
	}
	if a.picker.IsVisible() {
		t.Error("one link never needs the picker")
	}
}

// App: several links open the shared picker listing them; the selection
// comes back through PickerSelectedMsg and acts on the chosen URL.
func TestApp_LinkActions_SeveralLinksUsePicker(t *testing.T) {
	copied, opened := captureLinkSeams(t)
	a := newPaneThreadApp(t)
	a.messages.messages = linkMessages()
	two := a.messages.messages[2]

	model, _ := a.Update(MessageAction{Action: "open_link", Msg: two})
	a = model.(App)
	if !a.picker.IsVisible() {
		t.Fatal("several links must open the picker")
	}
	if len(a.picker.all) != 2 || a.picker.all[0].Primary != "https://a.example/1" || a.picker.all[1].Primary != "https://b.example/2" {
		t.Fatalf("picker items = %+v", a.picker.all)
	}
	if len(*opened) != 0 {
		t.Fatal("nothing opens until a link is chosen")
	}
	model, _ = a.Update(PickerSelectedMsg{Request: PickerRequest{Verb: "open_link"}, SelectedID: "https://b.example/2"})
	a = model.(App)
	if len(*opened) != 1 || (*opened)[0] != "https://b.example/2" {
		t.Errorf("opened = %v, want the chosen link", *opened)
	}

	model, _ = a.Update(PickerSelectedMsg{Request: PickerRequest{Verb: "copy_link"}, SelectedID: "https://a.example/1"})
	_ = model
	if len(*copied) != 1 || (*copied)[0] != "https://a.example/1" {
		t.Errorf("copied = %v, want the chosen link", *copied)
	}
}

// Over SSH the browser is on the other machine: open copies instead and
// says so.
func TestApp_LinkActions_RemoteSessionCopiesInsteadOfOpening(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")
	copied, opened := captureLinkSeams(t)
	a := newPaneThreadApp(t)
	a.messages.messages = linkMessages()

	model, _ := a.Update(MessageAction{Action: "open_link", Msg: a.messages.messages[1]})
	a = model.(App)
	if len(*opened) != 0 {
		t.Error("no browser launch over SSH")
	}
	if len(*copied) != 1 {
		t.Fatalf("the link must be copied instead; copied = %v", *copied)
	}
	if !strings.Contains(strings.ToLower(a.statusBar.View(100)), "remote session") {
		t.Errorf("status must explain the fallback: %q", a.statusBar.View(100))
	}
}

// Context menu: Open link / Copy link appear only for messages with links,
// with the read actions.
func TestContextMenu_LinkItems(t *testing.T) {
	var c ContextMenuModel
	c.Show(linkMessages()[0], 5, 5, ContextMenuOptions{})
	if menuIndexOf(c, "open_link") >= 0 || menuIndexOf(c, "copy_link") >= 0 {
		t.Errorf("no link items without links: %v", menuLabels(c))
	}
	c.Show(linkMessages()[1], 5, 5, ContextMenuOptions{IsRoom: true})
	want := []string{"Reply", "React", "Open link", "Copy link", "Pin to room", "Copy text"}
	got := menuLabels(c)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// Thread view: the same keys, confirmed in the footer; several links close
// the view for the picker; o prefers the attachment.
func TestThreadPanel_LinkKeysAndNotice(t *testing.T) {
	msgs := []DisplayMessage{{ID: "root", From: "Alice", FromID: "usr_alice", Body: "root", TS: 1000}}
	for _, m := range linkMessages() {
		m.ReplyTo = "root"
		msgs = append(msgs, m)
	}
	var p ThreadPanelModel
	p.Show("root", msgs)

	p.Select("one")
	p, cmd := p.Update(keyRune('C'))
	if messageActionOf(t, cmd, "copy_link").Msg.ID != "one" || !p.IsVisible() {
		t.Fatal("C on one link copies in place and keeps the view")
	}
	if v := p.View(80, 30); !strings.Contains(v, "copied link example.com/post") || strings.Contains(v, "Enter=go to") {
		t.Errorf("footer must show the notice instead of the keys:\n%s", v)
	}
	p, _ = p.Update(keyDown) // any key clears it
	if v := p.View(80, 30); strings.Contains(v, "copied link") || !strings.Contains(v, "Enter=go to") {
		t.Errorf("the next key must restore the footer:\n%s", v)
	}

	p.Select("one")
	p, cmd = p.Update(keyRune('o'))
	if messageActionOf(t, cmd, "open_link").Msg.ID != "one" {
		t.Fatal("o on a link-only message opens the link")
	}
	if v := p.View(80, 30); !strings.Contains(v, "opening example.com/post") {
		t.Errorf("open notice expected:\n%s", v)
	}

	p.Select("file")
	p, cmd = p.Update(keyRune('o'))
	if messageActionOf(t, cmd, "open_attachment").Msg.ID != "file" {
		t.Fatal("o prefers the attachment")
	}

	p.Select("two")
	p, cmd = p.Update(keyRune('Y'))
	if messageActionOf(t, cmd, "copy_link").Msg.ID != "two" {
		t.Fatal("Y with several links emits copy_link for the App's picker")
	}
	if p.IsVisible() {
		t.Error("several links need the picker, an overlay the view would hide: the view closes first")
	}

	p.Show("root", msgs)
	p.Select("plain")
	p, cmd = p.Update(keyRune('y'))
	if messageActionOf(t, cmd, "copy").Msg.ID != "plain" {
		t.Fatal("y copies the highlighted message's text")
	}
	if v := p.View(80, 30); !strings.Contains(v, "copied message text") {
		t.Errorf("copy notice expected:\n%s", v)
	}
	if _, cmd = p.Update(keyRune('C')); cmd != nil {
		t.Error("C on a message without links does nothing")
	}
}
