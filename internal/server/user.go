package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
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
		Remember bool   `json:"remember"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	if s.cfg.Auth.DisablePasswordLogin {
		writeError(w, http.StatusForbidden, "password sign-in is disabled; use single sign-on")

		return
	}

	token, user, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent(), req.Remember)
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

	s.setCookie(w, r, token, req.Remember)
	writeJSON(w, http.StatusOK, toMe(user))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		_ = s.auth.Logout(r.Context(), cookie.Value)
	}

	s.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// about tells signed-in users which dataport build they are using.
func (s *Server) about(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, config.Build)
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

	// Sign out every other session and start a new one here, remembered
	// if this one was.
	remember := false
	if cookie, err := r.Cookie(cookieName); err == nil {
		if _, sess, err := s.auth.AuthenticateSession(r.Context(), cookie.Value); err == nil {
			remember = sess.Remember
		}
	}

	_ = s.store.DeleteUserSessions(r.Context(), user.ID)

	token, err := s.auth.CreateSession(r.Context(), user.ID, clientIP(r), r.UserAgent(), remember)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	s.setCookie(w, r, token, remember)
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

		v.Editable = engine.Editable(v.DBType)
		v.MaxRowsLimit = s.cfg.Query.MaxRowsLimit
		v.MaxTimeoutMS = int(s.cfg.Query.MaxTimeout.Milliseconds())
		v.MaxRows = s.maxRows(g.Connection)
		v.TimeoutMS = int(s.timeout(g.Connection).Milliseconds())
		views = append(views, v)
	}

	writeJSON(w, http.StatusOK, views)
}

// grant loads the connection the request names and checks the user may use
// it. It writes the response and returns nil on failure.
func (s *Server) grant(w http.ResponseWriter, r *http.Request) (*store.Grant, string) {
	g, password, err := s.resolveGrant(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		var credErr *credentialsError

		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "connection not found")
		case errors.As(err, &credErr):
			writeCredentialsRequired(w, credErr.msg)
		case errors.Is(err, errSecret):
			writeError(w, http.StatusInternalServerError, "connection secret cannot be read")
		default:
			writeStoreError(w, err)
		}

		return nil, ""
	}

	return g, password
}

// credentialsError means the user has to sign in to the database with their
// own account first.
type credentialsError struct{ msg string }

func (e *credentialsError) Error() string { return e.msg }

var errSecret = errors.New("connection secret cannot be read")

// resolveGrant returns the user's grant on a connection with the password
// to connect with. For own-account connections the connection carries the
// user's account.
func (s *Server) resolveGrant(ctx context.Context, user *store.User, id string) (*store.Grant, string, error) {
	g, err := s.store.GetGrant(ctx, user, id)
	if err != nil {
		return nil, "", err
	}

	if g.Connection.UserCredentials {
		return s.userCredential(ctx, user, g)
	}

	password, err := s.cipher.Decrypt(g.Connection.PasswordEnc)
	if err != nil {
		slog.Error("decrypt connection password", "connection", g.Connection.ID, "error", err)

		return nil, "", errSecret
	}

	return g, password, nil
}

// userCredential puts the user's own database account into a copy of the
// connection. Without a saved account the request fails with
// credentials_required, and the UI asks for one.
func (s *Server) userCredential(ctx context.Context, user *store.User, g *store.Grant) (*store.Grant, string, error) {
	cred, err := s.store.GetCredential(ctx, g.Connection.ID, user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", &credentialsError{msg: "sign in to this database with your own account"}
		}

		return nil, "", err
	}

	password, err := s.cipher.Decrypt(cred.PasswordEnc)
	if err != nil {
		slog.Error("decrypt user credential", "connection", g.Connection.ID, "user", user.ID, "error", err)

		return nil, "", &credentialsError{msg: "your saved database password cannot be read; enter it again"}
	}

	c := *g.Connection
	c.Username = cred.Username
	c.Owner = user.ID
	c.Limits = c.Limits.Merge(cred.Limits)

	return &store.Grant{Connection: &c, ReadOnly: g.ReadOnly}, password, nil
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

func (s *Server) routines(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	list, err := s.engine.Routines(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) diagram(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	out, err := s.engine.Diagram(r.Context(), g.Connection, password, r.URL.Query().Get("schema"))
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) definition(w http.ResponseWriter, r *http.Request) {
	g, password := s.grant(w, r)
	if g == nil {
		return
	}

	q := r.URL.Query()

	def, err := s.engine.Definition(r.Context(), g.Connection, password, q.Get("schema"), engine.Routine{
		Name: q.Get("name"), Kind: q.Get("kind"), Signature: q.Get("signature"),
	})
	if err != nil {
		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"definition": def})
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
		// MaxRows chooses the rows of this run: 0 keeps the connection's
		// default, -1 asks for as many as the server allows.
		MaxRows int `json:"max_rows"`
		// TimeoutSec chooses the time limit of this run: nil keeps the
		// connection's, 0 asks for none, within the server's cap.
		TimeoutSec *int `json:"timeout_sec"`
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

	result, err := s.execute(r.Context(), currentUser(r), g, password, execInput{
		SQL: req.SQL, QueryID: req.QueryID, Confirm: req.Confirm, Source: "web", Rows: req.MaxRows, TimeoutSec: req.TimeoutSec,
	})
	if err != nil {
		var confirm *confirmationError
		if errors.As(err, &confirm) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"message":               "this is a production database; confirm to run the changes",
				"confirmation_required": true,
				"risk":                  confirm.risk,
			})

			return
		}

		writeEngineError(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

type execInput struct {
	SQL     string
	QueryID string
	// Confirm acknowledges the production prompt for writes.
	Confirm bool
	// ReadOnly narrows the grant, for read-only access tokens and the MCP
	// query tool.
	ReadOnly bool
	// MaxRows lowers the connection's row cap when set.
	MaxRows int
	// Rows replaces the connection's default row count, up to the server
	// ceiling; -1 asks for the ceiling itself.
	Rows int
	// TimeoutSec replaces the connection's time limit when set; 0 is none.
	TimeoutSec *int
	Source     string
}

// confirmationError means a production connection needs the user to confirm
// the changes first.
type confirmationError struct{ risk engine.Risk }

func (e *confirmationError) Error() string {
	return "this is a production database; confirm to run the changes"
}

// execute runs a script on a grant and writes it to the query log. Every
// path that runs user SQL goes through here, so the production check and
// the log cannot be skipped.
func (s *Server) execute(ctx context.Context, user *store.User, g *store.Grant, password string, in execInput) (*engine.ExecResult, error) {
	c := g.Connection
	readOnly := g.ReadOnly || in.ReadOnly

	if c.Production && !readOnly && !in.Confirm {
		if d, err := engine.DriverFor(c); err == nil {
			if risk := engine.Classify(d, in.SQL); risk != engine.RiskRead {
				return nil, &confirmationError{risk: risk}
			}
		}
	}

	maxRows := s.maxRows(c)

	switch {
	case in.Rows < 0:
		maxRows = s.rowCeiling()
	case in.Rows > 0:
		maxRows = min(in.Rows, s.rowCeiling())
	}

	if in.MaxRows > 0 && in.MaxRows < maxRows {
		maxRows = in.MaxRows
	}

	result, err := s.engine.Execute(ctx, c, password, engine.ExecRequest{
		QueryID:  in.QueryID,
		UserID:   user.ID,
		SQL:      in.SQL,
		ReadOnly: readOnly,
		MaxRows:  maxRows,
		Timeout:  s.runTimeout(c, in.TimeoutSec),
	})

	entry := &store.QueryLog{
		UserID:         user.ID,
		Username:       user.Username,
		ConnectionID:   c.ID,
		ConnectionName: c.Name,
		Database:       c.Database,
		// Passwords of CREATE USER and the like stay out of the log.
		SQL:    engine.Redact(in.SQL),
		Status: "ok",
		Source: in.Source,
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

	if logErr := s.store.AddQueryLog(ctx, entry); logErr != nil {
		slog.Error("write query log", "error", logErr)
	}

	return result, err
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

// rowCeiling is the most rows one statement may return to the workspace;
// zero in the configuration means no ceiling.
func (s *Server) rowCeiling() int {
	if limit := s.cfg.Query.MaxRowsLimit; limit > 0 {
		return limit
	}

	return math.MaxInt32
}

// timeout is the time limit of a run on the connection; zero is none.
func (s *Server) timeout(c *store.Connection) time.Duration {
	d := s.cfg.Query.DefaultTimeout
	if c.TimeoutMS > 0 {
		d = time.Duration(c.TimeoutMS) * time.Millisecond
	}

	return s.capTimeout(d)
}

// runTimeout is the time limit the user chose for one run, or the
// connection's when they chose none.
func (s *Server) runTimeout(c *store.Connection, sec *int) time.Duration {
	if sec == nil || *sec < 0 {
		return s.timeout(c)
	}

	return s.capTimeout(time.Duration(*sec) * time.Second)
}

// capTimeout keeps a limit within the server's cap; with a cap, "no limit"
// becomes the cap.
func (s *Server) capTimeout(d time.Duration) time.Duration {
	if max := s.cfg.Query.MaxTimeout; max > 0 && (d <= 0 || d > max) {
		return max
	}

	return d
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
