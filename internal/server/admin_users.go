package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@-]{2,64}$`)

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
