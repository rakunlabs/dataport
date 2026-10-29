package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/store"
)

type meResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	// CanCreateConnections is set while users may add their own
	// connections.
	CanCreateConnections bool `json:"can_create_connections"`
	// Subtitle names this instance, as on the sign-in page.
	Subtitle string `json:"subtitle,omitempty"`
	// CanUseNotebooks is set when notebooks are open to the user.
	CanUseNotebooks bool `json:"can_use_notebooks"`
	// CanBuildDocs is set when the user may build and refresh schema
	// documentation; others only read it.
	CanBuildDocs bool `json:"can_build_docs"`
}

func (s *Server) toMe(ctx context.Context, u *store.User) meResponse {
	return meResponse{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
		CanCreateConnections: s.connectionsCfg().UserCreate, Subtitle: s.loginPage(ctx).Subtitle,
		CanUseNotebooks: s.notebooksAllowed(ctx, u), CanBuildDocs: s.docsBuildAllowed(ctx, u)}
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
	writeJSON(w, http.StatusOK, s.toMe(r.Context(), user))
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
	writeJSON(w, http.StatusOK, s.toMe(r.Context(), currentUser(r)))
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

	if err := s.store.DeleteUserSessions(r.Context(), user.ID); err != nil {
		writeStoreError(w, err)

		return
	}

	token, err := s.auth.CreateSession(r.Context(), user.ID, clientIP(r), r.UserAgent(), remember)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	s.setCookie(w, r, token, remember)
	w.WriteHeader(http.StatusNoContent)
}
