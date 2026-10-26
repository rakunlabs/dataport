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
	// ConnectionID limits the token to one connection; empty allows every
	// connection of the user.
	ConnectionID string `json:"connection_id"`
	// ClientID is the OAuth client that got the token; empty for tokens
	// made by hand.
	ClientID         string `json:"client_id"`
	RefreshHash      string `json:"-"`
	RefreshExpiresAt int64  `json:"-"`
}

const tokenColumns = "id, user_id, name, token_hash, read_only, created_at, expires_at, last_used_at, connection_id, client_id, refresh_hash, refresh_expires_at"

func scanToken(row interface{ Scan(...any) error }) (*AccessToken, error) {
	var (
		t        AccessToken
		readOnly int
	)

	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &readOnly, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
		&t.ConnectionID, &t.ClientID, &t.RefreshHash, &t.RefreshExpiresAt); err != nil {
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

	_, err := s.exec(ctx, "INSERT INTO "+s.t("access_tokens")+" ("+tokenColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		t.ID, t.UserID, t.Name, t.TokenHash, b2i(t.ReadOnly), t.CreatedAt, t.ExpiresAt, 0,
		t.ConnectionID, t.ClientID, t.RefreshHash, t.RefreshExpiresAt)

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

func (s *Store) GetAccessTokenByRefresh(ctx context.Context, hash string) (*AccessToken, error) {
	if hash == "" {
		return nil, ErrNotFound
	}

	return scanToken(s.queryRow(ctx, "SELECT "+tokenColumns+" FROM "+s.t("access_tokens")+" WHERE refresh_hash = ?", hash))
}

// RotateAccessToken gives a token new secrets, for an OAuth refresh. It
// fails with ErrNotFound when oldRefresh was already used, so a refresh
// token works once.
func (s *Store) RotateAccessToken(ctx context.Context, id, oldRefresh string, t *AccessToken) error {
	res, err := s.exec(ctx, "UPDATE "+s.t("access_tokens")+
		" SET token_hash = ?, expires_at = ?, refresh_hash = ?, refresh_expires_at = ? WHERE id = ? AND refresh_hash = ?",
		t.TokenHash, t.ExpiresAt, t.RefreshHash, t.RefreshExpiresAt, id, oldRefresh)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

// DeleteExpiredAccessTokens removes tokens that can no longer be used nor
// renewed.
func (s *Store) DeleteExpiredAccessTokens(ctx context.Context) (int64, error) {
	ts := now()

	res, err := s.exec(ctx, "DELETE FROM "+s.t("access_tokens")+
		" WHERE client_id <> '' AND expires_at > 0 AND expires_at < ? AND refresh_expires_at > 0 AND refresh_expires_at < ?", ts, ts)
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func (s *Store) GetAccessToken(ctx context.Context, userID, id string) (*AccessToken, error) {
	return scanToken(s.queryRow(ctx, "SELECT "+tokenColumns+" FROM "+s.t("access_tokens")+" WHERE id = ? AND user_id = ?", id, userID))
}
