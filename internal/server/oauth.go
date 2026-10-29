package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// dataport is its own OAuth 2.1 authorization server for MCP clients, as the
// MCP authorization spec asks: a client registers itself (RFC 7591), sends
// the user to the browser to sign in and approve it, and exchanges the code
// (with PKCE) for an access token it renews with a refresh token. The token
// is an access token like the ones made by hand, so it acts as the user.

const (
	oauthCodeTTL   = 5 * time.Minute
	oauthAccessTTL = time.Hour
	// RefreshPrefix marks refresh tokens, as dpt_ marks access tokens.
	refreshPrefix = "dpr_"
)

// publicBase is the URL dataport is reached at, with the base path: the
// configured external URL, or the host the request came to. Behind a proxy
// that strips a path prefix, X-Forwarded-Prefix puts it back.
func (s *Server) publicBase(r *http.Request) string {
	if ext := s.externalURL(); ext != "" {
		return ext + s.cfg.Server.BasePath
	}

	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}

	return scheme + "://" + requestHost(r) + forwardedPrefix(r) + s.cfg.Server.BasePath
}

// publicPath is the path part of publicBase, for redirects within dataport.
func (s *Server) publicPath(r *http.Request) string {
	u, err := url.Parse(s.publicBase(r))
	if err != nil {
		return s.cfg.Server.BasePath
	}

	return strings.TrimRight(u.Path, "/")
}

// forwardedPrefix is the path a proxy removed before passing the request
// on, as Traefik and others report it; only a clean path is taken.
func forwardedPrefix(r *http.Request) string {
	p, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Prefix"), ",")
	p = strings.TrimRight(strings.TrimSpace(p), "/")

	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "?#\\ ") || strings.Contains(p, "..") {
		return ""
	}

	return p
}

// requestHost is the host the client sent the request to: the first entry
// of X-Forwarded-Host behind a proxy, else the Host header.
func requestHost(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	host, _, _ = strings.Cut(host, ",")

	return strings.TrimSpace(host)
}

func (s *Server) registerOAuth() {
	base := s.cfg.Server.BasePath

	// Metadata is public and read by clients from anywhere.
	asm := withCORS(http.HandlerFunc(s.oauthServerMetadata))
	prm := withCORS(http.HandlerFunc(s.protectedResourceMetadata))

	// Clients look for the server metadata at the root with the base path
	// inserted (RFC 8414) or under the base path (OpenID style); dataport
	// answers both, so it works behind a proxy that only forwards the base.
	for _, p := range uniq("/.well-known/oauth-authorization-server"+base, base+"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration"+base, base+"/.well-known/openid-configuration") {
		s.mux.Handle(p, asm)
	}

	for _, p := range uniq("/.well-known/oauth-protected-resource"+base+"/mcp", base+"/.well-known/oauth-protected-resource/mcp") {
		s.mux.Handle(p, prm)
		s.mux.Handle(p+"/{conn}", prm)
	}

	register := withCORS(limitBody(64 << 10)(http.HandlerFunc(s.oauthRegister)))
	token := withCORS(limitBody(64 << 10)(http.HandlerFunc(s.oauthToken)))
	authorize := http.HandlerFunc(s.oauthAuthorizeRedirect)

	// Clients that find no metadata fall back to /register, /token and
	// /authorize at the root of the server (MCP 2025-03-26), so those
	// answer as well as dataport's own paths.
	for _, p := range uniq(base+"/oauth", base, "") {
		s.mux.Handle(p+"/register", register)
		s.mux.Handle(p+"/token", token)
		s.mux.Handle(p+"/authorize", authorize)
	}
}

func uniq(list ...string) []string {
	out := []string{}

	for _, v := range list {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}

	return out
}

// withCORS lets browser-based MCP clients (such as the MCP Inspector) read
// metadata and get tokens. These endpoints use no cookies.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Protocol-Version")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) oauthServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBase(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
		// The redirect carries iss (RFC 9207), so a client can tell this
		// server's codes from another's.
		"authorization_response_iss_parameter_supported": true,
	})
}

// protectedResourceMetadata describes an MCP endpoint (RFC 9728): which
// server issues tokens for it.
func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBase(r)

	resource := base + "/mcp"
	if c := r.PathValue("conn"); c != "" {
		resource += "/" + url.PathEscape(c)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
		"resource_name":            "dataport",
	})
}

// resourceMetadataURL is where a client learns how to sign in to the MCP
// endpoint of the request.
func (s *Server) resourceMetadataURL(r *http.Request) string {
	u := s.publicBase(r) + "/.well-known/oauth-protected-resource/mcp"
	if c := r.PathValue("conn"); c != "" {
		u += "/" + url.PathEscape(c)
	}

	return u
}

// ////////////////////////////////////////////
// registration

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// validRedirect accepts https URIs, http on the loopback interface (CLI
// clients listen there) and app schemes such as cursor://.
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" {
		return false
	}

	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Host != ""
	case "http":
		h := u.Hostname()

		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	case "javascript", "data", "file", "vbscript", "about", "blob":
		return false
	}

	return true
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "use POST")

		return
	}

	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}

	// Clients send more metadata than dataport uses; it is ignored.
	if err := decodeLoose(r, &req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON")

		return
	}

	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "one to ten redirect_uris are required")

		return
	}

	for _, u := range req.RedirectURIs {
		if !validRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, http on localhost or an app scheme")

			return
		}
	}

	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "MCP client"
	}

	if len(name) > 100 {
		name = name[:100]
	}

	c := &store.OAuthClient{Name: name, RedirectURIs: req.RedirectURIs}
	if err := s.store.CreateOAuthClient(r.Context(), c); err != nil {
		slog.Error("register oauth client", "error", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "cannot register the client")

		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_name":                c.Name,
		"client_id_issued_at":        c.CreatedAt / 1000,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// decodeLoose reads JSON that may hold fields dataport does not use.
func decodeLoose(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func randomSecret(prefix string) (string, error) {
	raw, _, err := dpauth.NewAccessToken()
	if err != nil {
		return "", err
	}

	return prefix + strings.TrimPrefix(raw, dpauth.TokenPrefix), nil
}

// ////////////////////////////////////////////
// admin

func (s *Server) adminListOAuthClients(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListOAuthClients(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteOAuthClient(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
