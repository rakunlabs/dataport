package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type QueryLog struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	Database       string `json:"database"`
	SQL            string `json:"sql"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	RowCount       int64  `json:"row_count"`
	DurationMS     int64  `json:"duration_ms"`
	// Source is where the statement came from: "web" or "mcp".
	Source    string `json:"source"`
	CreatedAt int64  `json:"created_at"`
}

const maxLoggedSQL = 64 * 1024

func (s *Store) AddQueryLog(ctx context.Context, l *QueryLog) error {
	l.ID = NewID()
	if l.CreatedAt == 0 {
		l.CreatedAt = now()
	}

	if l.Source == "" {
		l.Source = "web"
	}

	if len(l.SQL) > maxLoggedSQL {
		l.SQL = l.SQL[:maxLoggedSQL]
	}

	_, err := s.exec(ctx, "INSERT INTO "+s.t("query_log")+
		" (id, user_id, username, connection_id, connection_name, database_name, sql_text, status, error, row_count, duration_ms, source, created_at)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		l.ID, l.UserID, l.Username, l.ConnectionID, l.ConnectionName, l.Database, l.SQL, l.Status, l.Error,
		l.RowCount, l.DurationMS, l.Source, l.CreatedAt)

	return err
}

type QueryLogFilter struct {
	UserID       string
	ConnectionID string
	Search       string
	Source       string
	Before       int64
	Limit        int
}

func (s *Store) ListQueryLog(ctx context.Context, f QueryLogFilter) ([]*QueryLog, error) {
	var (
		where []string
		args  []any
	)

	if f.UserID != "" {
		where = append(where, "user_id = ?")
		args = append(args, f.UserID)
	}

	if f.ConnectionID != "" {
		where = append(where, "connection_id = ?")
		args = append(args, f.ConnectionID)
	}

	if f.Search != "" {
		where = append(where, "LOWER(sql_text) LIKE ?")
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
	}

	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}

	if f.Before > 0 {
		where = append(where, "created_at < ?")
		args = append(args, f.Before)
	}

	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	query := "SELECT id, user_id, username, connection_id, connection_name, database_name, sql_text, status, error, row_count, duration_ms, source, created_at FROM " +
		s.t("query_log")
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, f.Limit)

	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*QueryLog{}

	for rows.Next() {
		var l QueryLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.ConnectionID, &l.ConnectionName, &l.Database, &l.SQL,
			&l.Status, &l.Error, &l.RowCount, &l.DurationMS, &l.Source, &l.CreatedAt); err != nil {
			return nil, err
		}

		list = append(list, &l)
	}

	return list, rows.Err()
}

// ////////////////////////////////////////////
// Saved queries

type SavedQuery struct {
	ID           string `json:"id"`
	UserID       string `json:"-"`
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	SQL          string `json:"sql"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

func (s *Store) ListSavedQueries(ctx context.Context, userID string) ([]*SavedQuery, error) {
	rows, err := s.query(ctx, "SELECT id, user_id, connection_id, name, sql_text, created_at, updated_at FROM "+
		s.t("saved_queries")+" WHERE user_id = ? ORDER BY name", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*SavedQuery{}

	for rows.Next() {
		var q SavedQuery
		if err := rows.Scan(&q.ID, &q.UserID, &q.ConnectionID, &q.Name, &q.SQL, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, err
		}

		list = append(list, &q)
	}

	return list, rows.Err()
}

func (s *Store) GetSavedQuery(ctx context.Context, userID, id string) (*SavedQuery, error) {
	var q SavedQuery

	err := s.queryRow(ctx, "SELECT id, user_id, connection_id, name, sql_text, created_at, updated_at FROM "+
		s.t("saved_queries")+" WHERE id = ? AND user_id = ?", id, userID).
		Scan(&q.ID, &q.UserID, &q.ConnectionID, &q.Name, &q.SQL, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	return &q, err
}

func (s *Store) CreateSavedQuery(ctx context.Context, q *SavedQuery) error {
	ts := now()
	q.ID = NewID()
	q.CreatedAt, q.UpdatedAt = ts, ts

	_, err := s.exec(ctx, "INSERT INTO "+s.t("saved_queries")+
		" (id, user_id, connection_id, name, sql_text, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		q.ID, q.UserID, q.ConnectionID, q.Name, q.SQL, ts, ts)

	return err
}

func (s *Store) UpdateSavedQuery(ctx context.Context, q *SavedQuery) error {
	q.UpdatedAt = now()

	res, err := s.exec(ctx, "UPDATE "+s.t("saved_queries")+
		" SET connection_id = ?, name = ?, sql_text = ?, updated_at = ? WHERE id = ? AND user_id = ?",
		q.ConnectionID, q.Name, q.SQL, q.UpdatedAt, q.ID, q.UserID)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) DeleteSavedQuery(ctx context.Context, userID, id string) error {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("saved_queries")+" WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}
