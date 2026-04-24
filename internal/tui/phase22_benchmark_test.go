package tui

import (
	"strconv"
	"testing"
)

// Phase 22 C.5: decrypt-and-display pipeline proxy benchmark.
// We benchmark the render side with a realistic in-memory message set.
func BenchmarkMessagesView_Render200(b *testing.B) {
	m := NewMessages()
	m.SetContext("room_bench", "", "")
	m.resolveName = func(userID string) string { return userID }
	m.resolveRoomName = func(roomID string) string { return roomID }

	msgs := make([]DisplayMessage, 200)
	for i := range msgs {
		msgs[i] = DisplayMessage{
			ID:   "msg_" + strconv.Itoa(i),
			From: "usr_alice",
			Body: "benchmark message body",
			TS:   int64(i + 1),
			Room: "room_bench",
		}
	}
	m.messages = msgs

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View(120, 30, true)
	}
}
