package tui

// upload-file-picker Step 5: App-level wiring of the file picker — the two
// doors, the guards (connection, context, read-only room, unreadable root),
// the modal enumeration sites (key intercept, mouse consume list,
// anyModalVisible), selection → startUpload with the Gen / validate /
// connection checks, and focus/root bookkeeping on close.

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newFilePickerApp is a live-session App (connected client, ConnStateOnline)
// in room_x, with HOME pointing at a fixture tree so bare /upload opens the
// picker there. Returns the App VALUE (App.Update is a value receiver).
func newFilePickerApp(t *testing.T) (App, string) {
	t.Helper()
	root := mkPickerTree(t)
	t.Setenv("HOME", root)
	a, st := newEditAppHarness(t)
	a.width, a.height = 100, 40
	a.messages.SetContext("room_x", "", "")
	if err := st.UpsertRoom("room_x", "general", "", 2); err != nil {
		t.Fatal(err)
	}
	return a, root
}

func bareUpload(room string) *SlashCommandMsg {
	return &SlashCommandMsg{Command: "/upload", Room: room}
}

func TestFilePickerApp_BareUploadWithoutConnectionRefuses(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.client = nil
	a.handleSlashCommand(bareUpload("room_x"))
	if a.filePicker.IsVisible() || a.statusBar.errorMsg != "offline - reconnect to upload files" {
		t.Fatalf("nil client: visible=%v status=%q", a.filePicker.IsVisible(), a.statusBar.errorMsg)
	}

	b, _ := newFilePickerApp(t)
	b.setConnState(ConnStateOffline)
	b.handleSlashCommand(bareUpload("room_x"))
	if b.filePicker.IsVisible() || b.statusBar.errorMsg != "offline - reconnect to upload files" {
		t.Fatalf("offline browse: visible=%v status=%q", b.filePicker.IsVisible(), b.statusBar.errorMsg)
	}
}

func TestFilePickerApp_BareUploadWithoutContextRefuses(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.handleSlashCommand(&SlashCommandMsg{Command: "/upload"})
	if a.filePicker.IsVisible() || a.statusBar.errorMsg != "No active room or group" {
		t.Fatalf("visible=%v status=%q", a.filePicker.IsVisible(), a.statusBar.errorMsg)
	}
}

func TestFilePickerApp_BareUploadOpensPicker(t *testing.T) {
	a, root := newFilePickerApp(t)
	a.handleSlashCommand(bareUpload("room_x"))
	if !a.filePicker.IsVisible() {
		t.Fatalf("picker must open, status = %q", a.statusBar.errorMsg)
	}
	if a.filePicker.req.Room != "room_x" || a.filePicker.req.Gen != a.connGen {
		t.Fatalf("request = %+v, want room_x at gen %d", a.filePicker.req, a.connGen)
	}
	if a.filePicker.LastRoot() != root {
		t.Fatalf("root = %q, want HOME %q", a.filePicker.LastRoot(), root)
	}
	if a.filePicker.req.Title != "Upload to #general" {
		t.Fatalf("title = %q, want 'Upload to #general'", a.filePicker.req.Title)
	}
}

func TestFilePickerApp_AliasBareUOpensPicker(t *testing.T) {
	a, _ := newFilePickerApp(t)
	i := NewInput()
	if !i.handleCommand("/u", nil, "room_x", "", "") {
		t.Fatal("/u must be a recognized verb")
	}
	sc := i.PendingCommand()
	if sc == nil {
		t.Fatal("bare /u must forward a command")
	}
	if cmd := a.handleSlashCommandCmd(sc); cmd != nil {
		t.Fatal("opening the picker must not return a cmd")
	}
	if !a.filePicker.IsVisible() {
		t.Fatalf("/u end to end must open the picker, status = %q", a.statusBar.errorMsg)
	}
}

func TestFilePickerApp_UnreadableHomeNeverShowsDeadModal(t *testing.T) {
	a, _ := newFilePickerApp(t)
	t.Setenv("HOME", "/nonexistent/sshkey-term-picker-root")
	a.handleSlashCommand(bareUpload("room_x"))
	if a.filePicker.IsVisible() {
		t.Fatal("an unreadable start root must never show a dead modal (#10)")
	}
	if !strings.HasPrefix(a.statusBar.errorMsg, "Cannot open ") {
		t.Fatalf("status = %q, want 'Cannot open …'", a.statusBar.errorMsg)
	}
}

func TestFilePickerApp_EscClosesAndRestoresFocus(t *testing.T) {
	a, root := newFilePickerApp(t)
	a.handleSlashCommand(bareUpload("room_x"))
	a.focus = FocusInput + 1 // anything else: Esc must put it back

	model, _ := a.Update(key(tea.KeyEsc))
	na := model.(App)
	if na.filePicker.IsVisible() {
		t.Fatal("Esc must close the picker")
	}
	if na.focus != FocusInput {
		t.Fatalf("focus = %v, want FocusInput", na.focus)
	}
	if na.filePickerLastRoot != root {
		t.Fatalf("last root = %q, want %q (session memory)", na.filePickerLastRoot, root)
	}
}

func TestFilePickerApp_SelectionStartsUploadInAllContexts(t *testing.T) {
	a, root := newFilePickerApp(t)
	photo := filepath.Join(root, "photo.jpg")
	for name, req := range map[string]FilePickerRequest{
		"room":  {Room: "room_x"},
		"group": {Group: "grp_x"},
		"dm":    {DM: "dm_x"},
	} {
		b := a
		req.Gen = b.connGen
		model, _ := b.Update(FilePickerSelectedMsg{Request: req, Path: photo})
		nb := model.(App)
		if nb.statusBar.errorMsg != "Uploading photo.jpg..." {
			t.Fatalf("%s: status = %q, want the upload to start", name, nb.statusBar.errorMsg)
		}
		if nb.focus != FocusInput {
			t.Fatalf("%s: focus must return to the input", name)
		}
	}

	// The file vanished between listing and Enter.
	model, _ := a.Update(FilePickerSelectedMsg{Request: FilePickerRequest{Room: "room_x", Gen: a.connGen}, Path: filepath.Join(root, "gone.png")})
	if na := model.(App); !strings.HasPrefix(na.statusBar.errorMsg, "File not found: ") {
		t.Fatalf("vanished file: status = %q", na.statusBar.errorMsg)
	}

	// Connection dropped while browsing.
	b := a
	b.setConnState(ConnStateOffline)
	model, _ = b.Update(FilePickerSelectedMsg{Request: FilePickerRequest{Room: "room_x", Gen: b.connGen}, Path: photo})
	if nb := model.(App); nb.statusBar.errorMsg != "offline - reconnect to upload files" {
		t.Fatalf("offline at selection: status = %q", nb.statusBar.errorMsg)
	}
}

func TestFilePickerApp_SelectionDroppedIfConnectionChanged(t *testing.T) {
	a, root := newFilePickerApp(t)
	photo := filepath.Join(root, "photo.jpg")
	stale := FilePickerSelectedMsg{Request: FilePickerRequest{Room: "room_x", Gen: a.connGen}, Path: photo}
	a.connGen++ // what a switch or reconnect does after the picker opened

	model, _ := a.Update(stale)
	na := model.(App)
	if !strings.HasPrefix(na.statusBar.errorMsg, "Upload cancelled") {
		t.Fatalf("stale-gen selection: status = %q, want 'Upload cancelled …'", na.statusBar.errorMsg)
	}
	if strings.HasPrefix(na.statusBar.errorMsg, "Uploading") {
		t.Fatal("a stale-gen selection must never start an upload")
	}
}

func TestFilePickerApp_CtrlCQuitsWithPickerOpen(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.handleSlashCommand(bareUpload("room_x"))
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C must quit even with the picker open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Ctrl+C cmd yielded %T, want tea.QuitMsg", cmd())
	}
}

func TestFilePickerApp_MouseConsumedWhileOpen(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.handleSlashCommand(bareUpload("room_x"))
	model, cmd := a.Update(tea.MouseMsg{X: 2, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	na := model.(App)
	if cmd != nil || !na.filePicker.IsVisible() {
		t.Fatal("mouse events must be consumed (no cmd, picker stays open)")
	}
	if na.messages.room != "room_x" {
		t.Fatal("a click at sidebar coordinates must not switch context under the picker")
	}
}

func TestFilePickerApp_AnyModalVisibleIncludesFilePicker(t *testing.T) {
	a, _ := newFilePickerApp(t)
	if a.anyModalVisible() {
		t.Fatal("precondition: no modal open")
	}
	a.handleSlashCommand(bareUpload("room_x"))
	if !a.anyModalVisible() {
		t.Fatal("anyModalVisible must include the file picker (inline-image preview suppression)")
	}
}

func TestFilePickerApp_MemberPanelOpenStillOpensNarrower(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.handleSlashCommand(bareUpload("room_x"))
	wide := a.filePicker.contentWidth
	a.filePicker.Hide()

	a.memberPanel.Toggle()
	a.handleSlashCommand(bareUpload("room_x"))
	if !a.filePicker.IsVisible() {
		t.Fatalf("picker must open over a docked member panel, status = %q", a.statusBar.errorMsg)
	}
	if a.filePicker.contentWidth >= wide {
		t.Fatalf("with the member panel open the dialog must size to the narrower pane: %d vs %d", a.filePicker.contentWidth, wide)
	}
}

func TestFilePickerApp_ReadOnlyRoomBlocksBothVerbs(t *testing.T) {
	a, _ := newFilePickerApp(t)
	a.messages.SetLeft(true)                   // archived room → the V8 dispatch gate applies
	a.handleSlashCommand(bareUpload("room_x")) // what BOTH /upload and /u canonicalize to
	if a.filePicker.IsVisible() {
		t.Fatal("the picker must not open in a read-only room")
	}
	if a.statusBar.errorMsg != `"/delete", "/search", and "/help" are available` {
		t.Fatalf("status = %q, want the read-only room message", a.statusBar.errorMsg)
	}
}
