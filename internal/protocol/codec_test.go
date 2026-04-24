package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTypeOf_MirrorFrames(t *testing.T) {
	cases := []struct {
		name     string
		raw      json.RawMessage
		wantType string
		wantErr  string
	}{
		{
			name:     "server_hello",
			raw:      json.RawMessage(`{"type":"server_hello","protocol":"sshkey-chat","version":1}`),
			wantType: "server_hello",
		},
		{
			name:     "message",
			raw:      json.RawMessage(`{"type":"message","id":"msg_1","room":"room_1"}`),
			wantType: "message",
		},
		{
			name:    "missing_type_field",
			raw:     json.RawMessage(`{"id":"msg_1"}`),
			wantErr: "message has no type field",
		},
		{
			name:    "type_not_string",
			raw:     json.RawMessage(`{"type":123}`),
			wantErr: "extract type:",
		},
		{
			name:    "malformed_json",
			raw:     json.RawMessage(`{"type":"message"`),
			wantErr: "extract type:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, err := TypeOf(tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("TypeOf(%s) unexpectedly succeeded with %q", tc.raw, gotType)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("TypeOf(%s) error = %q, want substring %q", tc.raw, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("TypeOf(%s) error = %v", tc.raw, err)
			}
			if gotType != tc.wantType {
				t.Fatalf("TypeOf(%s) = %q, want %q", tc.raw, gotType, tc.wantType)
			}
		})
	}
}
