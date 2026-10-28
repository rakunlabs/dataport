package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/rakunlabs/dataport/internal/store"
)

const notebookAccessSetting = "notebook_access"

// errNotebooksOff is returned to users the notebook feature is not open to.
var errNotebooksOff = errors.New("notebooks are not available for your account; ask an administrator")

// NotebookAccess decides who may use notebooks at all, apart from the
// sharing of each notebook. It is read from the store on each use, so every
// replica applies a change at once.
type NotebookAccess struct {
	// Restricted limits notebooks to admins and the members of GroupIDs.
	// Off, every user may use them.
	Restricted bool     `json:"restricted"`
	GroupIDs   []string `json:"group_ids"`
}

func (s *Server) notebookAccess(ctx context.Context) NotebookAccess {
	a := NotebookAccess{GroupIDs: []string{}}

	raw, err := s.store.GetSetting(ctx, notebookAccessSetting)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("read notebook access", "error", err)
		}

		return a
	}

	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		slog.Warn("ignoring stored notebook access", "error", err)

		return NotebookAccess{GroupIDs: []string{}}
	}

	if a.GroupIDs == nil {
		a.GroupIDs = []string{}
	}

	return a
}

// notebooksAllowed reports whether the user may use notebooks: list, open,
// save, share and run them, also through MCP and published endpoints.
func (s *Server) notebooksAllowed(ctx context.Context, u *store.User) bool {
	if u == nil {
		return false
	}

	if u.IsAdmin() {
		return true
	}

	a := s.notebookAccess(ctx)
	if !a.Restricted {
		return true
	}

	return slices.ContainsFunc(u.GroupIDs, func(g string) bool { return slices.Contains(a.GroupIDs, g) })
}

// requireNotebooks guards a notebook route.
func (s *Server) requireNotebooks(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.notebooksAllowed(r.Context(), currentUser(r)) {
			writeError(w, http.StatusForbidden, errNotebooksOff.Error())

			return
		}

		next(w, r)
	}
}

func (s *Server) adminGetNotebookAccess(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.notebookAccess(r.Context()))
}

func (s *Server) adminSetNotebookAccess(w http.ResponseWriter, r *http.Request) {
	var in NotebookAccess
	if !readJSON(w, r, &in) {
		return
	}

	groups := []string{}

	for _, id := range in.GroupIDs {
		if slices.Contains(groups, id) {
			continue
		}

		if _, err := s.store.GetGroup(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "unknown group")

				return
			}

			writeStoreError(w, err)

			return
		}

		groups = append(groups, id)
	}

	in.GroupIDs = groups

	data, err := json.Marshal(in)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(r.Context(), notebookAccessSetting, string(data)); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, in)
}
