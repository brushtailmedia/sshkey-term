package tui

import (
	"fmt"
	"testing"
)

func TestMessagesPinnedBarRowsForHitTest_IsWrapAware(t *testing.T) {
	m := NewMessages()
	m.SetPinnedBar(" ▶ 5 pinned message(s)  (Ctrl+p to expand)")

	base := m.PinnedBarRows()
	if base != 1 {
		t.Fatalf("precondition: base pinned rows = %d, want 1", base)
	}

	// Narrow pane forces the collapsed pinned line to soft-wrap.
	got := m.PinnedBarRowsForHitTest(24)
	if got <= base {
		t.Fatalf("wrap-aware pinned rows = %d, want > %d", got, base)
	}
}

func TestAppMouseClick_WrappedPinnedBarOffsetsSelection(t *testing.T) {
	a, _ := newEditAppHarness(t)
	a.width = 56
	a.height = 24
	a.sidebar = NewSidebar()
	a.messages.SetContext("room_mouse", "", "")
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", From: "alice", Body: "first", TS: 1, Room: "room_mouse"},
		{ID: "msg_2", FromID: "usr_bob", From: "bob", Body: "second", TS: 2, Room: "room_mouse"},
		{ID: "msg_3", FromID: "usr_bob", From: "bob", Body: "third", TS: 3, Room: "room_mouse"},
	}
	a.messages.remoteState = HistoryExhausted
	a.messages.cursor = 2
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	a.pinnedBar.SetPins("room_mouse", []string{"msg_1", "msg_2", "msg_3", "msg_1", "msg_2"}, a.messages.messages)
	a.messages.SetPinnedBar(a.pinnedBar.View(layout.MessagesWidth - 2))
	a.refreshMessageContent()

	pinnedBase := a.messages.PinnedBarRows()
	pinnedHit := a.messages.PinnedBarRowsForHitTest(layout.MessagesWidth)
	if pinnedHit <= pinnedBase {
		t.Fatalf("precondition: expected wrapped pinned hit rows > base (got %d vs %d)", pinnedHit, pinnedBase)
	}

	headerRows := a.messages.HeaderRowsForHitTest(layout.MessagesWidth)
	// First viewport row (first message start when content isn't scrolled)
	// = panel top border + header + pinned rows.
	x := layout.MessagesX0 + 2
	y := layout.MessagesY0 + 1 + headerRows + pinnedHit
	if panel := layout.HitTest(x, y); panel != "messages" {
		t.Fatalf("precondition: hit-test panel=%q, want messages", panel)
	}
	relY := y - layout.MessagesY0 - 1
	viewportRow := relY - headerRows - pinnedHit
	if idx := a.messages.MessageAtViewportRow(viewportRow); idx != 0 {
		t.Fatalf("precondition: computed message idx=%d, want 0", idx)
	}

	model, _ := a.handleMouseClick(x, y)
	updated := model.(App)

	if updated.messages.cursor != 0 {
		t.Fatalf("cursor after click = %d, want 0 (first message)", updated.messages.cursor)
	}
}

func TestPinnedBarPinIndexAtVisualRow_ExpandedWrapAware(t *testing.T) {
	p := PinnedBarModel{
		expanded: true,
		pins: []PinnedMessage{
			{ID: "m1", From: "alice", Body: "first"},
			{ID: "m2", From: "bob", Body: "ok"},
		},
	}
	// Force wrapped expanded-header rows while keeping pin rows single-line.
	width := 9
	headerRows := wrappedRows(searchHeaderStyle.Render(fmt.Sprintf(" Pinned (%d)", len(p.pins))), width)
	if headerRows < 2 {
		t.Fatalf("precondition: headerRows=%d, want >=2 to exercise wrapped-header mapping", headerRows)
	}
	line1Rows := wrappedRows(p.renderPinLine(p.pins[0], width, 0), width)
	if line1Rows != 1 {
		t.Fatalf("precondition: line1Rows=%d, want 1", line1Rows)
	}
	// Header rows are non-selectable.
	for r := 0; r < headerRows; r++ {
		if got := p.PinIndexAtVisualRow(r, width); got != -1 {
			t.Fatalf("header row %d mapped to pin %d, want -1", r, got)
		}
	}
	// First pin row maps to pin index 0 even when header wraps.
	if got := p.PinIndexAtVisualRow(headerRows, width); got != 0 {
		t.Fatalf("pin0 first row mapped to %d, want 0", got)
	}
	// First row of pin 1 maps to pin index 1.
	if got := p.PinIndexAtVisualRow(headerRows+1, width); got != 1 {
		t.Fatalf("pin1 first row mapped to %d, want 1", got)
	}
}

func TestAppMouseClick_ExpandedPinnedWrappedHeaderSelectsCorrectPin(t *testing.T) {
	a, _ := newEditAppHarness(t)
	// Keep messages pane narrow so expanded "Pinned (N)" wraps to multiple rows.
	a.width = 36
	a.height = 24
	a.sidebar = NewSidebar()
	a.messages.SetContext("room_mouse", "", "")
	a.messages.messages = []DisplayMessage{
		{ID: "msg_1", FromID: "usr_alice", From: "alice", Body: "first", TS: 1, Room: "room_mouse"},
		{ID: "msg_2", FromID: "usr_bob", From: "bob", Body: "second", TS: 2, Room: "room_mouse"},
		{ID: "msg_3", FromID: "usr_bob", From: "bob", Body: "third", TS: 3, Room: "room_mouse"},
	}
	a.messages.remoteState = HistoryExhausted
	layout := computeLayout(a.width, a.height, a.memberPanel.IsVisible())
	a.pinnedBar.SetPins("room_mouse", []string{"msg_1", "msg_2"}, a.messages.messages)
	a.pinnedBar.expanded = true
	barWidth := layout.MessagesWidth - 2
	a.messages.SetPinnedBar(a.pinnedBar.View(barWidth))
	a.refreshMessageContent()

	// Click on first pin row immediately after a wrapped pinned header.
	// Old row-1 mapping treated this as pin index (headerRows-1).
	headerRows := wrappedRows(searchHeaderStyle.Render(fmt.Sprintf(" Pinned (%d)", len(a.pinnedBar.pins))), barWidth)
	if headerRows < 2 {
		t.Fatalf("precondition: headerRows=%d, want >=2", headerRows)
	}
	msgHeaderRows := a.messages.HeaderRowsForHitTest(layout.MessagesWidth)
	pinRel := headerRows // first visual row of first pin line
	x := layout.MessagesX0 + 2
	y := layout.MessagesY0 + 1 + msgHeaderRows + pinRel

	model, _ := a.handleMouseClick(x, y)
	updated := model.(App)
	if updated.messages.cursor != 0 {
		t.Fatalf("cursor after wrapped pin-row click = %d, want 0 (msg_1)", updated.messages.cursor)
	}
	if updated.pinnedBar.expanded {
		t.Fatal("expanded pinned bar should collapse after pin jump click")
	}
}
