package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/rakunlabs/dataport/internal/store"
)

func (s *Server) notebookSharing(w http.ResponseWriter, r *http.Request) {
	n := s.notebookFor(w, r, "owner")
	if n == nil {
		return
	}
	if r.Method == http.MethodPut {
		var access []store.NotebookAccess
		if !readJSON(w, r, &access) {
			return
		}
		if err := s.setNotebookAccess(r.Context(), n, access); err != nil {
			writeNotebookError(w, err)
			return
		}
	}
	access, err := s.store.NotebookAccess(r.Context(), n.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, access)
}

var notebookPermissionRank = map[string]int{"view": 1, "run": 2, "edit": 3}

// setNotebookAccess replaces who an owned notebook is shared with. A
// subject named twice keeps its highest permission.
func (s *Server) setNotebookAccess(ctx context.Context, n *store.Notebook, access []store.NotebookAccess) error {
	if len(access) > 200 {
		return refuse(http.StatusBadRequest, "at most 200 notebook grants")
	}
	unique := make([]store.NotebookAccess, 0, len(access))
	seen := map[[2]string]int{}
	for _, a := range access {
		key := [2]string{a.SubjectType, a.SubjectID}
		if i, ok := seen[key]; ok {
			if notebookPermissionRank[a.Permission] > notebookPermissionRank[unique[i].Permission] {
				unique[i].Permission = a.Permission
			}
			continue
		}
		seen[key] = len(unique)
		unique = append(unique, a)
	}
	access = unique
	for _, a := range access {
		if a.Permission != "view" && a.Permission != "run" && a.Permission != "edit" {
			return refuse(http.StatusBadRequest, "permission must be view, run or edit")
		}
		var err error
		switch a.SubjectType {
		case "user":
			_, err = s.store.GetUser(ctx, a.SubjectID)
		case "group":
			_, err = s.store.GetGroup(ctx, a.SubjectID)
		default:
			err = errors.New("invalid subject type")
		}
		if err != nil {
			return refuse(http.StatusBadRequest, "unknown sharing subject")
		}
	}
	return s.store.SetNotebookAccess(ctx, n.ID, access)
}

func (s *Server) notebookSubjects(w http.ResponseWriter, r *http.Request) {
	if s.notebookFor(w, r, "owner") == nil {
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	type subject struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	out := []subject{}
	for _, u := range users {
		if !u.Disabled {
			out = append(out, subject{u.ID, u.Username, "user"})
		}
	}
	for _, g := range groups {
		out = append(out, subject{g.ID, g.Name, "group"})
	}
	writeJSON(w, http.StatusOK, out)
}
