package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

// notebookPermissionError is a notebook the user can see but not use for
// the action asked.
type notebookPermissionError struct {
	path, have, need string
}

func (e *notebookPermissionError) Error() string { return "notebook permission denied" }

// lookupNotebook finds a notebook by id, then by path, and checks the
// user's permission on it. A notebook outside scope, when set, is not found.
func (s *Server) lookupNotebook(ctx context.Context, user *store.User, ref, permission, scope string) (*store.Notebook, error) {
	n, err := s.store.GetNotebook(ctx, user, ref, false)
	if errors.Is(err, store.ErrNotFound) {
		n, err = s.store.GetNotebook(ctx, user, ref, true)
	}
	if err != nil {
		return nil, err
	}
	if scope != "" && !notebookInScope(n, scope) {
		return nil, store.ErrNotFound
	}
	if !notebookAllows(n, permission) {
		return nil, &notebookPermissionError{path: n.Path, have: n.Permission, need: permission}
	}
	return n, nil
}

func (s *Server) notebookFor(w http.ResponseWriter, r *http.Request, permission string) *store.Notebook {
	n, err := s.lookupNotebook(r.Context(), currentUser(r), r.PathValue("note"), permission, "")
	if err != nil {
		var denied *notebookPermissionError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Error())
			return nil
		}
		writeStoreError(w, err)
		return nil
	}
	return n
}

// notebookAllows reports whether the caller's permission on a notebook
// covers the action: view < run < edit < owner.
func notebookAllows(n *store.Notebook, permission string) bool {
	return permission == "view" || n.Permission == "owner" || permission == "edit" && n.Permission == "edit" || permission == "run" && n.Permission != "view"
}

func (s *Server) listNotebooks(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListNotebooks(r.Context(), currentUser(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getNotebook(w http.ResponseWriter, r *http.Request) {
	if n := s.notebookFor(w, r, "view"); n != nil {
		writeJSON(w, http.StatusOK, n)
	}
}

type saveNotebookRequest struct {
	Name     string            `json:"name"`
	Path     string            `json:"path"`
	Document notebook.Document `json:"document"`
	Version  int               `json:"version"`
}

func (s *Server) saveNotebook(w http.ResponseWriter, r *http.Request) {
	var req saveNotebookRequest
	if !readJSON(w, r, &req) {
		return
	}
	var existing *store.Notebook
	if r.PathValue("note") != "" {
		existing = s.notebookFor(w, r, "edit")
		if existing == nil {
			return
		}
	}
	n, err := s.storeNotebook(r.Context(), currentUser(r), existing, req)
	if err != nil {
		writeNotebookError(w, err)
		return
	}
	status := http.StatusOK
	if existing == nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, n)
}

// storeNotebook validates and saves a notebook; existing is nil for a new
// one and must already be checked for edit permission.
func (s *Server) storeNotebook(ctx context.Context, user *store.User, existing *store.Notebook, req saveNotebookRequest) (*store.Notebook, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 160 || !notebook.PathPattern.MatchString(req.Path) {
		return nil, refuse(http.StatusBadRequest, "a name and a valid notebook path are required")
	}
	if err := req.Document.Validate(); err != nil {
		return nil, refuse(http.StatusBadRequest, err.Error())
	}
	n := &store.Notebook{OwnerID: user.ID, Permission: "owner"}
	if existing != nil {
		n = existing
		if n.PublishedVersion > 0 && n.Path != req.Path {
			return nil, refuse(http.StatusConflict, "unpublish before changing the endpoint path")
		}
	}
	// Saving a document must not let an editor smuggle in an ungranted DB.
	// Connections kept out of notebooks cannot be put into a cell either.
	for _, c := range req.Document.Cells {
		if g, err := s.store.GetGrant(ctx, user, c.ConnectionID); err != nil || g.Connection.HideNotebooks {
			return nil, refuse(http.StatusForbidden, "cell "+c.Path+" names a connection not shared with you")
		}
		if c.Transfer != nil {
			if g, err := s.store.GetGrant(ctx, user, c.Transfer.ConnectionID); err != nil || g.Connection.HideNotebooks {
				return nil, refuse(http.StatusForbidden, "the transfer target of cell "+c.Path+" is not shared with you")
			}
		}
	}
	n.Name, n.Path, n.Document, n.Version = req.Name, req.Path, req.Document, req.Version
	if err := s.store.SaveNotebook(ctx, n); err != nil {
		if errors.Is(err, store.ErrVersion) {
			return nil, refuse(http.StatusConflict, err.Error())
		}
		return nil, err
	}
	return n, nil
}

// writeNotebookError answers a refusal with its status and anything else as
// a store error.
func writeNotebookError(w http.ResponseWriter, err error) {
	var failure *runFailure
	if errors.As(err, &failure) {
		writeError(w, failure.status, failure.msg)

		return
	}

	writeStoreError(w, err)
}

func (s *Server) publishNotebook(w http.ResponseWriter, r *http.Request) {
	n := s.notebookFor(w, r, "owner")
	if n == nil {
		return
	}
	var req struct {
		Version int  `json:"version"`
		Publish bool `json:"publish"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.setPublished(r.Context(), currentUser(r), n, req.Version, req.Publish)
	if err != nil {
		writeNotebookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// setPublished publishes the saved version of an owned notebook, or takes
// it off its endpoint.
func (s *Server) setPublished(ctx context.Context, user *store.User, n *store.Notebook, version int, publish bool) (*store.Notebook, error) {
	if publish && len(n.Document.Cells) == 0 {
		return nil, refuse(http.StatusBadRequest, "add a cell before publishing")
	}
	if err := s.store.PublishNotebook(ctx, n.ID, version, publish); err != nil {
		if errors.Is(err, store.ErrVersion) {
			return nil, refuse(http.StatusConflict, err.Error())
		}
		return nil, err
	}
	return s.store.GetNotebook(ctx, user, n.ID, false)
}

func (s *Server) deleteNotebook(w http.ResponseWriter, r *http.Request) {
	n := s.notebookFor(w, r, "owner")
	if n == nil {
		return
	}
	if err := s.store.DeleteNotebook(r.Context(), n.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
