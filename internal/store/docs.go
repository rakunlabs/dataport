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
	CronSpec      string
	NextRun       int64
	LeaseToken    string
	LeaseUntil    int64
}

const docColumns = "connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, cron_spec, next_run, lease_token, lease_until"
const docWhere = "connection_id = ? AND user_id = ? AND schema_name = ?"

func scanDoc(row interface{ Scan(...any) error }) (*DocSnapshot, error) {
	var d DocSnapshot
	if err := row.Scan(&d.ConnectionID, &d.UserID, &d.Schema, &d.SourceVersion, &d.Payload, &d.GeneratedAt,
		&d.AttemptedAt, &d.LastError, &d.CronSpec, &d.NextRun, &d.LeaseToken, &d.LeaseUntil); err != nil {
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

// ClaimDoc is an atomic, expiring lease. dueOnly also guards against two
// dispatchers processing the same scheduled occurrence after it completes.
func (s *Store) ClaimDoc(ctx context.Context, k DocKey, token string, until int64, dueOnly bool) (bool, error) {
	ts := now()
	query := "UPDATE " + s.t("schema_docs") + " SET lease_token = ?, lease_until = ?, attempted_at = ? WHERE " + docWhere + " AND lease_until <= ?"
	args := []any{token, until, ts, k.ConnectionID, k.UserID, k.Schema, ts}
	if dueOnly {
		query += " AND cron_spec <> '' AND next_run > 0 AND next_run <= ?"
		args = append(args, ts)
	}
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishDoc preserves the last successful payload on failure. The lease
// token fences slow/expired writers, and a concurrently edited schedule wins.
func (s *Store) FinishDoc(ctx context.Context, d *DocSnapshot, token string, success bool, next int64) (bool, error) {
	query := "UPDATE " + s.t("schema_docs") + " SET last_error = ?, lease_token = '', lease_until = 0, next_run = CASE WHEN cron_spec = ? THEN ? ELSE next_run END"
	args := []any{d.LastError, d.CronSpec, next}
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

func (s *Store) SetDocSchedule(ctx context.Context, k DocKey, spec string, next int64) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("schema_docs")+" SET cron_spec = ?, next_run = ? WHERE "+docWhere,
		spec, next, k.ConnectionID, k.UserID, k.Schema)
	return err
}

func (s *Store) DueDocs(ctx context.Context) ([]DocKey, error) {
	ts := now()
	rows, err := s.query(ctx, "SELECT connection_id, user_id, schema_name FROM "+s.t("schema_docs")+
		" WHERE cron_spec <> '' AND next_run > 0 AND next_run <= ? AND lease_until <= ? ORDER BY next_run LIMIT 100", ts, ts)
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
