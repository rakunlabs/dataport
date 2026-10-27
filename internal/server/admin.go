package server

import (
	"net/http"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// poolDefaults tells the connection form what an empty pool field means.
func (s *Server) poolDefaults(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Limits(&store.Connection{}))
}

func (s *Server) drivers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"drivers":       engine.Drivers(),
		"files_enabled": engine.FilesEnabled(),
		"odbc_dialects": engine.OdbcDialects(),
	})
}

// ////////////////////////////////////////////
// logs

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	list, err := s.store.ListQueryLog(r.Context(), store.QueryLogFilter{
		UserID:       q.Get("user_id"),
		ConnectionID: q.Get("connection_id"),
		Search:       q.Get("search"),
		Source:       q.Get("source"),
		Before:       parseInt64(q.Get("before")),
		Limit:        int(parseInt64(q.Get("limit"))),
	})
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminGetSessionLifetime(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.auth.Lifetime())
}

func (s *Server) adminSetSessionLifetime(w http.ResponseWriter, r *http.Request) {
	var in auth.SessionLifetime
	if !readJSON(w, r, &in) {
		return
	}

	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	if err := s.auth.SaveLifetime(r.Context(), in); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, s.auth.Lifetime())
}
