package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// The user's MCP policy decides per connection what agents may do, for
// every token at once and from the next call on.
func TestMCPAccessPolicy(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, alice := env.st, env.alice

	raw, h, err := auth.NewAccessToken()
	if err != nil {
		t.Fatal(err)
	}

	if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: alice.ID, Name: "t", TokenHash: h}); err != nil {
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   env.httpSrv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: raw, next: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	call := func(name string, args map[string]any) (string, map[string]any) {
		t.Helper()

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		var out map[string]any
		if res.StructuredContent != nil {
			data, _ := json.Marshal(res.StructuredContent)
			_ = json.Unmarshal(data, &out)
		}

		if res.IsError && len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				return tc.Text, out
			}
		}

		return "", out
	}

	connections := func() map[string]string {
		t.Helper()

		_, out := call("dataport_list_connections", nil)
		got := map[string]string{}

		for _, c := range out["connections"].([]any) {
			m := c.(map[string]any)
			got[m["name"].(string)] = m["access"].(string)
		}

		return got
	}

	shop := mustConnection(t, st, alice, "shop")
	prod := mustConnection(t, st, alice, "shop-prod")

	// The test environment allows everything for alice.
	if got := connections(); got["shop"] != "ddl" || got["shop-prod"] != "ddl" {
		t.Fatalf("allowed: %v", got)
	}

	setPolicy := func(def store.MCPMode, modes map[string]store.MCPMode) {
		t.Helper()

		if err := st.SetMCPPolicy(ctx, alice.ID, &store.MCPPolicy{DefaultMode: def, Connections: modes}); err != nil {
			t.Fatal(err)
		}
	}

	// shop is read-only for agents and shop-prod is not offered at all.
	setPolicy(store.MCPOff, map[string]store.MCPMode{shop.ID: store.MCPRead})

	if got := connections(); len(got) != 1 || got["shop"] != "read" {
		t.Fatalf("read policy: %v", got)
	}

	if msg, _ := call("dataport_list_tables", map[string]any{"connection": "shop-prod"}); !strings.Contains(msg, "not available to agents") {
		t.Fatalf("closed connection: %q", msg)
	}

	if msg, _ := call("dataport_query", map[string]any{"connection": "shop", "sql": "SELECT count(*) FROM customers"}); msg != "" {
		t.Fatalf("read on read: %q", msg)
	}

	if msg, _ := call("dataport_execute", map[string]any{"connection": "shop", "sql": "INSERT INTO customers (name) VALUES ('x')"}); !strings.Contains(msg, "only read") {
		t.Fatalf("write on read: %q", msg)
	}

	// write allows changing data but not the schema.
	setPolicy(store.MCPOff, map[string]store.MCPMode{shop.ID: store.MCPWrite})

	if msg, _ := call("dataport_execute", map[string]any{"connection": "shop", "sql": "INSERT INTO customers (name) VALUES ('x')"}); msg != "" {
		t.Fatalf("write on write: %q", msg)
	}

	if msg, _ := call("dataport_execute", map[string]any{"connection": "shop", "sql": "CREATE TABLE t1 (id INTEGER)"}); !strings.Contains(msg, "not the schema") {
		t.Fatalf("ddl on write: %q", msg)
	}

	// ddl allows both.
	setPolicy(store.MCPOff, map[string]store.MCPMode{shop.ID: store.MCPDDL})

	if msg, _ := call("dataport_execute", map[string]any{"connection": "shop", "sql": "CREATE TABLE t1 (id INTEGER)"}); msg != "" {
		t.Fatalf("ddl on ddl: %q", msg)
	}

	// Connections without their own mode, such as newly shared ones, get
	// the default.
	setPolicy(store.MCPRead, map[string]store.MCPMode{})

	if got := connections(); got["shop"] != "read" || got["shop-prod"] != "read" {
		t.Fatalf("default mode: %v", got)
	}

	// A notebook an agent runs follows the policy too.
	setPolicy(store.MCPOff, map[string]store.MCPMode{shop.ID: store.MCPRead, prod.ID: store.MCPOff})

	_, saved := call("dataport_save_notebook", map[string]any{
		"name": "nb", "path": "nb",
		"cells": []map[string]any{{"path": "w", "connection": "shop", "sql": "INSERT INTO customers (name) VALUES ('nb')", "write": true}},
	})
	if saved == nil {
		t.Fatal("notebook not saved")
	}

	if msg, out := call("dataport_run_notebook", map[string]any{"notebook": "nb"}); !strings.Contains(msg+mustJSON(out), "only read") {
		t.Fatalf("notebook write on read: %q %v", msg, out)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)

	return string(b)
}

func TestMCPAccessSettings(t *testing.T) {
	env := newMCPEnv(t, "")

	resp, err := http.Post(env.httpSrv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"x-password"}`))
	if err != nil {
		t.Fatal(err)
	}

	cookies := resp.Cookies()
	_ = resp.Body.Close()

	request := func(method, body string, want int) mcpAccessView {
		t.Helper()

		req, _ := http.NewRequest(method, env.httpSrv.URL+"/api/v1/mcp/access", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		for _, c := range cookies {
			req.AddCookie(c)
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()

		var v mcpAccessView
		_ = json.NewDecoder(res.Body).Decode(&v)

		if res.StatusCode != want {
			t.Fatalf("%s: %d", method, res.StatusCode)
		}

		return v
	}

	v := request("GET", "", http.StatusOK)
	if !v.Saved || len(v.Connections) != 2 {
		t.Fatalf("initial: %+v", v)
	}

	shop := mustConnection(t, env.st, env.alice, "shop")
	hidden := ""

	for _, c := range v.Connections {
		if c.Name == "shop-prod" {
			hidden = c.ID
		}
	}

	v = request("PUT", `{"default_mode":"off","connections":{"`+shop.ID+`":"write","`+hidden+`":"off","not-shared":"ddl"}}`, http.StatusOK)
	if !v.Saved || v.DefaultMode != store.MCPOff {
		t.Fatalf("saved: %+v", v)
	}

	for _, c := range v.Connections {
		want := map[string]store.MCPMode{"shop": store.MCPWrite, "shop-prod": store.MCPOff}[c.Name]
		if c.Mode != want {
			t.Fatalf("%s: %s, want %s", c.Name, c.Mode, want)
		}
	}

	// New connections cannot start with write access.
	request("PUT", `{"default_mode":"write","connections":{}}`, http.StatusBadRequest)
	request("PUT", `{"default_mode":"bogus","connections":{}}`, http.StatusBadRequest)
}

// Without a saved policy agents may only read, whatever the grant.
func TestMCPDefaultReadOnly(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")

	p, err := env.st.GetMCPPolicy(ctx, env.bob.ID)
	if err != nil || p != nil {
		t.Fatalf("bob has a policy: %v %v", p, err)
	}

	grants, err := env.st.ListGrants(ctx, env.bob)
	if err != nil || len(grants) == 0 {
		t.Fatal("bob has no grant", err)
	}

	if g := grants[0]; g.ReadOnly {
		t.Fatal("the test needs a writable grant")
	} else if mode := capMCPMode(p, &mcpCaller{user: env.bob, token: &store.AccessToken{}}, g); mode != store.MCPRead {
		t.Fatalf("mode without a policy: %s", mode)
	}

	view, err := env.srv.mcpAccessView(ctx, env.bob)
	if err != nil {
		t.Fatal(err)
	}

	if view.Saved || view.DefaultMode != store.MCPRead || len(view.Connections) == 0 || view.Connections[0].Mode != store.MCPRead {
		t.Fatalf("view: %+v", view)
	}
}
