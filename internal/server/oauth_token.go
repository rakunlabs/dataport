package server

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "use POST")

		return
	}

	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form")

		return
	}

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refreshToken(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
	}
}

func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm

	code, err := s.store.TakeOAuthCode(r.Context(), dpauth.HashSecret(f.Get("code")))
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used or expired code")

		return
	}

	switch {
	case code.ClientID != f.Get("client_id"):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code belongs to another client")

		return
	case code.RedirectURI != f.Get("redirect_uri"):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match")

		return
	case !pkceMatches(f.Get("code_verifier"), code.CodeChallenge):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")

		return
	}

	user, err := s.store.GetUser(r.Context(), code.UserID)
	if err != nil || user.Disabled {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the user cannot sign in")

		return
	}

	client, err := s.store.GetOAuthClient(r.Context(), code.ClientID)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client", "unknown client")

		return
	}

	t := &store.AccessToken{UserID: user.ID, Name: client.Name, ReadOnly: code.ReadOnly, ConnectionID: code.ConnectionID, ClientID: client.ID}

	access, refresh, err := s.newTokenSecrets(t)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "cannot make a token")

		return
	}

	if err := s.store.CreateAccessToken(r.Context(), t); err != nil {
		slog.Error("create oauth token", "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "cannot make a token")

		return
	}

	writeTokenResponse(w, access, refresh)
}

// newTokenSecrets gives a token a new access and refresh token.
func (s *Server) newTokenSecrets(t *store.AccessToken) (string, string, error) {
	access, hash, err := dpauth.NewAccessToken()
	if err != nil {
		return "", "", err
	}

	refresh, err := randomSecret(refreshPrefix)
	if err != nil {
		return "", "", err
	}

	nowTS := time.Now()

	t.TokenHash = hash
	t.ExpiresAt = nowTS.Add(oauthAccessTTL).UnixMilli()
	t.RefreshHash = dpauth.HashSecret(refresh)
	t.RefreshExpiresAt = 0

	// The refresh token lives as long as a token made by hand may.
	if ttl := s.mcpCfg().TokenTTL; ttl > 0 {
		t.RefreshExpiresAt = nowTS.Add(ttl).UnixMilli()
	}

	return access, refresh, nil
}

func writeTokenResponse(w http.ResponseWriter, access, refresh string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         "mcp",
	})
}

func (s *Server) refreshToken(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm

	raw := f.Get("refresh_token")
	if raw == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")

		return
	}

	old := dpauth.HashSecret(raw)

	t, err := s.store.GetAccessTokenByRefresh(r.Context(), old)
	if err != nil || (f.Get("client_id") != "" && f.Get("client_id") != t.ClientID) ||
		(t.RefreshExpiresAt > 0 && t.RefreshExpiresAt < time.Now().UnixMilli()) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or expired refresh token; sign in again")

		return
	}

	user, err := s.store.GetUser(r.Context(), t.UserID)
	if err != nil || user.Disabled {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the user cannot sign in")

		return
	}

	// The refresh token keeps the lifetime of the first sign-in, so a
	// client in use still signs in again at the end of it.
	until := t.RefreshExpiresAt

	access, refresh, err := s.newTokenSecrets(t)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "cannot make a token")

		return
	}

	t.RefreshExpiresAt = until

	// Rotation: the old refresh token stops working. Two refreshes racing
	// with the same one get one token; the other signs in again.
	if err := s.store.RotateAccessToken(r.Context(), t.ID, old, t); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token was already used")

		return
	}

	writeTokenResponse(w, access, refresh)
}
