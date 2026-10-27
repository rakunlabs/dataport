package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
)

type generateIn struct {
	Schema   string             `json:"schema"`
	Table    string             `json:"table"`
	Columns  []engine.GenColumn `json:"columns"`
	Rows     int                `json:"rows"`
	Seed     int64              `json:"seed"`
	Truncate bool               `json:"truncate"`
	Confirm  bool               `json:"confirm"`
	// Pools are values the browser made in the chosen language.
	Pools map[string][]string `json:"pools,omitempty"`
}

func (in generateIn) request() engine.GenerateRequest {
	return engine.GenerateRequest{
		Schema: in.Schema, Table: in.Table, Columns: in.Columns, Rows: in.Rows,
		Seed: in.Seed, Truncate: in.Truncate, Pools: in.Pools,
	}
}

// generatePreview returns the generator of each column with a few rows,
// writing nothing.
func (s *Server) generatePreview(w http.ResponseWriter, r *http.Request) {
	var in generateIn
	if !readJSON(w, r, &in) {
		return
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	req := in.request()
	req.Timeout = s.runTimeout(g.Connection, nil)

	plan, err := s.engine.GeneratePreview(r.Context(), g.Connection, password, req)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// generate inserts made-up rows into a table, reporting progress as lines
// of JSON while it runs.
func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	var in generateIn
	if !readJSON(w, r, &in) {
		return
	}

	g, password := s.grant(w, r)
	if g == nil || !writable(w, g, in.Confirm, engine.RiskWrite) {
		return
	}

	if in.Rows < 1 || in.Rows > engine.MaxGenerateRows {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rows must be between 1 and %d", engine.MaxGenerateRows))

		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	send := func(v any) {
		_ = enc.Encode(v)
		_ = rc.Flush()
	}

	req := in.request()
	req.Timeout = s.runTimeout(g.Connection, nil)
	start := time.Now()

	rows, err := s.engine.Generate(r.Context(), g.Connection, password, req, func(p engine.GenerateProgress) { send(p) })

	s.log(r.Context(), currentUser(r), g.Connection, generateSummary(in), err, rows, time.Since(start).Milliseconds())

	final := map[string]any{"finished": true, "rows": rows}
	if err != nil {
		final["error"] = err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) && !currentUser(r).IsAdmin() {
			final["error"] = "cannot reach the database, contact your administrator"
		}
	}

	send(final)
}

func generateSummary(in generateIn) string {
	s := fmt.Sprintf("-- generate %d rows into %s", in.Rows, in.Table)
	if in.Schema != "" {
		s = fmt.Sprintf("-- generate %d rows into %s.%s", in.Rows, in.Schema, in.Table)
	}

	if in.Truncate {
		s += " (table emptied first)"
	}

	if in.Seed != 0 {
		s += fmt.Sprintf(", seed %d", in.Seed)
	}

	if len(in.Columns) > 0 {
		s += "\n-- " + engine.DescribeColumns(in.Columns)
	}

	return s
}
