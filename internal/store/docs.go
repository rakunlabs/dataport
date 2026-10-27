package store

import (
	"context"
	"database/sql"
	"errors"
)

type DocKey struct {
	ConnectionID string
	UserID       string
	Schema       string
}

type DocSnapshot struct {
	DocKey
	SourceVersion string
	Payload       string
	GeneratedAt   int64
	AttemptedAt   int64
	LastError     string
	LeaseToken    string
	LeaseUntil    int64
}

const docColumns = "connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, lease_token, lease_until"
const docWhere = "connection_id = ? AND user_id = ? AND schema_name = ?"

func scanDoc(row interface{ Scan(...any) error }) (*DocSnapshot, error) {
	var d DocSnapshot
	if err := row.Scan(&d.ConnectionID, &d.UserID, &d.Schema, &d.SourceVersion, &d.Payload, &d.GeneratedAt,
		&d.AttemptedAt, &d.LastError, &d.LeaseToken, &d.LeaseUntil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) EnsureDoc(ctx context.Context, k DocKey) error {
	_, err := s.exec(ctx, "INSERT INTO "+s.t("schema_docs")+
		" (connection_id, user_id, schema_name) VALUES (?, ?, ?) ON CONFLICT (connection_id, user_id, schema_name) DO NOTHING",
		k.ConnectionID, k.UserID, k.Schema)
	return err
}

func (s *Store) GetDoc(ctx context.Context, k DocKey) (*DocSnapshot, error) {
	return scanDoc(s.queryRow(ctx, "SELECT "+docColumns+" FROM "+s.t("schema_docs")+" WHERE "+docWhere,
		k.ConnectionID, k.UserID, k.Schema))
}

// ClaimDoc is an atomic, expiring lease. A non-zero before claims only a
// snapshot not attempted since then, so two dispatchers cannot process the
// same scheduled occurrence.
func (s *Store) ClaimDoc(ctx context.Context, k DocKey, token string, until int64, before int64) (bool, error) {
	ts := now()
	query := "UPDATE " + s.t("schema_docs") + " SET lease_token = ?, lease_until = ?, attempted_at = ? WHERE " + docWhere + " AND lease_until <= ?"
	args := []any{token, until, ts, k.ConnectionID, k.UserID, k.Schema, ts}
	if before > 0 {
		query += " AND attempted_at < ?"
		args = append(args, before)
	}
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishDoc preserves the last successful payload on failure. The lease
// token fences slow/expired writers.
func (s *Store) FinishDoc(ctx context.Context, d *DocSnapshot, token string, success bool) (bool, error) {
	query := "UPDATE " + s.t("schema_docs") + " SET last_error = ?, lease_token = '', lease_until = 0"
	args := []any{d.LastError}
	if success {
		query += ", payload = ?, source_version = ?, generated_at = ?"
		args = append(args, d.Payload, d.SourceVersion, d.GeneratedAt)
	}
	query += " WHERE " + docWhere + " AND lease_token = ?"
	args = append(args, d.ConnectionID, d.UserID, d.Schema, token)
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountDueDocs counts snapshots not attempted since before.
func (s *Store) CountDueDocs(ctx context.Context, before int64) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM "+s.t("schema_docs")+" WHERE attempted_at < ?", before).Scan(&n)
	return n, err
}

// DueDocs lists snapshots not attempted since before and not being refreshed.
func (s *Store) DueDocs(ctx context.Context, before int64) ([]DocKey, error) {
	ts := now()
	rows, err := s.query(ctx, "SELECT connection_id, user_id, schema_name FROM "+s.t("schema_docs")+
		" WHERE attempted_at < ? AND lease_until <= ? ORDER BY attempted_at LIMIT 100", before, ts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	list := []DocKey{}
	for rows.Next() {
		var k DocKey
		if err := rows.Scan(&k.ConnectionID, &k.UserID, &k.Schema); err != nil {
			return nil, err
		}
		list = append(list, k)
	}
	return list, rows.Err()
}

// DocInfo describes a saved snapshot without its document.
type DocInfo struct {
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	DBType         string `json:"db_type"`
	UserID         string `json:"user_id,omitempty"`
	Username       string `json:"username,omitempty"`
	Schema         string `json:"schema"`
	GeneratedAt    int64  `json:"generated_at"`
	AttemptedAt    int64  `json:"attempted_at"`
	LastError      string `json:"last_error"`
	Refreshing     bool   `json:"refreshing"`
	// Ready is false until a first document was saved.
	Ready bool `json:"ready"`
}

const docInfoSelect = `SELECT d.connection_id, c.name, c.db_type, d.user_id, u.username, d.schema_name,
d.generated_at, d.attempted_at, d.last_error, d.lease_until`

func (s *Store) scanDocInfos(ctx context.Context, query string, args ...any) ([]DocInfo, error) {
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ts := now()
	list := []DocInfo{}
	for rows.Next() {
		var (
			d     DocInfo
			lease int64
		)
		if err := rows.Scan(&d.ConnectionID, &d.ConnectionName, &d.DBType, &d.UserID, &d.Username, &d.Schema,
			&d.GeneratedAt, &d.AttemptedAt, &d.LastError, &lease); err != nil {
			return nil, err
		}
		d.Refreshing = lease > ts
		d.Ready = d.GeneratedAt > 0
		list = append(list, d)
	}
	return list, rows.Err()
}

// DocOverview lists every saved snapshot, for admins.
func (s *Store) DocOverview(ctx context.Context) ([]DocInfo, error) {
	return s.scanDocInfos(ctx, docInfoSelect+" FROM "+s.t("schema_docs")+" d JOIN "+s.t("connections")+" c ON c.id = d.connection_id JOIN "+
		s.t("users")+" u ON u.id = d.user_id ORDER BY c.name, d.schema_name, u.username")
}

// UserDocs lists the snapshots of one user.
func (s *Store) UserDocs(ctx context.Context, userID string) ([]DocInfo, error) {
	list, err := s.scanDocInfos(ctx, docInfoSelect+" FROM "+s.t("schema_docs")+" d JOIN "+s.t("connections")+" c ON c.id = d.connection_id JOIN "+
		s.t("users")+" u ON u.id = d.user_id WHERE d.user_id = ? ORDER BY c.name, d.schema_name", userID)
	for i := range list {
		list[i].UserID, list[i].Username = "", ""
	}
	return list, err
}

