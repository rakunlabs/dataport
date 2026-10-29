package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// explainQuery shows the execution plan of one statement. Analyze runs the
// statement and always rolls it back; on production a write is still
// confirmed first, as it takes locks while it runs.
func (s *Server) explainQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SQL        string                `json:"sql"`
		QueryID    string                `json:"query_id"`
		Options    engine.ExplainOptions `json:"options"`
		Confirm    bool                  `json:"confirm"`
		TimeoutSec *int                  `json:"timeout_sec"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.SQL) == "" {
		writeError(w, http.StatusBadRequest, "sql is empty")

		return
	}

	if len(req.SQL) > maxSQLLength {
		writeError(w, http.StatusRequestEntityTooLarge, "sql is too long")

		return
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	c := g.Connection

	d, err := engine.DriverFor(c)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	if req.Options.Analyze && c.Production && !g.ReadOnly && !req.Confirm {
		if risk := engine.Classify(d, req.SQL); risk != engine.RiskRead {
			writeConfirmationRequired(w, risk)

			return
		}
	}

	user := currentUser(r)

	res, err := s.engine.Explain(r.Context(), c, password, engine.ExplainRequest{
		SQL:      req.SQL,
		Options:  req.Options,
		ReadOnly: g.ReadOnly,
		Timeout:  s.runTimeout(c, req.TimeoutSec),
		MaxRows:  s.rowCeiling(),
		QueryID:  req.QueryID,
		UserID:   user.ID,
	})

	logged := req.SQL
	if res != nil {
		logged = res.SQL
	}

	s.logRun(r, c, logged, err, 0, res)

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, res)
}

// countQuery counts every row a query returns, for the result footer.
func (s *Server) countQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SQL        string `json:"sql"`
		QueryID    string `json:"query_id"`
		TimeoutSec *int   `json:"timeout_sec"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.SQL) == "" || len(req.SQL) > maxSQLLength {
		writeError(w, http.StatusBadRequest, "sql is empty or too long")

		return
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	res, err := s.engine.Count(r.Context(), g.Connection, password, engine.CountRequest{
		SQL:     req.SQL,
		Timeout: s.runTimeout(g.Connection, req.TimeoutSec),
		QueryID: req.QueryID,
		UserID:  currentUser(r).ID,
	})

	logged := req.SQL
	if res != nil {
		logged = res.SQL
	}

	var rows int64
	if res != nil {
		rows = 1
	}

	s.logRun(r, g.Connection, logged, err, rows, res)

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, res)
}

// logRun writes a plan or count to the query log.
func (s *Server) logRun(r *http.Request, c *store.Connection, sql string, err error, rows int64, res any) {
	user := currentUser(r)

	entry := &store.QueryLog{
		UserID: user.ID, Username: user.Username,
		ConnectionID: c.ID, ConnectionName: c.Name, Database: c.Database,
		SQL: engine.Redact(sql), Status: "ok", Source: "web", RowCount: rows,
	}

	switch x := res.(type) {
	case *engine.ExplainResult:
		if x != nil && x.Result != nil {
			entry.DurationMS = x.Result.DurationMS
		}
	case *engine.CountResult:
		if x != nil {
			entry.DurationMS = x.DurationMS
		}
	}

	if err != nil {
		entry.Status, entry.Error = "rejected", err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) {
			entry.Status = "unreachable"
		}
	}

	s.audit(r.Context(), entry)
}
