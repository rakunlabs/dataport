package server

import (
	"net/http"

	"github.com/rakunlabs/dataport/internal/engine"
)

func (s *Server) schemas(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Schemas(r.Context(), g.Connection, password)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) objects(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Objects(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) columns(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()

	list, err := s.engine.Columns(r.Context(), g.Connection, password, q.Get("schema"), q.Get("table"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) routines(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Routines(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) diagram(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	out, err := s.engine.Diagram(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) definition(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()

	def, err := s.engine.Definition(r.Context(), g.Connection, password, q.Get("schema"), engine.Routine{
		Name: q.Get("name"), Kind: q.Get("kind"), Signature: q.Get("signature"),
	})
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"definition": def})
}

func (s *Server) structure(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()
	if q.Get("table") == "" {
		writeError(w, http.StatusBadRequest, "table is required")

		return
	}

	out, err := s.engine.Structure(r.Context(), g.Connection, password, q.Get("schema"), q.Get("table"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}
