package tui

// Phase 17c Step 6 — status bar refreshing indicator tests.

import (
	"strings"
	"testing"
	"time"
)

func TestStatusBar_SetRefreshing_ShowsIndicator(t *testing.T) {
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(200 * time.Millisecond)

	view := s.View(80)
	if !strings.Contains(view, "refreshing") {
		t.Errorf("refreshing indicator not rendered in view: %q", view)
	}
}

func TestStatusBar_SetRefreshing_HidesAfterFloor(t *testing.T) {
	// Minimum visibility of 1ms — should hide within a short sleep.
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(1 * time.Millisecond)

	time.Sleep(5 * time.Millisecond)
	view := s.View(80)
	if strings.Contains(view, "refreshing") {
		t.Errorf("refreshing indicator still rendered after floor elapsed: %q", view)
	}
}

func TestStatusBar_ErrorPrecedesRefreshing(t *testing.T) {
	// Error takes precedence — when both are set, user sees the error,
	// not the refreshing hint. Errors are the more important signal.
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(1 * time.Second)
	s.SetError("something went wrong")

	view := s.View(80)
	if !strings.Contains(view, "something went wrong") {
		t.Errorf("error message hidden by refreshing indicator: %q", view)
	}
	if strings.Contains(view, "refreshing") {
		t.Errorf("refreshing indicator shown despite error: %q", view)
	}
}

func TestStatusBar_SetRefreshing_FloorRespected(t *testing.T) {
	// A later SetRefreshing with a shorter window shouldn't push the
	// floor earlier — we keep the max.
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(500 * time.Millisecond)
	earlyFloor := s.refreshingUntil

	s.SetRefreshing(10 * time.Millisecond) // shorter — should not shorten
	if s.refreshingUntil.Before(earlyFloor) {
		t.Errorf("SetRefreshing with shorter duration shortened the floor; want monotonic extension only")
	}
}

func TestStatusBar_SetRefreshing_LongerWindowExtends(t *testing.T) {
	// A later SetRefreshing with a longer window DOES push the floor.
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(10 * time.Millisecond)
	earlyFloor := s.refreshingUntil

	// Wait just enough to be meaningful but still within the second window.
	time.Sleep(2 * time.Millisecond)
	s.SetRefreshing(500 * time.Millisecond) // longer — extends
	if !s.refreshingUntil.After(earlyFloor) {
		t.Errorf("longer SetRefreshing did not extend the floor")
	}
}

func TestStatusBar_ClearRefreshing_RespectsFloor(t *testing.T) {
	// ClearRefreshing is currently a no-op; the floor time in
	// refreshingUntil is what View checks. This test locks in that
	// contract — a ClearRefreshing call during the visibility window
	// must NOT hide the indicator prematurely.
	var s StatusBarModel
	s.SetConnected(true)
	s.SetRefreshing(1 * time.Second)
	s.ClearRefreshing()

	view := s.View(80)
	if !strings.Contains(view, "refreshing") {
		t.Errorf("ClearRefreshing hid the indicator before the floor elapsed: %q", view)
	}
}
