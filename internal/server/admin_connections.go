package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

type connectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DBType      string `json:"db_type"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Database    string `json:"database"`
	Username    string `json:"username"`
	// Password nil keeps the stored password; "" clears it.
	Password   *string           `json:"password"`
	Options    map[string]string `json:"options"`
	Color      string            `json:"color"`
	ReadOnly   bool              `json:"read_only"`
	Production bool              `json:"production"`
	// UserCredentials makes users sign in with their own database account;
	// Username and Password then serve only the connection test.
	UserCredentials bool `json:"user_credentials"`
	// Limits are the pool settings; zero values use the server defaults.
	Limits    store.Limits   `json:"limits"`
	MaxRows   int            `json:"max_rows"`
	TimeoutMS int            `json:"timeout_ms"`
	Access    []store.Access `json:"access"`
}

func (req *connectionRequest) validate() string {
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)

	c := &store.Connection{}
	req.apply(c)

	if err := engine.CheckConnection(c); err != nil {
		return err.Error()
	}

	switch {
	case req.Name == "" || len(req.Name) > 100:
		return "name must be 1-100 characters"
	case req.Port < 0 || req.Port > 65535:
		return "port is out of range"
	case req.MaxRows < 0:
		return "max rows cannot be negative"
	case req.TimeoutMS < 0 || req.TimeoutMS > int(24*time.Hour/time.Millisecond):
		return "timeout is out of range"
	}

	return validateLimits(req.Limits)
}

// Pool limits stay in a range a single pool can hold safely; zero means the
// server default.
const (
	maxPoolOpen     = 100
	maxPoolDuration = 24 * 3600
)

func validateLimits(l store.Limits) string {
	switch {
	case l.MaxOpen < 0 || l.MaxOpen > maxPoolOpen:
		return "max open connections must be 0-100"
	case l.MaxIdle < 0 || l.MaxIdle > maxPoolOpen:
		return "max idle connections must be 0-100"
	case l.MaxLifetimeSec < 0 || l.MaxLifetimeSec > maxPoolDuration,
		l.MaxIdleTimeSec < 0 || l.MaxIdleTimeSec > maxPoolDuration:
		return "connection lifetimes must be at most 24 hours"
	}

	return ""
}

func (req *connectionRequest) apply(c *store.Connection) {
	c.Name = req.Name
	c.Description = req.Description
	c.DBType = req.DBType
	c.Host = req.Host
	c.Port = req.Port
	c.Database = strings.TrimSpace(req.Database)
	c.Username = strings.TrimSpace(req.Username)
	c.Options = req.Options
	c.Color = req.Color
	c.ReadOnly = req.ReadOnly
	c.Production = req.Production
	c.UserCredentials = req.UserCredentials
	c.Limits = req.Limits
	c.MaxRows = req.MaxRows
	c.TimeoutMS = req.TimeoutMS
	c.Access = req.Access

	if c.Access == nil {
		c.Access = []store.Access{}
	}
}

func (s *Server) adminListConnections(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListConnections(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminGetConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, c)
}

func (s *Server) adminCreateConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	c := &store.Connection{}
	req.apply(c)

	if c.UserCredentials {
		c.Username = ""
	}

	if req.Password != nil && !req.UserCredentials {
		enc, err := s.cipher.Encrypt(*req.Password)
		if err != nil {
			writeStoreError(w, err)

			return
		}

		c.PasswordEnc = enc
	}

	if err := s.store.CreateConnection(r.Context(), c); err != nil {
		writeStoreError(w, err)

		return
	}

	c.HasPassword = c.PasswordEnc != ""
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) adminUpdateConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	ctx := r.Context()

	c, err := s.store.GetConnection(ctx, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	wasPerUser := c.UserCredentials
	req.apply(c)

	if req.Password != nil {
		if c.PasswordEnc, err = s.cipher.Encrypt(*req.Password); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	// A shared account is not kept for a per-user connection, so it cannot
	// be used by mistake.
	if c.UserCredentials {
		c.Username, c.PasswordEnc = "", ""
	}

	if err := s.store.UpdateConnection(ctx, c); err != nil {
		writeStoreError(w, err)

		return
	}

	// Users' own accounts are dropped when the connection goes back to a
	// shared account.
	if wasPerUser && !c.UserCredentials {
		if err := s.store.DeleteConnectionCredentials(ctx, c.ID); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	s.engine.Forget(c.ID)

	c.HasPassword = c.PasswordEnc != ""
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) adminDeleteConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.store.DeleteConnection(r.Context(), id); err != nil {
		writeStoreError(w, err)

		return
	}

	s.engine.Forget(id)
	w.WriteHeader(http.StatusNoContent)
}

// adminTestConnection tries the settings in the form. When the request
// carries the id of a saved connection and no password, the stored password
// is used, so an admin can test without typing it again.
func (s *Server) adminTestConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		connectionRequest
		ID string `json:"id"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	c := &store.Connection{}
	req.apply(c)

	password := ""

	switch {
	case req.Password != nil:
		password = *req.Password
	case req.ID != "" && !req.UserCredentials:
		saved, err := s.store.GetConnection(r.Context(), req.ID)
		if err != nil {
			writeStoreError(w, err)

			return
		}

		// The stored password only goes to the server it was saved for.
		if !sameTarget(saved, c) {
			writeError(w, http.StatusBadRequest, "enter the password to test a changed type, host, port or username")

			return
		}

		if password, err = s.cipher.Decrypt(saved.PasswordEnc); err != nil {
			writeError(w, http.StatusInternalServerError, "stored password cannot be read")

			return
		}
	}

	start := time.Now()

	if err := s.engine.Test(r.Context(), c, password); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duration_ms": time.Since(start).Milliseconds()})
}

func sameTarget(a, b *store.Connection) bool {
	return a.DBType == b.DBType && strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host)) &&
		a.Port == b.Port && a.Username == b.Username && a.Options["dsn"] == b.Options["dsn"]
}
