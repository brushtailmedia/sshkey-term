package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// collectMessageFileIDsByColumn returns attachment file IDs across
// every row in `messages` whose given column equals the supplied
// value. Used by the bulk-purge functions
// (PurgeRoomMessages/PurgeGroupMessages/PurgeDMMessages) to gather
// the file IDs that need filesystem cleanup BEFORE the rows are
// deleted from the DB.
//
// Best-effort: errors at any step (query, scan, JSON parse) skip
// the offending row rather than aborting; callers can still
// proceed with the DB delete and we just won't clean up the
// orphaned file. The column name is gated against a fixed allowlist
// to keep the function safe to use with a string-concatenated WHERE
// clause without SQL-injection risk.
func (s *Store) collectMessageFileIDsByColumn(column, value string) []string {
	switch column {
	case "room", "group_id", "dm_id":
		// allowed
	default:
		// caller bug — never reached in practice; guard prevents
		// arbitrary SQL injection via the concatenated WHERE clause.
		return nil
	}

	var fileIDs []string
	rows, err := s.db.Query(`SELECT attachments FROM messages WHERE `+column+` = ?`, value)
	if err != nil {
		return fileIDs
	}
	defer rows.Close()
	for rows.Next() {
		var attachJSON string
		if err := rows.Scan(&attachJSON); err != nil {
			continue
		}
		if attachJSON == "" {
			continue
		}
		var atts []StoredAttachment
		if err := json.Unmarshal([]byte(attachJSON), &atts); err != nil {
			continue
		}
		for _, a := range atts {
			if a.FileID != "" {
				fileIDs = append(fileIDs, a.FileID)
			}
		}
	}
	return fileIDs
}

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

// StoreHistoricalEpochKey saves an unwrapped room epoch key into the
// history-only store (separate table from epoch_keys). F7 Phase D scoped-key
// model: keys delivered via skip-verified sync_batch/history_result frames are
// written ONLY here (by the client's storeEpochKeyHistorical path) and read
// ONLY by the history resolver (RoomEpochKeyForHistory), which gates them to
// genuinely-historical epochs (epoch < currentEpoch). They must never feed
// live/current decryption.
func (s *Store) StoreHistoricalEpochKey(room string, epoch int64, key []byte) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO historical_epoch_keys (room, epoch, key) VALUES (?, ?, ?)`,
		room, epoch, key,
	)
	return err
}

// GetHistoricalEpochKey retrieves a history-only epoch key. Returns (nil, nil)
// if absent — missing is not an error. The epoch < currentEpoch usability gate
// is enforced by the caller (RoomEpochKeyForHistory); this is dumb storage.
func (s *Store) GetHistoricalEpochKey(room string, epoch int64) ([]byte, error) {
	var key []byte
	err := s.db.QueryRow(`SELECT key FROM historical_epoch_keys WHERE room = ? AND epoch = ?`, room, epoch).Scan(&key)
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

// ListVerifiedPinnedKeys returns the user IDs of every pinned_keys row
// currently marked verified=1. Backs the `/unverify` shared-picker
// candidate builder (shared-picker-widget.md §6 — the one net-new
// store helper the spec called out); enumerating verified users by
// scanning unrelated TUI state would be both incomplete (only live
// in-memory profiles) and a layering violation. Returns IDs only
// (not display names) — callers resolve display via the usual path.
func (s *Store) ListVerifiedPinnedKeys() ([]string, error) {
	rows, err := s.db.Query(`SELECT user FROM pinned_keys WHERE verified = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
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

// GetGroupNameMembers returns the cached {name,members} tuple for a group ID.
// members is returned as a comma-separated nanoid list. ok=false means the
// group is not present in the local cache.
func (s *Store) GetGroupNameMembers(id string) (name, members string, ok bool) {
	err := s.db.QueryRow(`SELECT name, members FROM groups WHERE id = ?`, id).Scan(&name, &members)
	if err != nil {
		return "", "", false
	}
	return name, members, true
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

// DeleteGroupRecord removes a group DM metadata row from the local cache.
// Used by /delete flows (group_deleted + deleted_groups catchup) so deleted
// groups do not reappear from archived-row merge logic on reconnect.
func (s *Store) DeleteGroupRecord(groupID string) error {
	_, err := s.db.Exec(`DELETE FROM groups WHERE id = ?`, groupID)
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
//
// Count-only legacy primitive. Production room_list handling should prefer
// UpsertRoomWithMembers (V8), which writes the member count and member_ids
// CSV in one statement so the two cannot drift. UpsertRoom is retained for
// callers that have no member-ID list (and as a test fixture builder).
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

// UpsertRoomWithMembers persists room metadata AND the full member-ID
// snapshot in a single statement (V8). The `members` count and the
// `member_ids` CSV are written together from the same normalized slice, so
// they cannot drift across a partial write. Used by the room_list handler.
func (s *Store) UpsertRoomWithMembers(id, name, topic string, memberIDs []string) error {
	now := time.Now().Unix()
	norm := normalizeMemberIDs(memberIDs)
	csv := strings.Join(norm, ",")
	_, err := s.db.Exec(`
		INSERT INTO rooms (id, name, topic, members, member_ids, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, topic = excluded.topic,
			members = excluded.members, member_ids = excluded.member_ids, updated_at = excluded.updated_at`,
		id, name, topic, len(norm), csv, now,
	)
	return err
}

// SetRoomMembers replaces the persisted member list for an existing room
// (UPDATE-only). Writes both the count and the member_ids CSV from one
// normalized slice so they stay consistent. Used by the explicit `r`
// refresh handler, whose room_members_list response carries members but no
// name/topic. Normalizes: trims and drops blank IDs, de-dupes keeping first
// occurrence, preserves server order.
func (s *Store) SetRoomMembers(roomID string, members []string) error {
	norm := normalizeMemberIDs(members)
	csv := strings.Join(norm, ",")
	_, err := s.db.Exec(`UPDATE rooms SET members = ?, member_ids = ? WHERE id = ?`,
		len(norm), csv, roomID)
	return err
}

// GetRoomMembers returns the persisted member list for a room. loaded is
// false when member_ids IS NULL (not yet loaded) or the room row is absent.
// A loaded-but-empty room ("" CSV) returns an empty non-nil slice with
// loaded=true. The returned slice is a fresh copy — callers may mutate it.
func (s *Store) GetRoomMembers(roomID string) (members []string, loaded bool, err error) {
	var mi sql.NullString
	err = s.db.QueryRow(`SELECT member_ids FROM rooms WHERE id = ?`, roomID).Scan(&mi)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !mi.Valid {
		return nil, false, nil // NULL = not loaded
	}
	return splitMemberIDs(mi.String), true, nil
}

// GetAllLoadedRoomMembers returns the member lists for every room with a
// non-NULL member_ids column. Used for startup cache hydration. Each slice
// is a fresh copy.
func (s *Store) GetAllLoadedRoomMembers() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT id, member_ids FROM rooms WHERE member_ids IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var id string
		var mi sql.NullString
		if err := rows.Scan(&id, &mi); err != nil {
			return nil, err
		}
		out[id] = splitMemberIDs(mi.String)
	}
	return out, rows.Err()
}

// ClearRoomMembers sets member_ids back to NULL (not loaded). Used by the
// retire / leave / removed / delete paths. On a hard-deleted row this is a
// harmless no-op UPDATE.
func (s *Store) ClearRoomMembers(roomID string) error {
	_, err := s.db.Exec(`UPDATE rooms SET member_ids = NULL WHERE id = ?`, roomID)
	return err
}

// normalizeMemberIDs trims, drops empty/blank IDs, and de-dupes (keeping the
// first occurrence) while preserving server order. Returns a fresh slice.
func normalizeMemberIDs(members []string) []string {
	seen := make(map[string]bool, len(members))
	out := make([]string, 0, len(members))
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// splitMemberIDs parses a stored member_ids CSV. An empty string is a
// loaded-but-empty room and returns a non-nil empty slice. Always returns a
// fresh slice (defensive copy).
func splitMemberIDs(csv string) []string {
	if csv == "" {
		return []string{}
	}
	return strings.Split(csv, ",")
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

// DeleteRoomRecord removes a room metadata row from the local cache.
// Used by /delete flows (room_deleted + deleted_rooms catchup) so deleted
// rooms do not reappear from archived-row merge logic on reconnect.
func (s *Store) DeleteRoomRecord(roomID string) error {
	_, err := s.db.Exec(`DELETE FROM rooms WHERE id = ?`, roomID)
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

// EnsureRetiredRoom creates or updates the local metadata row for a retired
// room (V8). Unlike MarkRoomRetired (UPDATE-only), this INSERTs a minimal
// row when absent — needed when a retired_rooms catchup entry arrives on a
// fresh local DB for a room never seen active (retired rooms are omitted
// from active room_list, so no row would otherwise exist). Does not touch
// member_ids: retired rooms have no member-list UI; the caller pairs this
// with ClearRoomMembers to null any stale cached list.
func (s *Store) EnsureRetiredRoom(roomID, displayName string, retiredAt int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO rooms (id, name, topic, members, updated_at, retired_at)
		VALUES (?, ?, '', 0, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name,
			retired_at = excluded.retired_at, updated_at = excluded.updated_at`,
		roomID, displayName, now, retiredAt,
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

// RetiredRoom is a room that has been retired server-side. Returned by
// GetRetiredRooms so the sidebar can keep showing it as read-only history
// after the server stops sending it in room_list. Mirrors LeftRoom with
// RetiredAt replacing LeftAt; there is no retirement-reason column.
type RetiredRoom struct {
	ID        string
	Name      string
	Topic     string
	Members   int
	RetiredAt int64
}

// GetRetiredRooms returns every retired room (retired_at > 0). Used by the
// TUI to merge retired entries back into the sidebar on connect/reconnect —
// the server omits retired rooms from room_list, so without this they would
// vanish on restart. Ordered by id for stable sidebar rendering. Does not
// expose member_ids: retired rooms have no member-list UI.
func (s *Store) GetRetiredRooms() ([]RetiredRoom, error) {
	rows, err := s.db.Query(
		`SELECT id, name, topic, members, retired_at FROM rooms WHERE retired_at > 0 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RetiredRoom
	for rows.Next() {
		var r RetiredRoom
		if err := rows.Scan(&r.ID, &r.Name, &r.Topic, &r.Members, &r.RetiredAt); err != nil {
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
	Hidden bool
}

// GetAllDMs loads all cached 1:1 DMs (visible and hidden tombstones).
// Callers that want only visible conversations should filter on Hidden.
func (s *Store) GetAllDMs() ([]StoredDM, error) {
	rows, err := s.db.Query(`SELECT id, user_a, user_b, left_at, hidden FROM direct_messages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dms []StoredDM
	for rows.Next() {
		var dm StoredDM
		var hidden int
		if err := rows.Scan(&dm.ID, &dm.UserA, &dm.UserB, &dm.LeftAt, &hidden); err != nil {
			return nil, err
		}
		dm.Hidden = hidden != 0
		dms = append(dms, dm)
	}
	return dms, rows.Err()
}

// MarkDMLeft marks the DM as hidden locally and stores the caller's
// left_at cutoff mirror.
func (s *Store) MarkDMLeft(dmID string, leftAt int64) error {
	_, err := s.db.Exec(
		`UPDATE direct_messages SET left_at = ?, hidden = 1 WHERE id = ?`,
		leftAt, dmID,
	)
	return err
}

// MarkDMRejoined clears local DM tombstone state. Used for local recovery
// flows where the client intentionally wants a fully-active row.
func (s *Store) MarkDMRejoined(dmID string) error {
	_, err := s.db.Exec(
		`UPDATE direct_messages SET left_at = 0, hidden = 0 WHERE id = ?`,
		dmID,
	)
	return err
}

// SetDMState mirrors the server's DM state in one write.
func (s *Store) SetDMState(dmID string, leftAt int64, hidden bool) error {
	h := 0
	if hidden {
		h = 1
	}
	_, err := s.db.Exec(
		`UPDATE direct_messages SET left_at = ?, hidden = ? WHERE id = ?`,
		leftAt, h, dmID,
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

// IsDMHidden reports whether this DM is currently tombstoned from local view.
func (s *Store) IsDMHidden(dmID string) bool {
	var hidden int
	s.db.QueryRow(
		`SELECT hidden FROM direct_messages WHERE id = ?`,
		dmID,
	).Scan(&hidden)
	return hidden != 0
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
func (s *Store) PurgeDMMessages(dmID string) ([]string, error) {
	// Collect file IDs from attachments BEFORE the messages are
	// deleted — caller uses these to clean up the on-disk
	// attachment cache so deleted-conversation files don't leak.
	fileIDs := s.collectMessageFileIDsByColumn("dm_id", dmID)

	// Delete reactions first via a subquery on messages, before the
	// messages themselves are gone. The reactions table is keyed by
	// message_id so we have to look up which messages belonged to the
	// DM before we drop them.
	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE dm_id = ?)`,
		dmID,
	); err != nil {
		return fileIDs, err
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE dm_id = ?`, dmID); err != nil {
		return fileIDs, err
	}
	return fileIDs, nil
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
func (s *Store) PurgeGroupMessages(groupID string) ([]string, error) {
	// Collect attachment file IDs before the rows go away — see
	// PurgeDMMessages for the rationale.
	fileIDs := s.collectMessageFileIDsByColumn("group_id", groupID)

	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE group_id = ?)`,
		groupID,
	); err != nil {
		return fileIDs, err
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE group_id = ?`, groupID); err != nil {
		return fileIDs, err
	}
	return fileIDs, nil
}

// PurgeRoomMessages drops all locally-stored messages, reactions, and
// epoch keys for the given room. Called when the server echoes
// room_deleted in response to a /delete, or when the deleted_rooms
// catchup list arrives on reconnect. Phase 12 parallel to
// PurgeGroupMessages.
//
// Idempotent: re-running on an already-purged room is a no-op. The
// helper itself only removes room-scoped message data; the caller owns the
// room metadata decision. The current /delete handlers call DeleteRoomRecord
// immediately after this so deleted rooms do not reappear from archived-row
// merge logic on reconnect.
func (s *Store) PurgeRoomMessages(roomID string) ([]string, error) {
	// Collect attachment file IDs before the rows go away — see
	// PurgeDMMessages for the rationale.
	fileIDs := s.collectMessageFileIDsByColumn("room", roomID)

	// Drop reactions attached to messages in this room
	if _, err := s.db.Exec(
		`DELETE FROM reactions WHERE message_id IN (SELECT id FROM messages WHERE room = ?)`,
		roomID,
	); err != nil {
		return fileIDs, err
	}
	// Drop the messages themselves
	if _, err := s.db.Exec(`DELETE FROM messages WHERE room = ?`, roomID); err != nil {
		return fileIDs, err
	}
	// Drop epoch keys — they're unrecoverable without the server's
	// wrapped keys anyway, and the room is being removed from the
	// user's view entirely
	if _, err := s.db.Exec(`DELETE FROM epoch_keys WHERE room = ?`, roomID); err != nil {
		return fileIDs, err
	}
	// F7 Phase D: drop history-only epoch keys for the room too, so a
	// client-local hard /delete (room_deleted / deleted_rooms) leaves no
	// scoped keys behind. Left/retired rooms do NOT purge — they keep
	// historical keys for scrollback.
	if _, err := s.db.Exec(`DELETE FROM historical_epoch_keys WHERE room = ?`, roomID); err != nil {
		return fileIDs, err
	}
	return fileIDs, nil
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
