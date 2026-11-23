package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
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
	// A write cell the editor adds or changes needs their own write grant,
	// as it later runs with the grant of whoever runs the notebook; write
	// cells left as they were stay, so a read-only editor can fix the rest.
	// Compared as stored: a missing list and an empty one are the same.
	before, next := normalizedCopy(notebook.Document{}), normalizedCopy(req.Document)
	if existing != nil {
		before = normalizedCopy(existing.Document)
	}
	for i, c := range req.Document.Cells {
		g, err := s.store.GetGrant(ctx, user, c.ConnectionID)
		if err != nil || g.Connection.HideNotebooks {
			return nil, refuse(http.StatusForbidden, "cell "+c.Path+" names a connection not shared with you")
		}
		unchanged := sameWriteCell(before, next, next.Cells[i])
		if c.Write && c.Transfer == nil && g.ReadOnly && !unchanged {
			return nil, refuse(http.StatusForbidden, "cell "+c.Path+" writes to a connection that is read-only for you")
		}
		if c.Transfer != nil {
			dst, err := s.store.GetGrant(ctx, user, c.Transfer.ConnectionID)
			if err != nil || dst.Connection.HideNotebooks {
				return nil, refuse(http.StatusForbidden, "the transfer target of cell "+c.Path+" is not shared with you")
			}
			if dst.ReadOnly && !unchanged {
				return nil, refuse(http.StatusForbidden, "the transfer target of cell "+c.Path+" is read-only for you")
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

// sameWriteCell reports a cell of next whose writing is as in before: the
// same connection, SQL, template flag, transfer and whether it runs. A
// template also renders from the parameters and the cells it depends on,
// so those must be as before too.
func sameWriteCell(before, next notebook.Document, c notebook.Cell) bool {
	old, ok := cellByID(before, c.ID)
	if !ok || old.Write != c.Write || old.Enabled != c.Enabled || old.ConnectionID != c.ConnectionID || old.SQL != c.SQL ||
		old.Template != c.Template || !reflect.DeepEqual(old.Transfer, c.Transfer) {
		return false
	}

	if !c.Template {
		return true
	}

	if !reflect.DeepEqual(before.Parameters, next.Parameters) || !slices.Equal(old.Dependencies, c.Dependencies) {
		return false
	}

	// What a dependency returns is fixed by its own SQL and inputs, all the
	// way down.
	seen := map[string]bool{}
	var same func(paths []string) bool
	same = func(paths []string) bool {
		for _, path := range paths {
			if seen[path] {
				continue
			}
			seen[path] = true

			a, okA := cellByPath(before, path)
			b, okB := cellByPath(next, path)
			if !okA || !okB || a.ConnectionID != b.ConnectionID || a.SQL != b.SQL || a.Template != b.Template ||
				a.Enabled != b.Enabled || !slices.Equal(a.Dependencies, b.Dependencies) || !same(b.Dependencies) {
				return false
			}
		}

		return true
	}

	return same(c.Dependencies)
}

// normalizedCopy normalizes a copy of d, leaving d as it was.
func normalizedCopy(d notebook.Document) notebook.Document {
	out := notebook.Document{Cells: make([]notebook.Cell, len(d.Cells)), Parameters: slices.Clone(d.Parameters)}
	for i, c := range d.Cells {
		c.Dependencies = slices.Clone(c.Dependencies)
		if c.Transfer != nil {
			tr := *c.Transfer
			tr.Mappings = slices.Clone(tr.Mappings)
			c.Transfer = &tr
		}
		out.Cells[i] = c
	}
	out.Normalize()

	return out
}

func cellByID(d notebook.Document, id string) (notebook.Cell, bool) {
	for _, c := range d.Cells {
		if c.ID == id && id != "" {
			return c, true
		}
	}

	return notebook.Cell{}, false
}

func cellByPath(d notebook.Document, path string) (notebook.Cell, bool) {
	for _, c := range d.Cells {
		if c.Path == path {
			return c, true
		}
	}

	return notebook.Cell{}, false
}
