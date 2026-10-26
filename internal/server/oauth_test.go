package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// TestMCPOAuth signs an MCP client in the way Claude Code does: it finds
// dataport's OAuth server from the 401, registers itself, the user approves
// it in the browser and the client gets a token. It runs under a base path.
func TestMCPOAuth(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "/dp")
	base := env.httpSrv.URL + "/dp"

	// The browser of alice: signed in to dataport.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := browser.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"x-password"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, resp.StatusCode)
	}

	approveAs := func(readOnly bool, connection string) auth.AuthorizationCodeFetcher {
		return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			// /oauth/authorize sends the browser to the approval page.
			resp, err := browser.Get(args.URL)
			if err != nil {
				return nil, err
			}

			loc := resp.Header.Get("Location")
			if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "/dp/#/oauth/authorize?") {
				t.Fatalf("authorize redirect: %d %q", resp.StatusCode, loc)
			}

			query, _ := url.ParseQuery(strings.TrimPrefix(loc, "/dp/#/oauth/authorize?"))

			// The page asks who wants what, then the user approves.
			info, err := browser.Get(base + "/api/v1/oauth/authorize?" + query.Encode())
			if err != nil {
				return nil, err
			}

			var page map[string]any
			_ = json.NewDecoder(info.Body).Decode(&page)

			if page["client"] != "test-cli" {
				t.Fatalf("approval page: %v", page)
			}

			body := map[string]any{"approve": true, "read_only": readOnly, "connection_id": connection}
			for k := range query {
				body[k] = query.Get(k)
			}

			data, _ := json.Marshal(body)

			resp, err = browser.Post(base+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(data)))
			if err != nil {
				return nil, err
			}

			var out struct {
				Redirect string `json:"redirect"`
			}

			raw, _ := io.ReadAll(resp.Body)
			_ = json.Unmarshal(raw, &out)

			u, err := url.Parse(out.Redirect)
			if err != nil || u.Query().Get("code") == "" {
				t.Fatalf("approve: %s", raw)
			}

			return &auth.AuthorizationResult{Code: u.Query().Get("code"), State: u.Query().Get("state"), Iss: u.Query().Get("iss")}, nil
		}
	}

	connect := func(endpoint string, fetch auth.AuthorizationCodeFetcher) *mcp.ClientSession {
		t.Helper()

		h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
			DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
				Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "test-cli", RedirectURIs: []string{"http://127.0.0.1:9999/callback"}},
			},
			RedirectURL:              "http://127.0.0.1:9999/callback",
			AuthorizationCodeFetcher: fetch,
		})
		if err != nil {
			t.Fatal(err)
		}

		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)

		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, OAuthHandler: h, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatalf("connect %s: %v", endpoint, err)
		}

		t.Cleanup(func() { _ = session.Close() })

		return session
	}

	listed := func(s *mcp.ClientSession) []string {
		t.Helper()

		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "dataport_list_connections"})
		if err != nil {
			t.Fatal(err)
		}

		var out listConnectionsOut

		data, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(data, &out)

		names := []string{}
		for _, c := range out.Connections {
			names = append(names, c.Name)
		}

		return names
	}

	// The whole endpoint: every connection of alice, as alice.
	all := connect(base+"/mcp", approveAs(false, ""))
	if got := listed(all); strings.Join(got, ",") != "shop,shop-prod" {
		t.Fatalf("connections: %v", got)
	}

	// The endpoint of one connection: only that one, no name needed.
	one := connect(base+"/mcp/shop", approveAs(true, ""))
	if got := listed(one); strings.Join(got, ",") != "shop" {
		t.Fatalf("one connection: %v", got)
	}

	res, err := one.CallTool(ctx, &mcp.CallToolParams{Name: "dataport_query", Arguments: map[string]any{"sql": "SELECT count(*) AS n FROM customers"}})
	if err != nil || res.IsError {
		t.Fatalf("query without a connection name: %v %+v", err, res)
	}

	res, _ = one.CallTool(ctx, &mcp.CallToolParams{Name: "dataport_query", Arguments: map[string]any{"connection": "shop-prod", "sql": "SELECT 1"}})
	if !res.IsError {
		t.Fatal("an endpoint for one connection reached another")
	}

	// The token from /mcp/shop is limited to shop and read-only.
	tokens, _ := env.st.ListAccessTokens(ctx, env.alice.ID)

	var scoped *store.AccessToken

	for _, tok := range tokens {
		if tok.ConnectionID != "" {
			scoped = tok
		}
	}

	if scoped == nil || !scoped.ReadOnly || scoped.ClientID == "" {
		t.Fatalf("tokens: %+v", tokens)
	}

	// Bob's connection is out of reach whatever the endpoint.
	resp, err = http.Get(base + "/mcp/hidden")
	if err != nil || resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/dp/.well-known/oauth-protected-resource/mcp/hidden") {
		t.Fatalf("challenge: %v %d %q", err, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// A refresh token gives a new access token once.
	refresh, access := oauthPair(t, env, base, browser)

	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}

	for i, want := range []int{http.StatusOK, http.StatusBadRequest} {
		resp, err := http.PostForm(base+"/oauth/token", form)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("refresh %d: %v %d", i, err, resp.StatusCode)
		}
	}

	if _, _, err := env.srv.auth.AuthenticateToken(ctx, access); err == nil {
		t.Fatal("the old access token works after a refresh")
	}
}

// oauthPair runs the code flow by hand and returns the refresh and access
// token.
func oauthPair(t *testing.T, env *mcpEnv, base string, browser *http.Client) (string, string) {
	t.Helper()

	resp, err := http.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"test-cli","redirect_uris":["http://localhost:1/cb"]}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %v %d", err, resp.StatusCode)
	}

	var client map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&client)

	verifier := strings.Repeat("v", 50)
	params := map[string]any{
		"client_id": client["client_id"], "redirect_uri": "http://localhost:1/cb", "response_type": "code", "state": "s",
		"code_challenge": pkceChallenge(verifier), "code_challenge_method": "S256", "approve": true,
	}

	data, _ := json.Marshal(params)

	resp, err = browser.Post(base+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}

	var out struct {
		Redirect string `json:"redirect"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&out)

	u, _ := url.Parse(out.Redirect)
	if u.Query().Get("state") != "s" {
		t.Fatalf("redirect: %s", out.Redirect)
	}

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {client["client_id"].(string)},
		"redirect_uri": {"http://localhost:1/cb"}, "code_verifier": {verifier},
	}

	resp, err = http.PostForm(base+"/oauth/token", form)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %v %d", err, resp.StatusCode)
	}

	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)

	// The code works once.
	if resp, _ := http.PostForm(base+"/oauth/token", form); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("code reused: %d", resp.StatusCode)
	}

	if !strings.HasPrefix(tok["access_token"].(string), dpauth.TokenPrefix) {
		t.Fatalf("token: %v", tok)
	}

	return tok["refresh_token"].(string), tok["access_token"].(string)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// "Remember me" gives the session cookie a lifetime; without it the cookie
// ends with the browser. A remembered MCP client is approved without asking.
func TestRememberMe(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	base := env.httpSrv.URL

	login := func(remember bool) *http.Cookie {
		t.Helper()

		body := `{"username":"alice","password":"x-password","remember":` + map[bool]string{true: "true", false: "false"}[remember] + `}`

		resp, err := http.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(body))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("login: %v %d", err, resp.StatusCode)
		}

		for _, c := range resp.Cookies() {
			if c.Name == cookieName {
				return c
			}
		}

		t.Fatal("no session cookie")

		return nil
	}

	if c := login(false); c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("session cookie outlives the browser: %+v", c)
	}

	c := login(true)
	if c.MaxAge < 29*24*3600 {
		t.Fatalf("remembered cookie: max-age %d", c.MaxAge)
	}

	if _, sess, err := env.srv.auth.AuthenticateSession(ctx, c.Value); err != nil || !sess.Remember ||
		time.Until(time.UnixMilli(sess.ExpiresAt)) < 29*24*time.Hour {
		t.Fatalf("remembered session: %v %+v", err, sess)
	}

	// The browser of alice, signed in.
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(base)
	jar.SetCookies(u, []*http.Cookie{c})
	browser := &http.Client{Jar: jar}

	resp, err := http.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"cli","redirect_uris":["http://localhost:1/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}

	var client map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&client)

	q := url.Values{
		"client_id": {client["client_id"].(string)}, "redirect_uri": {"http://localhost:1/cb"}, "response_type": {"code"},
		"code_challenge": {pkceChallenge(strings.Repeat("v", 50))}, "code_challenge_method": {"S256"},
	}

	info := func() map[string]any {
		resp, err := browser.Get(base + "/api/v1/oauth/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}

		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)

		return out
	}

	if out := info(); out["remembered"] != nil {
		t.Fatalf("approved before the user said so: %v", out)
	}

	body := map[string]any{"approve": true, "remember": true, "read_only": true}
	for k := range q {
		body[k] = q.Get(k)
	}

	data, _ := json.Marshal(body)
	if resp, err := browser.Post(base+"/api/v1/oauth/authorize", "application/json", strings.NewReader(string(data))); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %v", err)
	}

	out := info()
	if out["remembered"] != true || !strings.Contains(out["redirect"].(string), "code=") {
		t.Fatalf("remembered client asked again: %v", out)
	}

	// Forgetting the approval asks again.
	if err := env.st.DeleteOAuthConsent(ctx, env.alice.ID, client["client_id"].(string)); err != nil {
		t.Fatal(err)
	}

	if out := info(); out["remembered"] != nil {
		t.Fatalf("forgotten client approved: %v", out)
	}
}
