package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Identity links a user to an account at an SSO provider.
type Identity struct {
	Provider    string `json:"provider"`
	Subject     string `json:"subject"`
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at"`
}

func (s *Store) GetIdentity(ctx context.Context, provider, subject string) (*Identity, error) {
	var id Identity

	err := s.queryRow(ctx, "SELECT provider, subject, user_id, email, created_at, last_login_at FROM "+s.t("user_identities")+
		" WHERE provider = ? AND subject = ?", provider, subject).
		Scan(&id.Provider, &id.Subject, &id.UserID, &id.Email, &id.CreatedAt, &id.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	return &id, err
}

func (s *Store) LinkIdentity(ctx context.Context, id *Identity) error {
	ts := now()
	id.CreatedAt, id.LastLoginAt = ts, ts

	_, err := s.exec(ctx, "INSERT INTO "+s.t("user_identities")+
		" (provider, subject, user_id, email, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?)",
		id.Provider, id.Subject, id.UserID, id.Email, ts, ts)

	return wrapWriteErr(err)
}

func (s *Store) TouchIdentity(ctx context.Context, provider, subject, email string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("user_identities")+" SET email = ?, last_login_at = ? WHERE provider = ? AND subject = ?",
		email, now(), provider, subject)

	return err
}

func (s *Store) UnlinkIdentities(ctx context.Context, userID string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("user_identities")+" WHERE user_id = ?", userID)

	return err
}

// GetUserByEmail finds the user with the given email, compared without case.
// It returns ErrConflict when more than one user has it, so an ambiguous
// address never links to an arbitrary account.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, ErrNotFound
	}

	rows, err := s.query(ctx, "SELECT "+userColumns+" FROM "+s.t("users")+" WHERE LOWER(email) = ?", email)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var found *User

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}

		if found != nil {
			return nil, ErrConflict
		}

		found = u
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if found == nil {
		return nil, ErrNotFound
	}

	return found, nil
}
