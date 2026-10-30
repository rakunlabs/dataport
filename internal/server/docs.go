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
	// System is set on documentation the server builds for every user of
	// the connection with its documentation account.
	System bool `json:"system,omitempty"`
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
	if k.UserID == store.DocSystemUser {
		return s.systemDocAccess(ctx, k.ConnectionID)
	}

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
// A schema the connection documents for everyone is read from that
// snapshot, without the user's own database account. Otherwise every user
// reads the newest snapshot built with the same connection and database
// account, whoever built it, so documentation an admin built is there for
// everyone on a shared-account connection. Only a builder's explicit
// refresh, or a schema nobody built yet, reads catalogs. Users who may not
// build documentation never start a scan.
func (s *Server) documentation(ctx context.Context, k store.DocKey, force bool) (*docResponse, error) {
	u, err := s.store.GetUser(ctx, k.UserID)
	if err != nil {
		return nil, err
	}

	if blocked, err := s.docBlocked(ctx, k.ConnectionID, k.Schema); err != nil || blocked {
		if err == nil {
			err = errDocsBlocked
		}
		return nil, err
	}

	canBuild := s.docsBuildAllowed(ctx, u)

	if out, ok, err := s.systemDocumentation(ctx, u, k, force, canBuild); ok {
		return out, err
	}

	if !canBuild {
		return s.sharedDocumentation(ctx, k, force, true)
	}

	if !force {
		out, err := s.sharedDocumentation(ctx, k, false, false)
		if !errors.Is(err, errDocsReadOnly) {
			return out, err
		}
	}

	return s.ownDocumentation(ctx, k, force)
}

func (s *Server) sharedDocumentation(ctx context.Context, k store.DocKey, force, readOnly bool) (*docResponse, error) {
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
	st.ReadOnly = readOnly

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
	// A schema closed to documentation is not built, whoever asks.
	if blocked, err := s.docBlocked(ctx, k.ConnectionID, k.Schema); err != nil || blocked {
		if err == nil {
			_ = s.store.DeleteDoc(ctx, k)
			err = errDocsBlocked
		}
		return err
	}
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
	schema := r.URL.Query().Get("schema")
	if len(schema) > 512 {
		writeError(w, http.StatusBadRequest, "schema name is too long")
		return store.DocKey{}, false
	}
	u := currentUser(r)
	id := r.PathValue("id")
	// A schema closed to documentation is refused before asking the user
	// for a database account.
	if schema != "" {
		if blocked, err := s.docBlocked(r.Context(), id, schema); err != nil || blocked {
			if err == nil {
				err = errDocsBlocked
			}
			writeDocError(w, r, err)
			return store.DocKey{}, false
		}
	}
	// Documentation the connection keeps for everyone needs no database
	// account of the user's own.
	if schema != "" {
		if dc, err := s.docConnection(r.Context(), id); err == nil && docSchemaIncluded(dc, schema) {
			if err := s.docReader(r.Context(), u, id); err != nil {
				writeGrantError(w, err, "connection not found")
				return store.DocKey{}, false
			}
			return store.DocKey{ConnectionID: id, UserID: u.ID, Schema: schema}, true
		}
	}
	g, password := s.grant(w, r)
	if g == nil {
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
	return store.DocKey{ConnectionID: g.Connection.ID, UserID: u.ID, Schema: schema}, true
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
	case errors.Is(err, errDocsBuildOff), errors.Is(err, errDocsBlocked):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errDocsReadOnly):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errDocAccountNeeded):
		writeError(w, http.StatusConflict, err.Error())
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

// listDocs returns the documentation the user can read on the connections
// they may still open, with the schedule, for the documentation page: what
// a connection keeps for everyone, what was built with the user's database
// account, and their own snapshots. One entry per schema.
type docPatterns struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

func (s *Server) listDocs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	grants, err := s.store.ListGrants(ctx, u)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	canBuild := s.docsBuildAllowed(ctx, u)
	own := map[string][]store.DocInfo{}
	if canBuild {
		list, err := s.store.UserDocs(ctx, u.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for _, d := range list {
			own[d.ConnectionID] = append(own[d.ConnectionID], d)
		}
	}
	out := []store.DocInfo{}
	// Connections documented for everyone list every chosen schema, so the
	// page needs no schema list of the user's own for them.
	documented := []string{}
	// Schemas closed to documentation, as the patterns that choose the
	// others, so the page leaves them out.
	blocked := map[string]docPatterns{}
	for _, g := range grants {
		if g.Connection.NotebookOnly {
			continue
		}
		seen := map[string]bool{}
		add := func(list []store.DocInfo, chosen func(string) bool) {
			for _, d := range list {
				if !seen[d.Schema] && chosen(d.Schema) {
					seen[d.Schema] = true
					d.UserID, d.Username = "", ""
					out = append(out, d)
				}
			}
		}
		var dc *store.DocConnection
		if v, err := s.store.GetDocConnection(ctx, g.Connection.ID); err == nil && v.BlockExcluded {
			blocked[g.Connection.ID] = docPatterns{Include: v.Include, Exclude: v.Exclude}
		}
		if v, err := s.docConnection(ctx, g.Connection.ID); err == nil {
			dc = v
			documented = append(documented, g.Connection.ID)
			list, err := s.store.ConnectionDocs(ctx, g.Connection.ID, store.DocSystemUser)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			// Readers see only what was built; builders also what is
			// waiting, since opening it builds it.
			for _, d := range list {
				if !canBuild && !d.Ready {
					seen[d.Schema] = true
				}
			}
			add(list, func(sc string) bool { return docSchemaIncluded(dc, sc) })
		}
		notSystem := func(sc string) bool { return dc == nil || !docSchemaIncluded(dc, sc) }
		// The version does not depend on the schema.
		if _, _, version, err := s.docAccess(ctx, store.DocKey{ConnectionID: g.Connection.ID, UserID: u.ID}); err == nil {
			list, err := s.store.SharedDocs(ctx, g.Connection.ID, version)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			add(list, notSystem)
		}
		if canBuild {
			add(own[g.Connection.ID], notSystem)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"docs": out, "documented": documented, "blocked": blocked, "next_run": s.docNextRun(ctx), "run": s.docRunStatus(ctx), "read_only": !canBuild})
}
