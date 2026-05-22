package protocol

import (
	"bytes"
	"fmt"
	"testing"
)

// TestMaxInboundLineBytes_RaisedForRoomList documents the V8 frame-size
// guard: the term client decodes server→client frames (including
// room_list, which now carries full member ID lists), so its cap is
// raised above the server's 1 MiB client-input bound.
func TestMaxInboundLineBytes_RaisedForRoomList(t *testing.T) {
	const oneMiB = 1024 * 1024
	if MaxInboundLineBytes <= oneMiB {
		t.Fatalf("MaxInboundLineBytes = %d, want > %d (1 MiB) so large room_list frames decode", MaxInboundLineBytes, oneMiB)
	}
}

// TestDecode_LargeRoomList verifies a room_list payload larger than the
// old 1 MiB cap round-trips through the encoder/decoder under the raised
// V8 client receive cap, with member IDs preserved.
func TestDecode_LargeRoomList(t *testing.T) {
	// Build a room_list big enough to exceed the legacy 1 MiB line cap:
	// 200 rooms x 300 members x ~24 bytes/ID ≈ 1.4 MiB on the wire.
	const (
		numRooms          = 200
		membersPerRoom    = 300
		legacyCap         = 1024 * 1024
	)
	rl := RoomList{Type: "room_list"}
	for r := 0; r < numRooms; r++ {
		room := RoomInfo{
			ID:    fmt.Sprintf("room_%016x", r),
			Name:  fmt.Sprintf("Room Number %d", r),
			Topic: "a reasonably descriptive topic string for sizing",
		}
		for m := 0; m < membersPerRoom; m++ {
			room.Members = append(room.Members, fmt.Sprintf("usr_%016x%04x", r, m))
		}
		rl.Rooms = append(rl.Rooms, room)
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(rl); err != nil {
		t.Fatalf("encode large room_list: %v", err)
	}
	if buf.Len() <= legacyCap {
		t.Fatalf("test payload is %d bytes, expected > %d (1 MiB) to exercise the raised cap", buf.Len(), legacyCap)
	}
	if buf.Len() >= MaxInboundLineBytes {
		t.Fatalf("test payload is %d bytes, exceeds MaxInboundLineBytes %d", buf.Len(), MaxInboundLineBytes)
	}

	dec := NewDecoder(&buf)
	var got RoomList
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode large room_list under %d-byte cap: %v", MaxInboundLineBytes, err)
	}

	if len(got.Rooms) != numRooms {
		t.Fatalf("decoded %d rooms, want %d", len(got.Rooms), numRooms)
	}
	// Spot-check member IDs survived the round trip.
	if n := len(got.Rooms[0].Members); n != membersPerRoom {
		t.Fatalf("room 0 has %d members, want %d", n, membersPerRoom)
	}
	wantFirst := fmt.Sprintf("usr_%016x%04x", 0, 0)
	if got.Rooms[0].Members[0] != wantFirst {
		t.Fatalf("room 0 member 0 = %q, want %q", got.Rooms[0].Members[0], wantFirst)
	}
	wantLast := fmt.Sprintf("usr_%016x%04x", numRooms-1, membersPerRoom-1)
	lastRoom := got.Rooms[numRooms-1]
	if lastRoom.Members[membersPerRoom-1] != wantLast {
		t.Fatalf("last room last member = %q, want %q", lastRoom.Members[membersPerRoom-1], wantLast)
	}
}
