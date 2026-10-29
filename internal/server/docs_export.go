package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Documentation push copies saved schema documentation into a search
// service's push collection, one Markdown item per table and one overview
// per schema, so agents find it with the service's full-text and vector
// search. The protocol is krabby's (https://github.com/rytsh/krabby) push
// source API; any service that implements it can be the target:
//
//	GET    {url}/api/v1/sources/{collection}             -> {"source":{"type":"push","page_count":n}}, 404 when missing
//	POST   {url}/api/v1/sources                          {"name","type":"push","description"}
//	POST   {url}/api/v1/sources/{collection}/items       {"items":[{id,title,content_type,content,teams,updated_at}]}, at most 20
//	POST   {url}/api/v1/sources/{collection}/items/prune {"keep":[ids]} -> {"removed":n}
//	DELETE {url}/api/v1/sources/{collection}/items?id=   when nothing is kept
//
// Such services rarely check access themselves: everything sent is readable
// by whoever reaches them, so admins choose each connection and the user
// whose access documents it.

const (
	docExportSetting = "docs_export"
	// docExportStateSetting remembers what was sent, so unchanged tables
	// are not sent again and the target keeps their original update time.
	docExportStateSetting = "docs_export_state"

	docExportDefaultCollection = "dataport"
	docExportMaxTargets        = 200
	docExportMaxSchemas        = 50

	// Limits of one push request.
	pushBatchItems = 20
	pushBatchBytes = 16 << 20
	pushItemBytes  = 8<<20 - 1024
	pushTitleBytes = 1000
	// A push waits for the target to convert and embed the batch.
	pushTimeout = 5 * time.Minute
)

var collectionNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// docExportTarget is one connection documented with one user's access.
type docExportTarget struct {
	ConnectionID string `json:"connection_id"`
	UserID       string `json:"user_id"`
	// Schemas to export; empty exports the connection's default schema.
	Schemas []string `json:"schemas"`
}

type docExportConfig struct {
	Enabled    bool              `json:"enabled"`
	URL        string            `json:"url"`
	Collection string            `json:"collection"`
	TokenEnc   string            `json:"token_enc,omitempty"`
	Targets    []docExportTarget `json:"targets"`
}

// docExportStatus is the outcome of the last export, shown to admins.
type docExportStatus struct {
	Running    bool   `json:"running"`
	Manual     bool   `json:"manual"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Tables     int    `json:"tables"`
	Sent       int    `json:"sent"`
	Changed    int    `json:"changed"`
	Removed    int    `json:"removed"`
	Error      string `json:"error,omitempty"`
	// Failed lists the targets that could not be documented; their items
	// stay in the target as they were.
	Failed []string `json:"failed,omitempty"`
}

type docExportItemState struct {
	Hash      string `json:"hash"`
	UpdatedAt int64  `json:"updated_at"`
}

type docExportState struct {
	URL        string                        `json:"url"`
	Collection string                        `json:"collection"`
	Items      map[string]docExportItemState `json:"items"`
	Status     docExportStatus               `json:"status"`
}

// pushItem is an item of the push API.
type pushItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	ContentType string    `json:"content_type"`
	Content     string    `json:"content"`
	Teams       []string  `json:"teams,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Server) loadDocExport(ctx context.Context) (docExportConfig, error) {
	raw, err := s.store.GetSetting(ctx, docExportSetting)
	if errors.Is(err, store.ErrNotFound) {
		return docExportConfig{Collection: docExportDefaultCollection, Targets: []docExportTarget{}}, nil
	}

	if err != nil {
		return docExportConfig{}, err
	}

	var v docExportConfig
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return docExportConfig{}, fmt.Errorf("decode documentation export; %w", err)
	}

	if v.Targets == nil {
		v.Targets = []docExportTarget{}
	}

	return v, nil
}

func (s *Server) loadDocExportState(ctx context.Context) (docExportState, error) {
	v := docExportState{Items: map[string]docExportItemState{}}

	raw, err := s.store.GetSetting(ctx, docExportStateSetting)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil
	}

	if err != nil {
		return v, err
	}

	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return docExportState{Items: map[string]docExportItemState{}}, nil
	}

	if v.Items == nil {
		v.Items = map[string]docExportItemState{}
	}

	return v, nil
}

func (s *Server) saveDocExportState(v docExportState) {
	// The export's context may be cancelled; what was sent must be kept.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := json.Marshal(v)
	if err == nil {
		err = s.store.SetSetting(ctx, docExportStateSetting, string(raw))
	}

	if err != nil {
		slog.Warn("save documentation export state", "error", err)
	}
}

// docExportStatusNow is this instance's running export, else the last one
// recorded by any replica.
func (s *Server) docExportStatusNow(ctx context.Context) docExportStatus {
	s.docExportMu.Lock()
	running := s.docExportRun
	s.docExportMu.Unlock()

	if running.Running {
		return running
	}

	state, err := s.loadDocExportState(ctx)
	if err != nil {
		return running
	}

	return state.Status
}

// exportDocs pushes the documentation of every target after a
// refresh of all documentation. A non-zero since skips the export when one
// started after it, so a scheduled occurrence exports once, also with
// replicas. It does nothing while an export runs on this instance.
func (s *Server) exportDocs(ctx context.Context, since int64) {
	if !s.docExport.TryLock() {
		return
	}
	defer s.docExport.Unlock()

	if since > 0 {
		if state, err := s.loadDocExportState(ctx); err == nil && state.Status.StartedAt >= since {
			return
		}
	}

	s.exportDocsLocked(ctx, false)
}

// exportDocsLocked runs one export; the caller holds docExport. A manual
// export sends every table again, the others only what changed since the
// last one.
func (s *Server) exportDocsLocked(ctx context.Context, manual bool) {
	cfg, err := s.loadDocExport(ctx)
	if err != nil {
		slog.Error("load documentation export", "error", err)

		return
	}

	if !cfg.Enabled || cfg.URL == "" {
		return
	}

	status := docExportStatus{Running: true, Manual: manual, StartedAt: time.Now().UnixMilli()}
	s.setDocExportRun(status)

	state, err := s.loadDocExportState(ctx)
	if err == nil {
		state = s.runDocExport(ctx, cfg, state, &status, manual)
	} else {
		status.Error = "could not read the previous export"
	}

	status.Running = false
	status.FinishedAt = time.Now().UnixMilli()
	state.Status = status
	s.saveDocExportState(state)
	s.setDocExportRun(status)

	if status.Error != "" || len(status.Failed) > 0 {
		slog.Warn("documentation export did not complete", "error", status.Error, "failed", status.Failed)
	}
}

func (s *Server) setDocExportRun(v docExportStatus) {
	s.docExportMu.Lock()
	s.docExportRun = v
	s.docExportMu.Unlock()
}

// exportDocsInBackground starts an export outside the schedule. It returns
// false when one is already running here.
func (s *Server) exportDocsInBackground() bool {
	if !s.docExport.TryLock() {
		return false
	}

	// Mark it running before returning, so the page shows it at once.
	s.setDocExportRun(docExportStatus{Running: true, Manual: true, StartedAt: time.Now().UnixMilli()})

	ctx := s.notebookContext
	s.notebookWorkers.Add(1)

	go func() {
		defer s.notebookWorkers.Done()
		defer s.docExport.Unlock()

		s.exportDocsLocked(ctx, true)
	}()

	return true
}

// runDocExport sends what changed and prunes what is gone. The state it
// returns holds what the target is known to have, also when it stops early.
func (s *Server) runDocExport(ctx context.Context, cfg docExportConfig, state docExportState, status *docExportStatus, full bool) docExportState {
	token, err := s.cipher.Decrypt(cfg.TokenEnc)
	if err != nil {
		status.Error = "the push token cannot be read; save it again"

		return state
	}

	k := &pushClient{base: cfg.URL, collection: cfg.Collection, token: token, http: &http.Client{Timeout: pushTimeout}}

	// What was sent elsewhere says nothing about this collection.
	if state.URL != cfg.URL || state.Collection != cfg.Collection {
		state = docExportState{URL: cfg.URL, Collection: cfg.Collection, Items: map[string]docExportItemState{}}
	}

	count, created, err := k.ensureCollection(ctx)
	if err != nil {
		status.Error = err.Error()

		return state
	}

	// The target lost or gained items behind our back: send everything again.
	if created || count != len(state.Items) {
		full = true
	}

	items, failed := s.docExportItems(ctx, cfg.Targets)
	status.Failed = failed
	status.Tables = len(items)

	next := make(map[string]docExportItemState, len(items))
	pending := make([]pushItem, 0, len(items))
	now := time.Now()

	for _, it := range items {
		hash := docExportHash(it)
		prev, seen := state.Items[it.ID]

		if seen && prev.Hash == hash {
			// Unchanged tables keep the time they last changed.
			it.UpdatedAt = time.UnixMilli(prev.UpdatedAt)
			next[it.ID] = prev

			if !full {
				continue
			}
		} else {
			it.UpdatedAt = now
			next[it.ID] = docExportItemState{Hash: hash, UpdatedAt: now.UnixMilli()}
		}

		pending = append(pending, it)
	}

	// state.Items follows what the target holds, batch by batch.
	for _, batch := range pushBatches(pending) {
		res, err := k.push(ctx, batch)
		if err != nil {
			status.Error = err.Error()

			return state
		}

		status.Sent += len(batch)
		status.Changed += res.Changed

		for _, it := range batch {
			state.Items[it.ID] = next[it.ID]
		}
	}

	// The items of a target that failed stay; nothing is pruned.
	if len(failed) > 0 {
		return state
	}

	var stale []string

	for id := range state.Items {
		if _, ok := next[id]; !ok {
			stale = append(stale, id)
		}
	}

	if len(stale) > 0 || count > len(next) {
		removed, err := k.removeAllBut(ctx, next, stale)
		status.Removed = removed

		if err != nil {
			status.Error = err.Error()

			return state
		}
	}

	state.Items = next

	return state
}

func docExportHash(it pushItem) string {
	h := sha256.New()
	_, _ = io.WriteString(h, it.Title+"\x00"+strings.Join(it.Teams, "\x1f")+"\x00"+it.Content)

	return hex.EncodeToString(h.Sum(nil))
}

// docExportItems documents every target from its saved snapshot, building
// a snapshot first where there is none. It returns the targets that failed,
// in words an admin can act on.
func (s *Server) docExportItems(ctx context.Context, targets []docExportTarget) ([]pushItem, []string) {
	var (
		items  []pushItem
		failed []string
	)

	for _, t := range targets {
		if ctx.Err() != nil {
			failed = append(failed, "export stopped")

			break
		}

		conn, err := s.store.GetConnection(ctx, t.ConnectionID)
		if err != nil {
			failed = append(failed, "connection "+t.ConnectionID+": not found")

			continue
		}

		user, err := s.store.GetUser(ctx, t.UserID)
		if err != nil {
			failed = append(failed, conn.Name+": user not found")

			continue
		}

		schemas := t.Schemas
		if len(schemas) == 0 {
			g, password, err := s.resolveGrant(ctx, user, conn.ID)
			if err == nil {
				var schema string

				schema, err = s.mcpSchema(ctx, g, password, "")
				schemas = []string{schema}
			}

			if err != nil {
				failed = append(failed, conn.Name+" as "+user.Username+": "+docExportError(err))

				continue
			}
		}

		for _, schema := range schemas {
			saved, err := s.documentation(ctx, store.DocKey{ConnectionID: conn.ID, UserID: user.ID, Schema: schema}, false)
			if err != nil {
				failed = append(failed, conn.Name+"/"+schema+" as "+user.Username+": "+docExportError(err))

				continue
			}

			items = append(items, docExportSchemaItems(conn, saved.Document)...)
		}
	}

	return items, failed
}

// docExportError keeps target and store details out of the status: they
// may name hosts or carry a DSN.
func docExportError(err error) string {
	var credErr *credentialsError

	switch {
	case errors.Is(err, store.ErrNotFound):
		return "no access to the connection"
	case errors.As(err, &credErr):
		return "the user has not saved their database account"
	case errors.Is(err, errNotebookOnly):
		return err.Error()
	case errors.Is(err, errDocBusy), errors.Is(err, errDocChanged), errors.Is(err, errDocFailed), errors.Is(err, errDocRetry):
		return err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "documentation refresh timed out"
	default:
		return "documentation is unavailable"
	}
}

// docExportSchemaItems turns one schema document into push items: an
// overview of the schema and one item per table or view.
func docExportSchemaItems(conn *store.Connection, doc *engine.SchemaDoc) []pushItem {
	prefix := "dataport/" + conn.ID + "/" + doc.Schema + "/"
	teams := []string{conn.Name}
	items := make([]pushItem, 0, len(doc.Tables)+1)

	var b strings.Builder

	fmt.Fprintf(&b, "# Schema %s of %s\n\n", doc.Schema, conn.Name)
	b.WriteString(docExportContext(conn, doc.Schema))

	if conn.Description != "" {
		b.WriteString("\n" + conn.Description + "\n")
	}

	fmt.Fprintf(&b, "\ntables: %d\n", doc.TableCount)

	if doc.Truncated {
		fmt.Fprintf(&b, "truncated: the first %d tables are documented\n", len(doc.Tables))
	}

	b.WriteString("\n" + schemaReportText(doc))

	if len(doc.Routines) > 0 {
		b.WriteString("\n## routines\n")

		for _, r := range doc.Routines {
			b.WriteString(r.Kind + " " + r.Name + r.Signature + "\n")
		}
	}

	items = append(items, pushItem{
		ID:          prefix + "_schema",
		Title:       clip(conn.Name+" / "+doc.Schema+" schema", pushTitleBytes),
		ContentType: "text/markdown",
		Content:     clip(b.String(), pushItemBytes),
		Teams:       teams,
	})

	for _, t := range doc.Tables {
		out := tableDocs(doc, []string{t.Name}, true)

		if len(t.Triggers) > 0 && t.Kind == "table" {
			out.Triggers = map[string][]engine.TriggerInfo{t.Name: t.Triggers}
		}

		b.Reset()
		b.WriteString("# " + t.Name)

		if t.Kind != "table" {
			b.WriteString(" [" + t.Kind + "]")
		}

		b.WriteString("\n\n" + docExportContext(conn, doc.Schema))

		// Estimates are rounded, so their drift does not resend the table.
		if t.Rows != nil {
			b.WriteString("estimated rows: " + roughCount(*t.Rows) + "\n")
		}

		writeTableDoc(&b, conn.DBType, t, &out)
		writeRelatedTables(&b, out.Related)

		items = append(items, pushItem{
			ID:          prefix + t.Name,
			Title:       clip(conn.Name+" / "+doc.Schema+"."+t.Name, pushTitleBytes),
			ContentType: "text/markdown",
			Content:     clip(b.String(), pushItemBytes),
			Teams:       teams,
		})
	}

	return items
}

func docExportContext(conn *store.Connection, schema string) string {
	return fmt.Sprintf("connection: %s (%s)\nschema: %s\n", conn.Name, conn.DBType, schema)
}

// roughCount keeps two significant digits: 1234567 is "~1.2M".
func roughCount(n int64) string {
	if n < 1000 {
		if n < 100 {
			return "~" + strconv.FormatInt(n, 10)
		}

		return "~" + strconv.FormatInt(n/10*10, 10)
	}

	f := float64(n)

	for _, u := range []struct {
		size float64
		name string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "k"}} {
		if f >= u.size {
			v := f / u.size
			if v >= 10 {
				return fmt.Sprintf("~%.0f%s", v, u.name)
			}

			return fmt.Sprintf("~%.1f%s", v, u.name)
		}
	}

	return strconv.FormatInt(n, 10)
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}

	return s
}

func pushBatches(items []pushItem) [][]pushItem {
	var (
		out   [][]pushItem
		batch []pushItem
		size  int
	)

	for _, it := range items {
		if len(batch) == pushBatchItems || (len(batch) > 0 && size+len(it.Content) > pushBatchBytes) {
			out = append(out, batch)
			batch, size = nil, 0
		}

		batch = append(batch, it)
		size += len(it.Content)
	}

	if len(batch) > 0 {
		out = append(out, batch)
	}

	return out
}

// ////////////////////////////////////////////
// push client

type pushClient struct {
	base       string
	collection string
	token      string
	http       *http.Client
}

type pushResult struct {
	Imported  int `json:"imported"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

// do sends one request. Errors name the step, never the token.
func (k *pushClient) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, k.base+path, reader)
	if err != nil {
		return 0, fmt.Errorf("push request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if k.token != "" {
		req.Header.Set("Authorization", "Bearer "+k.token)
	}

	res, err := k.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}

		return 0, fmt.Errorf("the push target is unreachable: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))

	if res.StatusCode >= 300 {
		var msg struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}

		_ = json.Unmarshal(raw, &msg)

		text := cmpOr(msg.Error, msg.Message, http.StatusText(res.StatusCode))

		return res.StatusCode, fmt.Errorf("the push target answered %d: %s", res.StatusCode, clip(text, 300))
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, errors.New("the push target sent an unexpected answer; does it implement the push API?")
		}
	}

	return res.StatusCode, nil
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// ensureCollection creates the push collection when it is missing and
// returns how many items it holds.
func (k *pushClient) ensureCollection(ctx context.Context) (int, bool, error) {
	var got struct {
		Source *struct {
			Type      string `json:"type"`
			PageCount int    `json:"page_count"`
		} `json:"source"`
	}

	path := "/api/v1/sources/" + url.PathEscape(k.collection) + "?per_page=1"

	code, err := k.do(ctx, http.MethodGet, path, nil, &got)
	if code == http.StatusNotFound {
		_, err = k.do(ctx, http.MethodPost, "/api/v1/sources", map[string]any{
			"name": k.collection, "type": "push",
			"description": "Database schema documentation from dataport: one item per table or view, with columns, keys, joins, indexes, triggers and design notes, and an overview per schema.",
		}, nil)
		if err != nil {
			return 0, false, err
		}

		return 0, true, nil
	}

	if err != nil {
		return 0, false, err
	}

	if got.Source == nil {
		return 0, false, errors.New("the push target sent an unexpected answer; does it implement the push API?")
	}

	if got.Source.Type != "push" {
		return 0, false, fmt.Errorf("collection %s is of type %q; choose another name for a push collection", k.collection, got.Source.Type)
	}

	return got.Source.PageCount, false, nil
}

func (k *pushClient) push(ctx context.Context, items []pushItem) (pushResult, error) {
	var res pushResult

	_, err := k.do(ctx, http.MethodPost, "/api/v1/sources/"+url.PathEscape(k.collection)+"/items", map[string]any{"items": items}, &res)

	return res, err
}

// removeAllBut deletes the items not in keep. The API prunes against a list
// to keep; with nothing to keep, the stale items are deleted one by one.
func (k *pushClient) removeAllBut(ctx context.Context, keep map[string]docExportItemState, stale []string) (int, error) {
	base := "/api/v1/sources/" + url.PathEscape(k.collection) + "/items"

	if len(keep) > 0 {
		ids := make([]string, 0, len(keep))
		for id := range keep {
			ids = append(ids, id)
		}

		slices.Sort(ids)

		var res struct {
			Removed int `json:"removed"`
		}

		_, err := k.do(ctx, http.MethodPost, base+"/prune", map[string]any{"keep": ids}, &res)

		return res.Removed, err
	}

	removed := 0

	for _, id := range stale {
		code, err := k.do(ctx, http.MethodDelete, base+"?id="+url.QueryEscape(id), nil, nil)
		if err != nil && code != http.StatusBadRequest {
			return removed, err
		}

		if err == nil {
			removed++
		}
	}

	return removed, nil
}

// ////////////////////////////////////////////
// handlers

type docExportView struct {
	Enabled    bool              `json:"enabled"`
	URL        string            `json:"url"`
	Collection string            `json:"collection"`
	HasToken   bool              `json:"has_token"`
	Targets    []docExportTarget `json:"targets"`
	Status     docExportStatus   `json:"status"`
}

type docExportIn struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	Collection string `json:"collection"`
	// Token nil keeps the stored token; empty removes it.
	Token   *string           `json:"token"`
	Targets []docExportTarget `json:"targets"`
}

func (s *Server) adminGetDocExport(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadDocExport(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, docExportView{
		Enabled: v.Enabled, URL: v.URL, Collection: v.Collection, HasToken: v.TokenEnc != "",
		Targets: v.Targets, Status: s.docExportStatusNow(r.Context()),
	})
}

func (s *Server) adminSetDocExport(w http.ResponseWriter, r *http.Request) {
	var in docExportIn
	if !readJSON(w, r, &in) {
		return
	}

	current, err := s.loadDocExport(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	next, msg := s.checkDocExport(r.Context(), in, current)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	raw, err := json.Marshal(next)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(r.Context(), docExportSetting, string(raw)); err != nil {
		writeStoreError(w, err)

		return
	}

	s.adminGetDocExport(w, r)
}

// checkDocExport validates a saved export and returns it, or a message.
func (s *Server) checkDocExport(ctx context.Context, in docExportIn, current docExportConfig) (docExportConfig, string) {
	out := docExportConfig{Enabled: in.Enabled, TokenEnc: current.TokenEnc, Targets: []docExportTarget{}}

	out.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if out.URL != "" {
		u, err := url.Parse(out.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return out, "the push URL must be an http(s) address such as https://search.example.com"
		}
	} else if in.Enabled {
		return out, "a push URL is required while the push is on"
	}

	out.Collection = strings.TrimSpace(in.Collection)
	if out.Collection == "" {
		out.Collection = docExportDefaultCollection
	}

	if len(out.Collection) > 128 || !collectionNameRe.MatchString(out.Collection) {
		return out, "the collection name may use lowercase letters, digits, dot, dash and underscore"
	}

	if in.Token != nil {
		token := strings.TrimSpace(*in.Token)
		if len(token) > 4096 {
			return out, "the token is too long"
		}

		enc, err := s.cipher.Encrypt(token)
		if err != nil {
			return out, "the token cannot be saved"
		}

		out.TokenEnc = enc
	}

	if len(in.Targets) > docExportMaxTargets {
		return out, fmt.Sprintf("at most %d connections can be exported", docExportMaxTargets)
	}

	seen := map[string]bool{}

	for _, t := range in.Targets {
		conn, err := s.store.GetConnection(ctx, t.ConnectionID)
		if err != nil {
			return out, "a chosen connection does not exist"
		}

		// Personal connections belong to their owner alone.
		if conn.OwnerID != "" {
			return out, conn.Name + " is a personal connection and cannot be exported"
		}

		if _, err := s.store.GetUser(ctx, t.UserID); err != nil {
			return out, "the user chosen for " + conn.Name + " does not exist"
		}

		if len(t.Schemas) > docExportMaxSchemas {
			return out, fmt.Sprintf("at most %d schemas per connection", docExportMaxSchemas)
		}

		schemas := []string{}

		for _, sc := range t.Schemas {
			sc = strings.TrimSpace(sc)
			if sc == "" || slices.Contains(schemas, sc) {
				continue
			}

			if len(sc) > 512 || strings.Contains(sc, "/") {
				return out, "invalid schema name " + strconv.Quote(clip(sc, 64))
			}

			schemas = append(schemas, sc)
		}

		if seen[conn.ID] {
			return out, conn.Name + " is listed twice"
		}

		seen[conn.ID] = true

		out.Targets = append(out.Targets, docExportTarget{ConnectionID: conn.ID, UserID: t.UserID, Schemas: schemas})
	}

	return out, ""
}

// adminRunDocExport pushes the documentation now.
func (s *Server) adminRunDocExport(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadDocExport(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if !v.Enabled || v.URL == "" {
		writeError(w, http.StatusBadRequest, "turn the push on and save a URL first")

		return
	}

	if !s.exportDocsInBackground() {
		writeError(w, http.StatusConflict, "the documentation is already being pushed")

		return
	}

	s.adminGetDocExport(w, r)
}
