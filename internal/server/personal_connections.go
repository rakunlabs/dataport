package server

import (
	"context"
	"net/http"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Personal connections are the ones users create for themselves when an
// admin allows it. Only their owner sees and uses them; an admin may list
// and delete them. Turned off, they are kept but hidden and refused.

// maxPersonalConnections bounds the connections one user may create.
const maxPersonalConnections = 50

type personalConnectionList struct {
	// Enabled is set while users may create their own connections.
	Enabled bool `json:"enabled"`
	// Files is set when they may also open database files.
	Files       bool                `json:"files"`
	Connections []*store.Connection `json:"connections"`
}

func (s *Server) listPersonalConnections(w http.ResponseWriter, r *http.Request) {
	cfg := s.connectionsCfg()
	out := personalConnectionList{Enabled: cfg.UserCreate, Files: s.personalFiles(), Connections: []*store.Connection{}}

	if cfg.UserCreate {
		list, err := s.store.PersonalConnections(r.Context(), currentUser(r).ID)
		if err != nil {
			writeStoreError(w, err)

			return
		}

		out.Connections = list
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) personalFiles() bool {
	return s.connectionsCfg().UserFiles && engine.FilesEnabled()
}

// personalAllowed answers with 403 while personal connections are off.
func (s *Server) personalAllowed(w http.ResponseWriter) bool {
	if !s.connectionsCfg().UserCreate {
		writeError(w, http.StatusForbidden, "creating your own connections is turned off")

		return false
	}

	return true
}

// checkPersonal refuses what a personal connection may not be, and clears
// the settings only an admin chooses.
func (s *Server) checkPersonal(req *connectionRequest) string {
	if engine.TargetOf(&store.Connection{DBType: req.DBType}) == engine.TargetFile && !s.personalFiles() {
		return "database files are not allowed for your own connections"
	}

	req.UserCredentials = false
	req.Access = nil
	req.Limits = store.Limits{}
	req.HideWorkspace, req.HideNotebooks, req.HideMCP, req.NotebookOnly = false, false, false, false

	return ""
}

// ownConnection loads a personal connection of the user.
func (s *Server) ownConnection(ctx context.Context, user *store.User, id string) (*store.Connection, error) {
	c, err := s.store.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}

	if c.OwnerID == "" || c.OwnerID != user.ID {
		return nil, store.ErrNotFound
	}

	return c, nil
}

func (s *Server) createPersonalConnection(w http.ResponseWriter, r *http.Request) {
	if !s.personalAllowed(w) {
		return
	}

	var req connectionRequest
	if !readJSON(w, r, &req) {
		return
	}

	msg := s.checkPersonal(&req)
	if msg == "" {
		msg = req.validate()
	}

	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	ctx := r.Context()
	user := currentUser(r)

	list, err := s.store.PersonalConnections(ctx, user.ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if len(list) >= maxPersonalConnections {
		writeError(w, http.StatusBadRequest, "you have reached the most connections you may create")

		return
	}

	c := &store.Connection{OwnerID: user.ID}
	req.apply(c)

	if req.Password != nil {
		if c.PasswordEnc, err = s.cipher.Encrypt(*req.Password); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	if err := s.store.CreateConnection(ctx, c); err != nil {
		writeStoreError(w, err)

		return
	}

	c.HasPassword = c.PasswordEnc != ""
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) updatePersonalConnection(w http.ResponseWriter, r *http.Request) {
	if !s.personalAllowed(w) {
		return
	}

	var req connectionRequest
	if !readJSON(w, r, &req) {
		return
	}

	msg := s.checkPersonal(&req)
	if msg == "" {
		msg = req.validate()
	}

	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	ctx := r.Context()

	c, err := s.ownConnection(ctx, currentUser(r), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	req.apply(c)

	if req.Password != nil {
		if c.PasswordEnc, err = s.cipher.Encrypt(*req.Password); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	if err := s.store.UpdateConnection(ctx, c); err != nil {
		writeStoreError(w, err)

		return
	}

	s.engine.Forget(c.ID)

	c.HasPassword = c.PasswordEnc != ""
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deletePersonalConnection(w http.ResponseWriter, r *http.Request) {
	if !s.personalAllowed(w) {
		return
	}

	ctx := r.Context()

	c, err := s.ownConnection(ctx, currentUser(r), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.DeleteConnection(ctx, c.ID); err != nil {
		writeStoreError(w, err)

		return
	}

	s.engine.Forget(c.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testPersonalConnection(w http.ResponseWriter, r *http.Request) {
	if !s.personalAllowed(w) {
		return
	}

	user := currentUser(r)

	s.testConnection(w, r, s.checkPersonal, func(ctx context.Context, id string) (*store.Connection, error) {
		return s.ownConnection(ctx, user, id)
	})
}

// personalDrivers lists the database types a user may pick for their own
// connections, with the pool defaults the form shows.
func (s *Server) personalDrivers(w http.ResponseWriter, _ *http.Request) {
	files := s.personalFiles()
	list := []engine.DriverInfo{}

	for _, d := range engine.Drivers() {
		if d.Target == engine.TargetFile && !files {
			continue
		}

		list = append(list, d)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"drivers":       list,
		"files_enabled": files,
		"odbc_dialects": engine.OdbcDialects(),
	})
}
