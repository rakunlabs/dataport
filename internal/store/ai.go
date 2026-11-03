package store

import (
	"context"
	"database/sql"
	"errors"
)

// AIConversation is one conversation of a user with the AI assistant.
type AIConversation struct {
	ID        string `json:"id"`
	UserID    string `json:"-"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// AIMessage is a stored chat message; Payload is its JSON.
type AIMessage struct {
	Seq       int    `json:"seq"`
	Payload   string `json:"-"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) CreateAIConversation(ctx context.Context, c *AIConversation) error {
	ts := now()
	c.ID, c.CreatedAt, c.UpdatedAt = NewID(), ts, ts

	_, err := s.exec(ctx, "INSERT INTO "+s.t("ai_conversations")+" (id, user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		c.ID, c.UserID, c.Title, ts, ts)

	return wrapWriteErr(err)
}

// ListAIConversations lists the conversations of a user, latest first.
func (s *Store) ListAIConversations(ctx context.Context, userID string) ([]*AIConversation, error) {
	rows, err := s.query(ctx, "SELECT id, user_id, title, created_at, updated_at FROM "+s.t("ai_conversations")+
		" WHERE user_id = ? ORDER BY updated_at DESC LIMIT 500", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*AIConversation{}

	for rows.Next() {
		var c AIConversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}

		list = append(list, &c)
	}

	return list, rows.Err()
}

// GetAIConversation returns a conversation of the user; another user's is
// not found.
func (s *Store) GetAIConversation(ctx context.Context, userID, id string) (*AIConversation, error) {
	var c AIConversation

	err := s.queryRow(ctx, "SELECT id, user_id, title, created_at, updated_at FROM "+s.t("ai_conversations")+" WHERE id = ? AND user_id = ?", id, userID).
		Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	return &c, err
}

func (s *Store) RenameAIConversation(ctx context.Context, userID, id, title string) error {
	res, err := s.exec(ctx, "UPDATE "+s.t("ai_conversations")+" SET title = ? WHERE id = ? AND user_id = ?", title, id, userID)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

// DeleteAIConversation removes a conversation with its messages.
func (s *Store) DeleteAIConversation(ctx context.Context, userID, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("ai_conversations")+" WHERE id = ? AND user_id = ?"), id, userID)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		_, err = tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("ai_messages")+" WHERE conversation_id = ?"), id)

		return err
	})
}

// DeleteAIConversations removes every conversation of a user.
func (s *Store) DeleteAIConversations(ctx context.Context, userID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("ai_messages")+" WHERE conversation_id IN (SELECT id FROM "+
			s.t("ai_conversations")+" WHERE user_id = ?)"), userID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("ai_conversations")+" WHERE user_id = ?"), userID)

		return err
	})
}

// AppendAIMessage adds a message at the end of a conversation. Two sends
// to one conversation at once may pick the same place; the later one tries
// the next.
func (s *Store) AppendAIMessage(ctx context.Context, conversationID, payload string) (*AIMessage, error) {
	var (
		m   *AIMessage
		err error
	)

	for range 5 {
		if m, err = s.appendAIMessage(ctx, conversationID, payload); !errors.Is(err, ErrConflict) {
			break
		}
	}

	return m, err
}

func (s *Store) appendAIMessage(ctx context.Context, conversationID, payload string) (*AIMessage, error) {
	m := &AIMessage{Payload: payload, CreatedAt: now()}

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, s.q("SELECT COALESCE(MAX(seq), 0) + 1 FROM "+s.t("ai_messages")+" WHERE conversation_id = ?"), conversationID).
			Scan(&m.Seq); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("ai_messages")+" (conversation_id, seq, payload, created_at) VALUES (?, ?, ?, ?)"),
			conversationID, m.Seq, payload, m.CreatedAt); err != nil {
			return wrapWriteErr(err)
		}

		_, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("ai_conversations")+" SET updated_at = ? WHERE id = ?"), m.CreatedAt, conversationID)

		return err
	})

	return m, err
}

// DeleteAIMessagesFrom removes the messages of a conversation from seq on,
// to answer a turn again.
func (s *Store) DeleteAIMessagesFrom(ctx context.Context, conversationID string, seq int) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("ai_messages")+" WHERE conversation_id = ? AND seq >= ?", conversationID, seq)

	return err
}

// AIMessages returns the messages of a conversation in order.
func (s *Store) AIMessages(ctx context.Context, conversationID string) ([]*AIMessage, error) {
	rows, err := s.query(ctx, "SELECT seq, payload, created_at FROM "+s.t("ai_messages")+" WHERE conversation_id = ? ORDER BY seq", conversationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*AIMessage{}

	for rows.Next() {
		var m AIMessage
		if err := rows.Scan(&m.Seq, &m.Payload, &m.CreatedAt); err != nil {
			return nil, err
		}

		list = append(list, &m)
	}

	return list, rows.Err()
}
