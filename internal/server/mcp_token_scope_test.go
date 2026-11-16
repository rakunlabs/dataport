package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// A token made by hand reaches what the user's MCP access setting allows,
// live. One made by an earlier version keeps the connection it was made for,
// so no token gains access it did not have.
func TestMCPTokenFollowsAccessSetting(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, alice := env.st, env.alice
	shop := mustConnection(t, st, alice, "shop")

	newToken := func(connectionID string) string {
		raw, h, err := auth.NewAccessToken()
		if err != nil {
			t.Fatal(err)
		}

		if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: alice.ID, Name: "t", TokenHash: h, ConnectionID: connectionID}); err != nil {
			t.Fatal(err)
		}

		return raw
	}

	listed := func(raw string) []string {
		t.Helper()

		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   env.httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: raw, next: http.DefaultTransport}},
			MaxRetries: -1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = session.Close() }()

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "dataport_list_connections"})
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

		slices.Sort(names)

		return names
	}

	both := []string{"shop", "shop-prod"}

	token := newToken("")
	if got := listed(token); !slices.Equal(got, both) {
		t.Fatalf("new token: %v", got)
	}

	if got := listed(newToken(shop.ID)); !slices.Equal(got, []string{"shop"}) {
		t.Fatalf("token of an earlier version: %v", got)
	}

	// Turning a connection off in the setting applies to the same token at
	// once, and turning it on again brings it back.
	if err := st.SetMCPPolicy(ctx, alice.ID, &store.MCPPolicy{DefaultMode: store.MCPRead, Connections: map[string]store.MCPMode{shop.ID: store.MCPOff}}); err != nil {
		t.Fatal(err)
	}

	if got := listed(token); !slices.Equal(got, []string{"shop-prod"}) {
		t.Fatalf("after turning shop off: %v", got)
	}

	if err := st.SetMCPPolicy(ctx, alice.ID, &store.MCPPolicy{DefaultMode: store.MCPRead}); err != nil {
		t.Fatal(err)
	}

	if got := listed(token); !slices.Equal(got, both) {
		t.Fatalf("after turning shop on again: %v", got)
	}

	// The page no longer sends a connection; one sent anyway is ignored.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"x","connection_id":"`+shop.ID+`"}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, alice))
	w := httptest.NewRecorder()
	env.srv.createToken(w, r)

	var created struct {
		Token       string            `json:"token"`
		AccessToken store.AccessToken `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create: %d %v %s", w.Code, err, w.Body.String())
	}

	if created.AccessToken.ConnectionID != "" {
		t.Fatalf("a new token was tied to %q", created.AccessToken.ConnectionID)
	}

	if got := listed(created.Token); !slices.Equal(got, both) {
		t.Fatalf("token from the page: %v", got)
	}
}
