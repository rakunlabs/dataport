package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

const docLease = 3 * time.Minute

const docJobTimeout = 100 * time.Second

var (
	errDocBusy    = errors.New("documentation is being refreshed; try again shortly")
	errDocFailed  = errors.New("documentation refresh failed; the last successful snapshot was kept")
	errDocRetry   = errors.New("documentation refresh failed; try again")
	errDocChanged = errors.New("documentation changed while refreshing; try again")
	// errDocsBuildOff refuses a refresh to users who may only read.
	errDocsBuildOff = errors.New("building documentation is not open to your account; ask an administrator")
)

type docStatus struct {
	NextRun     int64  `json:"next_run"`
	AttemptedAt int64  `json:"attempted_at"`
	LastError   string `json:"last_error"`
	Refreshing  bool   `json:"refreshing"`
	// ReadOnly is set for users who may read documentation but not build
	// or refresh it.
	ReadOnly bool `json:"read_only,omitempty"`
}

type docResponse struct {
	Document *engine.SchemaDoc `json:"document"`
	Status   docStatus         `json:"status"`
}

func (s *Server) snapshotStatus(ctx context.Context, d *store.DocSnapshot) docStatus {
	return docStatus{NextRun: s.docNextRun(ctx), AttemptedAt: d.AttemptedAt,
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

// errDocsReadOnly is returned to users who may only read documentation when
// nothing was built with their database account yet.
var errDocsReadOnly = errors.New("no documentation has been built for this schema yet, and building it is not open to your account; ask an administrator")

// documentation serves the saved snapshot without querying the target DB.
// Only a missing/invalidated document or an explicit refresh reads catalogs.
// Users who may not build documentation read the newest snapshot built with
// the same connection and database account, and never start a scan.
func (s *Server) documentation(ctx context.Context, k store.DocKey, force bool) (*docResponse, error) {
	u, err := s.store.GetUser(ctx, k.UserID)
	if err != nil {
		return nil, err
	}

	if !s.docsBuildAllowed(ctx, u) {
		return s.sharedDocumentation(ctx, k, force)
	}

	return s.ownDocumentation(ctx, k, force)
}

func (s *Server) sharedDocumentation(ctx context.Context, k store.DocKey, force bool) (*docResponse, error) {
	if force {
		return nil, errDocsBuildOff
	}

	_, _, version, err := s.docAccess(ctx, k)
	if err != nil {
		return nil, err
	}

	d, err := s.store.DocBySource(ctx, k.ConnectionID, k.Schema, version)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errDocsReadOnly
	}

	if err != nil {
		return nil, err
	}

	var doc engine.SchemaDoc
	if err := json.Unmarshal([]byte(d.Payload), &doc); err != nil {
		return nil, err
	}

	st := s.snapshotStatus(ctx, d)
	st.ReadOnly = true

	return &docResponse{Document: &doc, Status: st}, nil
}

func (s *Server) ownDocumentation(ctx context.Context, k store.DocKey, force bool) (*docResponse, error) {
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
		refreshErr := s.refreshDoc(ctx, k, 0)
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
		return nil, errDocChanged
	}
	var doc engine.SchemaDoc
	if err := json.Unmarshal([]byte(d.Payload), &doc); err != nil {
		return nil, err
	}
	return &docResponse{Document: &doc, Status: s.snapshotStatus(ctx, d)}, nil
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
				return errDocRetry
			}
		}
	}
}

// refreshDoc uses a persisted lease, not a process-local mutex. Completion
// is fenced by its token, so an expired writer cannot overwrite newer work.
// A non-zero before skips a snapshot already attempted since then.
func (s *Server) refreshDoc(ctx context.Context, k store.DocKey, before int64) error {
	token := store.NewID()
	ok, err := s.store.ClaimDoc(ctx, k, token, time.Now().Add(docLease).UnixMilli(), before)
	if err != nil {
		return err
	}
	if !ok {
		return errDocBusy
	}
	d, err := s.store.GetDoc(ctx, k)
	if err != nil {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer finishCancel()
		// Release the lease so the next caller need not wait for it to expire.
		_, _ = s.store.FinishDoc(finishCtx, &store.DocSnapshot{DocKey: k, LastError: "refresh_failed"}, token, false)
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
	// Even on cancellation, release the lease and retain the last good copy.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	ok, err = s.store.FinishDoc(finishCtx, d, token, buildErr == nil)
	if err != nil {
		return err
	}
	if !ok {
		return errDocBusy
	}
	if buildErr != nil {
		return errDocFailed
	}
	return nil
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
		writeDocError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// writeDocError keeps raw store failures out of the response; engine and
// grant errors keep their usual answers.
func writeDocError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		credErr *credentialsError
		connErr *engine.ConnError
	)
	switch {
	case errors.Is(err, errDocBusy), errors.Is(err, errDocChanged):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errDocsBuildOff):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errDocsReadOnly):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errDocFailed), errors.Is(err, errDocRetry):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "documentation refresh timed out; try again")
	case errors.As(err, &credErr), errors.Is(err, errSecret):
		writeGrantError(w, err, "connection not found")
	case errors.As(err, &connErr), errors.Is(err, engine.ErrReadOnly):
		writeEngineError(w, r, err)
	default:
		writeStoreError(w, err)
	}
}

// listDocs returns the user's saved documentation on the connections they
// may still open, with the schedule, for the documentation page. Users who
// may only read see what was built with their database accounts.
func (s *Server) listDocs(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	grants, err := s.store.ListGrants(r.Context(), u)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	canBuild := s.docsBuildAllowed(r.Context(), u)
	out := []store.DocInfo{}
	if canBuild {
		list, err := s.store.UserDocs(r.Context(), u.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		allowed := make(map[string]bool, len(grants))
		for _, g := range grants {
			allowed[g.Connection.ID] = true
		}
		for _, d := range list {
			if allowed[d.ConnectionID] {
				out = append(out, d)
			}
		}
	} else {
		for _, g := range grants {
			// The version does not depend on the schema.
			_, _, version, err := s.docAccess(r.Context(), store.DocKey{ConnectionID: g.Connection.ID, UserID: u.ID})
			if err != nil {
				continue
			}
			list, err := s.store.SharedDocs(r.Context(), g.Connection.ID, version)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			out = append(out, list...)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"docs": out, "next_run": s.docNextRun(r.Context()), "run": s.docRunStatus(r.Context()), "read_only": !canBuild})
}
