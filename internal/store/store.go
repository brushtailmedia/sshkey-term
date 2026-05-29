// Package store implements the client-side encrypted local database.
// One SQLite DB per server, encrypted with SQLCipher.
// Key derived from the user's SSH private key via HKDF-SHA256.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mutecomm/go-sqlcipher/v4"
	"golang.org/x/crypto/hkdf"
)

// Store is the client-side encrypted local database for a single server.
type Store struct {
	db     *sql.DB
	hasFTS bool // true if FTS5 full-text search is available
}

// Open creates or opens an encrypted local database.
// The dbKey is derived from the user's SSH private key seed bytes.
// If dbKey is nil, the DB is opened unencrypted (for testing).
func Open(path string, dbKey []byte) (*Store, error) {
	if len(dbKey) == 0 {
		return nil, fmt.Errorf("encryption key required (refusing to create unencrypted database)")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	// Pass the key via _pragma_key in the DSN so that every connection the
	// pool creates is keyed automatically. Using a DSN parameter avoids the
	// classic race where PRAGMA key is set on one connection but the pool
	// hands out a fresh unkeyed connection later.
	hexKey := hex.EncodeToString(dbKey)
	dsn := fmt.Sprintf("file:%s?_pragma_key=x'%s'", path, hexKey)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w (is the key correct?)", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenUnencrypted opens an unencrypted database. Only for use in tests.
func OpenUnencrypted(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DeriveDBKey derives a 256-bit database encryption key from an Ed25519 private key seed.
// Uses HKDF-SHA256 with a fixed info string so the same key always produces the same DB key.
func DeriveDBKey(privateKeySeed []byte) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, privateKeySeed, nil, []byte("sshkey-chat local db"))
	key := make([]byte, 32)
	if _, err := hkdfReader.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// HasFTS returns true if FTS5 full-text search is available.
func (s *Store) HasFTS() bool {
	return s.hasFTS
}

func (s *Store) Close() error {
	s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

func (s *Store) init() error {
	// The app has no users, so a schema change to the local messages cache needs
	// no backwards-compatible migration: clean-rebuild an old messages table (and
	// its FTS index + triggers) so the CREATE below recreates it with the
	// server_order column + constraints. Fresh DBs are unaffected.
	if err := s.ensureMessagesServerOrderSchema(); err != nil {
		return err
	}
	// Create tables in separate execs to handle FTS5 gracefully
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id              TEXT PRIMARY KEY,
			sender          TEXT NOT NULL,
			body            TEXT NOT NULL,
			ts              INTEGER NOT NULL,
			room            TEXT NOT NULL DEFAULT '',
			group_id        TEXT NOT NULL DEFAULT '',
			dm_id           TEXT NOT NULL DEFAULT '',
			epoch           INTEGER NOT NULL DEFAULT 0,
			reply_to        TEXT NOT NULL DEFAULT '',
			mentions        TEXT NOT NULL DEFAULT '',
			has_attachments INTEGER NOT NULL DEFAULT 0,
			raw_payload     TEXT NOT NULL DEFAULT '',
			deleted         INTEGER NOT NULL DEFAULT 0,
			deleted_by      TEXT NOT NULL DEFAULT '',
			attachments     TEXT NOT NULL DEFAULT '',
			edited_at       INTEGER NOT NULL DEFAULT 0,
			-- server_order (S2): the server's authoritative per-conversation
			-- commit order. Every persisted row is server-originated, so it is
			-- NOT NULL and > 0; it is per-conversation (not global). S1's
			-- AUTOINCREMENT already guarantees the server never emits a duplicate
			-- (context, server_order), so the pagination indexes below are
			-- non-unique (the unique backstop would only catch a bug S1 prevents,
			-- at the cost of brittle distinct-value test fixtures).
			server_order    INTEGER NOT NULL CHECK(server_order > 0),
			-- Exactly one context column is set per message (room XOR group XOR dm).
			CHECK ((room != '') + (group_id != '') + (dm_id != '') = 1)
		);

		CREATE INDEX IF NOT EXISTS idx_messages_room_ts ON messages(room, ts) WHERE room != '';
		CREATE INDEX IF NOT EXISTS idx_messages_group_ts ON messages(group_id, ts) WHERE group_id != '';
		CREATE INDEX IF NOT EXISTS idx_messages_dm_ts ON messages(dm_id, ts) WHERE dm_id != '';

		-- Per-conversation server_order pagination index for ORDER BY
		-- server_order within a context (S3). Non-unique (see the column
		-- comment above).
		CREATE INDEX IF NOT EXISTS idx_messages_room_order ON messages(room, server_order) WHERE room != '';
		CREATE INDEX IF NOT EXISTS idx_messages_group_order ON messages(group_id, server_order) WHERE group_id != '';
		CREATE INDEX IF NOT EXISTS idx_messages_dm_order ON messages(dm_id, server_order) WHERE dm_id != '';

		-- Reactions (decrypted emoji stored locally)
		CREATE TABLE IF NOT EXISTS reactions (
			reaction_id TEXT PRIMARY KEY,
			message_id  TEXT NOT NULL,
			user        TEXT NOT NULL,
			emoji       TEXT NOT NULL,
			ts          INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_reactions_msg ON reactions(message_id);

		-- Epoch keys (unwrapped, stored for future decryption)
		CREATE TABLE IF NOT EXISTS epoch_keys (
			room  TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			key   BLOB NOT NULL,
			PRIMARY KEY (room, epoch)
		);

		-- Pinned public keys (key pinning / TOFU)
		CREATE TABLE IF NOT EXISTS pinned_keys (
			user        TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL,
			pubkey      TEXT NOT NULL,
			verified    INTEGER NOT NULL DEFAULT 0,
			first_seen  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL
		);

		-- Read positions (for unread tracking)
		CREATE TABLE IF NOT EXISTS read_positions (
			target    TEXT PRIMARY KEY,
			last_read TEXT NOT NULL,
			ts        INTEGER NOT NULL
		);

		-- Seq high-water marks (replay detection)
		CREATE TABLE IF NOT EXISTS seq_marks (
			key TEXT PRIMARY KEY,
			seq INTEGER NOT NULL
		);

		-- Rooms (persisted from server room_list).
		-- left_at = 0 means active member; >0 means the user has left this
		-- room (archived: greyed in sidebar, input disabled, history still
		-- scrollable until /delete).
		--
		-- retired_at = 0 means active room; >0 means the room has been
		-- retired (admin action, Phase 12). Distinct from left_at per Q9:
		-- a user can be in a retired room (retired_at > 0, left_at = 0)
		-- or leave a retired room (both > 0), and the TUI renders them
		-- differently. name carries the post-retirement suffixed form.
		CREATE TABLE IF NOT EXISTS rooms (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL DEFAULT '',
			topic         TEXT NOT NULL DEFAULT '',
			members       INTEGER NOT NULL DEFAULT 0,
			updated_at    INTEGER NOT NULL DEFAULT 0,
			left_at       INTEGER NOT NULL DEFAULT 0,
			retired_at    INTEGER NOT NULL DEFAULT 0,
			leave_reason  TEXT NOT NULL DEFAULT ''
		);

		-- Group DMs (local cache of group member lists + names).
		-- left_at = 0 means active member; >0 means the user has left this
		-- group (archived: greyed in sidebar, input disabled, history still
		-- scrollable until /delete).
		--
		-- Phase 14: is_admin tracks the LOCAL user's admin status in this
		-- group (1 = admin, 0 = regular member). Other members' admin
		-- status is NOT persisted client-side — it lives in the in-memory
		-- groupAdmins map on Client, sourced from the server's group_list
		-- payload and updated by group_event{promote/demote}. The local
		-- flag exists so the TUI pre-check (gating /add, /kick, etc.) can
		-- consult it without a server round-trip, and so it survives
		-- restart. is_admin is deliberately NOT part of the StoreGroup
		-- upsert — promote/demote events land via SetLocalUserGroupAdmin
		-- so they can't clobber the members list during a normal sync.
		CREATE TABLE IF NOT EXISTS groups (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL DEFAULT '',
			members      TEXT NOT NULL DEFAULT '',
			is_admin     INTEGER NOT NULL DEFAULT 0,
			left_at      INTEGER NOT NULL DEFAULT 0,
			leave_reason TEXT NOT NULL DEFAULT ''
		);

		-- Phase 14: group_events is the local replay/audit table for
		-- admin-initiated group mutations. Populated by:
		--   (a) live group_event broadcasts from the server (join, leave,
		--       promote, demote, rename)
		--   (b) offline replay entries from sync_batch.Events on reconnect
		-- Reads feed the /audit one-shot overlay and any future history
		-- surface that wants to show "who did what and when".
		--
		-- Unlike the server (DB-per-context), the client is single-DB
		-- per server, so this is one table keyed by group_id rather than
		-- one table per group-{id}.db file. ts is INTEGER (unix seconds)
		-- to match the server's group_events.ts type — sync replay uses
		-- the same sinceTS watermark for both events and messages.
		CREATE TABLE IF NOT EXISTS group_events (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id TEXT NOT NULL,
			event    TEXT NOT NULL,
			user     TEXT NOT NULL,
			by       TEXT NOT NULL DEFAULT '',
			reason   TEXT NOT NULL DEFAULT '',
			name     TEXT NOT NULL DEFAULT '',
			quiet    INTEGER NOT NULL DEFAULT 0,
			ts       INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_group_events_group_ts ON group_events(group_id, ts);

		-- Phase 20: room_events is the local replay/audit table for
		-- room audit events (leave / join / topic / rename / retire).
		-- Populated by:
		--   (a) live room_event broadcasts from the server
		--   (b) offline replay entries from sync_batch.Events on reconnect
		-- Mirrors group_events in shape but keyed by room_id. Separate
		-- table (rather than a context-type column on group_events)
		-- keeps the existing group-side code paths untouched.
		CREATE TABLE IF NOT EXISTS room_events (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			room_id  TEXT NOT NULL,
			event    TEXT NOT NULL,
			user     TEXT NOT NULL,
			by       TEXT NOT NULL DEFAULT '',
			reason   TEXT NOT NULL DEFAULT '',
			name     TEXT NOT NULL DEFAULT '',
			ts       INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_room_events_room_ts ON room_events(room_id, ts);

		-- 1:1 DMs (local cache of DM partner info).
		-- hidden = 0 means visible in sidebar; 1 means removed from local view.
		-- left_at remains the server's per-user history cutoff mirror.
		CREATE TABLE IF NOT EXISTS direct_messages (
			id         TEXT PRIMARY KEY,
			user_a     TEXT NOT NULL,
			user_b     TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			left_at    INTEGER NOT NULL DEFAULT 0,
			hidden     INTEGER NOT NULL DEFAULT 0
		);

		-- Client state
		CREATE TABLE IF NOT EXISTS state (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	if err := s.ensureDirectMessageSchema(); err != nil {
		return err
	}
	if err := s.ensureRoomMembersSchema(); err != nil {
		return err
	}

	// FTS5 search index — optional, may not be available in all SQLite builds.
	// Only create the triggers if the VIRTUAL TABLE was created successfully —
	// otherwise INSERT/DELETE on messages would fail trying to update a
	// non-existent FTS table.
	_, ftsErr := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		body, sender, id UNINDEXED,
		content='messages', content_rowid='rowid'
	)`)
	s.hasFTS = ftsErr == nil
	if ftsErr == nil {
		s.db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, body, sender, id) VALUES (new.rowid, new.body, new.sender, new.id);
		END`)
		s.db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, body, sender, id) VALUES ('delete', old.rowid, old.body, old.sender, old.id);
		END`)
	}
	// If FTS5 isn't available, search falls back to LIKE queries

	return nil
}

// ensureMessagesServerOrderSchema clean-rebuilds the local messages cache when
// it predates server_order (S2). The app has no users, so there is no
// backwards-compatibility burden: an old messages table (plus its FTS index and
// triggers, which reference it) is dropped so init recreates it with the
// server_order column, CHECK constraints, and per-conversation unique indexes.
// Cached messages re-sync from the server. Fresh DBs never trigger this.
func (s *Store) ensureMessagesServerOrderSchema() error {
	if !s.tableExists("messages") || s.messageColumnExists("server_order") {
		return nil // fresh DB or already migrated
	}
	s.db.Exec(`DROP TRIGGER IF EXISTS messages_ai`)
	s.db.Exec(`DROP TRIGGER IF EXISTS messages_ad`)
	s.db.Exec(`DROP TABLE IF EXISTS messages_fts`)
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS messages`); err != nil {
		return fmt.Errorf("clean-rebuild messages for server_order: %w", err)
	}
	return nil
}

func (s *Store) tableExists(name string) bool {
	var got string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	return err == nil
}

func (s *Store) messageColumnExists(name string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if colName == name {
			return true
		}
	}
	return false
}

func (s *Store) ensureDirectMessageSchema() error {
	if s.dmColumnExists("hidden") {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE direct_messages ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("migrate direct_messages.hidden: %w", err)
	}
	return nil
}

func (s *Store) dmColumnExists(name string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(direct_messages)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if colName == name {
			return true
		}
	}
	return false
}

// ensureRoomMembersSchema adds the rooms.member_ids column on first run
// (V8). Column semantics:
//
//	NULL          = not loaded (no room_list received for this room yet)
//	""            = loaded with zero members (edge case)
//	"a,b,c,..."   = CSV of loaded member user IDs
//
// SetRoomMembers normalizes input before storing (trims and drops blank IDs,
// de-dupes keeping first occurrence, preserves server order). GetRoomMembers
// / GetAllLoadedRoomMembers return defensive copies. Retired rooms keep
// member_ids NULL (cleared by the retire path) — they have no member-list UI.
func (s *Store) ensureRoomMembersSchema() error {
	if s.roomsColumnExists("member_ids") {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE rooms ADD COLUMN member_ids TEXT`); err != nil {
		return fmt.Errorf("migrate rooms.member_ids: %w", err)
	}
	return nil
}

func (s *Store) roomsColumnExists(name string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(rooms)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if colName == name {
			return true
		}
	}
	return false
}
