package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Behind a proxy that strips a path prefix and without an external URL,
// the OAuth addresses carry the prefix, and clients that fall back to
// /register at the root still register.
func TestOAuthBehindPrefixProxy(t *testing.T) {
	env := newMCPEnv(t, "")
	h := env.srv.mux

	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Host", "tools.example.com")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Prefix", "/dataport")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		return w
	}

	w := do(http.MethodGet, "/.well-known/oauth-authorization-server", "")
	var meta map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &meta)
	if meta["registration_endpoint"] != "https://tools.example.com/dataport/oauth/register" || meta["issuer"] != "https://tools.example.com/dataport" {
		t.Fatalf("metadata: %d %v", w.Code, meta)
	}
	if _, ok := meta["jwks_uri"]; ok {
		t.Fatalf("oauth metadata has OIDC fields: %v", meta)
	}

	// The OpenID form carries the fields its schema requires.
	w = do(http.MethodGet, "/.well-known/openid-configuration", "")
	meta = nil
	_ = json.Unmarshal(w.Body.Bytes(), &meta)
	if meta["jwks_uri"] != "https://tools.example.com/dataport/oauth/jwks" || meta["subject_types_supported"] == nil || meta["id_token_signing_alg_values_supported"] == nil || meta["token_endpoint"] == nil {
		t.Fatalf("openid metadata: %d %v", w.Code, meta)
	}
	if w := do(http.MethodGet, "/oauth/jwks", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"keys":[]`) {
		t.Fatalf("jwks: %d %s", w.Code, w.Body)
	}

	body := `{"client_name":"Claude Code (dataport)","redirect_uris":["http://localhost:53280/callback"],"token_endpoint_auth_method":"none","application_type":"native"}`
	ids := map[string]bool{}
	for _, path := range []string{"/oauth/register", "/register"} {
		w := do(http.MethodPost, path, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("register at %s: %d %s", path, w.Code, w.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		ids[out["client_id"].(string)] = true
	}

	// Each registration is its own client, also under a name taken before:
	// a program calling itself the same app gets nothing the first was given.
	again := do(http.MethodPost, "/oauth/register", `{"client_name":"Claude Code (dataport)","redirect_uris":["http://localhost:61000/callback"]}`)
	var second map[string]any
	_ = json.Unmarshal(again.Body.Bytes(), &second)
	if ids[second["client_id"].(string)] {
		t.Fatalf("a registration under the same name joined the first: %v %v", ids, second)
	}

	// Another app, or another redirect, is a client of its own.
	if w := do(http.MethodPost, "/register", `{"client_name":"OpenCode","redirect_uris":["http://127.0.0.1:19876/mcp/oauth/callback"]}`); w.Code != http.StatusCreated {
		t.Fatalf("register opencode: %d %s", w.Code, w.Body)
	}
	if !redirectMatches("http://127.0.0.1:1/cb", "http://127.0.0.1:2/cb") || redirectMatches("https://a.example/cb", "https://a.example:444/cb") ||
		redirectMatches("http://127.0.0.1:1/cb", "http://127.0.0.1:1/other") {
		t.Fatal("redirect matching")
	}

	// The approval page is reached through the proxy's prefix.
	w = do(http.MethodGet, "/oauth/authorize?client_id=x&state=s", "")
	if loc := w.Header().Get("Location"); w.Code != http.StatusFound || !strings.HasPrefix(loc, "/dataport/#/oauth/authorize?") {
		t.Fatalf("authorize: %d %q", w.Code, loc)
	}

	// A prefix that is not a clean path is ignored.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Prefix", "//evil.example.com")
	if p := forwardedPrefix(r); p != "" {
		t.Fatalf("prefix %q", p)
	}

	// Admins see the registered clients and can remove one.
	ctx := context.Background()
	list, err := env.st.ListOAuthClients(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("clients: %v %+v", err, list)
	}
	i := slices.IndexFunc(list, func(c store.OAuthClientInfo) bool { return c.Name == "Claude Code (dataport)" })
	claude := list[i]
	if err := env.st.CreateAccessToken(ctx, &store.AccessToken{UserID: env.alice.ID, Name: "c", TokenHash: "h", ClientID: claude.ID}); err != nil {
		t.Fatal(err)
	}
	if all, _ := env.st.ListOAuthClients(ctx); !slices.ContainsFunc(all, func(c store.OAuthClientInfo) bool {
		return c.ID == claude.ID && len(c.UserList) == 1 && c.UserList[0].Username == "alice"
	}) {
		t.Fatalf("client users: %+v", all)
	}

	// Registrations that never got a token go; the one with a token stays.
	if n, err := env.st.DeleteUnusedOAuthClients(ctx, time.Now().UnixMilli()+1); err != nil || n != 3 {
		t.Fatalf("unused: %d %v", n, err)
	}
	if err := env.st.DeleteOAuthClient(ctx, claude.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetAccessTokenByHash(ctx, "h"); err == nil {
		t.Fatal("a removed client's token still works")
	}
}

// A signed-out user who starts an MCP sign-in is asked to sign in, and the
// approval needs the session: the API refuses without one. After signing
// in, a remembered approval goes straight back to the client.
func TestOAuthSignInFirst(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	base := env.httpSrv.URL

	c := &store.OAuthClient{Name: "Claude Code", RedirectURIs: []string{"http://localhost:53280/callback"}}
	if err := env.st.CreateOAuthClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	q := "client_id=" + c.ID + "&redirect_uri=" + url.QueryEscape(c.RedirectURIs[0]) + "&response_type=code&state=s&code_challenge=abc&code_challenge_method=S256"

	// Signed out: the page is the app's, and the API says to sign in.
	resp, err := http.Get(base + "/api/v1/oauth/authorize?" + q)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed out: %v %d", err, resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	resp, err = browser.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"x-password","remember":true}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %d", err, resp.StatusCode)
	}

	page := func() map[string]any {
		resp, err := browser.Get(base + "/api/v1/oauth/authorize?" + q)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("page: %v %d", err, resp.StatusCode)
		}
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}

	// First time: the approval page asks.
	if p := page(); p["client"] != "Claude Code" || p["remembered"] != nil {
		t.Fatalf("consent: %v", p)
	}
	if err := env.st.SaveOAuthConsent(ctx, &store.OAuthConsent{UserID: env.alice.ID, ClientID: c.ID, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	// Remembered: back to the client with a code, no question.
	if p := page(); p["remembered"] != true || !strings.Contains(p["redirect"].(string), "code=") {
		t.Fatalf("remembered: %v", p)
	}

	// Another program registering as the same app is asked.
	resp, err = http.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Claude Code","redirect_uris":["http://localhost:53280/callback"]}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %v %d", err, resp.StatusCode)
	}
	var other map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&other)
	q = strings.Replace(q, "client_id="+c.ID, "client_id="+other["client_id"].(string), 1)
	if p := page(); p["remembered"] != nil || p["client"] != "Claude Code" {
		t.Fatalf("another registration under the name: %v", p)
	}
}

// The resource of an authorization names a connection by the path the
// client was given, with a proxy prefix, and an unknown resource is refused
// rather than read as every connection.
func TestOAuthResourceConnection(t *testing.T) {
	env := newMCPEnv(t, "")

	r := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	r.Header.Set("X-Forwarded-Host", "tools.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Prefix", "/dataport")

	tests := []struct {
		resource string
		conn     string
		ok       bool
	}{
		{"", "", true},
		{"https://tools.example.com/dataport/mcp", "", true},
		{"https://tools.example.com/dataport/mcp/shop", "shop", true},
		{"https://tools.example.com/dataport/mcp/orders%20db/", "orders db", true},
		{"https://tools.example.com/mcp/shop", "shop", true},
		{"https://tools.example.com/elsewhere/shop", "", false},
	}

	for _, tt := range tests {
		if conn, ok := env.srv.resourceConnection(r, tt.resource); conn != tt.conn || ok != tt.ok {
			t.Errorf("resourceConnection(%q) = %q, %v; want %q, %v", tt.resource, conn, ok, tt.conn, tt.ok)
		}
	}
}
