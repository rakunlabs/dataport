package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

type meResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

func toMe(u *store.User) meResponse {
	return meResponse{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if s.cfg.Auth.DisablePasswordLogin {
		writeError(w, http.StatusForbidden, "password sign-in is disabled; use single sign-on")

		return
	}

	token, user, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrLocked):
			writeError(w, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, err.Error())
		default:
			writeStoreError(w, err)
		}

		return
	}

	s.setCookie(w, r, token)
	writeJSON(w, http.StatusOK, toMe(user))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		_ = s.auth.Logout(r.Context(), cookie.Value)
	}

	s.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toMe(currentUser(r)))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	user := currentUser(r)

	full, err := s.store.GetUserByUsername(r.Context(), user.Username)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if !auth.VerifyPassword(req.Current, full.PasswordHash) {
		writeError(w, http.StatusBadRequest, "current password is wrong")

		return
	}

	if err := auth.ValidatePassword(req.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetUserPassword(r.Context(), user.ID, hash); err != nil {
		writeStoreError(w, err)

		return
	}

	// Sign out every other session and start a new one here.
	_ = s.store.DeleteUserSessions(r.Context(), user.ID)

	token, err := s.auth.CreateSession(r.Context(), user.ID, clientIP(r), r.UserAgent())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	s.setCookie(w, r, token)
	w.WriteHeader(http.StatusNoContent)
}

// ////////////////////////////////////////////
// connections

func (s *Server) listMyConnections(w http.ResponseWriter, r *http.Request) {
	grants, err := s.store.ListGrants(r.Context(), currentUser(r))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	creds, err := s.store.UserCredentials(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	views := make([]store.ConnectionView, 0, len(grants))
	for _, g := range grants {
		v := g.View()
		// Users signing in with their own account see the pool settings they
		// may override, and their own values.
		if v.UserCredentials {
			limits := s.engine.Limits(g.Connection)
			v.Limits = &limits

			if c := creds[v.ID]; c != nil {
				v.CredentialUser = c.Username
				v.MyLimits = &c.Limits
			}
		}

		v.MaxRows = s.maxRows(g.Connection)
		v.TimeoutMS = int(s.timeout(g.Connection).Milliseconds())
		views = append(views, v)
	}

	writeJSON(w, http.StatusOK, views)
}

// grant loads the connection the request names and checks the user may use
// it. It writes the response and returns nil on failure.
func (s *Server) grant(w http.ResponseWriter, r *http.Request) (*store.Grant, string) {
	g, err := s.store.GetGrant(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
		} else {
			writeStoreError(w, err)
		}

		return nil, ""
	}

	if g.Connection.UserCredentials {
		return s.userCredential(w, r, g)
	}

	password, err := s.cipher.Decrypt(g.Connection.PasswordEnc)
	if err != nil {
		slog.Error("decrypt connection password", "connection", g.Connection.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "connection secret cannot be read")

		return nil, ""
	}

	return g, password
}

// userCredential puts the user's own database account into a copy of the
// connection. Without a saved account the request fails with
// credentials_required, and the UI asks for one.
func (s *Server) userCredential(w http.ResponseWriter, r *http.Request, g *store.Grant) (*store.Grant, string) {
	user := currentUser(r)

	cred, err := s.store.GetCredential(r.Context(), g.Connection.ID, user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeCredentialsRequired(w, "sign in to this database with your own account")
		} else {
			writeStoreError(w, err)
		}

		return nil, ""
	}

	password, err := s.cipher.Decrypt(cred.PasswordEnc)
	if err != nil {
		slog.Error("decrypt user credential", "connection", g.Connection.ID, "user", user.ID, "error", err)
		writeCredentialsRequired(w, "your saved database password cannot be read; enter it again")

		return nil, ""
	}

	c := *g.Connection
	c.Username = cred.Username
	c.Owner = user.ID
	c.Limits = c.Limits.Merge(cred.Limits)

	return &store.Grant{Connection: &c, ReadOnly: g.ReadOnly}, password
}

// setCredential saves the user's own account for a connection after a
// successful sign-in to the database, so a typo is caught right away.
func (s *Server) setCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		// Password nil keeps the saved one, so the limits can change alone.
		Password *string      `json:"password"`
		Limits   store.Limits `json:"limits"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Username) > 128 || (req.Password != nil && len(*req.Password) > 1024) {
		writeError(w, http.StatusBadRequest, "username is required")

		return
	}

	if msg := validateLimits(req.Limits); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	ctx := r.Context()
	user := currentUser(r)

	g, err := s.store.GetGrant(ctx, user, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
		} else {
			writeStoreError(w, err)
		}

		return
	}

	if !g.Connection.UserCredentials {
		writeError(w, http.StatusBadRequest, "this connection uses a shared account")

		return
	}

	var password string

	if req.Password != nil {
		password = *req.Password
	} else {
		saved, err := s.store.GetCredential(ctx, g.Connection.ID, user.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "enter your database password")

			return
		}

		if password, err = s.cipher.Decrypt(saved.PasswordEnc); err != nil {
			writeError(w, http.StatusBadRequest, "enter your database password")

			return
		}
	}

	c := *g.Connection
	c.Username = req.Username

	if err := s.engine.Test(ctx, &c, password); err != nil {
		var connErr *engine.ConnError
		if errors.As(err, &connErr) && connErr.Auth {
			writeError(w, http.StatusBadRequest, "the database refused your username or password")

			return
		}

		writeEngineError(w, r, err)

		return
	}

	enc, err := s.cipher.Encrypt(password)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetCredential(ctx, &store.Credential{
		ConnectionID: c.ID, UserID: user.ID, Username: req.Username, PasswordEnc: enc, Limits: req.Limits,
	}); err != nil {
		writeStoreError(w, err)

		return
	}

	s.engine.ForgetUser(c.ID, user.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id := r.PathValue("id")

	if err := s.store.DeleteCredential(r.Context(), id, user.ID); err != nil {
		writeStoreError(w, err)

		return
	}

	s.engine.ForgetUser(id, user.ID)
	w.WriteHeader(http.StatusNoContent)
}

func writeCredentialsRequired(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusPreconditionRequired, map[string]any{"message": msg, "credentials_required": true})
}

func (s *Server) schemas(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Schemas(r.Context(), g.Connection, password)
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) objects(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Objects(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) columns(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()

	list, err := s.engine.Columns(r.Context(), g.Connection, password, q.Get("schema"), q.Get("table"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) structure(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()
	if q.Get("table") == "" {
		writeError(w, http.StatusBadRequest, "table is required")

		return
	}

	out, err := s.engine.Structure(r.Context(), g.Connection, password, q.Get("schema"), q.Get("table"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

const maxSQLLength = 1 << 20

func (s *Server) runQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SQL     string `json:"sql"`
		QueryID string `json:"query_id"`
		// Confirm acknowledges the production prompt for writes.
		Confirm bool `json:"confirm"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.SQL) == "" {
		writeError(w, http.StatusBadRequest, "sql is empty")

		return
	}

	if len(req.SQL) > maxSQLLength {
		writeError(w, http.StatusRequestEntityTooLarge, "sql is too long")

		return
	}

	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	user := currentUser(r)
	c := g.Connection

	if c.Production && !g.ReadOnly && !req.Confirm {
		if d, err := engine.GetDriver(c.DBType); err == nil {
			if risk := engine.Classify(d, req.SQL); risk != engine.RiskRead {
				writeJSON(w, http.StatusConflict, map[string]any{
					"message":               "this is a production database; confirm to run the changes",
					"confirmation_required": true,
					"risk":                  risk,
				})

				return
			}
		}
	}

	result, err := s.engine.Execute(r.Context(), c, password, engine.ExecRequest{
		QueryID:  req.QueryID,
		UserID:   user.ID,
		SQL:      req.SQL,
		ReadOnly: g.ReadOnly,
		MaxRows:  s.maxRows(c),
		Timeout:  s.timeout(c),
	})

	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		Database:       c.Database,
		SQL:            req.SQL,
		Status:         "ok",
	}

	if err != nil {
		entry.Status = "rejected"
		entry.Error = err.Error()

		var connErr *engine.ConnError
		if errors.As(err, &connErr) {
			entry.Status = "unreachable"
		}
	} else {
		entry.RowCount = result.TotalRows()
		entry.DurationMS = result.DurationMS

		if msg := result.FirstError(); msg != "" {
			entry.Status = "error"
			entry.Error = msg
		}
	}

	if logErr := s.store.AddQueryLog(r.Context(), entry); logErr != nil {
		slog.Error("write query log", "error", logErr)
	}

	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// writeEngineError reports an engine failure. A failure to reach the target
// database can name its host, so regular users get a generic message and the
// detail goes to the log.
func writeEngineError(w http.ResponseWriter, r *http.Request, err error) {
	var connErr *engine.ConnError

	switch {
	case errors.As(err, &connErr):
		slog.Warn("target database unreachable", "error", err)

		msg := "cannot reach the database, contact your administrator"
		if u := currentUser(r); u != nil && u.IsAdmin() {
			msg = err.Error()
		}

		// With their own account, a refused sign-in is the user's to fix.
		if connErr.Auth {
			writeCredentialsRequired(w, "the database refused your username or password")

			return
		}

		writeError(w, http.StatusBadGateway, msg)
	case errors.Is(err, engine.ErrReadOnly):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) cancelQuery(w http.ResponseWriter, r *http.Request) {
	if !s.engine.Cancel(currentUser(r).ID, r.PathValue("queryID")) {
		writeError(w, http.StatusNotFound, "query is not running")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) maxRows(c *store.Connection) int {
	n := c.MaxRows
	if n <= 0 {
		n = s.cfg.Query.DefaultMaxRows
	}

	if limit := s.cfg.Query.MaxRowsLimit; limit > 0 && n > limit {
		n = limit
	}

	return n
}

func (s *Server) timeout(c *store.Connection) time.Duration {
	if c.TimeoutMS > 0 {
		return time.Duration(c.TimeoutMS) * time.Millisecond
	}

	return s.cfg.Query.DefaultTimeout
}

// ////////////////////////////////////////////
// history and saved queries

func (s *Server) myHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	list, err := s.store.ListQueryLog(r.Context(), store.QueryLogFilter{
		UserID:       currentUser(r).ID,
		ConnectionID: q.Get("connection_id"),
		Search:       q.Get("search"),
		Before:       parseInt64(q.Get("before")),
		Limit:        int(parseInt64(q.Get("limit"))),
	})
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func parseInt64(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)

	return n
}

type savedRequest struct {
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	SQL          string `json:"sql"`
}

func (req *savedRequest) validate() string {
	req.Name = strings.TrimSpace(req.Name)

	switch {
	case req.Name == "":
		return "name is required"
	case len(req.Name) > 200:
		return "name is too long"
	case len(req.SQL) > maxSQLLength:
		return "sql is too long"
	}

	return ""
}

func (s *Server) listSaved(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSavedQueries(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createSaved(w http.ResponseWriter, r *http.Request) {
	var req savedRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	q := &store.SavedQuery{UserID: currentUser(r).ID, ConnectionID: req.ConnectionID, Name: req.Name, SQL: req.SQL}
	if err := s.store.CreateSavedQuery(r.Context(), q); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) updateSaved(w http.ResponseWriter, r *http.Request) {
	var req savedRequest
	if !readJSON(w, r, &req) {
		return
	}

	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	q := &store.SavedQuery{
		ID: r.PathValue("id"), UserID: currentUser(r).ID,
		ConnectionID: req.ConnectionID, Name: req.Name, SQL: req.SQL,
	}
	if err := s.store.UpdateSavedQuery(r.Context(), q); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, q)
}

func (s *Server) deleteSaved(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSavedQuery(r.Context(), currentUser(r).ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
