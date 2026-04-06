package tui

import (
	"testing"
	"time"
)

func TestReconnectDelay_Exponential(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second}, // capped
		{7, 30 * time.Second}, // stays capped
		{10, 30 * time.Second},
		{100, 30 * time.Second},
	}

	for _, tc := range tests {
		got := reconnectDelay(tc.attempt)
		if got != tc.want {
			t.Errorf("reconnectDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestReconnectDelay_FirstAttemptIsQuick(t *testing.T) {
	d := reconnectDelay(1)
	if d > 2*time.Second {
		t.Errorf("first attempt should be quick, got %v", d)
	}
}
