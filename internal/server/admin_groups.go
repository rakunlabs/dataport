package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

type groupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberIDs   []string `json:"member_ids"`
	// Features nil leaves what the group may use as it is.
	Features *groupFeatures `json:"features"`
}

// groupFeatures are the features limited to some groups that a group is
// one of. They matter while the feature is limited to groups.
type groupFeatures struct {
	Notebooks bool `json:"notebooks"`
	BuildDocs bool `json:"build_docs"`
}

type groupView struct {
	*store.Group
	Features groupFeatures `json:"features"`
}

// featureSettings pairs each group feature with its setting.
var featureSettings = []struct {
	setting string
	get     func(*groupFeatures) *bool
}{
	{notebookAccessSetting, func(f *groupFeatures) *bool { return &f.Notebooks }},
	{docAccessSetting, func(f *groupFeatures) *bool { return &f.BuildDocs }},
}

func (s *Server) groupView(r *http.Request, g *store.Group) groupView {
	v := groupView{Group: g}
	for _, f := range featureSettings {
		*f.get(&v.Features) = slices.Contains(s.featureAccess(r.Context(), f.setting).GroupIDs, g.ID)
	}

	return v
}

func (s *Server) setGroupFeatures(r *http.Request, id string, f *groupFeatures) error {
	if f == nil {
		return nil
	}

	for _, fs := range featureSettings {
		if err := s.setFeatureGroup(r.Context(), fs.setting, id, *fs.get(f)); err != nil {
			return err
		}
	}

	return nil
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

	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, s.groupView(r, g))
	}

	writeJSON(w, http.StatusOK, out)
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

	if err := s.setGroupFeatures(r, g.ID, req.Features); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, s.groupView(r, g))
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

	if err := s.setGroupFeatures(r, g.ID, req.Features); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, s.groupView(r, g))
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteGroup(r.Context(), id); err != nil {
		writeStoreError(w, err)

		return
	}

	// A deleted group keeps no feature.
	_ = s.setGroupFeatures(r, id, &groupFeatures{})

	w.WriteHeader(http.StatusNoContent)
}
