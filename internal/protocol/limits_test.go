package protocol

import (
	"encoding/json"
	"testing"
)

// The limits block is the server's size rules for one connection; the
// mirror must decode every field and read a missing block as a zero value
// (which the client's handshake then refuses), never as nil.
func TestServerHello_LimitsDecode(t *testing.T) {
	raw := `{"type":"server_hello","protocol":"sshkey-chat","version":1,"server_id":"chat.example.com",` +
		`"limits":{"max_group_members":100,"room_typing_max_members":100,"max_body_bytes":16384,` +
		`"wrapped_key_entry_bytes":160,"max_file_ids_per_message":20}}`
	var h ServerHello
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	want := ServerLimits{MaxGroupMembers: 100, RoomTypingMaxMembers: 100, MaxBodyBytes: 16384, WrappedKeyEntryBytes: 160, MaxFileIDsPerMessage: 20}
	if h.Limits != want {
		t.Fatalf("limits = %+v, want %+v", h.Limits, want)
	}
	if h.ServerID != "chat.example.com" || h.Version != 1 {
		t.Fatalf("hello fields = %+v", h)
	}

	var missing ServerHello
	if err := json.Unmarshal([]byte(`{"type":"server_hello","protocol":"sshkey-chat","version":1,"server_id":"x"}`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Limits != (ServerLimits{}) {
		t.Fatalf("a hello without the block must decode to the zero block, got %+v", missing.Limits)
	}

	var off ServerHello
	if err := json.Unmarshal([]byte(`{"type":"server_hello","limits":{"room_typing_max_members":0,"max_body_bytes":16384}}`), &off); err != nil {
		t.Fatal(err)
	}
	if off.Limits.RoomTypingMaxMembers != 0 || off.Limits.MaxBodyBytes != 16384 {
		t.Fatalf("typing-off hello: limits = %+v", off.Limits)
	}

	// The mirror writes every field, as the server does.
	out, _ := json.Marshal(ServerHello{Type: "server_hello", Limits: want})
	for _, key := range []string{"max_group_members", "room_typing_max_members", "max_body_bytes", "wrapped_key_entry_bytes", "max_file_ids_per_message"} {
		if !contains(string(out), `"`+key+`":`) {
			t.Errorf("encoded hello must carry %s, got %s", key, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestRoomLists_TypingFlagDecode(t *testing.T) {
	var rl RoomList
	err := json.Unmarshal([]byte(`{"type":"room_list","rooms":[`+
		`{"id":"room_a","name":"a","topic":"","members":["usr_x"],"typing":true},`+
		`{"id":"room_b","name":"b","topic":"","members":["usr_x"],"typing":false},`+
		`{"id":"room_c","name":"c","topic":"","members":["usr_x"]}]}`), &rl)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rl.Rooms {
		got[r.ID] = r.Typing
	}
	if !got["room_a"] || got["room_b"] || got["room_c"] {
		t.Fatalf("typing flags = %v, want a=true b=false c=false (absent)", got)
	}

	var ra RoomAddedTo
	if err := json.Unmarshal([]byte(`{"type":"room_added_to","room":"room_a","name":"a","members":["usr_x","usr_y"],"typing":true,"added_by":"os:1"}`), &ra); err != nil {
		t.Fatal(err)
	}
	if !ra.Typing || ra.AddedBy != "os:1" || len(ra.Members) != 2 {
		t.Fatalf("room_added_to = %+v", ra)
	}
}
