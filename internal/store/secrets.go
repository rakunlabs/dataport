package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ErrKeepSecret tells RewriteSecrets that a value was read and stays as it
// is, to check the secrets without writing.
var ErrKeepSecret = errors.New("keep the secret")

// SecretCount tells how many stored secrets a rewrite read (and sealed
// again), and how many it could not read and left as they were.
type SecretCount struct {
	Rewritten  int `json:"rewritten"`
	Unreadable int `json:"unreadable"`
}

// secretColumns are the encrypted columns, with the columns that find a
// row of their table.
var secretColumns = []struct {
	table  string
	keys   []string
	column string
}{
	{"connections", []string{"id"}, "password_enc"},
	{"connections", []string{"id"}, "proxy_enc"},
	{"user_credentials", []string{"connection_id", "user_id"}, "password_enc"},
	{"doc_connections", []string{"connection_id"}, "password_enc"},
}

// RewriteSecrets passes every encrypted value of the store through fn in one
// transaction: the password and proxy columns, and in settings stored as JSON every
// string under a key ending in "_enc". A value fn fails on is kept and
// counted as unreadable; one it answers ErrKeepSecret for is kept and
// counted as read.
func (s *Store) RewriteSecrets(ctx context.Context, fn func(string) (string, error)) (SecretCount, error) {
	return s.rewriteSecrets(ctx, fn, true)
}

// CheckSecrets passes every encrypted value through fn without locking or
// writing; fn answers ErrKeepSecret for a value it reads.
func (s *Store) CheckSecrets(ctx context.Context, fn func(string) (string, error)) (SecretCount, error) {
	return s.rewriteSecrets(ctx, fn, false)
}

func (s *Store) rewriteSecrets(ctx context.Context, fn func(string) (string, error), lock bool) (SecretCount, error) {
	var count SecretCount

	rewrite := func(v string) (string, bool) {
		if v == "" {
			return v, false
		}

		out, err := fn(v)
		if errors.Is(err, ErrKeepSecret) {
			count.Rewritten++

			return v, false
		}

		if err != nil {
			count.Unreadable++

			return v, false
		}

		count.Rewritten++

		return out, true
	}

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, t := range secretColumns {
			if err := s.rewriteColumn(ctx, tx, t.table, t.keys, t.column, rewrite, lock); err != nil {
				return err
			}
		}

		return s.rewriteSettings(ctx, tx, rewrite, lock)
	})

	return count, err
}

func (s *Store) rewriteColumn(ctx context.Context, tx *sql.Tx, table string, keys []string, column string, rewrite func(string) (string, bool), locked bool) error {
	// On PostgreSQL the rows are locked, so a save at the same time waits
	// instead of being written over with the value read here.
	lock := ""
	if s.postgres && locked {
		lock = " FOR UPDATE"
	}

	rows, err := tx.QueryContext(ctx, s.q("SELECT "+strings.Join(keys, ", ")+", "+column+" FROM "+s.t(table)+" WHERE "+column+" <> ''"+lock))
	if err != nil {
		return err
	}

	type row struct {
		keys  []any
		value string
	}

	var list []row

	for rows.Next() {
		ids := make([]string, len(keys))
		dest := make([]any, 0, len(keys)+1)

		for i := range ids {
			dest = append(dest, &ids[i])
		}

		var value string

		if err := rows.Scan(append(dest, &value)...); err != nil {
			_ = rows.Close()

			return err
		}

		r := row{value: value}
		for _, id := range ids {
			r.keys = append(r.keys, id)
		}

		list = append(list, r)
	}

	if err := rows.Close(); err != nil {
		return err
	}

	where := make([]string, len(keys))
	for i, k := range keys {
		where[i] = k + " = ?"
	}

	update := s.q("UPDATE " + s.t(table) + " SET " + column + " = ? WHERE " + strings.Join(where, " AND "))

	for _, r := range list {
		next, ok := rewrite(r.value)
		if !ok {
			continue
		}

		if _, err := tx.ExecContext(ctx, update, append([]any{next}, r.keys...)...); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) rewriteSettings(ctx context.Context, tx *sql.Tx, rewrite func(string) (string, bool), locked bool) error {
	lock := ""
	if s.postgres && locked {
		lock = " FOR UPDATE"
	}

	rows, err := tx.QueryContext(ctx, s.q("SELECT name, value FROM "+s.t("settings")+lock))
	if err != nil {
		return err
	}

	values := map[string]string{}

	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			_ = rows.Close()

			return err
		}

		// Only JSON that names an encrypted value is worth decoding.
		if strings.Contains(value, `_enc"`) {
			values[name] = value
		}
	}

	if err := rows.Close(); err != nil {
		return err
	}

	for name, value := range values {
		dec := json.NewDecoder(bytes.NewReader([]byte(value)))
		dec.UseNumber()

		var doc any
		if err := dec.Decode(&doc); err != nil {
			continue
		}

		if !rewriteEnc(doc, rewrite) {
			continue
		}

		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("settings")+" SET value = ?, updated_at = ? WHERE name = ?"), string(raw), now(), name); err != nil {
			return err
		}
	}

	return nil
}

// rewriteEnc rewrites the strings under keys ending in "_enc" anywhere in
// a decoded JSON value, and tells whether any changed.
func rewriteEnc(v any, rewrite func(string) (string, bool)) bool {
	changed := false

	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			if str, ok := item.(string); ok && strings.HasSuffix(k, "_enc") {
				if next, ok := rewrite(str); ok {
					x[k] = next
					changed = true
				}

				continue
			}

			if rewriteEnc(item, rewrite) {
				changed = true
			}
		}
	case []any:
		for _, item := range x {
			if rewriteEnc(item, rewrite) {
				changed = true
			}
		}
	}

	return changed
}

// DocSource is the version a user's or a shared snapshot was built from.
type DocSource struct {
	ConnectionID string
	UserID       string
	Version      string
}

// DocSources lists the distinct sources of the built snapshots.
func (s *Store) DocSources(ctx context.Context) ([]DocSource, error) {
	rows, err := s.query(ctx, "SELECT DISTINCT connection_id, user_id, source_version FROM "+s.t("schema_docs")+" WHERE source_version <> ''")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []DocSource{}

	for rows.Next() {
		var d DocSource
		if err := rows.Scan(&d.ConnectionID, &d.UserID, &d.Version); err != nil {
			return nil, err
		}

		list = append(list, d)
	}

	return list, rows.Err()
}

// RenameDocSource moves the snapshots of a connection built from one
// version to another, for when only the sealing of a secret changed.
func (s *Store) RenameDocSource(ctx context.Context, connectionID, from, to string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("schema_docs")+" SET source_version = ? WHERE connection_id = ? AND source_version = ?", to, connectionID, from)

	return err
}
