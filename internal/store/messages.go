package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// StoredAttachment holds attachment metadata persisted in the local DB.
// Includes the decrypt key so files can be downloaded and decrypted from
// DB-loaded messages without re-deriving keys.
type StoredAttachment struct {
	FileID     string `json:"file_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	DecryptKey string `json:"decrypt_key"` // base64-encoded symmetric key
}

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
	Deleted      bool
	DeletedBy    string
	Attachments  []StoredAttachment
}

// InsertMessage stores a decrypted message.
func (s *Store) InsertMessage(msg StoredMessage) error {
	mentions := strings.Join(msg.Mentions, ",")
	attachJSON := ""
	hasAttach := 0
	if len(msg.Attachments) > 0 {
		if b, err := json.Marshal(msg.Attachments); err == nil {
			attachJSON = string(b)
			hasAttach = 1
		}
	}
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO messages (id, sender, body, ts, room, conversation, epoch, reply_to, mentions, has_attachments, attachments)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.Sender, msg.Body, msg.TS, msg.Room, msg.Conversation, msg.Epoch, msg.ReplyTo, mentions, hasAttach, attachJSON,
	)
	return err
}

// GetRoomMessages returns messages for a room, ordered by timestamp ascending.
func (s *Store) GetRoomMessages(room string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions, deleted, deleted_by, attachments
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
		SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions, deleted, deleted_by, attachments
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
			SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions, deleted, deleted_by, attachments
			FROM messages WHERE room = ? AND rowid < (SELECT rowid FROM messages WHERE id = ?)
			ORDER BY rowid DESC LIMIT ?`,
			room, beforeID, limit,
		)
	} else {
		rows, err = s.db.Query(`
			SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions, deleted, deleted_by, attachments
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

// DeleteMessage soft-deletes a message and hard-deletes its reactions.
// Returns file IDs from stored attachments for cache cleanup.
func (s *Store) DeleteMessage(id, deletedBy string) ([]string, error) {
	// Read attachment file IDs before soft-deleting
	var attachJSON string
	s.db.QueryRow(`SELECT attachments FROM messages WHERE id = ?`, id).Scan(&attachJSON)

	s.db.Exec(`DELETE FROM reactions WHERE message_id = ?`, id)
	_, err := s.db.Exec(`UPDATE messages SET deleted = 1, deleted_by = ?, body = '' WHERE id = ?`,
		deletedBy, id)

	var fileIDs []string
	if attachJSON != "" {
		var atts []StoredAttachment
		if json.Unmarshal([]byte(attachJSON), &atts) == nil {
			for _, a := range atts {
				if a.FileID != "" {
					fileIDs = append(fileIDs, a.FileID)
				}
			}
		}
	}
	return fileIDs, err
}

// StoredReaction represents a decrypted reaction in the local DB.
type StoredReaction struct {
	ReactionID string
	MessageID  string
	User       string
	Emoji      string
	TS         int64
}

// InsertReaction stores a decrypted reaction locally.
func (s *Store) InsertReaction(r StoredReaction) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO reactions (reaction_id, message_id, user, emoji, ts)
		VALUES (?, ?, ?, ?, ?)`,
		r.ReactionID, r.MessageID, r.User, r.Emoji, r.TS,
	)
	return err
}

// DeleteReaction removes a reaction by its reaction_id.
func (s *Store) DeleteReaction(reactionID string) error {
	_, err := s.db.Exec(`DELETE FROM reactions WHERE reaction_id = ?`, reactionID)
	return err
}

// GetReactionsForMessages returns all reactions for a set of message IDs.
func (s *Store) GetReactionsForMessages(messageIDs []string) ([]StoredReaction, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	placeholders := ""
	args := make([]any, len(messageIDs))
	for i, id := range messageIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	rows, err := s.db.Query(`
		SELECT reaction_id, message_id, user, emoji, ts
		FROM reactions
		WHERE message_id IN (`+placeholders+`)
		ORDER BY ts ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reactions []StoredReaction
	for rows.Next() {
		var r StoredReaction
		if err := rows.Scan(&r.ReactionID, &r.MessageID, &r.User, &r.Emoji, &r.TS); err != nil {
			return nil, err
		}
		reactions = append(reactions, r)
	}
	return reactions, rows.Err()
}

// SearchMessages performs full-text search across all messages.
// Tries FTS5 first, falls back to LIKE if FTS5 is not available.
func (s *Store) SearchMessages(query string, limit int) ([]StoredMessage, error) {
	// Try FTS5 first
	rows, err := s.db.Query(`
		SELECT m.id, m.sender, m.body, m.ts, m.room, m.conversation, m.epoch, m.reply_to, m.mentions, m.deleted, m.deleted_by, m.attachments
		FROM messages_fts f
		JOIN messages m ON f.rowid = m.rowid
		WHERE messages_fts MATCH ? AND m.deleted = 0
		ORDER BY m.ts DESC LIMIT ?`,
		query, limit,
	)
	if err == nil {
		defer rows.Close()
		return scanMessages(rows)
	}

	// FTS5 not available — fall back to LIKE
	likeQuery := "%" + query + "%"
	rows, err = s.db.Query(`
		SELECT id, sender, body, ts, room, conversation, epoch, reply_to, mentions, deleted, deleted_by, attachments
		FROM messages
		WHERE deleted = 0 AND (body LIKE ? OR sender LIKE ?)
		ORDER BY ts DESC LIMIT ?`,
		likeQuery, likeQuery, limit,
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
		var deleted int
		var attachJSON string
		err := rows.Scan(&m.ID, &m.Sender, &m.Body, &m.TS, &m.Room, &m.Conversation, &m.Epoch, &m.ReplyTo, &mentionsStr, &deleted, &m.DeletedBy, &attachJSON)
		if err != nil {
			return nil, err
		}
		m.Deleted = deleted != 0
		if mentionsStr != "" {
			m.Mentions = strings.Split(mentionsStr, ",")
		}
		if attachJSON != "" {
			json.Unmarshal([]byte(attachJSON), &m.Attachments)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
