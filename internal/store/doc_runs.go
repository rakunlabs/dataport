package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DocRun is one refresh of every documentation snapshot.
type DocRun struct {
	ID         string `json:"id"`
	Manual     bool   `json:"manual"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Failed     int    `json:"failed"`
}

const (
	DocRunRunning = "running"
	DocRunDone    = "done"
	DocRunFailed  = "failed"
	DocRunStopped = "stopped"
)

// docRunStale is how long a running row may go without a heartbeat before
// it counts as cut off, such as by a restart.
const docRunStale = 2 * time.Minute

const docRunColumns = "id, manual, user_id, username, status, started_at, finished_at, total, done, failed, heartbeat_at"

func (s *Store) CreateDocRun(ctx context.Context, r *DocRun) error {
	r.ID = NewID()
	r.Status = DocRunRunning
	r.StartedAt = now()

	_, err := s.exec(ctx, "INSERT INTO "+s.t("doc_runs")+" ("+docRunColumns+") VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0, 0, ?)",
		r.ID, b2i(r.Manual), r.UserID, r.Username, r.Status, r.StartedAt, r.Total, r.StartedAt)

	return err
}

// UpdateDocRun saves the progress and heartbeat of a running refresh.
func (s *Store) UpdateDocRun(ctx context.Context, r *DocRun) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("doc_runs")+" SET total = ?, done = ?, failed = ?, heartbeat_at = ? WHERE id = ?",
		r.Total, r.Done, r.Failed, now(), r.ID)

	return err
}

func (s *Store) FinishDocRun(ctx context.Context, r *DocRun, status string) error {
	r.Status, r.FinishedAt = status, now()

	_, err := s.exec(ctx, "UPDATE "+s.t("doc_runs")+" SET status = ?, finished_at = ?, total = ?, done = ?, failed = ?, heartbeat_at = ? WHERE id = ?",
		r.Status, r.FinishedAt, r.Total, r.Done, r.Failed, r.FinishedAt, r.ID)

	return err
}

// ListDocRuns returns the latest runs, newest first. A running row whose
// heartbeat stopped reads as stopped.
func (s *Store) ListDocRuns(ctx context.Context, limit int) ([]DocRun, error) {
	rows, err := s.query(ctx, "SELECT "+docRunColumns+" FROM "+s.t("doc_runs")+" ORDER BY started_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	stale := now() - docRunStale.Milliseconds()
	list := []DocRun{}

	for rows.Next() {
		var (
			r         DocRun
			manual    int
			heartbeat int64
		)

		if err := rows.Scan(&r.ID, &manual, &r.UserID, &r.Username, &r.Status, &r.StartedAt, &r.FinishedAt, &r.Total, &r.Done, &r.Failed, &heartbeat); err != nil {
			return nil, err
		}

		r.Manual = manual != 0

		if r.Status == DocRunRunning && heartbeat < stale {
			r.Status, r.FinishedAt = DocRunStopped, heartbeat
		}

		list = append(list, r)
	}

	return list, rows.Err()
}

// RunningDocRun returns the run in progress on any replica, or ErrNotFound.
func (s *Store) RunningDocRun(ctx context.Context) (*DocRun, error) {
	list, err := s.ListDocRuns(ctx, 5)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if list[i].Status == DocRunRunning {
			return &list[i], nil
		}
	}

	return nil, ErrNotFound
}

// PruneDocRuns keeps the latest keep runs.
func (s *Store) PruneDocRuns(ctx context.Context, keep int) error {
	var cutoff int64

	err := s.queryRow(ctx, "SELECT started_at FROM "+s.t("doc_runs")+" ORDER BY started_at DESC LIMIT 1 OFFSET ?", keep-1).Scan(&cutoff)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return err
	}

	_, err = s.exec(ctx, "DELETE FROM "+s.t("doc_runs")+" WHERE started_at < ?", cutoff)

	return err
}
