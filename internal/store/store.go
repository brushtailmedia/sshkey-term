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
	// Create tables in separate execs to handle FTS5 gracefully
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id              TEXT PRIMARY KEY,
			sender          TEXT NOT NULL,
			body            TEXT NOT NULL,
			ts              INTEGER NOT NULL,
			room            TEXT NOT NULL DEFAULT '',
			conversation    TEXT NOT NULL DEFAULT '',
			epoch           INTEGER NOT NULL DEFAULT 0,
			reply_to        TEXT NOT NULL DEFAULT '',
			mentions        TEXT NOT NULL DEFAULT '',
			has_attachments INTEGER NOT NULL DEFAULT 0,
			raw_payload     TEXT NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_messages_room_ts ON messages(room, ts) WHERE room != '';
		CREATE INDEX IF NOT EXISTS idx_messages_conv_ts ON messages(conversation, ts) WHERE conversation != '';

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

		-- Conversations (local cache of DM member lists + names)
		CREATE TABLE IF NOT EXISTS conversations (
			id      TEXT PRIMARY KEY,
			name    TEXT NOT NULL DEFAULT '',
			members TEXT NOT NULL DEFAULT ''
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
