package server

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

type groupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []string `json:"member_ids"`
}

func (req *groupRequest) validate() string {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return "name must be 1-100 characters"
	}

	return ""
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	g := &store.Group{Name: req.Name, Description: req.Description, MemberIDs: req.MemberIDs}
	if err := s.store.CreateGroup(r.Context(), g); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	g := &store.Group{ID: r.PathValue("id"), Name: req.Name, Description: req.Description, MemberIDs: req.MemberIDs}
	if g.MemberIDs == nil {
		g.MemberIDs = []string{}
	}

	if err := s.store.UpdateGroup(r.Context(), g); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteGroup(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
