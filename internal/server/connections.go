package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

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
		v.ManualTx = engine.ManualTx(v.DBType) && !v.ReadOnly
		if sup := engine.ExplainSupportFor(v.DBType); sup.Supported {
			v.Explain = sup
		}
		v.MaxRowsLimit = s.queryCfg().MaxRowsLimit
		v.MaxTimeoutMS = int(s.queryCfg().MaxTimeout.Milliseconds())
		v.MaxRows = s.maxRows(g.Connection)
		v.TimeoutMS = int(s.timeout(g.Connection).Milliseconds())
		views = append(views, v)
	}

	writeJSON(w, http.StatusOK, views)
}

// grant loads the connection the request names and checks the user may use
// it outside notebooks. It writes the response and returns nil on failure.
func (s *Server) grant(w http.ResponseWriter, r *http.Request) (*store.Grant, string) {
	g, password, err := s.resolveGrant(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeGrantError(w, err, "connection not found")

		return nil, ""
	}

	return g, password
}

// credentialsError means the user has to sign in to the database with their
// own account first.
type credentialsError struct{ msg string }

func (e *credentialsError) Error() string { return e.msg }

var (
	errSecret       = errors.New("connection secret cannot be read")
	errNotebookOnly = errors.New("this connection can only be used in notebooks")
)

// resolveGrant returns the user's grant on a connection with the password
// to connect with, for any use but a notebook cell: notebook-only
// connections are refused. For own-account connections the connection
// carries the user's account.
func (s *Server) resolveGrant(ctx context.Context, user *store.User, id string) (*store.Grant, string, error) {
	g, password, err := s.resolveNotebookGrant(ctx, user, id)
	if err != nil {
		return nil, "", err
	}

	if g.Connection.NotebookOnly {
		return nil, "", errNotebookOnly
	}

	return g, password, nil
}

// resolveNotebookGrant is resolveGrant for a notebook cell, which may also
// use notebook-only connections.
func (s *Server) resolveNotebookGrant(ctx context.Context, user *store.User, id string) (*store.Grant, string, error) {
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

	return &store.Grant{Connection: &c, ReadOnly: g.ReadOnly, CanWrite: g.CanWrite, SelfReadOnly: g.SelfReadOnly}, password, nil
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
		writeGrantError(w, err, "connection not found")

		return
	}

	if g.Connection.NotebookOnly {
		writeGrantError(w, errNotebookOnly, "")

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

// setSelfReadOnly lets the user use a connection read-only although their
// grants allow writes, or allow writes again. It applies everywhere the
// user acts: the workspace, notebooks and MCP.
func (s *Server) setSelfReadOnly(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReadOnly bool `json:"read_only"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	user := currentUser(r)

	g, err := s.store.GetGrant(r.Context(), user, r.PathValue("id"))
	if err != nil {
		writeGrantError(w, err, "connection not found")

		return
	}

	if !g.CanWrite {
		writeError(w, http.StatusBadRequest, "this connection is read-only for you already")

		return
	}

	if err := s.store.SetSelfReadOnly(r.Context(), user.ID, g.Connection.ID, req.ReadOnly); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"read_only": req.ReadOnly || !g.CanWrite, "self_read_only": req.ReadOnly, "can_write": g.CanWrite})
}
