package server

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@-]{2,64}$`)

// poolDefaults tells the connection form what an empty pool field means.
func (s *Server) poolDefaults(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Limits(&store.Connection{}))
}

func (s *Server) drivers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"drivers":       engine.Drivers(),
		"files_enabled": engine.FilesEnabled(),
		"odbc_dialects": engine.OdbcDialects(),
	})
}

// ////////////////////////////////////////////
// users

type userRequest struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email"`
	Password    string   `json:"password"`
	Role        string   `json:"role"`
	Disabled    bool     `json:"disabled"`
	GroupIDs    []string `json:"group_ids"`
}

func (req *userRequest) validate(creating bool) string {
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if req.Email != "" && (len(req.Email) > 254 || !strings.Contains(req.Email, "@")) {
		return "email is not valid"
	}

	if !usernamePattern.MatchString(req.Username) {
		return "username must be 2-64 characters of letters, digits and . _ @ -"
	}

	if req.Role != store.RoleAdmin && req.Role != store.RoleUser {
		return "role must be admin or user"
	}

	if creating || req.Password != "" {
		if err := auth.ValidatePassword(req.Password); err != nil {
			return err.Error()
		}
	}

	return ""
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(true); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	u := &store.User{
		Username: req.Username, DisplayName: req.DisplayName, Email: req.Email, PasswordHash: hash,
		Role: req.Role, Disabled: req.Disabled, GroupIDs: req.GroupIDs,
	}

	if err := s.store.CreateUser(r.Context(), u); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(false); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	existing, err := s.store.GetUser(ctx, id)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	demoting := existing.IsAdmin() && !existing.Disabled && (req.Role != store.RoleAdmin || req.Disabled)
	if demoting {
		if id == currentUser(r).ID {
			writeError(w, http.StatusBadRequest, "you cannot remove your own admin access")

			return
		}

		if n, err := s.store.CountActiveAdmins(ctx); err != nil || n <= 1 {
			writeError(w, http.StatusBadRequest, "at least one active admin is required")

			return
		}
	}

	u := &store.User{
		ID: id, Username: req.Username, DisplayName: req.DisplayName, Email: req.Email,
		Role: req.Role, Disabled: req.Disabled, GroupIDs: req.GroupIDs,
	}

	if req.Password != "" {
		if u.PasswordHash, err = auth.HashPassword(req.Password); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	if u.GroupIDs == nil {
		u.GroupIDs = []string{}
	}

	if err := s.store.UpdateUser(ctx, u); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	if id == currentUser(r).ID {
		writeError(w, http.StatusBadRequest, "you cannot delete yourself")

		return
	}

	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if u.IsAdmin() && !u.Disabled {
		if n, err := s.store.CountActiveAdmins(ctx); err != nil || n <= 1 {
			writeError(w, http.StatusBadRequest, "at least one active admin is required")

			return
		}
	}

	if err := s.store.DeleteUser(ctx, id); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ////////////////////////////////////////////
// groups

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

// ////////////////////////////////////////////
// connections

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

// ////////////////////////////////////////////
// logs

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	list, err := s.store.ListQueryLog(r.Context(), store.QueryLogFilter{
		UserID:       q.Get("user_id"),
		ConnectionID: q.Get("connection_id"),
		Search:       q.Get("search"),
		Source:       q.Get("source"),
		Before:       parseInt64(q.Get("before")),
		Limit:        int(parseInt64(q.Get("limit"))),
	})
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}
