package store

import (
	"context"
	"database/sql"
	"errors"
)

// AccessToken lets a program, such as an MCP client, act as its user. The
// token itself is shown once; only its hash is stored.
type AccessToken struct {
	ID         string `json:"id"`
	UserID     string `json:"-"`
	Name       string `json:"name"`
	TokenHash  string `json:"-"`
	ReadOnly   bool   `json:"read_only"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

const tokenColumns = "id, user_id, name, token_hash, read_only, created_at, expires_at, last_used_at"

func scanToken(row interface{ Scan(...any) error }) (*AccessToken, error) {
	var (
		t        AccessToken
		readOnly int
	)

	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &readOnly, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	t.ReadOnly = readOnly != 0

	return &t, nil
}

func (s *Store) CreateAccessToken(ctx context.Context, t *AccessToken) error {
	t.ID = NewID()
	t.CreatedAt = now()

	_, err := s.exec(ctx, "INSERT INTO "+s.t("access_tokens")+" ("+tokenColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		t.ID, t.UserID, t.Name, t.TokenHash, b2i(t.ReadOnly), t.CreatedAt, t.ExpiresAt, 0)

	return wrapWriteErr(err)
}

func (s *Store) ListAccessTokens(ctx context.Context, userID string) ([]*AccessToken, error) {
	rows, err := s.query(ctx, "SELECT "+tokenColumns+" FROM "+s.t("access_tokens")+" WHERE user_id = ? ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*AccessToken{}

	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}

		list = append(list, t)
	}

	return list, rows.Err()
}

func (s *Store) GetAccessTokenByHash(ctx context.Context, hash string) (*AccessToken, error) {
	return scanToken(s.queryRow(ctx, "SELECT "+tokenColumns+" FROM "+s.t("access_tokens")+" WHERE token_hash = ?", hash))
}

func (s *Store) TouchAccessToken(ctx context.Context, id string, ts int64) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("access_tokens")+" SET last_used_at = ? WHERE id = ?", ts, id)

	return err
}

func (s *Store) DeleteAccessToken(ctx context.Context, userID, id string) error {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("access_tokens")+" WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}
