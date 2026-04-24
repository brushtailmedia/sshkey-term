package store

// Phase 20 — client-side room_events helpers.
//
// Parallel to group_events (see group_admin.go). Separate table rather
// than a context-type column on the existing group_events table; keeps
// the existing group-side code paths untouched.
//
// Populated by:
//   (a) live room_event broadcasts from the server (leave, join, topic,
//       rename, retire)
//   (b) offline replay entries from sync_batch.Events on reconnect
//
// Reads feed the inline system-message renderer in the room messages
// pane. Encrypted at rest on the client (SQLCipher) even though the
// server stores these as plaintext metadata — see the encryption
// boundary docs in PROJECT.md.

import (
	"database/sql"
)

// RoomEventRow is a single row from the local room_events table.
// Shape mirrors the server-side store.GroupEventRow minus the
// Quiet flag (Phase 14 Quiet is group-specific; rooms don't use it).
type RoomEventRow struct {
	ID     int64
	RoomID string
	Event  string // "leave" | "join" | "topic" | "rename" | "retire"
	User   string // target user ("" for topic/rename/retire)
	By     string // acting admin/operator
	Reason string // "" | "removed" | "user_retired"
	Name   string // new topic or display name (topic/rename events)
	TS     int64  // unix seconds
}

// RecordRoomEvent inserts an audit row into the client's local
// room_events table. Called from the incoming-message dispatch path
// for every live room_event broadcast, and from the sync replay loop
// for every entry in sync_batch.Events. Both sources should produce
// identical rows so replay is idempotent from the viewer's perspective.
//
// Failures are logged and continued — the local event stream is
// best-effort, same posture as the group-side helper.
func (s *Store) RecordRoomEvent(roomID, event, user, by, reason, name string, ts int64) error {
	_, err := s.db.Exec(`
		INSERT INTO room_events (room_id, event, user, by, reason, name, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		roomID, event, user, by, reason, name, ts,
	)
	return err
}

// GetRoomEvents returns the most recent limit events for a room,
// ordered by ts DESCENDING (newest first). Pass limit <= 0 for all
// events. Mirrors GetGroupEvents.
func (s *Store) GetRoomEvents(roomID string, limit int) ([]RoomEventRow, error) {
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(`
			SELECT id, room_id, event, user, by, reason, name, ts
			FROM room_events WHERE room_id = ?
			ORDER BY ts DESC, id DESC LIMIT ?`,
			roomID, limit,
		)
	} else {
		rows, err = s.db.Query(`
			SELECT id, room_id, event, user, by, reason, name, ts
			FROM room_events WHERE room_id = ?
			ORDER BY ts DESC, id DESC`,
			roomID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []RoomEventRow
	for rows.Next() {
		var r RoomEventRow
		if err := rows.Scan(&r.ID, &r.RoomID, &r.Event, &r.User, &r.By, &r.Reason, &r.Name, &r.TS); err != nil {
			return nil, err
		}
		events = append(events, r)
	}
	return events, rows.Err()
}
