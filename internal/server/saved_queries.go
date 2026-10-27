package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

func (s *Server) myHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	list, err := s.store.ListQueryLog(r.Context(), store.QueryLogFilter{
		UserID:       currentUser(r).ID,
		ConnectionID: q.Get("connection_id"),
		Search:       q.Get("search"),
		Before:       parseInt64(q.Get("before")),
		Limit:        int(parseInt64(q.Get("limit"))),
	})
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func parseInt64(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)

	return n
}

type savedRequest struct {
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	SQL          string `json:"sql"`
}

func (req *savedRequest) validate() string {
	req.Name = strings.TrimSpace(req.Name)

	switch {
	case req.Name == "":
		return "name is required"
	case len(req.Name) > 200:
		return "name is too long"
	case len(req.SQL) > maxSQLLength:
		return "sql is too long"
	}

	return ""
}

func (s *Server) listSaved(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSavedQueries(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createSaved(w http.ResponseWriter, r *http.Request) {
	var req savedRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	q := &store.SavedQuery{UserID: currentUser(r).ID, ConnectionID: req.ConnectionID, Name: req.Name, SQL: req.SQL}
	if err := s.store.CreateSavedQuery(r.Context(), q); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) updateSaved(w http.ResponseWriter, r *http.Request) {
	var req savedRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	q := &store.SavedQuery{
		ID: r.PathValue("id"), UserID: currentUser(r).ID,
		ConnectionID: req.ConnectionID, Name: req.Name, SQL: req.SQL,
	}
	if err := s.store.UpdateSavedQuery(r.Context(), q); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, q)
}

func (s *Server) deleteSaved(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSavedQuery(r.Context(), currentUser(r).ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
