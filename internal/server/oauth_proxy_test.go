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

	// The same app on another loopback port joins its first registration,
	// so admins see one client with all its users.
	again := do(http.MethodPost, "/oauth/register", `{"client_name":"Claude Code (dataport)","redirect_uris":["http://localhost:61000/callback"]}`)
	var joined map[string]any
	_ = json.Unmarshal(again.Body.Bytes(), &joined)
	if len(ids) != 1 || !ids[joined["client_id"].(string)] {
		t.Fatalf("the same app registered twice: %v %v", ids, joined)
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
	if err != nil || len(list) != 2 {
		t.Fatalf("clients: %v %+v", err, list)
	}
	// list[1] is Claude Code, list[0] the other.
	if list[0].Name == "Claude Code (dataport)" {
		list[0], list[1] = list[1], list[0]
	}
	if list[1].Name != "Claude Code (dataport)" || list[1].Tokens != 0 {
		t.Fatalf("clients: %+v", list)
	}
	if err := env.st.CreateAccessToken(ctx, &store.AccessToken{UserID: env.alice.ID, Name: "c", TokenHash: "h", ClientID: list[1].ID}); err != nil {
		t.Fatal(err)
	}
	if all, _ := env.st.ListOAuthClients(ctx); !slices.ContainsFunc(all, func(c store.OAuthClientInfo) bool {
		return c.ID == list[1].ID && len(c.UserList) == 1 && c.UserList[0].Username == "alice"
	}) {
		t.Fatalf("client users: %+v", all)
	}
	if err := env.st.DeleteOAuthClient(ctx, list[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetAccessTokenByHash(ctx, "h"); err == nil {
		t.Fatal("a removed client's token still works")
	}
	if n, err := env.st.DeleteUnusedOAuthClients(ctx, list[0].CreatedAt+1); err != nil || n != 1 {
		t.Fatalf("unused: %d %v", n, err)
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
}
