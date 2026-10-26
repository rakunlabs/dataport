package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/hardloop"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

const docLease = 3 * time.Minute
const docJobTimeout = 100 * time.Second

var errDocBusy = errors.New("documentation is being refreshed; try again shortly")

type docStatus struct {
	CronSpec    string `json:"cron_spec"`
	NextRun     int64  `json:"next_run"`
	AttemptedAt int64  `json:"attempted_at"`
	LastError   string `json:"last_error"`
	Refreshing  bool   `json:"refreshing"`
}

type docResponse struct {
	Document *engine.SchemaDoc `json:"document"`
	Status   docStatus         `json:"status"`
}

func snapshotStatus(d *store.DocSnapshot) docStatus {
	return docStatus{CronSpec: d.CronSpec, NextRun: d.NextRun, AttemptedAt: d.AttemptedAt,
		LastError: d.LastError, Refreshing: d.LeaseUntil > time.Now().UnixMilli()}
}

// docAccess is used both by interactive reads and unattended jobs. Never
// keep an old user's groups, grant or credential in a scheduled closure.
func (s *Server) docAccess(ctx context.Context, k store.DocKey) (*store.Grant, string, string, error) {
	u, err := s.store.GetUser(ctx, k.UserID)
	if err != nil {
		return nil, "", "", err
	}
	if u.Disabled {
		return nil, "", "", store.ErrNotFound
	}
	g, password, err := s.resolveGrant(ctx, u, k.ConnectionID)
	if err != nil {
		return nil, "", "", err
	}
	// Include encrypted credentials, never plaintext or a password digest.
	// Changing target, shared/own-account mode or the user's DB identity
	// invalidates the old snapshot before it can be served again.
	var cred *store.Credential
	if g.Connection.UserCredentials {
		cred, err = s.store.GetCredential(ctx, k.ConnectionID, k.UserID)
		if err != nil {
			return nil, "", "", err
		}
	}
	connection := *g.Connection
	// Access is rechecked separately; SQL does not promise its row order.
	connection.Access = nil
	b, err := json.Marshal(struct {
		Connection *store.Connection
		Secret     string
		Credential *store.Credential
	}{&connection, g.Connection.PasswordEnc, cred})
	if err != nil {
		return nil, "", "", err
	}
	h := sha256.Sum256(b)
	return g, password, hex.EncodeToString(h[:]), nil
}

// documentation serves the saved snapshot without querying the target DB.
// Only a missing/invalidated document or an explicit refresh reads catalogs.
func (s *Server) documentation(ctx context.Context, k store.DocKey, force bool) (*docResponse, error) {
	_, _, version, err := s.docAccess(ctx, k)
	if err != nil {
		return nil, err
	}
	if err := s.store.EnsureDoc(ctx, k); err != nil {
		return nil, err
	}
	d, err := s.store.GetDoc(ctx, k)
	if err != nil {
		return nil, err
	}
	if force || d.Payload == "" || d.SourceVersion != version {
		refreshErr := s.refreshDoc(ctx, k, false)
		if errors.Is(refreshErr, errDocBusy) && (d.Payload == "" || d.SourceVersion != version) {
			// The first caller is generating it. Join its work, including
			// across replicas, instead of starting another catalog scan.
			refreshErr = s.waitForDoc(ctx, k, version)
		}
		d, err = s.store.GetDoc(ctx, k)
		if err != nil {
			return nil, err
		}
		if refreshErr != nil && !errors.Is(refreshErr, errDocBusy) && (d.Payload == "" || d.SourceVersion != version) {
			return nil, refreshErr
		}
	}
	// Recheck access after a potentially long scan. A revoked grant or
	// changed credential must not race a response containing old metadata.
	_, _, current, err := s.docAccess(ctx, k)
	if err != nil {
		return nil, err
	}
	if d.SourceVersion != current || d.Payload == "" {
		return nil, errors.New("documentation changed while refreshing; try again")
	}
	var doc engine.SchemaDoc
	if err := json.Unmarshal([]byte(d.Payload), &doc); err != nil {
		return nil, err
	}
	return &docResponse{Document: &doc, Status: snapshotStatus(d)}, nil
}

func (s *Server) waitForDoc(ctx context.Context, k store.DocKey, version string) error {
	ctx, cancel := context.WithTimeout(ctx, docJobTimeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			d, err := s.store.GetDoc(ctx, k)
			if err != nil {
				return err
			}
			if d.Payload != "" && d.SourceVersion == version {
				return nil
			}
			if d.LeaseUntil <= time.Now().UnixMilli() {
				return errors.New("documentation refresh failed; try again")
			}
		}
	}
}

// refreshDoc uses a persisted lease, not a process-local mutex. Completion
// is fenced by its token, so an expired writer cannot overwrite newer work.
func (s *Server) refreshDoc(ctx context.Context, k store.DocKey, dueOnly bool) error {
	token := store.NewID()
	ok, err := s.store.ClaimDoc(ctx, k, token, time.Now().Add(docLease).UnixMilli(), dueOnly)
	if err != nil {
		return err
	}
	if !ok {
		return errDocBusy
	}
	d, err := s.store.GetDoc(ctx, k)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, docJobTimeout)
	defer cancel()
	g, password, version, buildErr := s.docAccess(ctx, k)
	var doc *engine.SchemaDoc
	if buildErr == nil {
		doc, buildErr = s.engine.SchemaDoc(ctx, g.Connection, password, k.Schema)
	}
	if buildErr == nil {
		_, _, current, err := s.docAccess(ctx, k)
		if err != nil {
			buildErr = err
		} else if current != version {
			buildErr = errors.New("connection changed during documentation refresh")
		}
	}
	if buildErr == nil {
		var b []byte
		b, buildErr = json.Marshal(doc)
		d.Payload, d.SourceVersion, d.GeneratedAt = string(b), version, doc.GeneratedAt
	}
	d.LastError = ""
	if buildErr != nil {
		// Target errors may include a DSN, host or password. Store only a
		// safe code for regular users; don't persist/log the raw error.
		d.LastError = "refresh_failed"
		slog.Warn("documentation refresh failed", "connection", k.ConnectionID, "user", k.UserID, "schema", k.Schema)
	}
	next, _ := docNextRun(d.CronSpec, time.Now())
	// Even on cancellation, release the lease and retain the last good copy.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	ok, err = s.store.FinishDoc(finishCtx, d, token, buildErr == nil, next)
	if err != nil {
		return err
	}
	if !ok {
		return errDocBusy
	}
	if buildErr != nil {
		return errors.New("documentation refresh failed; the last successful snapshot was kept")
	}
	return nil
}

func docNextRun(spec string, at time.Time) (int64, error) {
	if spec == "" {
		return 0, nil
	}
	if len(spec) > 256 {
		return 0, errors.New("cron expression is too long")
	}
	schedule, err := hardloop.Parse(spec)
	if err != nil {
		return 0, fmt.Errorf("invalid cron expression: %w", err)
	}
	next := schedule.Next(at)
	if next.IsZero() || schedule.Next(next).Sub(next) < time.Minute {
		return 0, errors.New("cron must have a future run and an interval of at least one minute")
	}
	return next.UnixMilli(), nil
}

func (s *Server) docRequestKey(w http.ResponseWriter, r *http.Request) (store.DocKey, bool) {
	g, password := s.grant(w, r)
	if g == nil {
		return store.DocKey{}, false
	}
	schema := r.URL.Query().Get("schema")
	if len(schema) > 512 {
		writeError(w, http.StatusBadRequest, "schema name is too long")
		return store.DocKey{}, false
	}
	if schema == "" {
		var err error
		schema, err = s.mcpSchema(r.Context(), g, password, "")
		if err != nil {
			writeEngineError(w, r, err)
			return store.DocKey{}, false
		}
	}
	return store.DocKey{ConnectionID: g.Connection.ID, UserID: currentUser(r).ID, Schema: schema}, true
}

func (s *Server) serveDoc(w http.ResponseWriter, r *http.Request) {
	k, ok := s.docRequestKey(w, r)
	if !ok {
		return
	}
	out, err := s.documentation(r.Context(), k, r.Method == http.MethodPost)
	if err != nil {
		writeEngineError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) docSchedule(w http.ResponseWriter, r *http.Request) {
	k, ok := s.docRequestKey(w, r)
	if !ok {
		return
	}
	var in struct {
		CronSpec string `json:"cron_spec"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.CronSpec = strings.TrimSpace(in.CronSpec)
	next, err := docNextRun(in.CronSpec, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.EnsureDoc(r.Context(), k); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.SetDocSchedule(r.Context(), k, in.CronSpec, next); err != nil {
		writeStoreError(w, err)
		return
	}
	d, err := s.store.GetDoc(r.Context(), k)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshotStatus(d))
}

// dispatchDocs catches up missed runs once on startup, then hardloop polls
// due schedules each minute. Each replica may poll; the store lease decides
// who works. Jobs are sequential to bound pressure on target databases.
func (s *Server) dispatchDocs(ctx context.Context) error {
	if !s.docDispatch.TryLock() {
		return nil
	}
	defer s.docDispatch.Unlock()
	keys, err := s.store.DueDocs(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.refreshDoc(ctx, k, true); err != nil && !errors.Is(err, errDocBusy) {
			slog.Warn("scheduled documentation refresh did not complete", "connection", k.ConnectionID, "user", k.UserID)
		}
	}
	return nil
}

func (s *Server) startDocJobs(ctx context.Context) (*hardloop.CronJob, error) {
	jobs, err := hardloop.NewCron(hardloop.Cron{
		Name: "documentation", Specs: []string{"* * * * *"}, Func: s.dispatchDocs,
	})
	if err != nil {
		return nil, err
	}
	if err := jobs.Start(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}
