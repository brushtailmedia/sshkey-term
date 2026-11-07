package main

import (
	"bufio"
	"io"
	"runtime/debug"
	"strconv"
	"strings"
)

// applyMemoryLimit sets Go's soft memory limit to percent of physical RAM
// (attachments v2 PLAN step 1b, gate T13). A soft limit does not prevent
// a large allocation — the decode budget does that — but it makes the
// runtime return transient decode memory to the OS promptly instead of
// leaving RSS high for minutes after a preview. Skipped silently when the
// percentage is non-positive or physical RAM cannot be determined on this
// platform; percentages above 100 are clamped. Returns the limit it set,
// or 0 when it set none.
func applyMemoryLimit(percent int) int64 {
	if percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	total, ok := physicalMemoryBytes()
	if !ok || total <= 0 {
		return 0
	}
	limit := total / 100 * int64(percent)
	if limit <= 0 {
		return 0
	}
	debug.SetMemoryLimit(limit)
	return limit
}

// parseMemTotal reads a /proc/meminfo stream and returns MemTotal in
// bytes. Shared by the Linux probe and its test (which runs everywhere).
func parseMemTotal(r io.Reader) (int64, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "MemTotal:"))
		if len(fields) == 0 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb <= 0 {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
