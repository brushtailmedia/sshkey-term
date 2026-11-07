//go:build darwin

package main

import (
	"encoding/binary"
	"syscall"
)

// physicalMemoryBytes reads hw.memsize. The standard library exposes the
// raw sysctl value as a string of little-endian bytes and trims one
// trailing NUL, so a 64-bit value whose top byte is zero (every real RAM
// size) comes back as seven bytes; pad to eight before decoding. Kept on
// the standard library rather than x/sys/unix so the step adds no direct
// dependency.
func physicalMemoryBytes() (int64, bool) {
	raw, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(raw) == 0 || len(raw) > 8 {
		return 0, false
	}
	var buf [8]byte
	copy(buf[:], raw)
	v := binary.LittleEndian.Uint64(buf[:])
	if v == 0 || v > 1<<62 {
		return 0, false
	}
	return int64(v), true
}
