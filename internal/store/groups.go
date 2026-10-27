package store

import (
	"context"
	"database/sql"
	"errors"
)

type Group struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
	MemberIDs   []string `json:"member_ids"`
}

func (s *Store) ListGroups(ctx context.Context) ([]*Group, error) {
	rows, err := s.query(ctx, "SELECT id, name, description, created_at, updated_at FROM "+s.t("user_groups")+" ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	groups := []*Group{}
	byID := map[string]*Group{}

	for rows.Next() {
		g := &Group{MemberIDs: []string{}}
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}

		groups = append(groups, g)
		byID[g.ID] = g
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

		if g := byID[groupID]; g != nil {
			g.MemberIDs = append(g.MemberIDs, userID)
		}
	}

	return groups, members.Err()
}

func (s *Store) GetGroup(ctx context.Context, id string) (*Group, error) {
	g := &Group{}

	err := s.queryRow(ctx, "SELECT id, name, description, created_at, updated_at FROM "+s.t("user_groups")+" WHERE id = ?", id).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	return g, err
}

func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	ts := now()
	g.ID = NewID()
	g.CreatedAt, g.UpdatedAt = ts, ts

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("user_groups")+
			" (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)"),
			g.ID, g.Name, g.Description, ts, ts); err != nil {
			return err
		}

		return s.setGroupMembers(ctx, tx, g.ID, g.MemberIDs)
	}))
}

func (s *Store) UpdateGroup(ctx context.Context, g *Group) error {
	g.UpdatedAt = now()

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("user_groups")+" SET name = ?, description = ?, updated_at = ? WHERE id = ?"),
			g.Name, g.Description, g.UpdatedAt, g.ID)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		if g.MemberIDs != nil {
			return s.setGroupMembers(ctx, tx, g.ID, g.MemberIDs)
		}

		return nil
	}))
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("connection_access")+" WHERE subject_type = ? AND subject_id = ?"),
			SubjectGroup, id); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("notebook_access")+" WHERE subject_type = ? AND subject_id = ?"),
			SubjectGroup, id); err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("user_groups")+" WHERE id = ?"), id)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		return nil
	})
}

func (s *Store) setGroupMembers(ctx context.Context, tx *sql.Tx, groupID string, userIDs []string) error {
	if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("group_members")+" WHERE group_id = ?"), groupID); err != nil {
		return err
	}

	for _, uid := range uniq(userIDs) {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("group_members")+" (group_id, user_id) VALUES (?, ?)"),
			groupID, uid); err != nil {
			return err
		}
	}

	return nil
}
