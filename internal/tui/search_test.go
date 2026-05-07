package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brushtailmedia/sshkey-term/internal/store"
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

func TestSearchUpdate_EscCloses(t *testing.T) {
	s := NewSearch()
	s.Show()
	if !s.IsVisible() {
		t.Fatal("precondition failed: search should be visible")
	}

	updated, _ := s.Update(tea.KeyMsg{Type: tea.KeyEsc}, nil)
	if updated.IsVisible() {
		t.Fatal("esc should close search")
	}
}

func TestSearchUpdate_CtrlOpenBracketCloses(t *testing.T) {
	s := NewSearch()
	s.Show()
	if !s.IsVisible() {
		t.Fatal("precondition failed: search should be visible")
	}

	updated, _ := s.Update(tea.KeyMsg{Type: tea.KeyCtrlOpenBracket}, nil)
	if updated.IsVisible() {
		t.Fatal("ctrl+[ should close search")
	}
}

// TestSearchView_ResolvesConversationDisplayNames verifies that
// search-result rows replace raw room/group/DM nanoids with the
// resolver-supplied display names. Pre-fix the View used `r.Room`
// / `r.Group` / `r.DM` directly, leaking nanoids like
// `room_qULlOdCvXErOTI9uuFMAf` into the rendered output (user-
// reported 2026-05-08).
func TestSearchView_ResolvesConversationDisplayNames(t *testing.T) {
	s := NewSearch()
	s.Show()
	s.resolveName = func(id string) string {
		if id == "usr_admin" {
			return "admin"
		}
		return id
	}
	s.resolveRoomName = func(id string) string {
		if id == "room_abc123" {
			return "general"
		}
		return ""
	}
	s.resolveGroupName = func(id string) string {
		if id == "group_xyz" {
			return "Project Alpha"
		}
		return ""
	}
	s.resolveDMName = func(id string) string {
		if id == "dm_pq" {
			return "bob"
		}
		return ""
	}
	s.results = []store.StoredMessage{
		{ID: "m1", Sender: "usr_admin", Body: "test", TS: 1715000000, Room: "room_abc123"},
		{ID: "m2", Sender: "usr_admin", Body: "test", TS: 1715000000, Group: "group_xyz"},
		{ID: "m3", Sender: "usr_admin", Body: "test", TS: 1715000000, DM: "dm_pq"},
	}

	view := s.View(120, 30)

	// Display names should appear.
	for _, want := range []string{"general", "Project Alpha", "bob"} {
		if !strings.Contains(view, want) {
			t.Errorf("view should contain display name %q, got:\n%s", want, view)
		}
	}
	// Raw nanoids should NOT appear in the rendered location segment.
	for _, leaked := range []string{"room_abc123", "group_xyz", "dm_pq"} {
		if strings.Contains(view, leaked) {
			t.Errorf("view should NOT contain raw nanoid %q, got:\n%s", leaked, view)
		}
	}
}

// TestSearchView_FallsBackToNanoidWhenResolverEmpty verifies the
// belt-and-suspenders fallback path: if a resolver is wired but
// returns "" for an unrecognized ID (e.g. a DM with a now-retired
// other-user where the display-name lookup misses), the rendered
// row falls back to the nanoid rather than producing an empty
// location segment.
func TestSearchView_FallsBackToNanoidWhenResolverEmpty(t *testing.T) {
	s := NewSearch()
	s.Show()
	s.resolveName = func(id string) string { return id }
	s.resolveRoomName = func(string) string { return "" } // always misses

	s.results = []store.StoredMessage{
		{ID: "m1", Sender: "u1", Body: "hi", TS: 1715000000, Room: "room_unknown"},
	}

	view := s.View(120, 30)
	if !strings.Contains(view, "room_unknown") {
		t.Errorf("empty resolver should leave nanoid in view, got:\n%s", view)
	}
}
