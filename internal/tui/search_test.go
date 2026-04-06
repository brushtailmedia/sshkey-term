package tui

import (
	"strings"
	"testing"
)

func TestSearchView_FTSAvailable(t *testing.T) {
	s := NewSearch()
	s.Show()
	s.hasFTS = true

	view := s.View(60, 20)
	if strings.Contains(view, "Basic search") {
		t.Error("should NOT show FTS warning when FTS5 is available")
	}
}

func TestSearchView_FTSUnavailable(t *testing.T) {
	s := NewSearch()
	s.Show()
	s.hasFTS = false

	view := s.View(60, 20)
	if !strings.Contains(view, "Basic search") {
		t.Error("should show FTS warning when FTS5 is unavailable")
	}
	if !strings.Contains(view, "FTS5") {
		t.Error("should mention FTS5 in the warning")
	}
}

func TestSearchSetFTS(t *testing.T) {
	s := NewSearch()
	s.SetFTS(true)
	if !s.hasFTS {
		t.Error("SetFTS(true) should set hasFTS")
	}
	s.SetFTS(false)
	if s.hasFTS {
		t.Error("SetFTS(false) should clear hasFTS")
	}
}
