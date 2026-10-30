package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/hardloop"

	"github.com/rakunlabs/dataport/internal/store"
)

// docScheduleSetting holds the server-wide documentation schedule, edited
// by admins. Until it is saved, the default applies.
const (
	docScheduleSetting = "docs_schedule"
	docScheduleDefault = "0 0 * * *"
)

type docSchedule struct {
	// Enabled turns scheduled refreshes on; the spec is kept while off.
	Enabled  bool   `json:"enabled"`
	CronSpec string `json:"cron_spec"`
}

// active is the spec to run, empty while scheduled refreshes are off.
func (v docSchedule) active() string {
	if !v.Enabled {
		return ""
	}
	return v.CronSpec
}

// docRun reports the progress of a refresh of every saved snapshot on this
// instance, scheduled or started by an admin.
type docRun struct {
	ID         string `json:"id,omitempty"`
	Running    bool   `json:"running"`
	Manual     bool   `json:"manual"`
	Username   string `json:"username,omitempty"`
	Status     string `json:"status,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Failed     int    `json:"failed"`
	// EstimatedEnd extrapolates the average time per snapshot so far.
	EstimatedEnd int64 `json:"estimated_end"`
}

func parseDocSchedule(spec string) (*hardloop.SpecSchedule, error) {
	if len(spec) > 256 {
		return nil, errors.New("cron expression is too long")
	}
	schedule, err := hardloop.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	next := schedule.Next(time.Now())
	if next.IsZero() || schedule.Next(next).Sub(next) < time.Minute {
		return nil, errors.New("cron must have a future run and an interval of at least one minute")
	}
	return schedule, nil
}

func (s *Server) loadDocSchedule(ctx context.Context) (docSchedule, error) {
	raw, err := s.store.GetSetting(ctx, docScheduleSetting)
	if errors.Is(err, store.ErrNotFound) {
		return docSchedule{Enabled: true, CronSpec: docScheduleDefault}, nil
	}
	if err != nil {
		return docSchedule{}, err
	}
	// Settings saved before the switch existed are on unless blank.
	v := docSchedule{Enabled: true}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return docSchedule{}, fmt.Errorf("decode documentation schedule; %w", err)
	}
	return v, nil
}

// docNextRun is the next scheduled refresh in Unix milliseconds, 0 when off.
func (s *Server) docNextRun(ctx context.Context) int64 {
	v, err := s.loadDocSchedule(ctx)
	if err != nil || v.active() == "" {
		return 0
	}
	schedule, err := hardloop.Parse(v.active())
	if err != nil {
		return 0
	}
	return schedule.Next(time.Now()).UnixMilli()
}

// dispatchDocs refreshes every saved snapshot not attempted since the latest
// scheduled time, so a missed run happens once on startup. Each replica may
// poll; the store lease decides who works. Jobs are sequential to bound
// pressure on target databases.
func (s *Server) dispatchDocs(ctx context.Context) error {
	return s.dispatchDocsAt(ctx, time.Now())
}

func (s *Server) dispatchDocsAt(ctx context.Context, at time.Time) error {
	if !s.docDispatch.TryLock() {
		return nil
	}
	defer s.docDispatch.Unlock()
	v, err := s.loadDocSchedule(ctx)
	if err != nil || v.active() == "" {
		return err
	}
	schedule, err := hardloop.Parse(v.active())
	if err != nil {
		return err
	}
	last := schedule.Prev(at.Add(time.Second))
	if last.IsZero() {
		return nil
	}
	s.discoverDocs(ctx, last.UnixMilli())
	err = s.refreshAllDocs(ctx, last.UnixMilli(), false)
	if ctx.Err() == nil {
		s.exportDocs(ctx, last.UnixMilli())
	}
	return err
}

// refreshAllDocs rebuilds every snapshot not attempted since before. Each
// snapshot keeps serving its previous document until its new one is saved.
// The caller holds docDispatch. by is the admin of a manual run.
func (s *Server) refreshAllDocs(ctx context.Context, before int64, manual bool, by ...*store.User) error {
	total, err := s.store.CountDueDocs(ctx, before)
	if err != nil {
		return err
	}
	// A scheduled run with nothing due leaves no trace; a manual one does,
	// so the admin sees it ran.
	if total == 0 && !manual {
		return nil
	}
	rec := &store.DocRun{Manual: manual, Total: total}
	if len(by) > 0 && by[0] != nil {
		rec.UserID, rec.Username = by[0].ID, by[0].Username
	}
	s.startDocRun(ctx, rec)
	status := store.DocRunDone
	defer func() { s.finishDocRun(rec, status) }()
	tried := map[store.DocKey]bool{}
	// Schemas a connection documents for everyone are read from its own
	// snapshot; users' copies of them are not rebuilt.
	shared := map[string]*store.DocConnection{}
	covered := func(k store.DocKey) bool {
		dc, ok := shared[k.ConnectionID]
		if !ok {
			dc, _ = s.docConnection(ctx, k.ConnectionID)
			shared[k.ConnectionID] = dc
		}
		return dc != nil && docSchemaIncluded(dc, k.Schema)
	}
	for {
		keys, err := s.store.DueDocs(ctx, before)
		if err != nil {
			status = store.DocRunStopped
			return err
		}
		claimed := false
		for _, k := range keys {
			if ctx.Err() != nil {
				status = store.DocRunStopped
				return ctx.Err()
			}
			if tried[k] {
				continue
			}
			tried[k] = true
			// Snapshots of users who may no longer build documentation are
			// kept as they are, not rebuilt with their access.
			if k.UserID != store.DocSystemUser {
				skip := covered(k)
				if u, err := s.store.GetUser(ctx, k.UserID); err == nil && !s.docsBuildAllowed(ctx, u) {
					skip = true
				}
				if skip {
					if err := s.store.SkipDoc(ctx, k); err != nil {
						status = store.DocRunStopped
						return err
					}
					claimed = true
					s.docRunSkip(ctx, rec)
					continue
				}
			}
			err := s.refreshDoc(ctx, k, before)
			if errors.Is(err, errDocBusy) {
				continue
			}
			claimed = true
			// A closed schema's snapshot was removed, not refreshed.
			if errors.Is(err, errDocsBlocked) {
				s.docRunSkip(ctx, rec)
				continue
			}
			s.docRunStep(ctx, rec, err != nil)
			if err != nil {
				status = store.DocRunFailed
				slog.Warn("scheduled documentation refresh did not complete", "connection", k.ConnectionID, "user", k.UserID)
			}
		}
		if !claimed {
			return nil
		}
	}
}

func (s *Server) startDocRun(ctx context.Context, rec *store.DocRun) {
	if err := s.store.CreateDocRun(ctx, rec); err != nil {
		slog.Warn("record documentation run", "error", err)
	}
	s.docRunMu.Lock()
	defer s.docRunMu.Unlock()
	s.docRun = docRun{ID: rec.ID, Running: true, Manual: rec.Manual, StartedAt: rec.StartedAt, Total: rec.Total, Username: rec.Username}
}

func (s *Server) docRunStep(ctx context.Context, rec *store.DocRun, failed bool) {
	s.docRunMu.Lock()
	s.docRun.Done++
	if failed {
		s.docRun.Failed++
	}
	// Snapshots created during the run may push it past the first count.
	s.docRun.Total = max(s.docRun.Total, s.docRun.Done)
	rec.Total, rec.Done, rec.Failed = s.docRun.Total, s.docRun.Done, s.docRun.Failed
	s.docRunMu.Unlock()
	if rec.ID != "" {
		if err := s.store.UpdateDocRun(ctx, rec); err != nil {
			slog.Warn("record documentation run", "error", err)
		}
	}
}

// docRunSkip takes a snapshot the run leaves alone out of its total, so the
// progress ends at its total.
func (s *Server) docRunSkip(_ context.Context, rec *store.DocRun) {
	s.docRunMu.Lock()
	s.docRun.Total = max(s.docRun.Done, s.docRun.Total-1)
	rec.Total = s.docRun.Total
	s.docRunMu.Unlock()
}

func (s *Server) finishDocRun(rec *store.DocRun, status string) {
	s.docRunMu.Lock()
	s.docRun.Running = false
	s.docRun.Status = status
	s.docRun.FinishedAt = time.Now().UnixMilli()
	if s.docRun.Done > 0 {
		s.docRunAverage = (s.docRun.FinishedAt - s.docRun.StartedAt) / int64(s.docRun.Done)
	}
	s.docRunMu.Unlock()
	if rec.ID == "" {
		return
	}
	// The run's context may be cancelled; the record must still close.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.FinishDocRun(ctx, rec, status); err != nil {
		slog.Warn("record documentation run", "error", err)
	}
	if err := s.store.PruneDocRuns(ctx, docRunsKept); err != nil {
		slog.Warn("prune documentation runs", "error", err)
	}
}

// docRunsKept is how many past runs the settings page can list.
const docRunsKept = 50

// docRunStatus is the run in progress or the last one: this instance's own
// run when it has one, else what the store recorded, which covers runs on
// other replicas and before a restart.
func (s *Server) docRunStatus(ctx context.Context) docRun {
	s.docRunMu.Lock()
	r := s.docRun
	average := s.docRunAverage
	s.docRunMu.Unlock()
	if !r.Running {
		if list, err := s.store.ListDocRuns(ctx, 1); err == nil && len(list) > 0 && list[0].StartedAt >= r.StartedAt {
			x := list[0]
			r = docRun{ID: x.ID, Running: x.Status == store.DocRunRunning, Manual: x.Manual, Username: x.Username, Status: x.Status,
				StartedAt: x.StartedAt, FinishedAt: x.FinishedAt, Total: x.Total, Done: x.Done, Failed: x.Failed}
		}
	}
	if !r.Running {
		return r
	}
	// Before the first snapshot finishes, the previous run's pace is the
	// best guess.
	switch {
	case r.Done > 0:
		elapsed := time.Now().UnixMilli() - r.StartedAt
		r.EstimatedEnd = r.StartedAt + elapsed*int64(r.Total)/int64(r.Done)
	case average > 0:
		r.EstimatedEnd = r.StartedAt + average*int64(r.Total)
	}
	return r
}

// runDocsNow refreshes every saved snapshot in the background, outside the
// schedule. It returns false when a refresh is already running here.
func (s *Server) runDocsNow(by *store.User) bool {
	if !s.docDispatch.TryLock() {
		return false
	}
	ctx := s.notebookContext
	s.notebookWorkers.Add(1)
	go func() {
		defer s.notebookWorkers.Done()
		defer s.docDispatch.Unlock()
		// Schemas of documented connections nobody opened yet are built too.
		now := time.Now().UnixMilli()
		s.discoverDocs(ctx, now)
		if err := s.refreshAllDocs(ctx, now, true, by); err != nil && ctx.Err() == nil {
			slog.Error("refresh all documentation", "error", err)
		}
		if ctx.Err() == nil {
			s.exportDocs(ctx, 0)
		}
	}()
	return true
}

// syncDocJobs runs the documentation cron job with the stored schedule,
// replacing it when the schedule changed (also on another replica). Nothing
// runs while scheduled refreshes are off.
func (s *Server) syncDocJobs(ctx context.Context) error {
	v, err := s.loadDocSchedule(ctx)
	if err != nil {
		return err
	}
	s.docJobsMu.Lock()
	defer s.docJobsMu.Unlock()
	spec := v.active()
	if s.docJobs != nil && s.docJobsSpec == spec {
		return nil
	}
	s.stopDocJobsLocked()
	if spec == "" {
		return nil
	}
	jobs, err := hardloop.NewCron(hardloop.Cron{
		Name: "documentation", Specs: []string{spec}, Func: s.dispatchDocs,
	})
	if err != nil {
		return err
	}
	if err := jobs.Start(ctx); err != nil {
		return err
	}
	s.docJobs, s.docJobsSpec = jobs, spec
	return nil
}

func (s *Server) stopDocJobsLocked() {
	if s.docJobs != nil {
		<-s.docJobs.Stop().Done()
		s.docJobs, s.docJobsSpec = nil, ""
	}
}

// runDocJobs keeps the cron job in step with the stored schedule until ctx
// ends, after catching up a run missed while the server was down.
func (s *Server) runDocJobs(ctx context.Context) {
	if err := s.dispatchDocs(ctx); err != nil && ctx.Err() == nil {
		slog.Error("catch up documentation schedule", "error", err)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.syncDocJobs(ctx); err != nil && ctx.Err() == nil {
			slog.Error("documentation schedule", "error", err)
		}
		select {
		case <-ctx.Done():
			s.docJobsMu.Lock()
			s.stopDocJobsLocked()
			s.docJobsMu.Unlock()
			return
		case <-ticker.C:
		case <-s.docScheduleChanged:
		}
	}
}

// docJobsChanged wakes runDocJobs after an admin saved the schedule.
func (s *Server) docJobsChanged() {
	select {
	case s.docScheduleChanged <- struct{}{}:
	default:
	}
}

func (s *Server) adminGetDocSchedule(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadDocSchedule(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	history, err := s.store.ListDocRuns(r.Context(), 20)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	snapshots, err := s.store.DocOverview(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": v.Enabled, "cron_spec": v.CronSpec, "next_run": s.docNextRun(r.Context()), "run": s.docRunStatus(r.Context()),
		"history": history, "snapshots": snapshots,
	})
}

func (s *Server) adminSetDocSchedule(w http.ResponseWriter, r *http.Request) {
	var in docSchedule
	if !readJSON(w, r, &in) {
		return
	}
	in.CronSpec = strings.TrimSpace(in.CronSpec)
	if in.Enabled && in.CronSpec == "" {
		writeError(w, http.StatusBadRequest, "a cron expression is required while scheduled refreshes are on")
		return
	}
	if in.CronSpec != "" {
		if _, err := parseDocSchedule(in.CronSpec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.SetSetting(r.Context(), docScheduleSetting, string(raw)); err != nil {
		writeStoreError(w, err)
		return
	}
	s.docJobsChanged()
	s.adminGetDocSchedule(w, r)
}

// adminRunDocs starts refreshing every saved snapshot now.
func (s *Server) adminRunDocs(w http.ResponseWriter, r *http.Request) {
	if !s.runDocsNow(currentUser(r)) {
		writeError(w, http.StatusConflict, "documentation is already being refreshed")
		return
	}
	s.adminGetDocSchedule(w, r)
}
