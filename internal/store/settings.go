package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting returns the stored JSON value of a setting, or ErrNotFound.
func (s *Store) GetSetting(ctx context.Context, name string) (string, error) {
	var value string

	err := s.queryRow(ctx, "SELECT value FROM "+s.t("settings")+" WHERE name = ?", name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}

	return value, err
}

// SetSetting stores a value in one statement, so two replicas saving the
// same setting for the first time do not race into a duplicate key.
func (s *Store) SetSetting(ctx context.Context, name, value string) error {
	_, err := s.exec(ctx, "INSERT INTO "+s.t("settings")+" (name, value, updated_at) VALUES (?, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at", name, value, now())

	return err
}

// DeleteSetting drops a stored setting; a missing one is no error.
func (s *Store) DeleteSetting(ctx context.Context, name string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("settings")+" WHERE name = ?", name)

	return err
}
