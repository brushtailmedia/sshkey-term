package tui

// Regression (owner report, 2026-10-23): after a cold OFFLINE start, with the
// viewport at the top of a short conversation, Up/k from the second message
// did nothing — repeatedly. Cause: Up at the top probes for older history;
// offline the probe could not be sent, the failure took the "aborted before
// an answer" path, which re-arms the probe budget, so every Up at the top
// probed again and was swallowed without moving the cursor. Connecting once
// cleared it because the server's answer marked the history exhausted.
//
// Fix: the pane knows whether the server is reachable and whether the local
// cache is exhausted; with both known it declines up front (Up walks the
// cursor, the hint hides), and an unsendable probe ends as "exhausted while
// offline" rather than as an abort.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/client"
)

// newOfflinePaneApp is an App that started offline: a local-only client that
// is not connected, ConnStateOffline, focus on the pane, a short conversation
// loaded with the viewport at its top and the cursor on the second message.
func newOfflinePaneApp(t *testing.T) App {
	t.Helper()
	c := client.New(client.Config{DeviceID: "dev_test"})
	client.SetUserIDForTesting(c, "usr_self")
	a := App{
		client:    c,
		connState: ConnStateOffline,
		width:     100,
		height:    30,
		messages:  NewMessages(),
		input:     NewInput(),
		statusBar: NewStatusBar(),
		focus:     FocusMessages,
	}
	a.messages.SetContext("room_support", "", "")
	seedMessages(&a.messages, 5)
	_ = a.messages.View(80, 16, true)
	if !a.messages.viewport.AtTop() {
		t.Fatal("precondition: a short conversation sits at the top of the viewport")
	}
	a.messages.cursor = 1
	return a
}

func isHistoryRequest(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(HistoryRequestMsg)
	return ok
}

// Pane level: offline with an exhausted cache, Up at the top walks the cursor
// and fires nothing; the hint is hidden.
func TestMessages_UpAtTopOfflineExhaustedWalksCursor(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 5)
	_ = m.View(80, 16, true)
	m.cursor = 1
	m.hintVisible = true // would show if the server were reachable
	m.localExhausted = true
	m.serverReachable = false

	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if isHistoryRequest(cmd) {
		t.Fatal("offline with an exhausted cache must not probe")
	}
	if after.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (Up walked the cursor)", after.cursor)
	}
	if after.shouldShowHistoryHint() {
		t.Fatal("the 'press up to load more' hint must hide when nothing can load")
	}
}

// Pane level: the same cache state with the server reachable still probes —
// the server may have more.
func TestMessages_UpAtTopReachableServerStillProbes(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 5)
	_ = m.View(80, 16, true)
	m.cursor = 1
	m.localExhausted = true
	m.serverReachable = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if !isHistoryRequest(cmd) {
		t.Fatal("with the server reachable, Up at the top must probe it")
	}
}

// App level, the reported loop: offline, cache state unknown, Up at the top
// fires one probe; the probe cannot be sent and ends as exhausted-while-
// offline (not an abort), so the next Up walks the cursor.
func TestApp_OfflineStart_UpAtTopDoesNotLoop(t *testing.T) {
	a := newOfflinePaneApp(t)
	a.messages.localExhausted = false // a pane that has not learned yet

	model, cmd := a.Update(keyRune('k'))
	a = model.(App)
	if !isHistoryRequest(cmd) {
		t.Fatal("first Up at the top with an unknown cache probes once")
	}
	a = deliver(t, a, cmd)
	if a.messages.loadingHistory {
		t.Fatal("an unsendable probe must not leave the pane loading")
	}
	if !a.messages.localExhausted {
		t.Fatal("an unsendable probe with no local page must mark the cache exhausted")
	}

	model, cmd = a.Update(keyRune('k'))
	a = model.(App)
	if isHistoryRequest(cmd) {
		t.Fatal("second Up must not probe again (the old abort path re-armed the budget and looped)")
	}
	if a.messages.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after the second Up", a.messages.cursor)
	}
}

// App level, the cold start as LoadFromDB leaves it: the cache is known
// exhausted, so the very first Up walks the cursor.
func TestApp_OfflineStart_FirstUpWalksCursor(t *testing.T) {
	a := newOfflinePaneApp(t)
	a.messages.localExhausted = true

	model, cmd := a.Update(keyRune('k'))
	a = model.(App)
	if isHistoryRequest(cmd) {
		t.Fatal("offline with an exhausted cache: no probe")
	}
	if a.messages.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 on the first Up", a.messages.cursor)
	}
}

// Reconnecting makes the server reachable again: the next Up at the top
// probes it once, as the server may hold older history.
func TestApp_Reconnect_ReopensHistoryProbe(t *testing.T) {
	a := newOfflinePaneApp(t)
	a.messages.localExhausted = true
	a.syncPaneReachability()
	if a.messages.requestHistory() != nil {
		t.Fatal("precondition: offline + exhausted declines")
	}

	client.SetConnectedForTesting(a.client, true)
	a.setConnState(ConnStateOnline)
	if !a.messages.serverReachable {
		t.Fatal("setConnState(Online) must mark the server reachable")
	}
	if a.messages.requestHistory() == nil {
		t.Fatal("online again: the probe must be allowed")
	}
}

// The initial load records local exhaustion: an empty context has nothing
// older in the cache.
func TestLoadFromDB_EmptyContextMarksLocalExhausted(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.messages.SetContext("room_empty", "", "")
	a.messages.LoadFromDB(a.client)
	if !a.messages.localExhausted {
		t.Fatal("an empty initial load must mark the local cache exhausted")
	}
	a.messages.SetContext("room_other", "", "")
	if a.messages.localExhausted {
		t.Fatal("a context switch must reset local exhaustion for the fresh load")
	}
}
