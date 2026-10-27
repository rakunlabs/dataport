package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/rakunlabs/dataport/internal/notebook"
)

type Notebook struct {
	ID               string             `json:"id"`
	OwnerID          string             `json:"owner_id"`
	Name             string             `json:"name"`
	Path             string             `json:"path"`
	Document         notebook.Document  `json:"document"`
	Published        *notebook.Document `json:"-"`
	Version          int                `json:"version"`
	PublishedVersion int                `json:"published_version"`
	CreatedAt        int64              `json:"created_at"`
	UpdatedAt        int64              `json:"updated_at"`
	Permission       string             `json:"permission"`
}

type NotebookAccess struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Permission  string `json:"permission"`
}

const notebookColumns = "id, owner_id, name, path, document, published, version, published_version, created_at, updated_at"

var ErrVersion = errors.New("notebook changed; reload before saving")

func scanNotebook(row interface{ Scan(...any) error }) (*Notebook, error) {
	var n Notebook
	var doc, pub string
	if err := row.Scan(&n.ID, &n.OwnerID, &n.Name, &n.Path, &doc, &pub, &n.Version, &n.PublishedVersion, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(doc), &n.Document); err != nil {
		return nil, err
	}
	n.Document.Normalize()
	if pub != "" {
		if err := json.Unmarshal([]byte(pub), &n.Published); err != nil {
			return nil, err
		}
		if n.Published != nil {
			n.Published.Normalize()
		}
	}
	return &n, nil
}

func (s *Store) NotebookPermission(ctx context.Context, u *User, n *Notebook) (string, error) {
	if u.IsAdmin() || n.OwnerID == u.ID {
		return "owner", nil
	}
	rows, err := s.query(ctx, "SELECT permission FROM "+s.t("notebook_access")+" WHERE notebook_id = ? AND ((subject_type = 'user' AND subject_id = ?) OR (subject_type = 'group' AND subject_id IN (SELECT group_id FROM "+s.t("group_members")+" WHERE user_id = ?)))", n.ID, u.ID, u.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	permission := ""
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return "", err
		}
		if p == "edit" || permission == "" || p == "run" && permission == "view" {
			permission = p
		}
	}
	return permission, rows.Err()
}

func (s *Store) GetNotebook(ctx context.Context, u *User, id string, byPath bool) (*Notebook, error) {
	field := "id"
	if byPath {
		field = "path"
	}
	n, err := scanNotebook(s.queryRow(ctx, "SELECT "+notebookColumns+" FROM "+s.t("notebooks")+" WHERE "+field+" = ?", id))
	if err != nil {
		return nil, err
	}
	n.Permission, err = s.NotebookPermission(ctx, u, n)
	if err != nil {
		return nil, err
	}
	if n.Permission == "" {
		return nil, ErrNotFound
	}
	return n, nil
}

func (s *Store) ListNotebooks(ctx context.Context, u *User) ([]*Notebook, error) {
	q := "SELECT " + notebookColumns + " FROM " + s.t("notebooks") + " n"
	var args []any
	if !u.IsAdmin() {
		q += " WHERE owner_id = ? OR EXISTS (SELECT 1 FROM " + s.t("notebook_access") + " a WHERE a.notebook_id = n.id AND ((a.subject_type = 'user' AND a.subject_id = ?) OR (a.subject_type = 'group' AND a.subject_id IN (SELECT group_id FROM " + s.t("group_members") + " WHERE user_id = ?))))"
		args = []any{u.ID, u.ID, u.ID}
	}
	rows, err := s.query(ctx, q+" ORDER BY updated_at DESC LIMIT 500", args...)
	if err != nil {
		return nil, err
	}
	list := []*Notebook{}
	for rows.Next() {
		n, err := scanNotebook(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, n := range list {
		n.Permission, err = s.NotebookPermission(ctx, u, n)
		if err != nil {
			return nil, err
		}
		// The listing carries metadata only, never SQL or published content.
		n.Document = notebook.Document{}
		n.Published = nil
	}
	return list, nil
}

func (s *Store) SaveNotebook(ctx context.Context, n *Notebook) error {
	n.Document.Normalize()
	doc, err := json.Marshal(n.Document)
	if err != nil {
		return err
	}
	n.UpdatedAt = now()
	if n.ID == "" {
		n.ID = NewID()
		n.Version = 1
		n.CreatedAt = n.UpdatedAt
		_, err := s.exec(ctx, "INSERT INTO "+s.t("notebooks")+" ("+notebookColumns+") VALUES (?, ?, ?, ?, ?, '', 1, 0, ?, ?)", n.ID, n.OwnerID, n.Name, n.Path, string(doc), n.CreatedAt, n.UpdatedAt)
		return wrapWriteErr(err)
	}
	res, err := s.exec(ctx, "UPDATE "+s.t("notebooks")+" SET name = ?, path = ?, document = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?", n.Name, n.Path, string(doc), n.UpdatedAt, n.ID, n.Version)
	if err != nil {
		return wrapWriteErr(err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrVersion
	}
	n.Version++
	return nil
}

func (s *Store) PublishNotebook(ctx context.Context, id string, version int, publish bool) error {
	q := "UPDATE " + s.t("notebooks") + " SET published = document, published_version = version WHERE id = ? AND version = ?"
	if !publish {
		q = "UPDATE " + s.t("notebooks") + " SET published = '', published_version = 0 WHERE id = ? AND version = ?"
	}
	res, err := s.exec(ctx, q, id, version)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrVersion
	}
	return nil
}

func (s *Store) DeleteNotebook(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("notebooks")+" WHERE id = ?", id)
	return err
}

func (s *Store) NotebookAccess(ctx context.Context, id string) ([]NotebookAccess, error) {
	rows, err := s.query(ctx, "SELECT subject_type, subject_id, permission FROM "+s.t("notebook_access")+" WHERE notebook_id = ? ORDER BY subject_type, subject_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotebookAccess{}
	for rows.Next() {
		var a NotebookAccess
		if err := rows.Scan(&a.SubjectType, &a.SubjectID, &a.Permission); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SetNotebookAccess(ctx context.Context, id string, access []NotebookAccess) error {
	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("notebook_access")+" WHERE notebook_id = ?"), id); err != nil {
			return err
		}
		for _, a := range access {
			if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("notebook_access")+" (notebook_id, subject_type, subject_id, permission) VALUES (?, ?, ?, ?)"), id, a.SubjectType, a.SubjectID, a.Permission); err != nil {
				return err
			}
		}
		return nil
	}))
}

// NotebookRun stores metadata only. Result rows and request parameters are not
// retained in the history; a background caller retrieves status, not raw data.
type NotebookRun struct {
	ID              string          `json:"id"`
	NotebookID      string          `json:"notebook_id"`
	UserID          string          `json:"user_id"`
	Version         int             `json:"version"`
	Status          string          `json:"status"`
	Source          string          `json:"source"`
	Info            json.RawMessage `json:"info"`
	CancelRequested bool            `json:"cancel_requested"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

func (s *Store) CreateNotebookRun(ctx context.Context, run *NotebookRun) error {
	run.ID = NewID()
	run.CreatedAt = now()
	run.UpdatedAt = run.CreatedAt
	run.Status = "running"
	_, err := s.exec(ctx, "INSERT INTO "+s.t("notebook_runs")+" (id, notebook_id, user_id, version, status, source, info, created_at, updated_at) VALUES (?, ?, ?, ?, 'running', ?, '{}', ?, ?)", run.ID, run.NotebookID, run.UserID, run.Version, run.Source, run.CreatedAt, run.UpdatedAt)
	return err
}

func scanNotebookRun(row interface{ Scan(...any) error }) (*NotebookRun, error) {
	var run NotebookRun
	var info string
	var cancel int
	if err := row.Scan(&run.ID, &run.NotebookID, &run.UserID, &run.Version, &run.Status, &run.Source, &info, &cancel, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	run.Info = json.RawMessage(info)
	run.CancelRequested = cancel != 0
	return &run, nil
}

const notebookRunColumns = "id, notebook_id, user_id, version, status, source, info, cancel_requested, created_at, updated_at"

func (s *Store) GetNotebookRun(ctx context.Context, id string) (*NotebookRun, error) {
	return scanNotebookRun(s.queryRow(ctx, "SELECT "+notebookRunColumns+" FROM "+s.t("notebook_runs")+" WHERE id = ?", id))
}

func (s *Store) ListNotebookRuns(ctx context.Context, notebookID, userID string, limit, offset int) ([]*NotebookRun, error) {
	q := "SELECT " + notebookRunColumns + " FROM " + s.t("notebook_runs") + " WHERE notebook_id = ?"
	args := []any{notebookID}
	if userID != "" {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	q += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotebookRun{}
	for rows.Next() {
		run, err := scanNotebookRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) UpdateNotebookRun(ctx context.Context, id, status string, info any) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, "UPDATE "+s.t("notebook_runs")+" SET status = ?, info = ?, updated_at = ? WHERE id = ? AND status = 'running'", status, string(b), now(), id)
	return err
}

// HeartbeatNotebookRun uses a fenced predicate: an expired worker must stop,
// and cannot overwrite a terminal state published by another instance.
func (s *Store) HeartbeatNotebookRun(ctx context.Context, id string) (bool, error) {
	res, err := s.exec(ctx, "UPDATE "+s.t("notebook_runs")+" SET updated_at = ? WHERE id = ? AND status = 'running' AND cancel_requested = 0", now(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) CancelNotebookRun(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("notebook_runs")+" SET cancel_requested = 1 WHERE id = ? AND status = 'running'", id)
	return err
}

func (s *Store) MaintainNotebookRuns(ctx context.Context, staleBefore, deleteBefore int64) error {
	if _, err := s.exec(ctx, "UPDATE "+s.t("notebook_runs")+" SET status = 'failed', info = ?, updated_at = ? WHERE status = 'running' AND updated_at < ?", `{"error":"worker heartbeat expired"}`, now(), staleBefore); err != nil {
		return err
	}
	_, err := s.exec(ctx, "DELETE FROM "+s.t("notebook_runs")+" WHERE status <> 'running' AND created_at < ?", deleteBefore)
	return err
}
