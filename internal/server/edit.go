package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// applyEdits saves row changes made in the table grid. Only grants that allow
// writes may use it; the SQL is built by the engine from the table metadata.
func (s *Server) applyEdits(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schema     string           `json:"schema"`
		Table      string           `json:"table"`
		Edits      []engine.RowEdit `json:"edits"`
		KeyColumns []string         `json:"key_columns"`
		Confirm    bool             `json:"confirm"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Table) == "" {
		writeError(w, http.StatusBadRequest, "table is required")

		return
	}

	g, password := s.grant(w, r)
	if g == nil || !writable(w, g, req.Confirm, engine.RiskWrite) {
		return
	}

	user := currentUser(r)
	c := g.Connection

	result, err := s.engine.ApplyEdits(r.Context(), c, password, engine.EditRequest{
		Schema:     req.Schema,
		Table:      req.Table,
		Edits:      req.Edits,
		KeyColumns: req.KeyColumns,
		Timeout:    s.timeout(c),
	})

	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		Database:       c.Database,
		Status:         "ok",
	}

	if err != nil {
		entry.Status = "error"
		entry.Error = err.Error()
		entry.SQL = "-- grid edit of " + req.Schema + "." + req.Table + ", " + plural(len(req.Edits), "change")

		var connErr *engine.ConnError
		if errors.As(err, &connErr) {
			entry.Status = "unreachable"
		}
	} else {
		entry.SQL = strings.Join(result.Statements, ";\n")
		entry.RowCount = result.Affected
		entry.DurationMS = result.DurationMS
	}

	s.audit(r.Context(), entry)

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// previewEdits returns the statements a save would run, without running them.
func (s *Server) previewEdits(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schema     string           `json:"schema"`
		Table      string           `json:"table"`
		Edits      []engine.RowEdit `json:"edits"`
		KeyColumns []string         `json:"key_columns"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	if g.ReadOnly {
		writeError(w, http.StatusForbidden, engine.ErrReadOnly.Error())

		return
	}

	stmts, err := s.engine.PreviewEdits(r.Context(), g.Connection, password, engine.EditRequest{
		Schema: req.Schema, Table: req.Table, Edits: req.Edits, KeyColumns: req.KeyColumns,
	})
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"statements": stmts})
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return strconv.Itoa(n) + " " + word + "s"
}
