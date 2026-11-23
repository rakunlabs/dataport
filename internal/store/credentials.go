package store

import (
	"context"
	"database/sql"
	"errors"
)

// Credential is a user's own account at the database of a connection that
// has UserCredentials set. The password is encrypted like the connection's.
type Credential struct {
	ConnectionID string
	UserID       string
	Username     string
	PasswordEnc  string
	// Limits are the user's pool settings over the connection's.
	Limits    Limits
	CreatedAt int64
	UpdatedAt int64
}

func (s *Store) GetCredential(ctx context.Context, connectionID, userID string) (*Credential, error) {
	var (
		c      Credential
		limits string
	)

	err := s.queryRow(ctx, "SELECT connection_id, user_id, username, password_enc, limits, created_at, updated_at FROM "+s.t("user_credentials")+
		" WHERE connection_id = ? AND user_id = ?", connectionID, userID).
		Scan(&c.ConnectionID, &c.UserID, &c.Username, &c.PasswordEnc, &limits, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	c.Limits = parseLimits(limits)

	return &c, err
}

// UserCredentials returns the stored accounts of a user, by connection id,
// without their passwords.
func (s *Store) UserCredentials(ctx context.Context, userID string) (map[string]*Credential, error) {
	rows, err := s.query(ctx, "SELECT connection_id, username, limits FROM "+s.t("user_credentials")+" WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]*Credential{}

	for rows.Next() {
		var (
			c      = &Credential{UserID: userID}
			limits string
		)

		if err := rows.Scan(&c.ConnectionID, &c.Username, &limits); err != nil {
			return nil, err
		}

		c.Limits = parseLimits(limits)
		out[c.ConnectionID] = c
	}

	return out, rows.Err()
}

func (s *Store) SetCredential(ctx context.Context, c *Credential) error {
	ts := now()
	c.UpdatedAt = ts

	// One statement, so two first saves at once do not race into a
	// duplicate key; an existing row keeps its created_at.
	_, err := s.exec(ctx, "INSERT INTO "+s.t("user_credentials")+
		" (connection_id, user_id, username, password_enc, limits, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)"+
		" ON CONFLICT (connection_id, user_id) DO UPDATE SET username = excluded.username, password_enc = excluded.password_enc,"+
		" limits = excluded.limits, updated_at = excluded.updated_at",
		c.ConnectionID, c.UserID, c.Username, c.PasswordEnc, limitsJSON(c.Limits), ts, ts)

	return err
}

func (s *Store) DeleteCredential(ctx context.Context, connectionID, userID string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("user_credentials")+" WHERE connection_id = ? AND user_id = ?", connectionID, userID)

	return err
}

// DeleteConnectionCredentials removes every user's account of a connection,
// for when an admin switches it back to a shared account.
func (s *Store) DeleteConnectionCredentials(ctx context.Context, connectionID string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("user_credentials")+" WHERE connection_id = ?", connectionID)

	return err
}
