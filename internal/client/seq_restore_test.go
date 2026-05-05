package client

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// TestNextSeqLocked_RestoresFromHighWaterAfterRestart pins the
// 2026-05-05 fix for the "possible replay detected (seq=1,
// high_water=N)" warning that fired on every send after a client
// restart.
//
// Pre-fix: seqCounters is a map[string]int64 in-memory only,
// initialized to {} on every New(). After 54 messages in session 1
// (high_water=54 in the encrypted DB), session 2 would start from 0,
// send seq=1, the server would broadcast it back, and the client's
// own checkReplay would see seq=1 ≤ high_water=54 and DROP its own
// message + emit a noisy WARN.
//
// Fix: nextSeqLocked lazily restores the in-memory counter from the
// persisted high-water on the first send to a target this session.
func TestNextSeqLocked_RestoresFromHighWaterAfterRestart(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()

	// Seed the store as if a previous session had advanced our
	// high-water for room "general" to 54.
	const me = "usr_alice"
	const myDevice = "dev_alice_laptop"
	const room = "general"
	markKey := me + ":" + myDevice + ":" + room
	if err := st.StoreSeqMark(markKey, 54); err != nil {
		t.Fatalf("seed seq mark: %v", err)
	}

	// Fresh client — simulates a restart. seqCounters is empty.
	c := New(Config{DeviceID: myDevice})
	c.store = st
	c.userID = me

	c.mu.Lock()
	got := c.nextSeqLocked("room:"+room, room)
	c.mu.Unlock()

	if got != 55 {
		t.Errorf("first send after restart: nextSeqLocked = %d, want 55 (high_water 54 + 1)", got)
	}

	// Subsequent sends in the same session don't re-read the store
	// (counter is now non-zero) — verify monotonic increment.
	c.mu.Lock()
	got2 := c.nextSeqLocked("room:"+room, room)
	c.mu.Unlock()
	if got2 != 56 {
		t.Errorf("second send: nextSeqLocked = %d, want 56", got2)
	}
}

// TestNextSeqLocked_NoStoreFallsBackToZeroBased verifies the
// degraded-store path still works (counter starts from 0) — relevant
// for tests that don't set up a store, and for the brief window
// between New() and the connect handshake.
func TestNextSeqLocked_NoStoreFallsBackToZeroBased(t *testing.T) {
	c := New(Config{DeviceID: "dev_x"})
	// no store, no userID

	c.mu.Lock()
	got := c.nextSeqLocked("room:general", "general")
	c.mu.Unlock()

	if got != 1 {
		t.Errorf("no-store first send: nextSeqLocked = %d, want 1", got)
	}
}

// TestNextSeqLocked_NoUserIDFallsBackToZeroBased covers the window
// between New() and the welcome message — c.userID is empty, so the
// markKey would be malformed if we tried to query the store. The
// helper must treat that as "no persisted state" and start fresh.
func TestNextSeqLocked_NoUserIDFallsBackToZeroBased(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()

	c := New(Config{DeviceID: "dev_x"})
	c.store = st
	// userID intentionally empty — pre-handshake state.

	c.mu.Lock()
	got := c.nextSeqLocked("room:general", "general")
	c.mu.Unlock()

	if got != 1 {
		t.Errorf("pre-handshake first send: nextSeqLocked = %d, want 1", got)
	}
}

// TestNextSeqLocked_DistinctTargetsHaveSeparateCounters ensures the
// lazy-restore key namespacing is correct — sending to room "general"
// must not advance the counter for room "support".
func TestNextSeqLocked_DistinctTargetsHaveSeparateCounters(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()

	const me = "usr_alice"
	const myDevice = "dev_alice"
	st.StoreSeqMark(me+":"+myDevice+":general", 100)
	st.StoreSeqMark(me+":"+myDevice+":support", 7)

	c := New(Config{DeviceID: myDevice})
	c.store = st
	c.userID = me

	c.mu.Lock()
	got1 := c.nextSeqLocked("room:general", "general")
	got2 := c.nextSeqLocked("room:support", "support")
	c.mu.Unlock()

	if got1 != 101 {
		t.Errorf("room:general nextSeq = %d, want 101", got1)
	}
	if got2 != 8 {
		t.Errorf("room:support nextSeq = %d, want 8", got2)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}
