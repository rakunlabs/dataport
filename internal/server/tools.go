package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Connection tools from the tree's context menu: disconnect, the process
// list, users and privileges, export, import and transfer between
// connections. Each writes the query log like a run does.

// log writes an entry of the query log; failures are only logged.
func (s *Server) log(ctx context.Context, user *store.User, c *store.Connection, sql string, err error, rows, ms int64) {
	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		Database:       c.Database,
		SQL:            sql,
		Status:         "ok",
		Source:         "web",
		RowCount:       rows,
		DurationMS:     ms,
	}

	if err != nil {
		entry.Status = "error"
		entry.Error = err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) {
			entry.Status = "unreachable"
		}
	}

	if logErr := s.store.AddQueryLog(ctx, entry); logErr != nil {
		slog.Error("write query log", "error", logErr)
	}
}

// writable answers for grants that may not change data and production
// connections not yet confirmed. It returns false after writing a response.
func writable(w http.ResponseWriter, g *store.Grant, confirm bool, risk engine.Risk) bool {
	if g.ReadOnly {
		writeError(w, http.StatusForbidden, engine.ErrReadOnly.Error())

		return false
	}

	if g.Connection.Production && !confirm {
		writeJSON(w, http.StatusConflict, map[string]any{
			"message":               "this is a production database; confirm to run the changes",
			"confirmation_required": true,
			"risk":                  risk,
		})

		return false
	}

	return true
}

// disconnect lets go of the sessions dataport holds on the connection.
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GetGrant(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
		} else {
			writeStoreError(w, err)
		}

		return
	}

	c := *g.Connection
	if c.UserCredentials {
		c.Owner = currentUser(r).ID
	}

	s.engine.Disconnect(&c)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) processes(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	out, err := s.engine.Processes(r.Context(), g.Connection, password)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) killProcess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		Terminate bool   `json:"terminate"`
		Confirm   bool   `json:"confirm"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	g, password := s.grant(w, r)
	if g == nil || !writable(w, g, req.Confirm, engine.RiskWrite) {
		return
	}

	start := time.Now()
	stmt, err := s.engine.KillProcess(r.Context(), g.Connection, password, req.ID, req.Terminate)

	if stmt != "" {
		s.log(r.Context(), currentUser(r), g.Connection, stmt, err, 0, time.Since(start).Milliseconds())
	}

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dbUsers(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Users(r.Context(), g.Connection, password)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) dbGrants(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()

	list, err := s.engine.Grants(r.Context(), g.Connection, password, q.Get("name"), q.Get("host"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

// lazyWriter sends the download headers with the first byte, so an export
// that fails before writing anything can still answer with an error.
type lazyWriter struct {
	w       http.ResponseWriter
	headers func(h http.Header)
	started bool
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	if !l.started {
		l.started = true
		l.headers(l.w.Header())
		l.w.WriteHeader(http.StatusOK)
	}

	return l.w.Write(p)
}

// export streams tables of a schema as CSV, JSON or SQL.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	req := engine.ExportRequest{
		Schema:    q.Get("schema"),
		Tables:    q["table"],
		Format:    q.Get("format"),
		Structure: q.Get("structure") == "1",
		Data:      q.Get("data") != "0",
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	req.Timeout = s.runTimeout(g.Connection, nil)

	name := engine.ExportFileName(req)
	types := map[string]string{"csv": "text/csv; charset=utf-8", "json": "application/json", "sql": "application/sql; charset=utf-8"}

	lw := &lazyWriter{w: w, headers: func(h http.Header) {
		ct := types[req.Format]
		if strings.HasSuffix(name, ".zip") {
			ct = "application/zip"
		}

		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		h.Set("Cache-Control", "no-store")
	}}

	start := time.Now()
	stats, err := s.engine.Export(r.Context(), g.Connection, password, req, lw)

	what := fmt.Sprintf("-- export %s of %s: %s", req.Format, orAll(req.Schema), strings.Join(req.Tables, ", "))
	if stats != nil {
		s.log(r.Context(), currentUser(r), g.Connection, what, err, stats.Rows, time.Since(start).Milliseconds())
	}

	if err != nil {
		if !lw.started {
			writeEngineError(w, r, err)

			return
		}

		// The file is cut short; the browser shows a failed download.
		slog.Warn("export failed", "connection", g.Connection.ID, "error", err)
		panic(http.ErrAbortHandler)
	}

	if !lw.started {
		lw.Write(nil) //nolint:errcheck // An empty export still sends its headers.
	}
}

func orAll(schema string) string {
	if schema == "" {
		return "default schema"
	}

	return schema
}

// importRows adds one batch of rows from a file to a table.
func (s *Server) importRows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schema    string              `json:"schema"`
		Table     string              `json:"table"`
		Columns   []string            `json:"columns"`
		Rows      [][]any             `json:"rows"`
		Create    []engine.ColumnInfo `json:"create"`
		Truncate  bool                `json:"truncate"`
		EmptyNull bool                `json:"empty_null"`
		Confirm   bool                `json:"confirm"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	g, password := s.grant(w, r)
	if g == nil || !writable(w, g, req.Confirm, engine.RiskWrite) {
		return
	}

	res, err := s.engine.Import(r.Context(), g.Connection, password, engine.ImportRequest{
		Schema: req.Schema, Table: req.Table, Columns: req.Columns, Rows: req.Rows,
		Create: req.Create, Truncate: req.Truncate, EmptyNull: req.EmptyNull,
		Timeout: s.runTimeout(g.Connection, nil),
	})

	sql := "-- import into " + req.Table
	if res != nil && len(res.Statements) > 0 {
		sql = strings.Join(res.Statements, ";\n")
	}

	var rows, ms int64
	if res != nil {
		rows, ms = res.Inserted, res.DurationMS
	}

	s.log(r.Context(), currentUser(r), g.Connection, sql, err, rows, ms)

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, res)
}

// transfer copies tables to another connection, reporting progress as
// lines of JSON while it runs.
func (s *Server) transfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schema       string   `json:"schema"`
		Tables       []string `json:"tables"`
		TargetID     string   `json:"target_id"`
		TargetSchema string   `json:"target_schema"`
		Create       bool     `json:"create"`
		Truncate     bool     `json:"truncate"`
		Data         bool     `json:"data"`
		Confirm      bool     `json:"confirm"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	src, srcPassword := s.grant(w, r)
	if src == nil {
		return
	}

	user := currentUser(r)

	dst, dstPassword, err := s.resolveGrant(r.Context(), user, req.TargetID)
	if err != nil {
		var credErr *credentialsError

		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "target connection not found")
		case errors.As(err, &credErr):
			writeError(w, http.StatusPreconditionRequired, "sign in to the target database first: "+credErr.msg)
		default:
			writeStoreError(w, err)
		}

		return
	}

	if !writable(w, dst, req.Confirm, engine.RiskWrite) {
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)

	var (
		mu     sync.Mutex
		rows   int64
		done   []string
		failed []string
	)

	send := func(v any) {
		mu.Lock()
		defer mu.Unlock()

		_ = enc.Encode(v)
		_ = rc.Flush()
	}

	start := time.Now()

	err = s.engine.Transfer(r.Context(), src.Connection, srcPassword, dst.Connection, dstPassword, engine.TransferRequest{
		Schema: req.Schema, Tables: req.Tables, TargetSchema: req.TargetSchema,
		Create: req.Create, Truncate: req.Truncate, Data: req.Data,
		Timeout: s.runTimeout(dst.Connection, nil),
	}, func(p engine.TransferProgress) {
		if p.Done {
			rows += p.Rows
			done = append(done, p.Table)
		}

		if p.Error != "" {
			failed = append(failed, p.Table+": "+p.Error)
		}

		send(p)
	})

	summary := fmt.Sprintf("-- transfer from %s (%s) to %s: %d tables copied", src.Connection.Name, orAll(req.Schema), orAll(req.TargetSchema), len(done))
	if len(done) > 0 {
		summary += "\n-- " + strings.Join(done, ", ")
	}

	logErr := err
	if logErr == nil && len(failed) > 0 {
		logErr = errors.New(strings.Join(failed, "; "))
	}

	s.log(r.Context(), user, dst.Connection, summary, logErr, rows, time.Since(start).Milliseconds())

	final := map[string]any{"finished": true, "rows": rows, "tables": len(done), "failed": len(failed)}
	if err != nil {
		final["error"] = err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) && !user.IsAdmin() {
			final["error"] = "cannot reach the database, contact your administrator"
		}
	}

	send(final)
}
