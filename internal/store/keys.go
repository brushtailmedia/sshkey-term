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

// StoreReadPosition saves the read position for a room or conversation.
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

// StoreConversation caches a conversation's members and name.
func (s *Store) StoreConversation(id, name, members string) error {
	_, err := s.db.Exec(`
		INSERT INTO conversations (id, name, members) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, members = excluded.members`,
		id, name, members,
	)
	return err
}

// GetAllConversations loads all cached conversations. Returns a map of
// conversation ID → {name, members (comma-separated)}.
func (s *Store) GetAllConversations() (map[string][2]string, error) {
	rows, err := s.db.Query(`SELECT id, name, members FROM conversations`)
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
