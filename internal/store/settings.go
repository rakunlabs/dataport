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

func (s *Store) SetSetting(ctx context.Context, name, value string) error {
	ts := now()

	res, err := s.exec(ctx, "UPDATE "+s.t("settings")+" SET value = ?, updated_at = ? WHERE name = ?", value, ts, name)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}

	_, err = s.exec(ctx, "INSERT INTO "+s.t("settings")+" (name, value, updated_at) VALUES (?, ?, ?)", name, value, ts)

	return err
}

// DeleteSetting drops a stored setting; a missing one is no error.
func (s *Store) DeleteSetting(ctx context.Context, name string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("settings")+" WHERE name = ?", name)

	return err
}
