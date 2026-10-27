package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	DisplayName  string   `json:"display_name"`
	Email        string   `json:"email"`
	PasswordHash string   `json:"-"`
	Role         string   `json:"role"`
	Disabled     bool     `json:"disabled"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
	LastLoginAt  int64    `json:"last_login_at"`
	GroupIDs     []string `json:"group_ids,omitempty"`
	// Providers lists the SSO providers linked to the user.
	Providers []string `json:"providers,omitempty"`
}

func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

const userColumns = "id, username, display_name, email, password_hash, role, disabled, created_at, updated_at, last_login_at"

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u        User
		disabled int
	)

	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.Role, &disabled,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	u.Disabled = disabled != 0

	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM "+s.t("users")).Scan(&n)

	return n, err
}

func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM "+s.t("users")+" WHERE role = ? AND disabled = 0", RoleAdmin).Scan(&n)

	return n, err
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	u, err := scanUser(s.queryRow(ctx, "SELECT "+userColumns+" FROM "+s.t("users")+" WHERE id = ?", id))
	if err != nil {
		return nil, err
	}

	u.GroupIDs, err = s.userGroupIDs(ctx, u.ID)

	return u, err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(s.queryRow(ctx, "SELECT "+userColumns+" FROM "+s.t("users")+" WHERE username = ?", username))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.query(ctx, "SELECT "+userColumns+" FROM "+s.t("users")+" ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	users := []*User{}
	byID := map[string]*User{}

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}

		u.GroupIDs = []string{}
		users = append(users, u)
		byID[u.ID] = u
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	members, err := s.query(ctx, "SELECT group_id, user_id FROM "+s.t("group_members"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = members.Close() }()

	for members.Next() {
		var groupID, userID string
		if err := members.Scan(&groupID, &userID); err != nil {
			return nil, err
		}

		if u := byID[userID]; u != nil {
			u.GroupIDs = append(u.GroupIDs, groupID)
		}
	}

	if err := members.Err(); err != nil {
		return nil, err
	}

	links, err := s.query(ctx, "SELECT DISTINCT provider, user_id FROM "+s.t("user_identities")+" ORDER BY provider")
	if err != nil {
		return nil, err
	}
	defer func() { _ = links.Close() }()

	for links.Next() {
		var provider, userID string
		if err := links.Scan(&provider, &userID); err != nil {
			return nil, err
		}

		if u := byID[userID]; u != nil {
			u.Providers = append(u.Providers, provider)
		}
	}

	return users, links.Err()
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	ts := now()
	u.ID = NewID()
	u.CreatedAt, u.UpdatedAt = ts, ts

	if u.Role == "" {
		u.Role = RoleUser
	}

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("users")+
			" (id, username, display_name, email, password_hash, role, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"),
			u.ID, u.Username, u.DisplayName, u.Email, u.PasswordHash, u.Role, b2i(u.Disabled), ts, ts); err != nil {
			return err
		}

		return s.setUserGroups(ctx, tx, u.ID, u.GroupIDs)
	}))
}

// UpdateUser stores the profile fields; the password is changed only when
// PasswordHash is not empty.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	u.UpdatedAt = now()

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("users")+
			" SET username = ?, display_name = ?, email = ?, role = ?, disabled = ?, updated_at = ? WHERE id = ?"),
			u.Username, u.DisplayName, u.Email, u.Role, b2i(u.Disabled), u.UpdatedAt, u.ID)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		if u.PasswordHash != "" {
			if _, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("users")+" SET password_hash = ? WHERE id = ?"),
				u.PasswordHash, u.ID); err != nil {
				return err
			}
		}

		if u.Disabled || u.PasswordHash != "" {
			if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("sessions")+" WHERE user_id = ?"), u.ID); err != nil {
				return err
			}
		}

		if u.GroupIDs != nil {
			return s.setUserGroups(ctx, tx, u.ID, u.GroupIDs)
		}

		return nil
	}))
}

func (s *Store) SetUserPassword(ctx context.Context, id, hash string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("users")+" SET password_hash = ?, updated_at = ? WHERE id = ?", hash, now(), id)

	return err
}

func (s *Store) TouchUserLogin(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("users")+" SET last_login_at = ? WHERE id = ?", now(), id)

	return err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("connection_access")+" WHERE subject_type = ? AND subject_id = ?"),
			SubjectUser, id); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("notebook_access")+" WHERE subject_type = ? AND subject_id = ?"),
			SubjectUser, id); err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("users")+" WHERE id = ?"), id)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		return nil
	})
}

func (s *Store) userGroupIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.query(ctx, "SELECT group_id FROM "+s.t("group_members")+" WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}

func (s *Store) setUserGroups(ctx context.Context, tx *sql.Tx, userID string, groupIDs []string) error {
	if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("group_members")+" WHERE user_id = ?"), userID); err != nil {
		return err
	}

	for _, gid := range uniq(groupIDs) {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("group_members")+" (group_id, user_id) VALUES (?, ?)"),
			gid, userID); err != nil {
			return fmt.Errorf("add user to group %s; %w", gid, err)
		}
	}

	return nil
}

func uniq(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, v := range values {
		if v == "" {
			continue
		}

		if _, ok := seen[v]; ok {
			continue
		}

		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}

// ////////////////////////////////////////////
// Sessions

type Session struct {
	TokenHash  string
	UserID     string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
	IP         string
	UserAgent  string
	// Remember keeps the session for the longer "remember me" lifetime.
	Remember bool
}

func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.exec(ctx, "INSERT INTO "+s.t("sessions")+
		" (token_hash, user_id, created_at, expires_at, last_seen_at, ip, user_agent, remember) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		sess.TokenHash, sess.UserID, sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt, sess.IP, sess.UserAgent, b2i(sess.Remember))

	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*Session, error) {
	var (
		sess     Session
		remember int
	)

	err := s.queryRow(ctx, "SELECT token_hash, user_id, created_at, expires_at, last_seen_at, ip, user_agent, remember FROM "+
		s.t("sessions")+" WHERE token_hash = ?", tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.IP, &sess.UserAgent, &remember)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	sess.Remember = remember != 0

	return &sess, err
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string, lastSeen, expiresAt int64) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("sessions")+" SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?",
		lastSeen, expiresAt, tokenHash)

	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("sessions")+" WHERE token_hash = ?", tokenHash)

	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("sessions")+" WHERE user_id = ?", userID)

	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("sessions")+" WHERE expires_at < ?", now())
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
