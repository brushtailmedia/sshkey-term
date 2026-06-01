package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// StoredAttachment holds attachment metadata persisted in the local DB.
// Includes the decrypt key so files can be downloaded and decrypted from
// DB-loaded messages without re-deriving keys.
type StoredAttachment struct {
	FileID      string `json:"file_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	DecryptKey  string `json:"decrypt_key"`            // base64-encoded symmetric key
	ContentHash string `json:"content_hash,omitempty"` // F11: E2E-committed hash of the encrypted bytes; verified on download
}

// StoredMessage represents a message in the local DB.
type StoredMessage struct {
	ID          string
	ServerOrder int64 // server's authoritative per-conversation commit order (S2)
	Sender      string
	Body        string
	TS          int64
	Room        string
	Group       string
	DM          string
	Epoch       int64
	ReplyTo     string
	Mentions    []string
	Deleted     bool
	DeletedBy   string
	Attachments []StoredAttachment
	EditedAt    int64 // Phase 15: 0 if never edited, else server's edit wall clock
}

// InsertMessage stores a decrypted message.
//
// Returns (inserted, err): inserted is true ONLY if a new row was
// written. False indicates the message ID was already present
// (INSERT OR IGNORE) — callers can use this to skip work that
// should only happen once per message (e.g. firing off an
// auto-preview download goroutine; without this check the same
// message arriving via live broadcast + sync_batch + history_result
// would queue three redundant download goroutines for the same
// fileID, each rewriting the cached file and invalidating the
// image-render cache's mod-time check).
func (s *Store) InsertMessage(msg StoredMessage) (bool, error) {
	// Validate the server-origin invariants in Go (clear errors for tests and
	// callers) before the SQL CHECK constraints act as the final backstop:
	// exactly one context column, and a positive server_order. A missing/zero
	// server_order or a malformed context is a bug (server regression or a
	// caller that didn't populate the row) and must fail loudly, not silently
	// drop the message.
	ctxCount := 0
	if msg.Room != "" {
		ctxCount++
	}
	if msg.Group != "" {
		ctxCount++
	}
	if msg.DM != "" {
		ctxCount++
	}
	if ctxCount != 1 {
		return false, fmt.Errorf("InsertMessage: exactly one of room/group/dm must be set (got %d) for id %q", ctxCount, msg.ID)
	}
	if msg.ServerOrder <= 0 {
		return false, fmt.Errorf("InsertMessage: server_order must be > 0 (got %d) for id %q", msg.ServerOrder, msg.ID)
	}

	mentions := strings.Join(msg.Mentions, ",")
	attachJSON := ""
	hasAttach := 0
	if len(msg.Attachments) > 0 {
		if b, err := json.Marshal(msg.Attachments); err == nil {
			attachJSON = string(b)
			hasAttach = 1
		}
	}
	// Targeted same-ID idempotency (ON CONFLICT(id) DO NOTHING) instead of a
	// broad INSERT OR IGNORE: a re-delivered identical message (live + sync +
	// history all re-hit the same id) converges quietly, but a duplicate
	// (context, server_order) from a buggy server, a CHECK violation, or any
	// other constraint failure surfaces as an error rather than being silently
	// swallowed.
	result, err := s.db.Exec(`
		INSERT INTO messages (id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, has_attachments, attachments, server_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		msg.ID, msg.Sender, msg.Body, msg.TS, msg.Room, msg.Group, msg.DM, msg.Epoch, msg.ReplyTo, mentions, hasAttach, attachJSON, msg.ServerOrder,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		// Driver always supports RowsAffected; this branch shouldn't
		// fire in practice. Treat as conservative "not new" so callers
		// don't fire side effects on a row whose insert state we
		// couldn't verify.
		return false, err
	}
	return affected > 0, nil
}

// GetRoomMessages returns the latest window of messages for a room, ordered
// oldest-first by server_order. The SQL pages newest-first (server_order DESC +
// LIMIT) to grab the most recent window, then reverses. Uses server_order rather
// than rowid: remote history backfill can insert older messages after newer
// local rows, so local rowid no longer tracks chronology (S3).
func (s *Store) GetRoomMessages(room string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages WHERE room = ? ORDER BY server_order DESC LIMIT ?`,
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

// GetGroupMessages returns the latest window of messages for a group DM,
// ordered oldest-first by server_order (see GetRoomMessages for why server_order
// not rowid).
func (s *Store) GetGroupMessages(groupID string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages WHERE group_id = ? ORDER BY server_order DESC LIMIT ?`,
		groupID, limit,
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

// GetDMMessages returns the latest window of messages for a 1:1 DM, ordered
// oldest-first by server_order (see GetRoomMessages for why server_order not
// rowid).
func (s *Store) GetDMMessages(dmID string, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages WHERE dm_id = ? ORDER BY server_order DESC LIMIT ?`,
		dmID, limit,
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

// GetMessagesBefore returns messages before a given ID for scroll-back,
// ordered oldest-first (chronological) to match GetRoomMessages /
// GetGroupMessages / GetDMMessages. The SQL pages newest-first
// (server_order DESC + LIMIT) to grab the page immediately below the cursor,
// then reverses so the scroll-back caller can prepend the batch in
// display order without re-sorting. Pages by server_order, not rowid:
// remote history backfill can insert older messages after newer local rows,
// so rowid no longer tracks chronology (S3). If beforeID is unknown locally
// the server_order subquery is NULL and the comparison matches no rows, so the
// caller falls back to a server history probe with the original cursor.
func (s *Store) GetMessagesBefore(room, groupID, dmID, beforeID string, limit int) ([]StoredMessage, error) {
	var rows *sql.Rows
	var err error

	var col, val string
	switch {
	case room != "":
		col, val = "room", room
	case groupID != "":
		col, val = "group_id", groupID
	case dmID != "":
		col, val = "dm_id", dmID
	default:
		return nil, nil
	}

	rows, err = s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages WHERE `+col+` = ? AND server_order < (SELECT server_order FROM messages WHERE id = ?)
		ORDER BY server_order DESC LIMIT ?`,
		val, beforeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	// Reverse so oldest first (the SQL pages server_order DESC for correct
	// before-cursor pagination; callers prepend in chronological order).
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// contextColumn returns the (column, value) for the single set context, with ok
// = false unless exactly one of room/group/dm is set. The returned column is one
// of the fixed unified-messages-table context columns, so it is safe to
// interpolate into a SQL predicate.
func contextColumn(room, group, dm string) (col, val string, ok bool) {
	n := 0
	if room != "" {
		n++
		col, val = "room", room
	}
	if group != "" {
		n++
		col, val = "group_id", group
	}
	if dm != "" {
		n++
		col, val = "dm_id", dm
	}
	return col, val, n == 1
}

// DeleteMessage soft-deletes a message by id ONLY and hard-deletes its
// reactions. Returns file IDs from stored attachments for cache cleanup.
//
// id-only: fine for local/test callers (the id is the messages-table PRIMARY
// KEY, so at most one row matches). Remote, signature-verified `deleted`
// tombstones MUST instead use DeleteMessageInContext so the local mutation is
// scoped to the same (kind, contextID, msgID) the deleter signed
// (VerifyDeleteAuthor) — a wrong-context tombstone then no-ops rather than
// blanking a same-id row stored under a different context (F6).
func (s *Store) DeleteMessage(id, deletedBy string) ([]string, error) {
	// Read attachment file IDs before soft-deleting
	var attachJSON string
	s.db.QueryRow(`SELECT attachments FROM messages WHERE id = ?`, id).Scan(&attachJSON)

	if err := s.DeleteReactionsForMessage(id); err != nil {
		return nil, err
	}
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

// softDeleteInContext soft-deletes a message scoped to (id, context): it blanks
// the body and sets deleted/deleted_by via a context-gated UPDATE
// (WHERE id = ? AND <ctx_col> = ?), so a tombstone signed for one context can
// never mutate a same-id row stored under a different context. Only when the
// UPDATE actually matches a row does it purge that message's reactions and
// return its attachment file IDs for cache cleanup. `affected` reports whether a
// row matched, letting callers decide what a no-match means: DeleteMessageInContext
// no-ops, UpsertDeletedMessage inserts an absent-row tombstone. Shared by both so
// the two context-scoped delete paths cannot drift. Errors only on a bad context
// (not exactly one) or a SQL failure.
func (s *Store) softDeleteInContext(id, deletedBy, room, group, dm string) (fileIDs []string, affected int64, err error) {
	col, val, ok := contextColumn(room, group, dm)
	if !ok {
		return nil, 0, fmt.Errorf("softDeleteInContext: exactly one of room/group/dm must be set for id %q", id)
	}

	// Capture attachment file IDs (same context-scoped predicate as the UPDATE)
	// before the soft-delete clears the row.
	var attachJSON string
	s.db.QueryRow(`SELECT attachments FROM messages WHERE id = ? AND `+col+` = ?`, id, val).Scan(&attachJSON)

	res, err := s.db.Exec(`UPDATE messages SET deleted = 1, deleted_by = ?, body = '' WHERE id = ? AND `+col+` = ?`,
		deletedBy, id, val)
	if err != nil {
		return nil, 0, err
	}
	affected, err = res.RowsAffected()
	if err != nil {
		return nil, 0, err
	}
	if affected == 0 {
		return nil, 0, nil
	}

	// Matched row: mirror DeleteMessage — purge reactions, return file IDs.
	if err := s.DeleteReactionsForMessage(id); err != nil {
		return nil, affected, err
	}
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
	return fileIDs, affected, nil
}

// DeleteMessageInContext soft-deletes a message scoped to its conversation
// context (room/group/dm) — the durable apply for a verified remote `deleted`
// tombstone on the LIVE path (F6). Unlike the id-only DeleteMessage it mutates
// only a row whose stored context matches the tombstone's signed context
// (VerifyDeleteAuthor binds (kind, contextID, msgID)); a wrong-context or
// not-currently-cached tombstone is a no-op returning (nil, nil) — errors are
// reserved for bad input / SQL failure. Returns attachment file IDs for cache
// cleanup when a row matched. The absent-row tombstone insert is the catch-up
// path's job (UpsertDeletedMessage), not the live path's.
func (s *Store) DeleteMessageInContext(id, deletedBy, room, group, dm string) ([]string, error) {
	fileIDs, _, err := s.softDeleteInContext(id, deletedBy, room, group, dm)
	return fileIDs, err
}

// UpsertDeletedMessage makes a remote `deleted` tombstone durable. Unlike
// DeleteMessage (which only soft-deletes an already-cached row), this also
// inserts a minimal tombstone when the original message was never cached — the
// history/catchup case where a client joins after a message was created *and*
// deleted, so the original `id` was never seen locally. Without this, such a
// tombstone renders once in the live history_result but vanishes on reload.
//
// It is update-first: a known row gets the same soft-delete shape as
// DeleteMessage (deleted flag set, deleted_by set, body cleared, reactions
// purged) and returns the row's attachment file IDs for the caller to clean
// up; an absent row gets a
// minimal inserted tombstone (no attachments, so no file IDs). It is
// idempotent/collision-safe — live, sync_batch, and history_result can all
// deliver the same tombstone — via UPDATE…RowsAffected + ON CONFLICT(id) DO
// NOTHING, never a broad INSERT OR IGNORE (which would also mask server_order /
// exact-one-context constraint violations on the insert path).
//
// Use this only from the history/catchup `deleted` path; the live delete event
// keeps using DeleteMessage so its product semantics (authoritative soft-delete
// of a visible cached row) are unchanged.
func (s *Store) UpsertDeletedMessage(id, deletedBy string, ts, serverOrder int64, room, group, dm string) ([]string, error) {
	// F6: soft-delete scoped to (id, context) so a wrong-context tombstone can't
	// mutate a same-id row in a different context; a foreign-context (or absent)
	// row matches nothing and falls through to the ON CONFLICT(id) DO NOTHING
	// insert below (no phantom tombstone). Shared with the live path via
	// softDeleteInContext, so the two context-scoped deletes can't drift.
	fileIDs, affected, err := s.softDeleteInContext(id, deletedBy, room, group, dm)
	if err != nil {
		return nil, err
	}
	if affected > 0 {
		// Known row: soft-deleted in place (reactions purged, file IDs returned).
		return fileIDs, nil
	}

	// Absent (or foreign-context) row: insert a minimal tombstone. Context is
	// already validated exactly-one by softDeleteInContext; the insert must also
	// satisfy InsertMessage's server_order > 0 invariant (validate in Go for a
	// clear error before the SQL CHECK).
	if serverOrder <= 0 {
		return nil, fmt.Errorf("UpsertDeletedMessage: server_order must be > 0 (got %d) for id %q", serverOrder, id)
	}

	// sender stays '' so the renderer uses deleted_by ("removed by <deleter>")
	// rather than mistaking the deleter for the original author.
	if _, err := s.db.Exec(`
		INSERT INTO messages (id, sender, body, ts, room, group_id, dm_id, deleted, deleted_by, server_order)
		VALUES (?, '', '', ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, ts, room, group, dm, deletedBy, serverOrder,
	); err != nil {
		return nil, err
	}
	return nil, nil
}

// DeleteReactionsForMessage removes all reactions for a single message id.
func (s *Store) DeleteReactionsForMessage(msgID string) error {
	_, err := s.db.Exec(`DELETE FROM reactions WHERE message_id = ?`, msgID)
	return err
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
//
// Search is global across rooms/groups/DMs, so it stays primarily timestamp-
// ordered for cross-conversation recency. server_order is per-conversation (not
// globally comparable), so it is used only as a deterministic tie-breaker
// (`ts DESC, server_order DESC`): within one conversation it disambiguates
// same-second messages by commit order, and across conversations it just makes
// the tie stable rather than arbitrary (S5). The FTS join still keys on rowid —
// that is the external-content row link (`content_rowid='rowid'`), not chronology.
func (s *Store) SearchMessages(query string, limit int) ([]StoredMessage, error) {
	// Try FTS5 first
	rows, err := s.db.Query(`
		SELECT m.id, m.sender, m.body, m.ts, m.room, m.group_id, m.dm_id, m.epoch, m.reply_to, m.mentions, m.deleted, m.deleted_by, m.attachments, m.edited_at, m.server_order
		FROM messages_fts f
		JOIN messages m ON f.rowid = m.rowid
		WHERE messages_fts MATCH ? AND m.deleted = 0
		ORDER BY m.ts DESC, m.server_order DESC LIMIT ?`,
		query, limit,
	)
	if err == nil {
		defer rows.Close()
		return scanMessages(rows)
	}

	// FTS5 not available — fall back to LIKE
	likeQuery := "%" + query + "%"
	rows, err = s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages
		WHERE deleted = 0 AND (body LIKE ? OR sender LIKE ?)
		ORDER BY ts DESC, server_order DESC LIMIT ?`,
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
		err := rows.Scan(&m.ID, &m.Sender, &m.Body, &m.TS, &m.Room, &m.Group, &m.DM, &m.Epoch, &m.ReplyTo, &mentionsStr, &deleted, &m.DeletedBy, &attachJSON, &m.EditedAt, &m.ServerOrder)
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

// UpdateMessageEdited replaces a message's body and sets edited_at to
// the server's authoritative timestamp. Called from the client's
// dispatch path when `edited` / `group_edited` / `dm_edited` envelopes
// arrive. Also clears any locally-cached reactions for the edited
// message — matches Decision log Q12 in message_editing.md (client
// unconditionally clears reactions on receipt of an edited event).
// Returns the number of rows affected so callers can detect a miss.
func (s *Store) UpdateMessageEdited(msgID, newBody string, editedAt int64) (int64, error) {
	result, err := s.db.Exec(
		`UPDATE messages SET body = ?, edited_at = ? WHERE id = ? AND deleted = 0`,
		newBody, editedAt, msgID,
	)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		// Clear locally-cached reactions on the edited message.
		if err := s.DeleteReactionsForMessage(msgID); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// GetMessageByID fetches a single message row by id. Used by the edit
// flow: the client's send methods need to decrypt the original payload
// to copy payload-internal fields (ReplyTo / Attachments / etc.) into
// the new encrypted payload before sending the edit. Returns nil +
// sql.ErrNoRows if the row is missing.
func (s *Store) GetMessageByID(msgID string) (*StoredMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, sender, body, ts, room, group_id, dm_id, epoch, reply_to, mentions, deleted, deleted_by, attachments, edited_at, server_order
		FROM messages WHERE id = ? LIMIT 1`,
		msgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, sql.ErrNoRows
	}
	return &msgs[0], nil
}
