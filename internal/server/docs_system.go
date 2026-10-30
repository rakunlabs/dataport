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
	"path"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Connection documentation is built by the server, not by a user: an admin
// turns it on for a connection, picks its schemas (all of them but some, or
// only some) and may give it a documentation account. Every user who may
// open the connection reads it, without a database account of their own
// and without building anything. Its snapshots are kept under
// store.DocSystemUser.

const (
	// docSchemaLimit bounds the schemas documented per connection.
	docSchemaLimit = 500
	docPatternMax  = 100
	// docDiscoverTimeout bounds listing a connection's schemas.
	docDiscoverTimeout = 30 * time.Second
)

var (
	errDocAccountNeeded = errors.New("this connection signs every user in with their own account; save a documentation account for it")
	errDocsOff          = errors.New("documentation is not turned on for this connection")
	// errDocsBlocked refuses a schema an admin closed to documentation.
	errDocsBlocked = errors.New("documentation is turned off for this schema by an administrator")
)

// docBlocked reports whether an admin closed the schema of a connection to
// documentation: nobody builds or reads it, in the UI, MCP or the push.
func (s *Server) docBlocked(ctx context.Context, connectionID, schema string) (bool, error) {
	dc, err := s.store.GetDocConnection(ctx, connectionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return dc.BlockExcluded && !docSchemaIncluded(dc, schema), nil
}

// dropBlockedDocs removes every user's snapshots of the schemas a
// connection closed to documentation.
func (s *Server) dropBlockedDocs(ctx context.Context, dc *store.DocConnection) error {
	if !dc.BlockExcluded {
		return nil
	}

	keys, err := s.store.ConnectionDocKeys(ctx, dc.ConnectionID)
	if err != nil {
		return err
	}

	for _, k := range keys {
		if !docSchemaIncluded(dc, k.Schema) {
			if err := s.store.DeleteDoc(ctx, k); err != nil {
				return err
			}
		}
	}

	return nil
}

// docSchemaIncluded applies the include patterns (empty: every schema) and
// then the exclude patterns. Patterns are globs such as "tmp_*", matched
// without regard to case.
func docSchemaIncluded(dc *store.DocConnection, schema string) bool {
	match := func(patterns []string) bool {
		name := strings.ToLower(schema)

		return slices.ContainsFunc(patterns, func(p string) bool {
			ok, _ := path.Match(strings.ToLower(p), name)

			return ok
		})
	}

	if len(dc.Include) > 0 && !match(dc.Include) {
		return false
	}

	return !match(dc.Exclude)
}

// docConnection returns the settings of a connection whose documentation
// is on, or ErrNotFound.
func (s *Server) docConnection(ctx context.Context, connectionID string) (*store.DocConnection, error) {
	dc, err := s.store.GetDocConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}

	if !dc.Enabled {
		return nil, store.ErrNotFound
	}

	return dc, nil
}

// docAccount is the connection as the documentation account sees it, with
// its password and a fingerprint of both. A connection of its own account
// gets a pool of its own.
func (s *Server) docAccount(c *store.Connection, dc *store.DocConnection) (*store.Grant, string, string, error) {
	if c.OwnerID != "" || c.NotebookOnly {
		return nil, "", "", store.ErrNotFound
	}

	conn := *c
	conn.Access = nil
	secret := c.PasswordEnc

	switch {
	case dc.Username != "":
		conn.Username, secret, conn.Owner = dc.Username, dc.PasswordEnc, store.DocSystemUser
	case c.UserCredentials:
		return nil, "", "", errDocAccountNeeded
	}

	// It is an account of the server, never a user's to fix.
	conn.UserCredentials = false

	password, err := s.cipher.Decrypt(secret)
	if err != nil {
		slog.Error("decrypt documentation account", "connection", c.ID, "error", err)

		return nil, "", "", errSecret
	}

	b, err := json.Marshal(struct {
		Connection *store.Connection
		Secret     string
	}{&conn, secret})
	if err != nil {
		return nil, "", "", err
	}

	h := sha256.Sum256(b)

	return &store.Grant{Connection: &conn, ReadOnly: true}, password, hex.EncodeToString(h[:]), nil
}

// systemDocAccess is docAccess for the snapshots of a connection's
// documentation.
func (s *Server) systemDocAccess(ctx context.Context, connectionID string) (*store.Grant, string, string, error) {
	dc, err := s.docConnection(ctx, connectionID)
	if err != nil {
		return nil, "", "", err
	}

	c, err := s.store.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, "", "", err
	}

	return s.docAccount(c, dc)
}

// docReader checks that the user may read the documentation of a
// connection: they may open it, outside notebooks. No database account of
// theirs is needed.
func (s *Server) docReader(ctx context.Context, u *store.User, connectionID string) error {
	if u.Disabled {
		return store.ErrNotFound
	}

	g, err := s.store.GetGrant(ctx, u, connectionID)
	if err != nil {
		return err
	}

	if g.Connection.NotebookOnly {
		return errNotebookOnly
	}

	return nil
}

// systemDocumentation serves a schema of a connection's documentation. ok
// is false when the connection's documentation does not cover the schema,
// and the caller falls back to the documentation of users.
func (s *Server) systemDocumentation(ctx context.Context, u *store.User, k store.DocKey, force, canBuild bool) (*docResponse, bool, error) {
	dc, err := s.store.GetDocConnection(ctx, k.ConnectionID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (!dc.Enabled || !docSchemaIncluded(dc, k.Schema))) {
		return nil, false, nil
	}

	if err != nil {
		return nil, true, err
	}

	if err := s.docReader(ctx, u, k.ConnectionID); err != nil {
		return nil, true, err
	}

	if force && !canBuild {
		return nil, true, errDocsBuildOff
	}

	sk := store.DocKey{ConnectionID: k.ConnectionID, UserID: store.DocSystemUser, Schema: k.Schema}

	if !canBuild {
		_, _, version, err := s.systemDocAccess(ctx, k.ConnectionID)
		if err != nil {
			return nil, true, errDocsReadOnly
		}

		d, err := s.store.GetDoc(ctx, sk)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (d.Payload == "" || d.SourceVersion != version)) {
			return nil, true, errDocsReadOnly
		}

		if err != nil {
			return nil, true, err
		}

		out, err := s.docOf(ctx, d)
		if err != nil {
			return nil, true, err
		}

		out.Status.ReadOnly = true

		return out, true, nil
	}

	// A builder builds what is missing, or refreshes it.
	out, err := s.ownDocumentation(ctx, sk, force)
	if err != nil {
		return nil, true, err
	}

	out.Status.System = true

	return out, true, nil
}

// docOf decodes a saved snapshot.
func (s *Server) docOf(ctx context.Context, d *store.DocSnapshot) (*docResponse, error) {
	out := &docResponse{Status: s.snapshotStatus(ctx, d)}
	if err := json.Unmarshal([]byte(d.Payload), &out.Document); err != nil {
		return nil, err
	}

	out.Status.System = d.UserID == store.DocSystemUser

	return out, nil
}

// discoverDocs lists the schemas of every connection whose documentation
// is on and was not listed since before, so a refresh of all documentation
// builds the schemas nobody opened yet and drops those no longer chosen.
func (s *Server) discoverDocs(ctx context.Context, before int64) {
	list, err := s.store.ListDocConnections(ctx)
	if err != nil {
		slog.Warn("list documented connections", "error", err)

		return
	}

	for _, dc := range list {
		if ctx.Err() != nil {
			return
		}

		if dc.Enabled && dc.DiscoveredAt < before {
			_, _ = s.discoverConnectionDocs(ctx, dc)
		}
	}
}

// discoverConnectionDocs creates a snapshot for each chosen schema of a
// connection and removes the others. Nothing is removed when the schemas
// cannot be listed.
func (s *Server) discoverConnectionDocs(ctx context.Context, dc *store.DocConnection) ([]string, error) {
	schemas, err := s.docSchemas(ctx, dc)

	at := time.Now().UnixMilli()
	if err != nil {
		code := "discovery_failed"
		if errors.Is(err, errDocAccountNeeded) {
			code = "account_needed"
		}

		_ = s.store.SetDocDiscovery(ctx, dc.ConnectionID, at, code)
		slog.Warn("list schemas for documentation", "connection", dc.ConnectionID)

		return nil, err
	}

	var chosen []string

	for _, sc := range schemas {
		if docSchemaIncluded(dc, sc) {
			chosen = append(chosen, sc)
		}
	}

	// Beyond the limit the rest is not documented; the settings say so.
	limited := len(chosen) > docSchemaLimit
	if limited {
		chosen = chosen[:docSchemaLimit]
	}

	for _, sc := range chosen {
		if err := s.store.EnsureDoc(ctx, store.DocKey{ConnectionID: dc.ConnectionID, UserID: store.DocSystemUser, Schema: sc}); err != nil {
			return nil, err
		}
	}

	if err := s.store.KeepDocs(ctx, dc.ConnectionID, store.DocSystemUser, chosen); err != nil {
		return nil, err
	}

	code := ""
	if limited {
		code = "limit_reached"
	}

	return chosen, s.store.SetDocDiscovery(ctx, dc.ConnectionID, at, code)
}

// docSchemas lists every schema the documentation account sees.
func (s *Server) docSchemas(ctx context.Context, dc *store.DocConnection) ([]string, error) {
	c, err := s.store.GetConnection(ctx, dc.ConnectionID)
	if err != nil {
		return nil, err
	}

	g, password, _, err := s.docAccount(c, dc)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, docDiscoverTimeout)
	defer cancel()

	return s.engine.Schemas(ctx, g.Connection, password)
}

// buildConnectionDocs lists a connection's schemas and builds every one of
// them, as a run the settings page follows. The caller holds docDispatch.
func (s *Server) buildConnectionDocs(ctx context.Context, dc *store.DocConnection, by *store.User) {
	schemas, err := s.discoverConnectionDocs(ctx, dc)
	s.buildSchemaDocs(ctx, dc.ConnectionID, schemas, err != nil, by)
}

func (s *Server) buildSchemaDocs(ctx context.Context, connectionID string, schemas []string, failed bool, by *store.User) {
	rec := &store.DocRun{Manual: true, Total: len(schemas)}
	if by != nil {
		rec.UserID, rec.Username = by.ID, by.Username
	}

	s.startDocRun(ctx, rec)

	status := store.DocRunDone
	if failed {
		status = store.DocRunFailed
	}

	defer func() { s.finishDocRun(rec, status) }()

	for _, sc := range schemas {
		if ctx.Err() != nil {
			status = store.DocRunStopped

			return
		}

		err := s.refreshDoc(ctx, store.DocKey{ConnectionID: connectionID, UserID: store.DocSystemUser, Schema: sc}, 0)
		if errors.Is(err, errDocBusy) {
			// Someone else is building it right now.
			s.docRunStep(ctx, rec, false)

			continue
		}

		s.docRunStep(ctx, rec, err != nil)

		if err != nil {
			status = store.DocRunFailed
		}
	}
}

// buildConnectionDocsInBackground starts buildConnectionDocs. It returns
// false while documentation is being refreshed here.
func (s *Server) buildConnectionDocsInBackground(dc *store.DocConnection, by *store.User) bool {
	if !s.docDispatch.TryLock() {
		return false
	}

	ctx := s.notebookContext
	s.notebookWorkers.Add(1)

	go func() {
		defer s.notebookWorkers.Done()
		defer s.docDispatch.Unlock()

		s.buildConnectionDocs(ctx, dc, by)
		s.exportAfterBuild(ctx)
	}()

	return true
}

// docsChanged builds a connection's documentation after its settings
// changed, after a refresh already running here, and pushes the result.
// The schemas are listed first, so a running refresh builds them too; they
// are listed again with the settings saved last, so an older save never
// wins over a newer one.
func (s *Server) docsChanged(connectionID string, by *store.User) {
	ctx := s.notebookContext
	s.notebookWorkers.Add(1)

	go func() {
		defer s.notebookWorkers.Done()

		if dc, err := s.docConnection(ctx, connectionID); err == nil {
			_, _ = s.discoverConnectionDocs(ctx, dc)
		}

		s.docDispatch.Lock()
		defer s.docDispatch.Unlock()

		if ctx.Err() != nil {
			return
		}

		// Turned off or closed schemas leave the search index too.
		if dc, err := s.docConnection(ctx, connectionID); err == nil {
			schemas, err := s.discoverConnectionDocs(ctx, dc)
			s.buildSchemaDocs(ctx, connectionID, schemas, err != nil, by)
		}

		s.exportAfterBuild(ctx)
	}()
}

// exportAfterBuild pushes what a build changed, as a refresh of all
// documentation does. With nothing changed the target is not contacted.
func (s *Server) exportAfterBuild(ctx context.Context) {
	if ctx.Err() == nil {
		s.exportDocs(ctx, 0)
	}
}

// ////////////////////////////////////////////
// handlers

type docConnectionView struct {
	ConnectionID    string   `json:"connection_id"`
	Name            string   `json:"name"`
	DBType          string   `json:"db_type"`
	UserCredentials bool     `json:"user_credentials"`
	NotebookOnly    bool     `json:"notebook_only"`
	Enabled         bool     `json:"enabled"`
	Include         []string `json:"include"`
	Exclude         []string `json:"exclude"`
	BlockExcluded   bool     `json:"block_excluded"`
	Username        string   `json:"username"`
	HasPassword     bool     `json:"has_password"`
	DiscoveredAt    int64    `json:"discovered_at"`
	LastError       string   `json:"last_error"`
	Schemas         int      `json:"schemas"`
	Ready           int      `json:"ready"`
	Failed          int      `json:"failed"`
}

type docConnectionIn struct {
	Enabled       bool     `json:"enabled"`
	Include       []string `json:"include"`
	Exclude       []string `json:"exclude"`
	BlockExcluded bool     `json:"block_excluded"`
	Username      string   `json:"username"`
	// Password nil keeps the saved one; empty removes it.
	Password *string `json:"password"`
}

func (s *Server) docConnectionView(ctx context.Context, c *store.Connection, dc *store.DocConnection) (docConnectionView, error) {
	v := docConnectionView{ConnectionID: c.ID, Name: c.Name, DBType: c.DBType, UserCredentials: c.UserCredentials, NotebookOnly: c.NotebookOnly,
		Include: []string{}, Exclude: []string{}}

	if dc == nil {
		return v, nil
	}

	v.Enabled, v.Include, v.Exclude, v.Username, v.BlockExcluded = dc.Enabled, dc.Include, dc.Exclude, dc.Username, dc.BlockExcluded
	v.HasPassword, v.DiscoveredAt, v.LastError = dc.PasswordEnc != "", dc.DiscoveredAt, dc.LastError

	docs, err := s.store.ConnectionDocs(ctx, c.ID, store.DocSystemUser)
	if err != nil {
		return v, err
	}

	for _, d := range docs {
		v.Schemas++

		if d.Ready {
			v.Ready++
		}

		if d.LastError != "" {
			v.Failed++
		}
	}

	return v, nil
}

// adminListDocConnections lists the connections an admin may document for
// everyone, with their settings.
func (s *Server) adminListDocConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.store.ListConnections(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	saved, err := s.store.ListDocConnections(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	byID := map[string]*store.DocConnection{}
	for _, dc := range saved {
		byID[dc.ConnectionID] = dc
	}

	out := []docConnectionView{}

	for _, c := range conns {
		// Personal connections hold their owner's own account.
		if c.OwnerID != "" {
			continue
		}

		v, err := s.docConnectionView(r.Context(), c, byID[c.ID])
		if err != nil {
			writeStoreError(w, err)

			return
		}

		out = append(out, v)
	}

	slices.SortFunc(out, func(a, b docConnectionView) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	writeJSON(w, http.StatusOK, out)
}

// adminConnectionForDocs returns a connection that may be documented for
// everyone, and its saved settings (nil when there are none).
func (s *Server) adminConnectionForDocs(w http.ResponseWriter, r *http.Request) (*store.Connection, *store.DocConnection, bool) {
	c, err := s.store.GetConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)

		return nil, nil, false
	}

	if c.OwnerID != "" {
		writeError(w, http.StatusBadRequest, "personal connections cannot be documented for everyone")

		return nil, nil, false
	}

	dc, err := s.store.GetDocConnection(r.Context(), c.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)

		return nil, nil, false
	}

	return c, dc, true
}

func checkDocPatterns(list []string) ([]string, string) {
	out := []string{}

	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}

		if len(p) > 256 {
			return nil, "a schema pattern is too long"
		}

		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Sprintf("invalid schema pattern %q", clip(p, 64))
		}

		out = append(out, p)
	}

	if len(out) > docPatternMax {
		return nil, fmt.Sprintf("at most %d schema patterns", docPatternMax)
	}

	return out, ""
}

func (s *Server) adminSetDocConnection(w http.ResponseWriter, r *http.Request) {
	var in docConnectionIn
	if !readJSON(w, r, &in) {
		return
	}

	c, current, ok := s.adminConnectionForDocs(w, r)
	if !ok {
		return
	}

	next := &store.DocConnection{ConnectionID: c.ID, Enabled: in.Enabled, BlockExcluded: in.BlockExcluded, Username: strings.TrimSpace(in.Username)}
	if current != nil {
		next.PasswordEnc = current.PasswordEnc
	}

	var msg string
	if next.Include, msg = checkDocPatterns(in.Include); msg == "" {
		next.Exclude, msg = checkDocPatterns(in.Exclude)
	}

	switch {
	case msg != "":
	case len(next.Username) > 256:
		msg = "the username is too long"
	case in.Enabled && c.NotebookOnly:
		msg = c.Name + " can only be used in notebooks; its documentation cannot be shown"
	case in.Enabled && c.UserCredentials && next.Username == "":
		msg = errDocAccountNeeded.Error()
	}

	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	if in.Password != nil {
		if len(*in.Password) > 4096 {
			writeError(w, http.StatusBadRequest, "the password is too long")

			return
		}

		enc, err := s.cipher.Encrypt(*in.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "the password cannot be saved")

			return
		}

		next.PasswordEnc = enc
	}

	// Without a username of its own the connection's account is used.
	if next.Username == "" {
		next.PasswordEnc = ""
	}

	if err := s.store.SaveDocConnection(r.Context(), next); err != nil {
		writeStoreError(w, err)

		return
	}

	// What is closed to documentation is not kept either.
	if err := s.dropBlockedDocs(r.Context(), next); err != nil {
		writeStoreError(w, err)

		return
	}

	// The documentation account's pool must not outlive its password.
	s.engine.ForgetUser(c.ID, store.DocSystemUser)

	// Turned on, the documentation is built at once; in any case the push
	// follows the change.
	s.docsChanged(c.ID, currentUser(r))

	saved, err := s.store.GetDocConnection(r.Context(), c.ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	v, err := s.docConnectionView(r.Context(), c, saved)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, v)
}

// adminDocConnectionSchemas lists the schemas the saved account sees and
// which of them the given patterns choose, before they are saved.
func (s *Server) adminDocConnectionSchemas(w http.ResponseWriter, r *http.Request) {
	c, dc, ok := s.adminConnectionForDocs(w, r)
	if !ok {
		return
	}

	if dc == nil {
		dc = &store.DocConnection{ConnectionID: c.ID}
	}

	preview := *dc

	var msg string
	if preview.Include, msg = checkDocPatterns(r.URL.Query()["include"]); msg == "" {
		preview.Exclude, msg = checkDocPatterns(r.URL.Query()["exclude"])
	}

	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	list, err := s.docSchemas(r.Context(), &preview)
	if err != nil {
		switch {
		case errors.Is(err, errDocAccountNeeded):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusBadRequest, "this connection cannot be documented for everyone")
		default:
			writeDocError(w, r, err)
		}

		return
	}

	type item struct {
		Name     string `json:"name"`
		Included bool   `json:"included"`
	}

	out := make([]item, 0, len(list))
	for _, sc := range list {
		out = append(out, item{Name: sc, Included: docSchemaIncluded(&preview, sc)})
	}

	writeJSON(w, http.StatusOK, map[string]any{"schemas": out, "limit": docSchemaLimit})
}

// adminRunDocConnection builds every chosen schema of a connection now.
func (s *Server) adminRunDocConnection(w http.ResponseWriter, r *http.Request) {
	c, dc, ok := s.adminConnectionForDocs(w, r)
	if !ok {
		return
	}

	if dc == nil || !dc.Enabled {
		writeError(w, http.StatusBadRequest, errDocsOff.Error())

		return
	}

	if !s.buildConnectionDocsInBackground(dc, currentUser(r)) {
		writeError(w, http.StatusConflict, "documentation is already being refreshed")

		return
	}

	v, err := s.docConnectionView(r.Context(), c, dc)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, v)
}
