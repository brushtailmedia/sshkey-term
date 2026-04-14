package store

import (
	"database/sql"
	"errors"
)

// Phase 14 client-side store helpers for the in-group admin model.
//
// Two concerns live here:
//
//   1. The local user's own admin status per group. Persisted in the
//      groups.is_admin column (see store.go schema). Used by the TUI
//      pre-check to gate admin commands (/add, /kick, /promote, etc.)
//      without a server round-trip. Promote/demote events for the
//      local user land here via SetLocalUserGroupAdmin so the flag
//      survives restart.
//
//   2. The local group_events audit table. Populated by both live
//      group_event broadcasts and offline replay entries from
//      sync_batch.Events. Reads feed /audit and any future history
//      surface. Ordering is by (group_id, ts ASC, id ASC) so sync
//      replay and live delivery produce identical ordering.
//
// The in-memory admin set for OTHER members lives on the client
// (groupAdmins map[string][]string) — not persisted. This file only
// touches the two persistent concerns above.

// IsLocalUserGroupAdmin reports whether the local user is currently
// recorded as an admin of the given group. Returns (false, nil) if the
// group isn't in the local cache at all. Used by the TUI pre-check —
// failures fall back to assuming non-admin (safe default: the command
// goes to the server which is the authoritative source).
func (s *Store) IsLocalUserGroupAdmin(groupID string) (bool, error) {
	var flag int
	err := s.db.QueryRow(
		`SELECT is_admin FROM groups WHERE id = ?`, groupID,
	).Scan(&flag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return flag == 1, nil
}

// SetLocalUserGroupAdmin updates the local user's admin flag for a
// group. Distinct from StoreGroup so admin state changes via
// group_event{promote/demote} don't clobber the members list during
// a normal sync update.
//
// If the group row doesn't exist yet, this is a no-op — the group
// will be inserted via StoreGroup on the next group_list or
// group_added_to payload, at which point the caller should call
// SetLocalUserGroupAdmin again if the local user's admin state is
// known. In practice the order of operations puts StoreGroup first
// so this window is short.
func (s *Store) SetLocalUserGroupAdmin(groupID string, isAdmin bool) error {
	flag := 0
	if isAdmin {
		flag = 1
	}
	_, err := s.db.Exec(
		`UPDATE groups SET is_admin = ? WHERE id = ?`, flag, groupID,
	)
	return err
}

// GroupEventRow is a single row from the local group_events table,
// used by /audit and sync-replay paths. Field shapes mirror the
// server-side store.GroupEventRow exactly so code that handles both
// sources (live broadcast + sync replay) can share serialization
// logic. Quiet is bool in Go but stored as INTEGER 0/1 in SQLite.
type GroupEventRow struct {
	ID      int64
	GroupID string
	Event   string // "join" | "leave" | "promote" | "demote" | "rename"
	User    string // the member this event is about
	By      string // the admin who triggered it; empty for self-leave, retirement, retirement_succession
	Reason  string // "" | "removed" | "retirement" | "retirement_succession"
	Name    string // new name (for rename events only)
	Quiet   bool   // event should be suppressed in live rendering but still stored
	TS      int64  // unix seconds
}

// RecordGroupEvent inserts an audit row into the client's local
// group_events table. Called from the incoming-message dispatch
// path for every live group_event broadcast, and from the sync
// replay loop for every entry in sync_batch.Events. Both sources
// should produce identical rows so replay is idempotent from the
// viewer's perspective.
//
// Failures are logged and continued — the local event stream is
// best-effort, just like the server-side audit trail. A persistent
// failure silently breaks /audit for that one group; the error logs
// are the canary.
func (s *Store) RecordGroupEvent(groupID, event, user, by, reason, name string, quiet bool, ts int64) error {
	quietFlag := 0
	if quiet {
		quietFlag = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO group_events (group_id, event, user, by, reason, name, quiet, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		groupID, event, user, by, reason, name, quietFlag, ts,
	)
	return err
}

// GetGroupEvents returns the most recent limit events for a group,
// ordered by ts DESCENDING (newest first). Used by /audit's default
// 10-most-recent view and by any history surface that wants to page
// backwards. Pass limit <= 0 for all events.
func (s *Store) GetGroupEvents(groupID string, limit int) ([]GroupEventRow, error) {
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(`
			SELECT id, group_id, event, user, by, reason, name, quiet, ts
			FROM group_events WHERE group_id = ?
			ORDER BY ts DESC, id DESC LIMIT ?`,
			groupID, limit,
		)
	} else {
		rows, err = s.db.Query(`
			SELECT id, group_id, event, user, by, reason, name, quiet, ts
			FROM group_events WHERE group_id = ?
			ORDER BY ts DESC, id DESC`,
			groupID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []GroupEventRow
	for rows.Next() {
		var r GroupEventRow
		var quietFlag int
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Event, &r.User, &r.By, &r.Reason, &r.Name, &quietFlag, &r.TS); err != nil {
			return nil, err
		}
		r.Quiet = quietFlag == 1
		events = append(events, r)
	}
	return events, rows.Err()
}

// GetRecentGroupEvents is a thin convenience wrapper around
// GetGroupEvents for the /audit default N-most-recent overlay.
// Equivalent to GetGroupEvents(groupID, n).
func (s *Store) GetRecentGroupEvents(groupID string, n int) ([]GroupEventRow, error) {
	return s.GetGroupEvents(groupID, n)
}
