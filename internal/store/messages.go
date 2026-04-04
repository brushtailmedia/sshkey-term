package store

import (
	"database/sql"
	"strings"
)

// StoredMessage represents a message in the local DB.
type StoredMessage struct {
	ID           string
	Sender       string
	Body         string
	TS           int64
	Room         string
	Conversation string
	Epoch        int64
	ReplyTo      string
	Mentions     []string
}

// InsertMessage stores a decrypted message.
func (s *Store) InsertMessage(msg StoredMessage) error {
	mentions := strings.Join(msg.Mentions, ",")
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO messages (id, sender, body, ts, room, conversation, epoch, reply_to, mentions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.Sender, msg.Body, msg.TS, msg.Room, msg.Conversation, msg.Epoch, msg.ReplyTo, mentions,
	)
	return err
}

// GetRoomMessages returns messages for a room, ordered by timestamp ascending.
func (s *Store) GetRoomMessages(room string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions
		FROM messages WHERE room = ? ORDER BY rowid DESC LIMIT ?`,
		room, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}

	// Reverse so oldest first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetConvMessages returns messages for a conversation, ordered by timestamp ascending.
func (s *Store) GetConvMessages(convID string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions
		FROM messages WHERE conversation = ? ORDER BY rowid DESC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetMessagesBefore returns messages before a given ID for scroll-back.
func (s *Store) GetMessagesBefore(room, convID, beforeID string, limit int) ([]StoredMessage, error) {
	var rows *sql.Rows
	var err error

	if room != "" {
		rows, err = s.db.Query(`
			SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions
			FROM messages WHERE room = ? AND rowid < (SELECT rowid FROM messages WHERE id = ?)
			ORDER BY rowid DESC LIMIT ?`,
			room, beforeID, limit,
		)
	} else {
		rows, err = s.db.Query(`
			SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions
			FROM messages WHERE conversation = ? AND rowid < (SELECT rowid FROM messages WHERE id = ?)
			ORDER BY rowid DESC LIMIT ?`,
			convID, beforeID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// DeleteMessage removes a message from the local DB.
func (s *Store) DeleteMessage(id string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

// SearchMessages performs full-text search across all messages.
func (s *Store) SearchMessages(query string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.sender, m.body, m.ts, m.room, m.conversation, m.epoch, m.reply_to, m.mentions
		FROM messages_fts f
		JOIN messages m ON f.rowid = m.rowid
		WHERE messages_fts MATCH ?
		ORDER BY m.ts DESC LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func scanMessages(rows *sql.Rows) ([]StoredMessage, error) {
	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var mentionsStr string
		err := rows.Scan(&m.ID, &m.Sender, &m.Body, &m.TS, &m.Room, &m.Conversation, &m.Epoch, &m.ReplyTo, &mentionsStr)
		if err != nil {
			return nil, err
		}
		if mentionsStr != "" {
			m.Mentions = strings.Split(mentionsStr, ",")
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
