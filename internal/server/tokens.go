package server

import (
	"net/http"
	"strings"
	"time"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

type tokenRequest struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
	// ExpiresInDays of zero uses the longest lifetime the server allows.
	ExpiresInDays int `json:"expires_in_days"`
	// ConnectionID limits the token to one connection.
	ConnectionID string `json:"connection_id"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAccessTokens(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tokens":        list,
		"mcp_enabled":   s.mcpCfg().Enabled,
		"mcp_path":      s.cfg.Server.BasePath + "/mcp",
		"max_ttl_days":  int(s.mcpCfg().TokenTTL.Hours() / 24),
		"default_rows":  s.mcpCfg().MaxRows,
		"external_base": strings.TrimRight(s.cfg.Auth.ExternalURL, "/"),
		"base_url":      s.publicBase(r),
	})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if !readJSON(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		writeError(w, http.StatusBadRequest, "name must be 1-100 characters")

		return
	}

	maxDays := int(s.mcpCfg().TokenTTL.Hours() / 24)

	switch {
	case req.ExpiresInDays < 0:
		writeError(w, http.StatusBadRequest, "expiry cannot be negative")

		return
	case maxDays > 0 && (req.ExpiresInDays == 0 || req.ExpiresInDays > maxDays):
		req.ExpiresInDays = maxDays
	}

	raw, hash, err := dpauth.NewAccessToken()
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if req.ConnectionID != "" {
		if _, err := s.store.GetGrant(r.Context(), currentUser(r), req.ConnectionID); err != nil {
			writeError(w, http.StatusBadRequest, "you have no access to this connection")

			return
		}
	}

	t := &store.AccessToken{UserID: currentUser(r).ID, Name: req.Name, TokenHash: hash, ReadOnly: req.ReadOnly, ConnectionID: req.ConnectionID}
	if req.ExpiresInDays > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).UnixMilli()
	}

	if err := s.store.CreateAccessToken(r.Context(), t); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "access_token": t})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)

	// Revoking a signed-in client also forgets its approval, so it has to
	// ask again.
	if t, err := s.store.GetAccessToken(r.Context(), user.ID, r.PathValue("id")); err == nil && t.ClientID != "" {
		_ = s.store.DeleteOAuthConsent(r.Context(), user.ID, t.ClientID)
	}

	if err := s.store.DeleteAccessToken(r.Context(), user.ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
