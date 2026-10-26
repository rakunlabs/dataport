package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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
// configured external URL, or the host the request came to.
func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.Auth.ExternalURL != "" {
		return strings.TrimRight(s.cfg.Auth.ExternalURL, "/") + s.cfg.Server.BasePath
	}

	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	host, _, _ = strings.Cut(host, ",")

	return scheme + "://" + strings.TrimSpace(host) + s.cfg.Server.BasePath
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

	s.mux.Handle(base+"/oauth/register", withCORS(limitBody(64<<10)(http.HandlerFunc(s.oauthRegister))))
	s.mux.Handle(base+"/oauth/token", withCORS(limitBody(64<<10)(http.HandlerFunc(s.oauthToken))))
	s.mux.Handle(base+"/oauth/authorize", http.HandlerFunc(s.oauthAuthorizeRedirect))
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

// ////////////////////////////////////////////
// authorization

// oauthAuthorizeRedirect sends the browser to the approval page of the app,
// which signs the user in first when needed.
func (s *Server) oauthAuthorizeRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.cookiePath()+"#/oauth/authorize?"+r.URL.RawQuery, http.StatusFound)
}

type authorizeParams struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
	Scope               string `json:"scope"`
}

// authorizeRequest is a checked authorization request.
type authorizeRequest struct {
	params authorizeParams
	client *store.OAuthClient
	// connection is the one connection the client asked for through its
	// resource (an endpoint /mcp/<name>); nil for all of them.
	connection *store.Grant
}

// errRedirect is an error the client hears about through its redirect URI;
// others are shown to the user, as the redirect cannot be trusted.
type errRedirect struct{ code, desc string }

func (e *errRedirect) Error() string { return e.desc }

func (s *Server) checkAuthorize(r *http.Request, user *store.User, p authorizeParams) (*authorizeRequest, error) {
	client, err := s.store.GetOAuthClient(r.Context(), p.ClientID)
	if err != nil {
		return nil, errors.New("unknown client; register it again")
	}

	if !slices.Contains(client.RedirectURIs, p.RedirectURI) {
		return nil, errors.New("the redirect URI was not registered by this client")
	}

	out := &authorizeRequest{params: p, client: client}

	switch {
	case p.ResponseType != "code":
		return out, &errRedirect{"unsupported_response_type", "only the code flow is supported"}
	case p.CodeChallenge == "" || p.CodeChallengeMethod != "S256":
		return out, &errRedirect{"invalid_request", "PKCE with S256 is required"}
	}

	if conn := s.resourceConnection(r, p.Resource); conn != "" {
		g, err := s.grantByName(r, user, conn)
		if err != nil {
			return out, &errRedirect{"invalid_target", "you have no access to connection " + conn}
		}

		out.connection = g
	}

	return out, nil
}

// resourceConnection returns the connection named by a resource URL such
// as https://host/base/mcp/shop; empty for the whole /mcp endpoint.
func (s *Server) resourceConnection(r *http.Request, resource string) string {
	if resource == "" {
		return ""
	}

	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}

	rest, ok := strings.CutPrefix(u.Path, s.cfg.Server.BasePath+"/mcp/")
	if !ok {
		return ""
	}

	return strings.Trim(rest, "/")
}

// grantByName finds a connection of the user by name or id.
func (s *Server) grantByName(r *http.Request, user *store.User, name string) (*store.Grant, error) {
	grants, err := s.store.ListGrants(r.Context(), user)
	if err != nil {
		return nil, err
	}

	if g := findGrant(grants, name); g != nil {
		return g, nil
	}

	return nil, store.ErrNotFound
}

func findGrant(grants []*store.Grant, name string) *store.Grant {
	for _, g := range grants {
		if g.Connection.Name == name || g.Connection.ID == name {
			return g
		}
	}

	for _, g := range grants {
		if strings.EqualFold(g.Connection.Name, name) {
			return g
		}
	}

	return nil
}

func authorizeParamsOf(q url.Values) authorizeParams {
	return authorizeParams{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"),
		State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource: q.Get("resource"), Scope: q.Get("scope"),
	}
}

// oauthAuthorizeInfo tells the approval page who asks for what.
func (s *Server) oauthAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)

	req, err := s.checkAuthorize(r, user, authorizeParamsOf(r.URL.Query()))

	var redirect *errRedirect

	switch {
	case req == nil:
		writeError(w, http.StatusBadRequest, err.Error())

		return
	case errors.As(err, &redirect):
		writeJSON(w, http.StatusOK, map[string]any{"error": redirect.desc, "redirect": redirectWith(req.params, url.Values{
			"error": {redirect.code}, "error_description": {redirect.desc},
		})})

		return
	}

	// A client the user chose to remember is approved again at once, with
	// the same choices, while they still hold.
	if next := s.rememberedApproval(r, user, req); next != "" {
		writeJSON(w, http.StatusOK, map[string]any{"redirect": next, "remembered": true})

		return
	}

	grants, err := s.store.ListGrants(r.Context(), user)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		conns = append(conns, map[string]any{"id": g.Connection.ID, "name": g.Connection.Name, "db_type": g.Connection.DBType, "writable": !g.ReadOnly})
	}

	out := map[string]any{
		"client":       req.client.Name,
		"redirect_uri": req.params.RedirectURI,
		"connections":  conns,
	}

	if req.connection != nil {
		out["connection"] = req.connection.Connection.ID
	}

	writeJSON(w, http.StatusOK, out)
}

func redirectWith(p authorizeParams, v url.Values) string {
	if p.State != "" {
		v.Set("state", p.State)
	}

	sep := "?"
	if strings.Contains(p.RedirectURI, "?") {
		sep = "&"
	}

	return p.RedirectURI + sep + v.Encode()
}

// oauthApprove answers the approval page: it makes a code for the client,
// or tells it the user said no, and returns where the browser goes next.
func (s *Server) oauthApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		authorizeParams
		Approve      bool   `json:"approve"`
		ConnectionID string `json:"connection_id"`
		ReadOnly     bool   `json:"read_only"`
		// Remember approves this client again without asking.
		Remember bool `json:"remember"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	user := currentUser(r)

	ar, err := s.checkAuthorize(r, user, req.authorizeParams)
	if ar == nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	var redirect *errRedirect
	if errors.As(err, &redirect) {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirectWith(req.authorizeParams, url.Values{
			"error": {redirect.code}, "error_description": {redirect.desc},
		})})

		return
	}

	if !req.Approve {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirectWith(req.authorizeParams, url.Values{
			"error": {"access_denied"}, "error_description": {"the user declined"},
		})})

		return
	}

	// A client that asked for one connection gets only that one.
	connID := req.ConnectionID
	if ar.connection != nil {
		connID = ar.connection.Connection.ID
	}

	if connID != "" {
		if _, err := s.store.GetGrant(r.Context(), user, connID); err != nil {
			writeError(w, http.StatusBadRequest, "you have no access to this connection")

			return
		}
	}

	if req.Remember {
		if err := s.store.SaveOAuthConsent(r.Context(), &store.OAuthConsent{
			UserID: user.ID, ClientID: ar.client.ID, ConnectionID: connID, ReadOnly: req.ReadOnly,
		}); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	next, err := s.issueCode(r, user, ar, connID, req.ReadOnly)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

// issueCode makes an authorization code and returns the redirect that
// hands it to the client.
func (s *Server) issueCode(r *http.Request, user *store.User, ar *authorizeRequest, connID string, readOnly bool) (string, error) {
	code, err := randomSecret("")
	if err != nil {
		return "", err
	}

	if err := s.store.CreateOAuthCode(r.Context(), &store.OAuthCode{
		CodeHash: dpauth.HashSecret(code), ClientID: ar.client.ID, UserID: user.ID, RedirectURI: ar.params.RedirectURI,
		CodeChallenge: ar.params.CodeChallenge, ConnectionID: connID, ReadOnly: readOnly,
		ExpiresAt: time.Now().Add(oauthCodeTTL).UnixMilli(),
	}); err != nil {
		return "", err
	}

	v := url.Values{"code": {code}}
	v.Set("iss", s.publicBase(r))

	return redirectWith(ar.params, v), nil
}

// rememberedApproval approves a request again from a remembered consent.
// It returns "" to ask the user: nothing remembered, another connection
// asked for, or the grant behind it gone.
func (s *Server) rememberedApproval(r *http.Request, user *store.User, ar *authorizeRequest) string {
	c, err := s.store.GetOAuthConsent(r.Context(), user.ID, ar.client.ID)
	if err != nil {
		return ""
	}

	if ar.connection != nil && ar.connection.Connection.ID != c.ConnectionID {
		return ""
	}

	readOnly := c.ReadOnly

	if c.ConnectionID != "" {
		g, err := s.store.GetGrant(r.Context(), user, c.ConnectionID)
		if err != nil {
			return ""
		}

		// A grant narrowed to read-only since is honoured; the token would
		// be refused writes anyway.
		readOnly = readOnly || g.ReadOnly
	}

	redirect, err := s.issueCode(r, user, ar, c.ConnectionID, readOnly)
	if err != nil {
		slog.Error("oauth remembered approval", "error", err)

		return ""
	}

	return redirect
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
// tokens

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
	if ttl := s.cfg.MCP.TokenTTL; ttl > 0 {
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
	old := dpauth.HashSecret(f.Get("refresh_token"))

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
