package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

// TestApplyMemoryLimit_SetsAPercentageOfPhysicalRAM: on a platform where
// physical RAM is known the soft limit becomes that percentage of it;
// where it is not, nothing is set and the runtime's limit is untouched.
func TestApplyMemoryLimit_SetsAPercentageOfPhysicalRAM(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(prev)

	total, ok := physicalMemoryBytes()
	got := applyMemoryLimit(25)
	if !ok {
		if got != 0 || debug.SetMemoryLimit(-1) != prev {
			t.Fatalf("unknown RAM: limit changed (returned %d)", got)
		}
		return
	}
	want := total / 100 * 25
	if got != want {
		t.Fatalf("applyMemoryLimit(25) = %d, want %d (25%% of %d)", got, want, total)
	}
	if now := debug.SetMemoryLimit(-1); now != want {
		t.Fatalf("runtime limit = %d, want %d", now, want)
	}
}

// TestApplyMemoryLimit_NonPositiveIsSkippedAndOver100Clamps pins the two
// edges: zero or negative sets nothing; more than 100 means all of RAM.
func TestApplyMemoryLimit_NonPositiveIsSkippedAndOver100Clamps(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(prev)

	for _, p := range []int{0, -1} {
		if got := applyMemoryLimit(p); got != 0 {
			t.Fatalf("applyMemoryLimit(%d) = %d, want 0", p, got)
		}
		if debug.SetMemoryLimit(-1) != prev {
			t.Fatalf("applyMemoryLimit(%d) changed the runtime limit", p)
		}
	}
	total, ok := physicalMemoryBytes()
	if !ok {
		t.Skip("physical RAM unknown on this platform")
	}
	if got, want := applyMemoryLimit(250), total/100*100; got != want {
		t.Fatalf("applyMemoryLimit(250) = %d, want clamp to %d", got, want)
	}
}

func TestParseMemTotal(t *testing.T) {
	sample := "MemTotal:       16303460 kB\nMemFree:         1234567 kB\nMemAvailable:    9876543 kB\n"
	got, ok := parseMemTotal(strings.NewReader(sample))
	if !ok || got != 16303460*1024 {
		t.Fatalf("parseMemTotal = %d, %v; want %d, true", got, ok, 16303460*1024)
	}
	for name, s := range map[string]string{
		"missing":   "MemFree: 1 kB\n",
		"malformed": "MemTotal: lots kB\n",
		"zero":      "MemTotal: 0 kB\n",
		"empty":     "",
	} {
		if v, ok := parseMemTotal(strings.NewReader(s)); ok {
			t.Errorf("%s: parseMemTotal = %d, true; want false", name, v)
		}
	}
}
