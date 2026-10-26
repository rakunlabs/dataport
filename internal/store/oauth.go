package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// OAuth clients and authorization codes, for MCP clients that sign in
// through the browser instead of a token pasted by hand.

type OAuthClient struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	CreatedAt    int64    `json:"-"`
}

func (s *Store) CreateOAuthClient(ctx context.Context, c *OAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}

	c.ID = NewID()
	c.CreatedAt = now()

	_, err = s.exec(ctx, "INSERT INTO "+s.t("oauth_clients")+" (id, name, redirect_uris, created_at) VALUES (?, ?, ?, ?)",
		c.ID, c.Name, string(uris), c.CreatedAt)

	return wrapWriteErr(err)
}

func (s *Store) GetOAuthClient(ctx context.Context, id string) (*OAuthClient, error) {
	var (
		c    OAuthClient
		uris string
	)

	err := s.queryRow(ctx, "SELECT id, name, redirect_uris, created_at FROM "+s.t("oauth_clients")+" WHERE id = ?", id).
		Scan(&c.ID, &c.Name, &uris, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return nil, err
	}

	return &c, nil
}

// OAuthCode is an authorization code waiting to be exchanged for a token.
type OAuthCode struct {
	CodeHash      string
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	ConnectionID  string
	ReadOnly      bool
	ExpiresAt     int64
}

func (s *Store) CreateOAuthCode(ctx context.Context, c *OAuthCode) error {
	_, err := s.exec(ctx, "INSERT INTO "+s.t("oauth_codes")+
		" (code_hash, client_id, user_id, redirect_uri, code_challenge, connection_id, read_only, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		c.CodeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, c.ConnectionID, b2i(c.ReadOnly), c.ExpiresAt)

	return wrapWriteErr(err)
}

// TakeOAuthCode returns a code and deletes it, so it is used once.
func (s *Store) TakeOAuthCode(ctx context.Context, hash string) (*OAuthCode, error) {
	var c OAuthCode

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var readOnly int

		err := tx.QueryRowContext(ctx, s.q("SELECT code_hash, client_id, user_id, redirect_uri, code_challenge, connection_id, read_only, expires_at FROM "+
			s.t("oauth_codes")+" WHERE code_hash = ?"), hash).
			Scan(&c.CodeHash, &c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.ConnectionID, &readOnly, &c.ExpiresAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}

			return err
		}

		c.ReadOnly = readOnly != 0

		res, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("oauth_codes")+" WHERE code_hash = ?"), hash)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if c.ExpiresAt < now() {
		return nil, ErrNotFound
	}

	return &c, nil
}

func (s *Store) DeleteExpiredOAuthCodes(ctx context.Context) (int64, error) {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("oauth_codes")+" WHERE expires_at < ?", now())
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

// OAuthConsent is an approval the user asked to remember.
type OAuthConsent struct {
	UserID       string
	ClientID     string
	ConnectionID string
	ReadOnly     bool
}

func (s *Store) SaveOAuthConsent(ctx context.Context, c *OAuthConsent) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("oauth_consents")+" WHERE user_id = ? AND client_id = ?"), c.UserID, c.ClientID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("oauth_consents")+
			" (user_id, client_id, connection_id, read_only, created_at) VALUES (?, ?, ?, ?, ?)"),
			c.UserID, c.ClientID, c.ConnectionID, b2i(c.ReadOnly), now())

		return err
	})
}

func (s *Store) GetOAuthConsent(ctx context.Context, userID, clientID string) (*OAuthConsent, error) {
	var (
		c        = OAuthConsent{UserID: userID, ClientID: clientID}
		readOnly int
	)

	err := s.queryRow(ctx, "SELECT connection_id, read_only FROM "+s.t("oauth_consents")+" WHERE user_id = ? AND client_id = ?", userID, clientID).
		Scan(&c.ConnectionID, &readOnly)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	c.ReadOnly = readOnly != 0

	return &c, nil
}

func (s *Store) DeleteOAuthConsent(ctx context.Context, userID, clientID string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("oauth_consents")+" WHERE user_id = ? AND client_id = ?", userID, clientID)

	return err
}
