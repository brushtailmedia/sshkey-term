package tui

// Phase 17c Step 5 polish — quit-confirmation pending-send warning tests.

import (
	"strings"
	"testing"
)

func TestQuitConfirm_NoPendingSends_NoWarning(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("server_a", 0)

	view := q.View(80)
	if strings.Contains(view, "still sending") {
		t.Errorf("warning shown when pendingSend=0: %q", view)
	}
	if !strings.Contains(view, "Disconnect from server_a") {
		t.Errorf("base dialog not rendered: %q", view)
	}
}

func TestQuitConfirm_SinglePending_SingularNoun(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("server_a", 1)

	view := q.View(80)
	if !strings.Contains(view, "1 message still sending") {
		t.Errorf("expected singular 'message' for N=1, got: %q", view)
	}
}

func TestQuitConfirm_MultiplePending_PluralNoun(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("server_a", 3)

	view := q.View(80)
	if !strings.Contains(view, "3 messages still sending") {
		t.Errorf("expected plural 'messages' for N=3, got: %q", view)
	}
}

func TestQuitConfirm_LargeCount_RendersDecimal(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("server_a", 128)

	view := q.View(80)
	if !strings.Contains(view, "128 messages") {
		t.Errorf("large count not rendered correctly: %q", view)
	}
}

func TestQuitConfirm_Hide(t *testing.T) {
	var q QuitConfirmModel
	q.ShowWithPending("server_a", 5)
	if !q.IsVisible() {
		t.Error("should be visible after Show")
	}
	q.Hide()
	if q.IsVisible() {
		t.Error("should be hidden after Hide")
	}
	if view := q.View(80); view != "" {
		t.Errorf("hidden dialog rendered non-empty: %q", view)
	}
}

func TestFmtInt(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{42, "42"},
		{1000, "1000"},
		{-1, "-1"},
		{-42, "-42"},
	}
	for _, tc := range cases {
		if got := fmtInt(tc.n); got != tc.want {
			t.Errorf("fmtInt(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
