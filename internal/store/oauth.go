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

// OAuthClientInfo is a registered client with how it is used, for admins.
type OAuthClientInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	CreatedAt    int64    `json:"created_at"`
	// Tokens counts the access tokens it holds, Users the people they act
	// as, and LastUsedAt is when one of them was used last.
	Tokens     int   `json:"tokens"`
	Users      int   `json:"users"`
	LastUsedAt int64 `json:"last_used_at"`
}

// ListOAuthClients lists every registered client, newest first.
func (s *Store) ListOAuthClients(ctx context.Context) ([]OAuthClientInfo, error) {
	rows, err := s.query(ctx, "SELECT c.id, c.name, c.redirect_uris, c.created_at, COUNT(t.id), COUNT(DISTINCT t.user_id), COALESCE(MAX(t.last_used_at), 0) FROM "+
		s.t("oauth_clients")+" c LEFT JOIN "+s.t("access_tokens")+" t ON t.client_id = c.id GROUP BY c.id, c.name, c.redirect_uris, c.created_at ORDER BY c.created_at DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []OAuthClientInfo{}

	for rows.Next() {
		var (
			c    OAuthClientInfo
			uris string
		)

		if err := rows.Scan(&c.ID, &c.Name, &uris, &c.CreatedAt, &c.Tokens, &c.Users, &c.LastUsedAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
			c.RedirectURIs = []string{}
		}

		list = append(list, c)
	}

	return list, rows.Err()
}

// DeleteOAuthClient removes a client with its tokens, codes and remembered
// approvals, so it signs nobody in until it registers again.
func (s *Store) DeleteOAuthClient(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"access_tokens", "oauth_codes", "oauth_consents"} {
			if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t(table)+" WHERE client_id = ?"), id); err != nil {
				return err
			}
		}

		res, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("oauth_clients")+" WHERE id = ?"), id)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		return nil
	})
}

// DeleteUnusedOAuthClients removes clients that registered before the given
// time and never got a token, such as attempts that were not approved.
func (s *Store) DeleteUnusedOAuthClients(ctx context.Context, before int64) (int64, error) {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("oauth_clients")+" WHERE created_at < ? AND id NOT IN (SELECT client_id FROM "+
		s.t("access_tokens")+") AND id NOT IN (SELECT client_id FROM "+s.t("oauth_codes")+") AND id NOT IN (SELECT client_id FROM "+s.t("oauth_consents")+")", before)
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
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
