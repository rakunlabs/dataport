package server

import (
	"net/http"

	"github.com/rakunlabs/dataport/internal/cluster"
)

// clusterView is the replica overview, with the names of the connections
// and users the pools belong to.
type clusterView struct {
	cluster.Overview

	Connections map[string]clusterConnection `json:"connections"`
	Users       map[string]string            `json:"users"`
}

type clusterConnection struct {
	Name   string `json:"name"`
	DBType string `json:"db_type"`
	Color  string `json:"color"`
}

func (s *Server) adminCluster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var view clusterView
	if s.router != nil {
		view.Overview = s.router.Overview(ctx)
	} else {
		view.Overview = cluster.Standalone(s.local)
	}

	view.Connections = map[string]clusterConnection{}
	view.Users = map[string]string{}

	conns, err := s.store.ListConnections(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	for _, c := range conns {
		view.Connections[c.ID] = clusterConnection{Name: c.Name, DBType: c.DBType, Color: c.Color}
	}

	users, err := s.store.ListUsers(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	for _, u := range users {
		view.Users[u.ID] = u.Username
	}

	writeJSON(w, http.StatusOK, view)
}

// adminReleasePool closes a pool on the replica that holds it, which gives
// up its ownership; the next use opens it again wherever it is needed.
func (s *Server) adminReleasePool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")

		return
	}

	released := false
	if s.router != nil {
		released = s.router.ReleasePool(r.Context(), req.Key)
	} else {
		released = s.local.Release(req.Key)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"released": released})
}
