package store

import (
	"database/sql"
	"errors"
	"time"
)

// StoreEpochKey saves an unwrapped epoch key locally.
func (s *Store) StoreEpochKey(room string, epoch int64, key []byte) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO epoch_keys (room, epoch, key) VALUES (?, ?, ?)`,
		room, epoch, key,
	)
	return err
}

// GetEpochKey retrieves a stored epoch key. Returns (nil, nil) if the key
// doesn't exist — missing is not an error at the application level.
func (s *Store) GetEpochKey(room string, epoch int64) ([]byte, error) {
	var key []byte
	err := s.db.QueryRow(`SELECT key FROM epoch_keys WHERE room = ? AND epoch = ?`, room, epoch).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return key, err
}

// GetAllEpochKeys loads every stored epoch key. Used on startup to populate
// the in-memory key cache so previously-synced messages can be decrypted
// without a server round-trip.
func (s *Store) GetAllEpochKeys() (map[string]map[int64][]byte, error) {
	rows, err := s.db.Query(`SELECT room, epoch, key FROM epoch_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]map[int64][]byte)
	for rows.Next() {
		var room string
		var epoch int64
		var key []byte
		if err := rows.Scan(&room, &epoch, &key); err != nil {
			return nil, err
		}
		if result[room] == nil {
			result[room] = make(map[int64][]byte)
		}
		result[room][epoch] = key
	}
	return result, rows.Err()
}

// PinKey stores a user's public key fingerprint (TOFU). If this is an update
// and the fingerprint changed, the verified flag is reset to 0 — the user
// must re-verify the new key via safety number comparison before trusting it.
//
// Fingerprint changes are expected in two scenarios:
//
//  1. Retirement + return. sshkey-chat has no in-band key rotation (see
//     PROJECT.md "Account Lifecycle"). When a user's key changes, it's
//     because the old account was retired and admin re-added the user under
//     the same username with a new key. From the client's perspective this
//     is a distinct identity reusing a name — the old verified state is not
//     transferable, so we reset it.
//
//  2. A compromised server swapping a user's pubkey. Key pinning is the
//     client-side TOFU check that catches this attack. Resetting verified
//     ensures the user cannot continue to trust a verified badge when the
//     underlying key has changed.
//
// In both cases, the correct UX is a hard warning + safety-number
// re-verification. The verified=0 reset here is what makes that flow
// possible.
func (s *Store) PinKey(user, fingerprint, pubkey string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO pinned_keys (user, fingerprint, pubkey, first_seen, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user) DO UPDATE SET
			fingerprint = excluded.fingerprint,
			pubkey = excluded.pubkey,
			updated_at = excluded.updated_at,
			verified = CASE WHEN fingerprint != excluded.fingerprint THEN 0 ELSE verified END`,
		user, fingerprint, pubkey, now, now,
	)
	return err
}

// GetPinnedKey returns the pinned fingerprint for a user. Returns ("", false,
// nil) if the user hasn't been pinned yet — missing is not an error.
func (s *Store) GetPinnedKey(user string) (fingerprint string, verified bool, err error) {
	var v int
	err = s.db.QueryRow(`SELECT fingerprint, verified FROM pinned_keys WHERE user = ?`, user).Scan(&fingerprint, &v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return fingerprint, v == 1, err
}

// GetPinnedKeyFull returns fingerprint, verified, and the stored public key.
// Used as a fallback when the user's live profile isn't available (offline).
func (s *Store) GetPinnedKeyFull(user string) (fingerprint string, verified bool, pubkey string) {
	var v int
	err := s.db.QueryRow(`SELECT fingerprint, verified, pubkey FROM pinned_keys WHERE user = ?`, user).Scan(&fingerprint, &v, &pubkey)
	if err != nil {
		return "", false, ""
	}
	return fingerprint, v == 1, pubkey
}

// PinnedKeyInfo carries the full pinned_keys row for a user — used by
// `/whois` (Phase 21 F30 closure 2026-04-19) to show the operator all
// locally-known state at once without a chain of individual getters.
// Timestamps are Unix seconds (schema uses INTEGER columns).
type PinnedKeyInfo struct {
	Fingerprint string
	Pubkey      string
	Verified    bool
	FirstSeen   int64 // Unix seconds; 0 if not pinned
	UpdatedAt   int64 // Unix seconds; 0 if not pinned
}

// GetPinnedKeyInfo returns the full pinned_keys row as a struct.
// Missing user returns a zero-value PinnedKeyInfo (all fields zero,
// no error) — missing is the norm for unpinned users, not an error.
// Real SQL errors still propagate.
func (s *Store) GetPinnedKeyInfo(user string) (PinnedKeyInfo, error) {
	var info PinnedKeyInfo
	var v int
	err := s.db.QueryRow(`
		SELECT fingerprint, pubkey, verified, first_seen, updated_at
		FROM pinned_keys WHERE user = ?`, user,
	).Scan(&info.Fingerprint, &info.Pubkey, &v, &info.FirstSeen, &info.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PinnedKeyInfo{}, nil
	}
	if err != nil {
		return PinnedKeyInfo{}, err
	}
	info.Verified = v == 1
	return info, nil
}

// MarkVerified marks a user as verified (safety number confirmed).
func (s *Store) MarkVerified(user string) error {
	_, err := s.db.Exec(`UPDATE pinned_keys SET verified = 1 WHERE user = ?`, user)
	return err
}

// ClearVerified clears verification (key changed).
func (s *Store) ClearVerified(user string) error {
	_, err := s.db.Exec(`UPDATE pinned_keys SET verified = 0 WHERE user = ?`, user)
	return err
}

// StoreSeqMark stores the high-water seq for replay detection.
func (s *Store) StoreSeqMark(key string, seq int64) error {
	_, err := s.db.Exec(`
		INSERT INTO seq_marks (key, seq) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET seq = excluded.seq`,
		key, seq,
	)
	return err
}

// GetSeqMark returns the high-water seq for a key. Returns (0, nil) if the
// key hasn't been stored yet — missing is not an error.
func (s *Store) GetSeqMark(key string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT seq FROM seq_marks WHERE key = ?`, key).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

// StoreReadPosition saves the read position for a room or group DM.
func (s *Store) StoreReadPosition(target, lastRead string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO read_positions (target, last_read, ts) VALUES (?, ?, ?)
		ON CONFLICT (target) DO UPDATE SET last_read = excluded.last_read, ts = excluded.ts`,
		target, lastRead, now,
	)
	return err
}

// GetReadPosition returns the last read message ID for a target. Returns
// ("", nil) if no read position has been stored — missing is not an error.
func (s *Store) GetReadPosition(target string) (string, error) {
	var lastRead string
	err := s.db.QueryRow(`SELECT last_read FROM read_positions WHERE target = ?`, target).Scan(&lastRead)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return lastRead, err
}

// StoreGroup caches a group DM's members and name.
func (s *Store) StoreGroup(id, name, members string) error {
	_, err := s.db.Exec(`
		INSERT INTO groups (id, name, members) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, members = excluded.members`,
		id, name, members,
	)
	return err
}

// GetAllGroups loads all cached group DMs. Returns a map of group ID →
// {name, members (comma-separated)}.
func (s *Store) GetAllGroups() (map[string][2]string, error) {
	rows, err := s.db.Query(`SELECT id, name, members FROM groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][2]string)
	for rows.Next() {
		var id, name, members string
		if err := rows.Scan(&id, &name, &members); err != nil {
			return nil, err
		}
		result[id] = [2]string{name, members}
	}
	return result, rows.Err()
}

// Phase 20 removed GetActiveGroupIDs and GetActiveRoomIDs along with
// the client-side reconciliation walks that were their only callers.
// Server is now authoritative via left_rooms / left_groups catchup
// messages sent on the connect handshake before the respective *_list
// messages. See Phase 20 (Option D) in refactor_plan.md.

// ArchivedGroup is a group DM the user has left. Returned by
// GetArchivedGroups so the sidebar can render the entry as read-only even
// after the server stops sending it in group_list.
type ArchivedGroup struct {
	ID          string
	Name        string
	Members     string // comma-separated member IDs
	LeftAt      int64
	LeaveReason string // Phase 20: "" | "removed" | "retirement"
}

// GetArchivedGroups returns every group DM the user has left (left_at > 0).
// Used by the TUI to merge archived entries back into the sidebar on
// connect/reconnect — the server only sends active groups in group_list,
// so without this the archived history would vanish the moment the client
// restarts or reconnects. Ordered by id for stable sidebar rendering.
func (s *Store) GetArchivedGroups() ([]ArchivedGroup, error) {
	rows, err := s.db.Query(
		`SELECT id, name, members, left_at, leave_reason FROM groups WHERE left_at > 0 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ArchivedGroup
	for rows.Next() {
		var g ArchivedGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Members, &g.LeftAt, &g.LeaveReason); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// MarkGroupLeft marks a group DM as "left" (archived) on the client.
// The group stays in the local DB and sidebar but is rendered read-only.
// Call when the server confirms the user has left (via group_left).
// Phase 20: reason parameter carries the server's authoritative cause
// ("" | "removed" | "retirement") so the sidebar can render distinct
// system messages.
func (s *Store) MarkGroupLeft(groupID string, leftAt int64, reason string) error {
	_, err := s.db.Exec(
		`UPDATE groups SET left_at = ?, leave_reason = ? WHERE id = ?`,
		leftAt, reason, groupID,
	)
	return err
}

// MarkGroupRejoined clears the left_at AND leave_reason flags on a group
// DM, returning it to active state. Called when the user is re-added to
// a group. Phase 20 extended this to also clear leave_reason so the
// local mirror matches the server's authoritative fresh state.
func (s *Store) MarkGroupRejoined(groupID string) error {
	_, err := s.db.Exec(
		`UPDATE groups SET left_at = 0, leave_reason = '' WHERE id = ?`,
		groupID,
	)
	return err
}

// IsGroupLeft returns true if the user has left this group DM (archived state,
// read-only in the TUI).
func (s *Store) IsGroupLeft(groupID string) bool {
	var leftAt int64
	err := s.db.QueryRow(
		`SELECT left_at FROM groups WHERE id = ?`,
		groupID,
	).Scan(&leftAt)
	if err != nil {
		return false
	}
	return leftAt > 0
}

// GetGroupLeftAt returns the unix timestamp when the user left this group DM,
// or 0 if they are still an active member (or the group does not exist).
func (s *Store) GetGroupLeftAt(groupID string) int64 {
	var leftAt int64
	s.db.QueryRow(
		`SELECT left_at FROM groups WHERE id = ?`,
		groupID,
	).Scan(&leftAt)
	return leftAt
}

// UpsertRoom persists room metadata from the server's room_list message.
func (s *Store) UpsertRoom(id, name, topic string, members int) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO rooms (id, name, topic, members, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, topic = excluded.topic,
			members = excluded.members, updated_at = excluded.updated_at`,
		id, name, topic, members, now,
	)
	return err
}

// UpdateRoomNameTopic updates only the name and topic columns on an
// existing room row, leaving members untouched. Phase 16 Gap 1 — used
// by the room_updated event handler when an admin runs `sshkey-ctl
// update-topic` or `sshkey-ctl rename-room` on the server.
//
// We don't reuse UpsertRoom here because the room_updated event
// carries no member count, and overwriting members with 0 would
// silently break the sidebar's member-count display until the next
// room_list refresh. UPDATE-only avoids that hazard.
//
// Returns nil silently if the room doesn't exist locally — the
// client may receive a room_updated event for a room it hasn't
// fetched yet (e.g. just-promoted admin who joined a room they
// weren't in before). The next room_list refresh will catch up.
func (s *Store) UpdateRoomNameTopic(id, name, topic string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`UPDATE rooms SET name = ?, topic = ?, updated_at = ? WHERE id = ?`,
		name, topic, now, id,
	)
	return err
}

// GetRoomName returns the display name for a room nanoid ID.
// Returns the raw ID if not found (graceful fallback).
func (s *Store) GetRoomName(id string) string {
	var name string
	err := s.db.QueryRow(`SELECT name FROM rooms WHERE id = ?`, id).Scan(&name)
	if err != nil || name == "" {
		return id
	}
	return name
}

// GetRoomTopic returns the topic for a room nanoid ID, or an empty string
// if the room has no topic set (or is not in the local cache yet). Parallel
// to GetRoomName — the server serves topics via RoomInfo.Topic on every
// room_list refresh, and UpsertRoom persists them into the local rooms
// table. Phase 18 wires this read path through to the TUI so the topic
// renders in the messages header and info panel.
//
// Empty-string semantics (vs GetRoomName's raw-ID fallback): an unknown
// room returns "" not the raw ID, because the render layer uses
// `if topic != ""` to decide whether to show the topic line at all.
// Showing a raw nanoid as the "topic" would be worse than showing nothing.
func (s *Store) GetRoomTopic(id string) string {
	var topic string
	err := s.db.QueryRow(`SELECT topic FROM rooms WHERE id = ?`, id).Scan(&topic)
	if err != nil {
		return ""
	}
	return topic
}

// MarkRoomLeft marks a room as "left" (archived) on the client. The room
// stays in the local DB and sidebar but is rendered read-only. Called when
// the server confirms the user has left (via room_left echo or left_rooms
// catchup). Phase 20: reason carries the server's authoritative cause
// ("" | "removed" | "user_retired") so sidebar rendering can show the
// right suffix.
func (s *Store) MarkRoomLeft(roomID string, leftAt int64, reason string) error {
	_, err := s.db.Exec(
		`UPDATE rooms SET left_at = ?, leave_reason = ? WHERE id = ?`,
		leftAt, reason, roomID,
	)
	return err
}

// MarkRoomRejoined clears the left_at AND leave_reason flags on a room,
// returning it to active state. Called when the server's room_list
// re-includes a room we had marked left (the user was added back via
// admin CLI). Phase 20 extended this to also clear leave_reason so
// the local mirror matches the server's authoritative fresh state.
func (s *Store) MarkRoomRejoined(roomID string) error {
	_, err := s.db.Exec(
		`UPDATE rooms SET left_at = 0, leave_reason = '' WHERE id = ?`,
		roomID,
	)
	return err
}

// IsRoomLeft returns true if the user has left this room (archived state,
// read-only in the TUI).
func (s *Store) IsRoomLeft(roomID string) bool {
	var leftAt int64
	err := s.db.QueryRow(
		`SELECT left_at FROM rooms WHERE id = ?`,
		roomID,
	).Scan(&leftAt)
	if err != nil {
		return false
	}
	return leftAt > 0
}

// MarkRoomRetired flags a room as retired on the client and updates
// its display name to the post-retirement suffixed form. Called from
// the room_retired event handler and the retired_rooms catchup list
// handler (Phase 12). Distinct from MarkRoomLeft — a room can be
// retired without the user having left it, or left before retirement
// (per Q9: two separate flags).
//
// The rooms row stays in place; the sidebar continues to render it
// (greyed with a "(retired)" marker instead of "(left)") until the
// user runs /delete.
func (s *Store) MarkRoomRetired(roomID, newDisplayName string, retiredAt int64) error {
	_, err := s.db.Exec(
		`UPDATE rooms SET retired_at = ?, name = ? WHERE id = ?`,
		retiredAt, newDisplayName, roomID,
	)
	return err
}

// IsRoomRetired returns true if the room has been retired server-side
// and the client has recorded the retirement locally. Used by the TUI
// to render the correct state label and by the /delete command flow
// to pick the right confirmation dialog wording.
func (s *Store) IsRoomRetired(roomID string) bool {
	var retiredAt int64
	err := s.db.QueryRow(
		`SELECT retired_at FROM rooms WHERE id = ?`,
		roomID,
	).Scan(&retiredAt)
	if err != nil {
		return false
	}
	return retiredAt > 0
}

// GetRoomLeftAt returns the unix timestamp when the user left this room,
// or 0 if they are still an active member (or the room does not exist).
func (s *Store) GetRoomLeftAt(roomID string) int64 {
	var leftAt int64
	s.db.QueryRow(
		`SELECT left_at FROM rooms WHERE id = ?`,
		roomID,
	).Scan(&leftAt)
	return leftAt
}

// LeftRoom is a room the user has left. Returned by GetLeftRooms so the
// sidebar can render the entry as read-only even after the server stops
// sending it in room_list. Mirrors ArchivedGroup for the group flow.
// Phase 20 added LeaveReason so the sidebar can render distinct
// suffixes per cause ((left) / (removed) / (retired)).
type LeftRoom struct {
	ID          string
	Name        string
	Topic       string
	Members     int
	LeftAt      int64
	LeaveReason string // Phase 20: "" | "removed" | "user_retired"
}

// GetLeftRooms returns every room the user has left (left_at > 0). Used
// by the TUI to merge archived entries back into the sidebar on
// connect/reconnect — the server only sends active rooms in room_list,
// so without this the archived history would vanish the moment the client
// restarts or reconnects. Ordered by id for stable sidebar rendering.
func (s *Store) GetLeftRooms() ([]LeftRoom, error) {
	rows, err := s.db.Query(
		`SELECT id, name, topic, members, left_at, leave_reason FROM rooms WHERE left_at > 0 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LeftRoom
	for rows.Next() {
		var r LeftRoom
		if err := rows.Scan(&r.ID, &r.Name, &r.Topic, &r.Members, &r.LeftAt, &r.LeaveReason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StoreDM caches a 1:1 DM's member pair.
func (s *Store) StoreDM(id, userA, userB string) error {
	_, err := s.db.Exec(`
		INSERT INTO direct_messages (id, user_a, user_b) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET user_a = excluded.user_a, user_b = excluded.user_b`,
		id, userA, userB,
	)
	return err
}

// StoredDM holds a cached 1:1 DM entry.
type StoredDM struct {
	ID     string
	UserA  string
	UserB  string
	LeftAt int64
}

// GetAllDMs loads all cached 1:1 DMs (active AND left). Callers that want
// to filter by active status should check LeftAt themselves.
func (s *Store) GetAllDMs() ([]StoredDM, error) {
	rows, err := s.db.Query(`SELECT id, user_a, user_b, left_at FROM direct_messages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dms []StoredDM
	for rows.Next() {
		var dm StoredDM
		if err := rows.Scan(&dm.ID, &dm.UserA, &dm.UserB, &dm.LeftAt); err != nil {
			return nil, err
		}
		dms = append(dms, dm)
	}
	return dms, rows.Err()
}

// MarkDMLeft sets the local left_at flag on a 1:1 DM. Mirrors the
// room/group helpers — called when the user runs /delete on this device,
// when a dm_left echo arrives from another of the user's devices, or when
// sync's dm_list reports a left_at_for_caller > 0 we didn't already know
// about.
func (s *Store) MarkDMLeft(dmID string, leftAt int64) error {
	_, err := s.db.Exec(
		`UPDATE direct_messages SET left_at = ? WHERE id = ?`,
		leftAt, dmID,
	)
	return err
}

// MarkDMRejoined clears the local left_at flag on a 1:1 DM. There is no
// server-side path that retreats the cutoff (cutoffs are one-way), but
// the client uses this when it locally re-creates a DM after the row was
// purged (the "fresh on re-contact" path) and needs to drop the leave
// flag if it was somehow still set.
func (s *Store) MarkDMRejoined(dmID string) error {
	_, err := s.db.Exec(
		`UPDATE direct_messages SET left_at = 0 WHERE id = ?`,
		dmID,
	)
	return err
}

// GetDMLeftAt returns the unix timestamp at which the user left this DM,
// or 0 if they have not left (or the row does not exist locally).
func (s *Store) GetDMLeftAt(dmID string) int64 {
	var leftAt int64
	s.db.QueryRow(
		`SELECT left_at FROM direct_messages WHERE id = ?`,
		dmID,
	).Scan(&leftAt)
	return leftAt
}

// PurgeDMMessages deletes every locally-stored message row for a 1:1 DM
// and the reactions hanging off those messages. Used by the dm_left
// handler to wipe local history when /delete is run on any device.
//
// The direct_messages row itself is preserved (with left_at set) so that
// multi-device sync on a different device can recognise the leave state
// on its next reconnect. Read positions and seq marks are not touched —
// 1:1 DMs do not currently use read_positions, and seq_marks are scoped
// to the server-side dm_id which is still valid until both parties leave.
func (s *Store) PurgeDMMessages(dmID string) error {
	// Delete reactions first via a subquery on messages, before the
	// messages themselves are gone. The reactions table is keyed by
	// message_id so we have to look up which messages belonged to the
	// DM before we drop them.
	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE dm_id = ?)`,
		dmID,
	); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE dm_id = ?`, dmID); err != nil {
		return err
	}
	return nil
}

// PurgeGroupMessages deletes every locally-stored message row for a
// group DM and the reactions hanging off those messages. Mirrors
// PurgeDMMessages — used by the group_deleted handler to wipe local
// history when /delete is run on any device.
//
// The local groups row is preserved (with left_at set) so that multi-
// device sync on a different device can recognise the leave state on
// next reconnect, and so the sidebar's archived rendering can stay if
// the user later wants to /delete again.
func (s *Store) PurgeGroupMessages(groupID string) error {
	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE group_id = ?)`,
		groupID,
	); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	return nil
}

// PurgeRoomMessages drops all locally-stored messages, reactions, and
// epoch keys for the given room. Called when the server echoes
// room_deleted in response to a /delete, or when the deleted_rooms
// catchup list arrives on reconnect. Phase 12 parallel to
// PurgeGroupMessages.
//
// Idempotent: re-running on an already-purged room is a no-op. The
// local rooms row is preserved (with left_at set by the caller) so
// that multi-device catchup can detect the state on reconnect and so
// the TUI app layer can detect the row in its sidebar cleanup. The
// app-level sidebar entry removal happens separately via
// sidebar.RemoveRoom.
func (s *Store) PurgeRoomMessages(roomID string) error {
	// Drop reactions attached to messages in this room
	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE room = ?)`,
		roomID,
	); err != nil {
		return err
	}
	// Drop the messages themselves
	if _, err := s.db.Exec(`DELETE FROM messages WHERE room = ?`, roomID); err != nil {
		return err
	}
	// Drop epoch keys — they're unrecoverable without the server's
	// wrapped keys anyway, and the room is being removed from the
	// user's view entirely
	if _, err := s.db.Exec(`DELETE FROM epoch_keys WHERE room = ?`, roomID); err != nil {
		return err
	}
	return nil
}

// SetState stores a client state value.
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO state (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// GetState retrieves a client state value. Returns ("", nil) if the key
// hasn't been set — missing is not an error.
func (s *Store) GetState(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
