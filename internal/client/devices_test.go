package client

import (
	"path/filepath"
	"testing"

	"github.com/brushtailmedia/sshkey-term/internal/protocol"
	"github.com/brushtailmedia/sshkey-term/internal/store"
)

// Tests for the shadow-device transparency Tier 1 client half (devices.go):
// the known-set diff that decides which devices are "new to us" and thus worth
// alerting. The first reconcile on a fresh client must seed SILENTLY (no
// alarm-storm on an existing multi-device account); thereafter only genuinely
// new, non-current devices are surfaced, exactly once.

func newDeviceTestClient(t *testing.T) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.OpenUnencrypted(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	c := New(Config{})
	c.store = st
	return c
}

func di(id string, current bool) protocol.DeviceInfo {
	return protocol.DeviceInfo{DeviceID: id, Current: current, CreatedAt: "2026-05-31T00:00:00Z"}
}

func devIDs(ds []protocol.DeviceInfo) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.DeviceID
	}
	return out
}

func TestReconcileDevices_FirstRunSeedsSilently(t *testing.T) {
	c := newDeviceTestClient(t)
	got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true), di("dev_b", false)})
	if len(got) != 0 {
		t.Errorf("first run should seed silently, got alerts for %v", devIDs(got))
	}
	// Now seeded: a re-list with the same devices returns nothing.
	if got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true), di("dev_b", false)}); len(got) != 0 {
		t.Errorf("re-list of known devices should be silent, got %v", devIDs(got))
	}
}

func TestReconcileDevices_DetectsNewDevice(t *testing.T) {
	c := newDeviceTestClient(t)
	c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true)}) // seed
	got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true), di("dev_intruder", false)})
	if len(got) != 1 || got[0].DeviceID != "dev_intruder" {
		t.Fatalf("expected [dev_intruder], got %v", devIDs(got))
	}
	// Idempotent: a second reconcile no longer alerts.
	if got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true), di("dev_intruder", false)}); len(got) != 0 {
		t.Errorf("intruder already seen, should be silent, got %v", devIDs(got))
	}
}

func TestReconcileDevices_NeverAlertsCurrentDevice(t *testing.T) {
	c := newDeviceTestClient(t)
	c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", false)}) // seed without a current entry
	// A new device flagged Current (our own device re-listed) must not alert.
	got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", false), di("dev_self", true)})
	if len(got) != 0 {
		t.Errorf("current device must never alert, got %v", devIDs(got))
	}
}

func TestNoteAddedDevice_DedupsWhenSeeded(t *testing.T) {
	c := newDeviceTestClient(t)
	c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true)}) // seed
	if !c.NoteAddedDevice("dev_push") {
		t.Error("first push of dev_push should alert")
	}
	if c.NoteAddedDevice("dev_push") {
		t.Error("second push of dev_push should be deduped (already known)")
	}
}

func TestNoteAddedDevice_NotSeededAlertsButDefersSeeding(t *testing.T) {
	c := newDeviceTestClient(t)
	if !c.NoteAddedDevice("dev_push") {
		t.Error("push on a fresh client should still alert (server says new)")
	}
	// It must NOT have seeded the set — otherwise the next reconcile would
	// alarm on every pre-existing device. Reconcile must still seed silently.
	got := c.ReconcileDevices([]protocol.DeviceInfo{di("dev_a", true), di("dev_b", false), di("dev_push", false)})
	if len(got) != 0 {
		t.Errorf("fresh reconcile after a not-seeded push must seed silently, got %v", devIDs(got))
	}
}

func TestNoteAddedDevice_EmptyID(t *testing.T) {
	c := newDeviceTestClient(t)
	if c.NoteAddedDevice("") {
		t.Error("empty device id should not alert")
	}
}
