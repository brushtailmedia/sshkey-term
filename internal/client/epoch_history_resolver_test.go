package client

// F7 Phase D — RoomEpochKeyForHistory resolver tests. This is the
// security-critical gate that keeps skip-verified sync/history keys out of
// current/live decryption (the "no shadow reader" guarantee on the
// sync/history channel) while still allowing genuinely-historical scrollback.

import "testing"

const hrRoom = "rm_general"

// adopted always wins for the same (room, epoch), even when a history-only key
// also exists and the epoch is historical.
func TestRoomEpochKeyForHistory_AdoptedWins(t *testing.T) {
	h := newEditVerifyHarness(t)
	adoptedKey := []byte("adopted-key")
	historyKey := []byte("history-key")

	h.alice.mu.Lock()
	h.alice.epochKeys[hrRoom] = map[int64][]byte{4: adoptedKey}
	h.alice.historyEpochKeys[hrRoom] = map[int64][]byte{4: historyKey}
	h.alice.currentEpoch[hrRoom] = 10 // epoch 4 is historical, but also adopted
	h.alice.mu.Unlock()

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 4); string(got) != string(adoptedKey) {
		t.Errorf("adopted must win: got %q, want %q", got, adoptedKey)
	}
}

// The strict gate blocks a history-only key for the current or a future epoch —
// this is the shadow-reader case: a sync-delivered current-epoch key must never
// decrypt current/live traffic.
func TestRoomEpochKeyForHistory_GateBlocksCurrentAndFuture(t *testing.T) {
	h := newEditVerifyHarness(t)
	historyKey := []byte("history-key")

	h.alice.mu.Lock()
	h.alice.historyEpochKeys[hrRoom] = map[int64][]byte{5: historyKey, 6: historyKey}
	h.alice.currentEpoch[hrRoom] = 5
	h.alice.mu.Unlock()

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 5); got != nil {
		t.Errorf("history key for the current epoch must be blocked; got %d bytes", len(got))
	}
	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 6); got != nil {
		t.Errorf("history key for a future epoch must be blocked; got %d bytes", len(got))
	}
}

// With no known currentEpoch (reads as 0), nothing is "historical" yet, so even
// an in-memory history key is unusable. This is the exact locking-test scenario
// (sync key for epoch N arrives before any adopted key advances currentEpoch).
func TestRoomEpochKeyForHistory_UnknownCurrentEpochBlocks(t *testing.T) {
	h := newEditVerifyHarness(t)
	historyKey := []byte("history-key")

	h.alice.mu.Lock()
	h.alice.historyEpochKeys[hrRoom] = map[int64][]byte{5: historyKey}
	h.alice.mu.Unlock() // no currentEpoch[hrRoom] entry → 0

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 5); got != nil {
		t.Errorf("with unknown currentEpoch (0), epoch 5 must be blocked; got %d bytes", len(got))
	}
}

// Once an adopted key has advanced currentEpoch past N, the history-only key for
// epoch N becomes usable for scrollback.
func TestRoomEpochKeyForHistory_HistoricalUsableOncePast(t *testing.T) {
	h := newEditVerifyHarness(t)
	historyKey := []byte("history-key")

	h.alice.mu.Lock()
	h.alice.historyEpochKeys[hrRoom] = map[int64][]byte{2: historyKey}
	h.alice.currentEpoch[hrRoom] = 5 // 2 < 5 → genuinely historical
	h.alice.mu.Unlock()

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 2); string(got) != string(historyKey) {
		t.Errorf("historical key (epoch 2 < currentEpoch 5) must resolve; got %q", got)
	}
}

// A historical key present only in the historical_epoch_keys TABLE (not in
// memory) is lazy-loaded and cached.
func TestRoomEpochKeyForHistory_LazyLoadsFromHistoryTable(t *testing.T) {
	h := newEditVerifyHarness(t)
	historyKey := []byte("history-key")

	if err := h.alice.store.StoreHistoricalEpochKey(hrRoom, 2, historyKey); err != nil {
		t.Fatalf("StoreHistoricalEpochKey: %v", err)
	}
	h.alice.mu.Lock()
	h.alice.currentEpoch[hrRoom] = 5
	h.alice.mu.Unlock()

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 2); string(got) != string(historyKey) {
		t.Errorf("resolver must lazy-load historical key from DB; got %q", got)
	}
	h.alice.mu.RLock()
	cached := h.alice.historyEpochKeys[hrRoom][2]
	h.alice.mu.RUnlock()
	if string(cached) != string(historyKey) {
		t.Errorf("lazy-load should populate the in-memory history cache")
	}
}

// The gate is checked BEFORE the DB lazy-load: a current-epoch history key in
// the table is never even read, let alone returned.
func TestRoomEpochKeyForHistory_LazyLoadBlockedByGate(t *testing.T) {
	h := newEditVerifyHarness(t)
	historyKey := []byte("history-key")

	if err := h.alice.store.StoreHistoricalEpochKey(hrRoom, 5, historyKey); err != nil {
		t.Fatalf("StoreHistoricalEpochKey: %v", err)
	}
	h.alice.mu.Lock()
	h.alice.currentEpoch[hrRoom] = 5
	h.alice.mu.Unlock()

	if got := h.alice.RoomEpochKeyForHistory(hrRoom, 5); got != nil {
		t.Errorf("gate must block a current-epoch history key even from the DB; got %d bytes", len(got))
	}
}
