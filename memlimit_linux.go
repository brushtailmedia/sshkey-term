//go:build linux

package main

import "os"

// physicalMemoryBytes reads MemTotal from /proc/meminfo.
func physicalMemoryBytes() (int64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	return parseMemTotal(f)
}
