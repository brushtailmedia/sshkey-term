package tui

import (
	"strings"
	"testing"
)

// Regression tests for the history hint-state model (history-state-model.md
// "Final Model"): the overloaded hasMore boolean is replaced by an explicit
// remoteState + hintVisible + probeDone (loadingHistory stays as "Loading").
// These lock the transitions that fix the original bug — a fresh context no
// longer shows a confident "press up to load more history" hint before there
// is any evidence older history exists.

func TestHistoryHint_NewContextShowsNoHint(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 10) // messages present, but remote history unproven

	if m.remoteState != HistoryUnknown {
		t.Errorf("new context remoteState = %d, want HistoryUnknown", m.remoteState)
	}
	if m.hintVisible || m.probeDone || m.loadingHistory {
		t.Errorf("new context must reset state: hintVisible=%v probeDone=%v loading=%v",
			m.hintVisible, m.probeDone, m.loadingHistory)
	}
	if m.shouldShowHistoryHint() {
		t.Error("fresh context with unproven remote history must not show the hint")
	}
}

func TestHistoryHint_ServerHasMoreShowsHint(t *testing.T) {
	m := NewMessages()
	seedMessages(&m, 10)
	m.loadingHistory = true
	m.markServerHistoryResult(true)

	if m.remoteState != HistoryAvailable || !m.hintVisible || m.loadingHistory {
		t.Errorf("has_more=true: remoteState=%d hintVisible=%v loading=%v, want Available/true/false",
			m.remoteState, m.hintVisible, m.loadingHistory)
	}
	if !m.shouldShowHistoryHint() {
		t.Error("server has_more=true must show the hint")
	}
}

func TestHistoryHint_ServerExhaustedHidesHint(t *testing.T) {
	m := NewMessages()
	seedMessages(&m, 10)
	m.loadingHistory = true
	m.markServerHistoryResult(false)

	if m.remoteState != HistoryExhausted || m.hintVisible || m.loadingHistory {
		t.Errorf("has_more=false: remoteState=%d hintVisible=%v loading=%v, want Exhausted/false/false",
			m.remoteState, m.hintVisible, m.loadingHistory)
	}
	if m.shouldShowHistoryHint() {
		t.Error("server exhausted must hide the hint")
	}
}

// Render is authoritative on exhaustion: even if a later count-based reload
// re-raised hintVisible, a proven-exhausted context never re-shows the hint.
func TestHistoryHint_ExhaustedIsRenderAuthoritative(t *testing.T) {
	m := NewMessages()
	seedMessages(&m, 10)
	m.markServerHistoryResult(false) // Exhausted, hintVisible=false
	m.hintVisible = true             // simulate a count-based reload re-raising it

	if m.shouldShowHistoryHint() {
		t.Error("exhausted context must not show the hint even if hintVisible is re-raised")
	}
}

func TestHistoryHint_LocalFullPageStopsAndShows(t *testing.T) {
	m := NewMessages()
	m.loadingHistory = true
	m.markLocalHistoryPage(historyPageLimit, historyPageLimit) // full page

	if m.loadingHistory {
		t.Error("a full local page must clear Loading (load stops here)")
	}
	if !m.hintVisible {
		t.Error("a full local page implies more local pages — hint should show")
	}
}

func TestHistoryHint_LocalShortPageKeepsLoading(t *testing.T) {
	m := NewMessages()
	m.loadingHistory = true
	m.markLocalHistoryPage(historyPageLimit-1, historyPageLimit) // short page

	if !m.loadingHistory {
		t.Error("a short local page must keep Loading set so the server continuation is one logical load (no double-fire)")
	}
}

// The quiet-probe budget: when the hint is hidden (small local conversation,
// remote unknown), exactly one server probe is allowed per context.
func TestHistoryHint_QuietProbeSpentOnce(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 5) // small conversation, hint hidden

	cmd := m.requestHistory()
	if cmd == nil {
		t.Fatal("first scroll-to-top on a hidden-hint context must fire one quiet probe")
	}
	if !m.loadingHistory || !m.probeDone {
		t.Errorf("probe must set loading + probeDone: loading=%v probeDone=%v", m.loadingHistory, m.probeDone)
	}

	// Simulate the probe completing without proving exhaustion (e.g. dropped
	// or a non-exhausting result) by clearing Loading only.
	m.loadingHistory = false
	if cmd := m.requestHistory(); cmd != nil {
		t.Error("a second scroll-to-top with the probe already spent must not re-probe")
	}
}

// When the hint is visible (large conversation / server said more exists),
// repeated top scrolls keep loading — the probe budget does not apply.
func TestHistoryHint_VisibleHintAllowsRepeatLoads(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_support", "", "")
	seedMessages(&m, 10)
	m.hintVisible = true
	m.probeDone = true // irrelevant when the hint is visible

	if cmd := m.requestHistory(); cmd == nil {
		t.Error("with the hint visible, scroll-to-top must keep loading regardless of probeDone")
	}
}

func TestHistoryHint_ExhaustedBlocksRequest(t *testing.T) {
	m := NewMessages()
	seedMessages(&m, 10)
	m.remoteState = HistoryExhausted

	if cmd := m.requestHistory(); cmd != nil {
		t.Error("exhausted remote history must not request more")
	}
}

// TestHistoryHint_QuietProbeRendersNoLoadingBanner locks the option-A polish:
// a speculative quiet probe (loadingHistory while remote history is unproven and
// the hint hidden) must NOT render the "loading history" banner. Otherwise a
// small conversation flashes the indicator (and a one-row layout shift) for the
// single server round-trip before the server confirms exhaustion. The banner
// renders only once there is evidence of more history (hintVisible).
func TestHistoryHint_QuietProbeRendersNoLoadingBanner(t *testing.T) {
	m := NewMessages()
	m.SetContext("room_x", "", "")
	seedMessages(&m, 5)

	// Speculative probe in flight: loading, but no evidence of more history.
	m.loadingHistory = true
	m.hintVisible = false
	m.remoteState = HistoryUnknown
	content, _ := m.buildContent(80)
	if strings.Contains(content, "loading history") {
		t.Error("speculative quiet probe must not render the loading-history banner (the flash)")
	}

	// Known load (full local window or server has_more → hintVisible): banner is
	// appropriate and must render.
	m.hintVisible = true
	content, _ = m.buildContent(80)
	if !strings.Contains(content, "loading history") {
		t.Error("a load with hintVisible must render the loading-history banner")
	}
}
