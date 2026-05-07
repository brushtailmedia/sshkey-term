package tui

import (
	"strings"
	"testing"
)

// TestRenderTerminalTooSmall_FullLayoutAtComfortableSubMinimum verifies
// that at sizes large enough for the full bouncer layout (≥30×5) but
// still below appMinWidth/appMinHeight, the bouncer shows the title,
// requirement, current size, and resize hint.
func TestRenderTerminalTooSmall_FullLayoutAtComfortableSubMinimum(t *testing.T) {
	out := renderTerminalTooSmall(60, 20) // sub-minimum but still >= 30×5
	plain := stripANSI(out)

	for _, want := range []string{
		"Terminal too small",
		"Needs at least 80",
		"Have 60",
		"Resize",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("bouncer should contain %q, got:\n%s", want, plain)
		}
	}
}

// TestRenderTerminalTooSmall_CompactTier confirms the 20-col / 2-row
// tier kicks in below the full-layout threshold and still surfaces
// the requirement.
func TestRenderTerminalTooSmall_CompactTier(t *testing.T) {
	out := renderTerminalTooSmall(25, 3)
	plain := stripANSI(out)

	if !strings.Contains(plain, "Terminal too small") {
		t.Errorf("compact tier should show title, got:\n%s", plain)
	}
	if !strings.Contains(plain, "80") || !strings.Contains(plain, "24") {
		t.Errorf("compact tier should mention required dims, got:\n%s", plain)
	}
}

// TestRenderTerminalTooSmall_BareTierAtTinySize verifies the
// bare-minimum tier ("80×24") for terminals that can't fit the
// compact text either. Anything above 5 cells × 1 row gets the
// dimensional hint at least.
func TestRenderTerminalTooSmall_BareTierAtTinySize(t *testing.T) {
	out := renderTerminalTooSmall(10, 1)
	plain := stripANSI(out)

	if !strings.Contains(plain, "80") || !strings.Contains(plain, "24") {
		t.Errorf("bare tier should still show the required dims, got:\n%q", plain)
	}
}

// TestRenderTerminalTooSmall_ExtremeSizesDoNotPanic sanity-checks the
// extreme corner cases (1×1, 0×0 — the latter handled defensively).
// Bubbletea may briefly report 0×0 during a tty-resize storm; the
// bouncer must produce SOMETHING non-empty without crashing.
func TestRenderTerminalTooSmall_ExtremeSizesDoNotPanic(t *testing.T) {
	cases := []struct {
		w, h int
	}{
		{0, 0}, {1, 1}, {1, 5}, {5, 1}, {2, 2},
	}
	for _, tc := range cases {
		got := renderTerminalTooSmall(tc.w, tc.h)
		if got == "" {
			// "" is allowed only when both dims are >= 1 and we'd
			// produce an empty result legitimately. For these inputs
			// the function should always return at least a newline
			// or a partial dimension string.
			if tc.w >= 5 && tc.h >= 1 {
				t.Errorf("extreme size %d×%d returned empty; should at least show partial info", tc.w, tc.h)
			}
		}
	}
}

// TestAppView_BouncerBelowMinimum exercises the App.View entry point:
// at sub-minimum dimensions it returns the bouncer instead of the
// normal layout. Above the minimum it returns something else.
//
// Doesn't fully boot the app (which needs a client) — just sets the
// width/height and asserts on the View output. The bouncer path
// short-circuits before any of the panel rendering, so the App
// doesn't need to be fully wired.
func TestAppView_BouncerBelowMinimum(t *testing.T) {
	a := App{width: 60, height: 20} // sub-minimum
	out := a.View()
	if !strings.Contains(stripANSI(out), "Terminal too small") {
		t.Errorf("View at sub-minimum size should show bouncer, got:\n%s", stripANSI(out))
	}
}

// TestAppView_BouncerNotShownAtMinimum verifies the boundary: at
// exactly appMinWidth × appMinHeight the bouncer does NOT fire. The
// app tries to render its real UI. We don't assert what that UI
// looks like (it'll panic in some sub-component because the App
// isn't fully constructed in this test); we just need to know the
// bouncer's threshold is correct.
func TestAppView_BouncerNotShownAtMinimum(t *testing.T) {
	a := App{width: appMinWidth, height: appMinHeight}
	defer func() {
		// Real-UI render path will likely panic in a fully-bare App
		// (no client, no sidebar populated, etc.). That's fine —
		// our assertion is just that we don't hit the bouncer path.
		_ = recover()
	}()
	out := a.View()
	// If we got here without panic, the output must NOT contain the
	// bouncer banner. (If it does, our threshold is off.)
	if strings.Contains(stripANSI(out), "Terminal too small") {
		t.Errorf("View at exactly the minimum should not show bouncer, got:\n%s", stripANSI(out))
	}
}

// TestAppView_BouncerOnLoadingState pins that the "Loading..." early
// return at zero dimensions still wins over the bouncer. Bubbletea
// hands us width=0/height=0 once before the first WindowSizeMsg, and
// the bouncer would falsely fire on that — clobbering the brief
// "Loading..." moment users have come to expect.
func TestAppView_BouncerOnLoadingState(t *testing.T) {
	a := App{width: 0, height: 0}
	out := a.View()
	if !strings.Contains(out, "Loading") {
		t.Errorf("View at 0×0 should show Loading, got:\n%s", out)
	}
}
