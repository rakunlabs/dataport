package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

const maxSQLLength = 1 << 20

func (s *Server) runQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SQL     string `json:"sql"`
		QueryID string `json:"query_id"`
		// Confirm acknowledges the production prompt for writes.
		Confirm bool `json:"confirm"`
		// MaxRows chooses the rows of this run: 0 keeps the connection's
		// default, -1 asks for as many as the server allows.
		MaxRows int `json:"max_rows"`
		// TimeoutSec chooses the time limit of this run: nil keeps the
		// connection's, 0 asks for none, within the server's cap.
		TimeoutSec *int `json:"timeout_sec"`
		// TxID runs in the tab's manual transaction, which stays open until
		// committed or rolled back; TxContinue expects it open already.
		TxID       string `json:"tx_id"`
		TxContinue bool   `json:"tx_continue"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.SQL) == "" {
		writeError(w, http.StatusBadRequest, "sql is empty")

		return
	}

	if len(req.TxID) > 64 {
		writeError(w, http.StatusBadRequest, "tx_id is too long")

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

	result, err := s.execute(r.Context(), currentUser(r), g, password, execInput{
		SQL: req.SQL, QueryID: req.QueryID, Confirm: req.Confirm, Source: "web", Rows: req.MaxRows, TimeoutSec: req.TimeoutSec,
		TxID: req.TxID, TxContinue: req.TxContinue,
	})
	if err != nil {
		var confirm *confirmationError
		if errors.As(err, &confirm) {
			writeConfirmationRequired(w, confirm.risk)

			return
		}

		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

type execInput struct {
	Args    []any
	SQL     string
	QueryID string
	// Confirm acknowledges the production prompt for writes.
	Confirm bool
	// ReadOnly narrows the grant, for read-only access tokens and the MCP
	// query tool.
	ReadOnly bool
	// MaxRows lowers the connection's row cap when set.
	MaxRows int
	// Rows replaces the connection's default row count, up to the server
	// ceiling; -1 asks for the ceiling itself.
	Rows int
	// TimeoutSec replaces the connection's time limit when set; 0 is none.
	TimeoutSec *int
	Source     string
	// TxID and TxContinue run in a manual transaction of the web workspace.
	TxID       string
	TxContinue bool
}

// confirmationError means a production connection needs the user to confirm
// the changes first.
type confirmationError struct{ risk engine.Risk }

func (e *confirmationError) Error() string {
	return "this is a production database; confirm to run the changes"
}

// execute runs a script on a grant and writes it to the query log. Every
// path that runs user SQL goes through here, so the production check and
// the log cannot be skipped.
func (s *Server) execute(ctx context.Context, user *store.User, g *store.Grant, password string, in execInput) (*engine.ExecResult, error) {
	c := g.Connection
	readOnly := g.ReadOnly || in.ReadOnly

	if c.Production && !readOnly && !in.Confirm {
		if d, err := engine.DriverFor(c); err == nil {
			if risk := engine.Classify(d, in.SQL); risk != engine.RiskRead {
				return nil, &confirmationError{risk: risk}
			}
		}
	}

	maxRows := s.maxRows(c)

	switch {
	case in.Rows < 0:
		maxRows = s.rowCeiling()
	case in.Rows > 0:
		maxRows = min(in.Rows, s.rowCeiling())
	}

	if in.MaxRows > 0 && in.MaxRows < maxRows {
		maxRows = in.MaxRows
	}

	result, err := s.engine.Execute(ctx, c, password, engine.ExecRequest{
		Args:       in.Args,
		QueryID:    in.QueryID,
		UserID:     user.ID,
		SQL:        in.SQL,
		ReadOnly:   readOnly,
		MaxRows:    maxRows,
		Timeout:    s.runTimeout(c, in.TimeoutSec),
		TxID:       in.TxID,
		TxContinue: in.TxContinue,
	})

	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		Database:       c.Database,
		// Passwords of CREATE USER and the like stay out of the log.
		SQL:    engine.Redact(in.SQL),
		Status: "ok",
		Source: in.Source,
	}

	if err != nil {
		entry.Status = "rejected"
		entry.Error = err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) {
			entry.Status = "unreachable"
		}
	} else {
		entry.RowCount = result.TotalRows()
		entry.DurationMS = result.DurationMS

		if msg := result.FirstError(); msg != "" {
			entry.Status = "error"
			entry.Error = msg
		}
	}

	s.audit(ctx, entry)

	return result, err
}

func (s *Server) cancelQuery(w http.ResponseWriter, r *http.Request) {
	if !s.engine.Cancel(r.Context(), currentUser(r).ID, r.PathValue("queryID")) {
		writeError(w, http.StatusNotFound, "query is not running")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) maxRows(c *store.Connection) int {
	n := c.MaxRows
	if n <= 0 {
		n = s.queryCfg().DefaultMaxRows
	}

	if limit := s.queryCfg().MaxRowsLimit; limit > 0 && n > limit {
		n = limit
	}

	return n
}

// rowCeiling is the most rows one statement may return to the workspace;
// zero in the configuration means no ceiling.
func (s *Server) rowCeiling() int {
	if limit := s.queryCfg().MaxRowsLimit; limit > 0 {
		return limit
	}

	return math.MaxInt32
}

// timeout is the time limit of a run on the connection; zero is none.
func (s *Server) timeout(c *store.Connection) time.Duration {
	d := s.queryCfg().DefaultTimeout
	if c.TimeoutMS > 0 {
		d = time.Duration(c.TimeoutMS) * time.Millisecond
	}

	return s.capTimeout(d)
}

// runTimeout is the time limit the user chose for one run, or the
// connection's when they chose none.
func (s *Server) runTimeout(c *store.Connection, sec *int) time.Duration {
	if sec == nil || *sec < 0 {
		return s.timeout(c)
	}

	return s.capTimeout(time.Duration(*sec) * time.Second)
}

// capTimeout keeps a limit within the server's cap; with a cap, "no limit"
// becomes the cap.
func (s *Server) capTimeout(d time.Duration) time.Duration {
	if max := s.queryCfg().MaxTimeout; max > 0 && (d <= 0 || d > max) {
		return max
	}

	return d
}

// endTx commits or rolls back the manual transaction of a query tab, and
// writes it to the query log.
func (s *Server) endTx(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxID   string `json:"tx_id"`
		Commit bool   `json:"commit"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	g, _ := s.grant(w, r)
	if g == nil {
		return
	}

	// Writes on production were confirmed when they ran.
	user := currentUser(r)

	// A connection the user since made read-only keeps no earlier writes.
	if g.ReadOnly {
		req.Commit = false
	}

	end, err := s.engine.EndTx(r.Context(), user.ID, req.TxID, g.Connection.ID, req.Commit)

	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   g.Connection.ID,
		ConnectionName: g.Connection.Name,
		Database:       g.Connection.Database,
		SQL:            "ROLLBACK",
		Status:         "ok",
		Source:         "web",
	}

	if req.Commit {
		entry.SQL = "COMMIT"
	}

	if err != nil {
		entry.Status = "error"
		entry.Error = err.Error()
	}

	// A transaction that was already gone has nothing to log.
	if !errors.Is(err, engine.ErrTxLost) && !errors.Is(err, engine.ErrTxBusy) {
		s.audit(r.Context(), entry)
	}

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, end)
}

// listTxs returns the user's open manual transactions, so the workspace
// learns which of its tabs still have one.
func (s *Server) listTxs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Txs(r.Context(), currentUser(r).ID))
}
