package client

// Offline-browse Step 2 (offline-startup-browse R2/R3/R18): a local-only
// client (OpenLocal, never Connect) must report disconnected, HasLocalCache
// must track last_synced, and every auto-sender must stay dormant — never
// touching the nil encoder, never marking queue entries sending.

import (
	"encoding/json"
	"testing"
)

func TestLocalOnlyClient_IsNotConnected(t *testing.T) {
	c := newLocalOnlyTestClient(t)
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	if c.IsConnected() {
		t.Fatal("local-only client must not report connected")
	}
}

func TestHasLocalCache_TracksLastSynced(t *testing.T) {
	keyPath := writeTestKeyFile(t)
	dataDir := t.TempDir()

	// First open: a fresh store has never synced → no cache.
	first := New(Config{KeyPath: keyPath, DataDir: dataDir})
	if err := first.OpenLocal(); err != nil {
		t.Fatalf("first OpenLocal: %v", err)
	}
	if first.HasLocalCache() {
		t.Fatal("fresh store must not count as cache (no last_synced)")
	}
	// Simulate a completed sync, then release the handle.
	if err := first.store.SetState("last_synced", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second open on the same key + dir hydrates last_synced → cache present.
	second := New(Config{KeyPath: keyPath, DataDir: dataDir})
	t.Cleanup(func() {
		if second.store != nil {
			second.store.Close()
		}
	})
	if err := second.OpenLocal(); err != nil {
		t.Fatalf("second OpenLocal: %v", err)
	}
	if !second.HasLocalCache() {
		t.Fatal("store with last_synced must count as cache")
	}
	if second.IsConnected() {
		t.Fatal("OpenLocal must never flip connected")
	}
}

func TestLocalOnlyClient_AutoSendersStayDormant(t *testing.T) {
	c := newLocalOnlyTestClient(t)
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	// c.enc is nil on a local-only client: any path that reaches it panics.
	if err := c.SendTyping("room_x", "", ""); err != nil {
		t.Fatalf("SendTyping offline must be a silent no-op, got %v", err)
	}
	if err := c.SendRead("room_x", "", "", "msg_1"); err != nil {
		t.Fatalf("SendRead offline must be a silent no-op, got %v", err)
	}

	c.sendQueue.EnqueueWithID("corr_1", "send", map[string]string{"type": "send"})
	c.driverTick()
	if got := c.sendQueue.Get("corr_1").State; got != StatePending {
		t.Fatalf("driverTick offline must leave entries pending, got state %v", got)
	}
	c.TriggerEpochRetry("room_x")
	if got := c.sendQueue.Get("corr_1").State; got != StatePending {
		t.Fatalf("TriggerEpochRetry offline must leave entries pending, got state %v", got)
	}

	raw, err := json.Marshal(map[string]any{
		"type":      "epoch_trigger",
		"room":      "room_x",
		"new_epoch": 2,
		"members":   []map[string]string{{"user": "usr_a", "pubkey": "ssh-ed25519 AAAA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.handleEpochTrigger(raw) // must return at the guard, before any encode
}
