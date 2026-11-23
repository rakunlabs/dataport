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
	// ConnectionID is sent by pages of earlier versions and ignored: a new
	// token reaches what the user's MCP access setting allows. Tokens made
	// before keep the connection they were made for.
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
		"max_ttl_days":  ttlDays(s.mcpCfg().TokenTTL),
		"default_rows":  s.mcpCfg().MaxRows,
		"external_base": s.externalURL(),
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

	ttl := s.mcpCfg().TokenTTL

	const maxTokenDays = 36500

	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxTokenDays {
		writeError(w, http.StatusBadRequest, "expiry must be between 0 and 36500 days")

		return
	}

	// The lifetime asked for, capped by the server's limit, which may be
	// shorter than a day; zero for both means no expiry. The day count is
	// bounded above, so the product cannot overflow into a negative one.
	life := time.Duration(req.ExpiresInDays) * 24 * time.Hour
	if ttl > 0 && (life == 0 || life > ttl) {
		life = ttl
	}

	raw, hash, err := dpauth.NewAccessToken()
	if err != nil {
		writeStoreError(w, err)

		return
	}

	t := &store.AccessToken{UserID: currentUser(r).ID, Name: req.Name, TokenHash: hash, ReadOnly: req.ReadOnly}
	if life > 0 {
		t.ExpiresAt = time.Now().Add(life).UnixMilli()
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

// ttlDays is the token lifetime limit in whole days for the form, rounded up
// so a limit under a day does not read as none.
func ttlDays(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}

	return int((ttl + 24*time.Hour - 1) / (24 * time.Hour))
}
