//go:build !darwin && !linux

package main

// physicalMemoryBytes is unknown on this platform; the soft memory limit
// is skipped silently (PLAN step 1b).
func physicalMemoryBytes() (int64, bool) {
	return 0, false
}
