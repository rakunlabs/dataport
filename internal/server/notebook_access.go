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

// Features an admin may open to everyone or to admins and some groups:
// notebooks, and building schema documentation. Each is a setting read from
// the store on every use, so every replica applies a change at once.

const (
	notebookAccessSetting = "notebook_access"
	docAccessSetting      = "doc_access"
)

// errNotebooksOff is returned to users the notebook feature is not open to.
var errNotebooksOff = errors.New("notebooks are not available for your account; ask an administrator")

// FeatureAccess decides who may use a feature.
type FeatureAccess struct {
	// Restricted limits the feature to admins and the members of GroupIDs.
	// Off, every user may use it.
	Restricted bool     `json:"restricted"`
	GroupIDs   []string `json:"group_ids"`
}

// NotebookAccess decides who may use notebooks at all, apart from the
// sharing of each notebook.
type NotebookAccess = FeatureAccess

func (s *Server) featureAccess(ctx context.Context, setting string) FeatureAccess {
	a := FeatureAccess{GroupIDs: []string{}}

	raw, err := s.store.GetSetting(ctx, setting)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("read feature access", "setting", setting, "error", err)
		}

		return a
	}

	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		slog.Warn("ignoring stored feature access", "setting", setting, "error", err)

		return FeatureAccess{GroupIDs: []string{}}
	}

	if a.GroupIDs == nil {
		a.GroupIDs = []string{}
	}

	return a
}

func (s *Server) notebookAccess(ctx context.Context) NotebookAccess {
	return s.featureAccess(ctx, notebookAccessSetting)
}

func (a FeatureAccess) allows(u *store.User) bool {
	if u == nil {
		return false
	}

	if u.IsAdmin() || !a.Restricted {
		return true
	}

	return slices.ContainsFunc(u.GroupIDs, func(g string) bool { return slices.Contains(a.GroupIDs, g) })
}

// notebooksAllowed reports whether the user may use notebooks: list, open,
// save, share and run them, also through MCP and published endpoints.
func (s *Server) notebooksAllowed(ctx context.Context, u *store.User) bool {
	return s.notebookAccess(ctx).allows(u)
}

// docsBuildAllowed reports whether the user may build and refresh schema
// documentation. Others read what was built with the same database account.
func (s *Server) docsBuildAllowed(ctx context.Context, u *store.User) bool {
	return s.featureAccess(ctx, docAccessSetting).allows(u)
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

// setFeatureGroup adds a group to a feature's groups or takes it away.
func (s *Server) setFeatureGroup(ctx context.Context, setting, groupID string, on bool) error {
	a := s.featureAccess(ctx, setting)

	has := slices.Contains(a.GroupIDs, groupID)
	if has == on {
		return nil
	}

	if on {
		a.GroupIDs = append(a.GroupIDs, groupID)
	} else {
		a.GroupIDs = slices.DeleteFunc(a.GroupIDs, func(g string) bool { return g == groupID })
	}

	return s.saveFeatureAccess(ctx, setting, a)
}

func (s *Server) saveFeatureAccess(ctx context.Context, setting string, a FeatureAccess) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}

	return s.store.SetSetting(ctx, setting, string(data))
}

func (s *Server) adminGetFeatureAccess(setting string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.featureAccess(r.Context(), setting))
	}
}

func (s *Server) adminSetFeatureAccess(setting string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in FeatureAccess
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

		if err := s.saveFeatureAccess(r.Context(), setting, in); err != nil {
			writeStoreError(w, err)

			return
		}

		writeJSON(w, http.StatusOK, in)
	}
}

func (s *Server) adminGetNotebookAccess(w http.ResponseWriter, r *http.Request) {
	s.adminGetFeatureAccess(notebookAccessSetting)(w, r)
}

func (s *Server) adminSetNotebookAccess(w http.ResponseWriter, r *http.Request) {
	s.adminSetFeatureAccess(notebookAccessSetting)(w, r)
}
