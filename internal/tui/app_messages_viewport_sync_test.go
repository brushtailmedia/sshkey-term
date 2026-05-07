package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func expectedMessagesViewportHeight(a *App) int {
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	mainWidth := layout.MessagesWidth
	mainHeight := layout.MessagesY1 - 2
	if mainWidth < 20 {
		mainWidth = 20
	}
	if mainHeight < 5 {
		mainHeight = 5
	}

	msgHeight := mainHeight - a.input.BannerRows()
	if a.showHelpHint {
		msgHeight--
	}
	if msgHeight < 1 {
		msgHeight = 1
	}

	_, headerLines := a.messages.renderHeader(mainWidth)
	pinned := a.pinnedBar.View(mainWidth - 2)
	pinnedRows := 0
	if pinned != "" {
		pinnedRows = strings.Count(pinned, "\n") + 1
	}

	vh := msgHeight - 2 - headerLines - pinnedRows
	if vh < 1 {
		vh = 1
	}
	return vh
}

func TestApp_WindowSizeMsgSyncsPersistentMessagesViewport(t *testing.T) {
	a := App{
		messages:    NewMessages(),
		input:       NewInput(),
		sidebar:     NewSidebar(),
		statusBar:   NewStatusBar(),
		memberPanel: NewMemberPanel(),
		focus:       FocusMessages,
	}
	a.messages.SetContext("room_sync", "", "")
	a.messages.messages = makeMultilineMessages(8)

	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated := model.(App)

	want := expectedMessagesViewportHeight(&updated)
	if got := updated.messages.viewport.Height; got != want {
		t.Fatalf("persistent viewport height = %d, want %d", got, want)
	}
}

func TestApp_DownArrowUsesSyncedSmallViewport(t *testing.T) {
	a := App{
		messages:    NewMessages(),
		input:       NewInput(),
		sidebar:     NewSidebar(),
		statusBar:   NewStatusBar(),
		memberPanel: NewMemberPanel(),
		focus:       FocusMessages,
	}
	a.messages.SetContext("room_scroll", "", "")
	a.messages.messages = makeMultilineMessages(40)

	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = model.(App)

	// Start at the top and drive cursor navigation down.
	a.messages.cursor = 0
	a.messages.viewport.GotoTop()
	for i := 0; i < 16; i++ {
		model, _ = a.Update(tea.KeyMsg{Type: tea.KeyDown})
		a = model.(App)
	}

	if a.messages.cursor != 16 {
		t.Fatalf("cursor after down navigation = %d, want 16", a.messages.cursor)
	}

	// Visibility check uses the ACTUAL rendered viewport height for this
	// terminal size, not the model's stored height, to catch stale-geometry
	// regressions.
	wantHeight := expectedMessagesViewportHeight(&a)
	start := a.messages.rowMap[a.messages.cursor]
	top := a.messages.viewport.YOffset
	bottom := top + wantHeight - 1
	if start < top || start > bottom {
		t.Fatalf("selected message row %d not visible in actual viewport [%d,%d]", start, top, bottom)
	}
}
