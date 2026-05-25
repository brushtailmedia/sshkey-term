package protocol

import (
	"encoding/json"
	"testing"
)

// TestPendingKeysList_UnmarshalsNewFields guards the term protocol mirror
// against server/term wire drift: the server's pending_keys_list entries carry
// `pubkey` and `requested_username` (DP8(b)), and admin_notify carries
// `requested_username`. If a future struct edit drops a json tag, these
// assertions fail.
func TestPendingKeysList_UnmarshalsNewFields(t *testing.T) {
	raw := `{
		"type": "pending_keys_list",
		"keys": [
			{
				"fingerprint": "SHA256:abc",
				"attempts": 3,
				"first_seen": "2026-05-25T10:00:00Z",
				"last_seen": "2026-05-25T11:00:00Z",
				"pubkey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST alice",
				"requested_username": "Alice"
			}
		]
	}`

	var list PendingKeysList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("unmarshal pending_keys_list: %v", err)
	}
	if len(list.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(list.Keys))
	}
	k := list.Keys[0]
	if k.PubKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST alice" {
		t.Errorf("PubKey = %q, want the full authorized-keys line", k.PubKey)
	}
	if k.RequestedUsername != "Alice" {
		t.Errorf("RequestedUsername = %q, want Alice", k.RequestedUsername)
	}
	if k.Fingerprint != "SHA256:abc" || k.Attempts != 3 {
		t.Errorf("base fields drifted: fp=%q attempts=%d", k.Fingerprint, k.Attempts)
	}
}

// A legacy row with no pubkey/requested_username must still decode (empty
// strings), since the server may omit them for keys recorded before the
// columns existed.
func TestPendingKeyEntry_LegacyOmittedFields(t *testing.T) {
	raw := `{"fingerprint":"SHA256:legacy","attempts":1,"first_seen":"t","last_seen":"t"}`
	var k PendingKeyEntry
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		t.Fatalf("unmarshal legacy entry: %v", err)
	}
	if k.PubKey != "" || k.RequestedUsername != "" {
		t.Errorf("legacy entry should leave new fields empty, got pubkey=%q req=%q", k.PubKey, k.RequestedUsername)
	}
}

func TestAdminNotify_UnmarshalsRequestedUsername(t *testing.T) {
	raw := `{"type":"admin_notify","event":"pending_key","fingerprint":"SHA256:xx","attempts":1,"first_seen":"t","requested_username":"Bob"}`
	var n AdminNotify
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("unmarshal admin_notify: %v", err)
	}
	if n.RequestedUsername != "Bob" {
		t.Errorf("RequestedUsername = %q, want Bob", n.RequestedUsername)
	}
}
