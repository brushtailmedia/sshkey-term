package store

import "time"

// StoreEpochKey saves an unwrapped epoch key locally.
func (s *Store) StoreEpochKey(room string, epoch int64, key []byte) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO epoch_keys (room, epoch, key) VALUES (?, ?, ?)`,
		room, epoch, key,
	)
	return err
}

// GetEpochKey retrieves a stored epoch key.
func (s *Store) GetEpochKey(room string, epoch int64) ([]byte, error) {
	var key []byte
	err := s.db.QueryRow(`SELECT key FROM epoch_keys WHERE room = ? AND epoch = ?`, room, epoch).Scan(&key)
	return key, err
}

// PinKey stores a user's public key fingerprint (TOFU).
func (s *Store) PinKey(user, fingerprint, pubkey string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO pinned_keys (user, fingerprint, pubkey, first_seen, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user) DO UPDATE SET
			fingerprint = excluded.fingerprint,
			pubkey = excluded.pubkey,
			updated_at = excluded.updated_at`,
		user, fingerprint, pubkey, now, now,
	)
	return err
}

// GetPinnedKey returns the pinned fingerprint for a user.
func (s *Store) GetPinnedKey(user string) (fingerprint string, verified bool, err error) {
	var v int
	err = s.db.QueryRow(`SELECT fingerprint, verified FROM pinned_keys WHERE user = ?`, user).Scan(&fingerprint, &v)
	return fingerprint, v == 1, err
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

// GetSeqMark returns the high-water seq for a key.
func (s *Store) GetSeqMark(key string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT seq FROM seq_marks WHERE key = ?`, key).Scan(&seq)
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

// GetReadPosition returns the last read message ID for a target.
func (s *Store) GetReadPosition(target string) (string, error) {
	var lastRead string
	err := s.db.QueryRow(`SELECT last_read FROM read_positions WHERE target = ?`, target).Scan(&lastRead)
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

// SetState stores a client state value.
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO state (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// GetState retrieves a client state value.
func (s *Store) GetState(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM state WHERE key = ?`, key).Scan(&value)
	return value, err
}
