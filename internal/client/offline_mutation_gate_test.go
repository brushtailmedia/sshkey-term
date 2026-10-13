package client

// Offline-browse Step 5 (offline-startup-browse R6): the central mutation
// gate. EVERY exported method that encodes to the server must return
// ErrNotConnected on a local-only client — before touching the nil encoder,
// before any file I/O, before enqueueing anything — and the auto-senders
// (SendRead, SendTyping, driverTick, TriggerEpochRetry, handleEpochTrigger)
// must stay dormant.
//
// R6 regression tripwire: when adding a client method that encodes
// (`c.enc.Encode`), add it to the table below. The hardcoded list IS the
// point — a method missing here is a method that may panic offline.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isNotConnected(err error) bool {
	return err != nil && (errors.Is(err, ErrNotConnected) || strings.Contains(err.Error(), ErrNotConnected.Error()))
}

func TestOfflineMutationGate_EveryEncoderReturnsErrNotConnected(t *testing.T) {
	c := newLocalOnlyTestClient(t)
	if err := c.OpenLocal(); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	if c.IsConnected() {
		t.Fatal("precondition: a local-only client must be disconnected")
	}

	// A path that does not exist proves the upload gates run BEFORE file I/O
	// (otherwise the error would be a read/stat failure, not ErrNotConnected).
	missing := filepath.Join(t.TempDir(), "does-not-exist.bin")
	// The *MessageFile wrappers stat the file first, so they get a real one.
	existing := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(existing, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)

	cases := []struct {
		name string
		call func() error
	}{
		// send.go
		{"SendRoomMessageFull", func() error { return c.SendRoomMessageFull("room_x", "hi", "", nil, nil) }},
		{"SendGroupMessageFull", func() error { return c.SendGroupMessageFull("grp_x", "hi", "", nil, nil) }},
		{"SendDMMessageFull", func() error { return c.SendDMMessageFull("dm_x", "hi", "", nil, nil) }},
		{"SendRoomMessageFile", func() error { return c.SendRoomMessageFile("room_x", "photo.jpg", existing, "", nil) }},
		{"SendGroupMessageFile", func() error { return c.SendGroupMessageFile("grp_x", "photo.jpg", existing, "", nil) }},
		{"SendDMMessageFile", func() error { return c.SendDMMessageFile("dm_x", "photo.jpg", existing, "", nil) }},
		{"SendRoomReaction", func() error { return c.SendRoomReaction("room_x", "msg_1", "👍") }},
		{"SendGroupReaction", func() error { return c.SendGroupReaction("grp_x", "msg_1", "👍") }},
		{"SendDMReaction", func() error { return c.SendDMReaction("dm_x", "msg_1", "👍") }},
		{"SendUnreact", func() error { return c.SendUnreact("react_1", "room_x", "", "") }},
		{"SendRoomUpdate", func() error { return c.SendRoomUpdate("room_x", "topic") }},
		{"SendDelete", func() error { return c.SendDelete("msg_1", "room_x", "", "") }},
		{"CreateGroup", func() error { return c.CreateGroup([]string{"usr_a"}, "name") }},
		{"DeleteGroup", func() error { return c.DeleteGroup("grp_x") }},
		{"DeleteRoom", func() error { return c.DeleteRoom("room_x") }},
		{"CreateDM", func() error { return c.CreateDM("usr_b") }},
		{"AddToGroup", func() error { return c.AddToGroup("grp_x", "usr_a", false) }},
		{"RemoveFromGroup", func() error { return c.RemoveFromGroup("grp_x", "usr_a") }},
		{"PromoteGroupAdmin", func() error { return c.PromoteGroupAdmin("grp_x", "usr_a", false) }},
		{"DemoteGroupAdmin", func() error { return c.DemoteGroupAdmin("grp_x", "usr_a", false) }},
		{"RequestHistory", func() error { return c.RequestHistory("room_x", "", "", "", 50) }},
		{"RequestHistoryWithID", func() error { return c.RequestHistoryWithID("corr_h", "room_x", "", "", "", 50) }},
		{"SendRetireMe", func() error { return c.SendRetireMe("test") }},
		{"SendListDevices", func() error { return c.SendListDevices() }},
		{"SendRevokeDevice", func() error { return c.SendRevokeDevice("dev_x") }},
		{"SendListPendingKeys", func() error { return c.SendListPendingKeys() }},
		{"RequestRoomMembers", func() error { return c.RequestRoomMembers("room_x") }},
		// edit.go
		{"EditRoomMessage", func() error { return c.EditRoomMessage("msg_1", "room_x", "new") }},
		{"EditGroupMessage", func() error { return c.EditGroupMessage("msg_1", "grp_x", "new") }},
		{"EditDMMessage", func() error { return c.EditDMMessage("msg_1", "dm_x", "new") }},
		// filetransfer.go
		{"UploadFile", func() error { _, _, _, err := c.UploadFile(missing, "room_x", ""); return err }},
		{"UploadGroupFile", func() error { _, _, err := c.UploadGroupFile(missing, "grp_x", key); return err }},
		{"UploadDMFile", func() error { _, _, err := c.UploadDMFile(missing, "dm_x", key); return err }},
		{"DownloadFile", func() error { _, err := c.DownloadFile("file_x", key, ""); return err }},
	}

	// One seeded queue entry: nothing below may mark it sending or add to it.
	c.sendQueue.EnqueueWithID("corr_seed", "send", map[string]string{"type": "send"})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call() // a panic here is the bug this test exists to catch
			if !isNotConnected(err) {
				t.Fatalf("%s offline: got %v, want ErrNotConnected", tc.name, err)
			}
		})
	}

	// Auto-senders: silent no-ops, never errors (R18).
	if err := c.SendTyping("room_x", "", ""); err != nil {
		t.Errorf("SendTyping offline must be a silent no-op, got %v", err)
	}
	if err := c.SendRead("room_x", "", "", "msg_1"); err != nil {
		t.Errorf("SendRead offline must be a silent no-op, got %v", err)
	}
	c.driverTick()
	c.TriggerEpochRetry("room_x")
	raw, _ := json.Marshal(map[string]any{"type": "epoch_trigger", "room": "room_x", "new_epoch": 2,
		"members": []map[string]string{{"user": "usr_a", "pubkey": "ssh-ed25519 AAAA"}}})
	c.handleEpochTrigger(raw)

	// The queue is exactly as seeded: no entry marked sending, nothing added.
	if got := c.sendQueue.Get("corr_seed").State; got != StatePending {
		t.Fatalf("seeded entry state = %v, want StatePending (auto-senders must stay dormant)", got)
	}
	if n := c.sendQueue.Len(); n != 1 {
		t.Fatalf("send queue length = %d, want 1: gated methods must never enqueue", n)
	}
}

// The gate must also hold on a client that was never even OpenLocal'd (the
// state a stale/aborted connect leaves behind) — nil store AND nil encoder.
func TestOfflineMutationGate_BareClientNeverPanics(t *testing.T) {
	c := New(Config{})
	if !isNotConnected(c.SendDelete("msg_1", "room_x", "", "")) {
		t.Fatal("bare client must return ErrNotConnected, not panic")
	}
	if err := c.SendTyping("room_x", "", ""); err != nil {
		t.Fatalf("bare client SendTyping must be a silent no-op, got %v", err)
	}
	// And the codec chokepoint itself: a nil encoder reports, never derefs.
	if err := c.Enc().Encode(map[string]string{"type": "x"}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("nil encoder must return a not-connected error, got %v", err)
	}
}
