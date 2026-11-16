package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// TestMCPRawHTTP speaks the protocol the way minimal clients do (at's chat
// tools in the browser and in Go): plain POSTs that carry the session ID of
// initialize, plus the CORS preflight a browser sends first.
func TestMCPRawHTTP(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")

	raw, h, err := auth.NewAccessToken()
	if err != nil {
		t.Fatal(err)
	}

	if err := env.st.CreateAccessToken(ctx, &store.AccessToken{UserID: env.alice.ID, Name: "t", TokenHash: h, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}

	url := env.httpSrv.URL + "/mcp"

	pre, _ := http.NewRequest(http.MethodOptions, url, nil)
	pre.Header.Set("Origin", "https://at.example.com")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "content-type, authorization, mcp-session-id, mcp-protocol-version")
	pre.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	if !strings.Contains(strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers")), "mcp-session-id") {
		t.Fatalf("preflight allows %q", resp.Header.Get("Access-Control-Allow-Headers"))
	}

	if resp.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatal("preflight does not allow private network access")
	}

	session := ""
	post := func(body string) (*http.Response, string) {
		t.Helper()

		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+raw)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
			req.Header.Set("MCP-Protocol-Version", "2025-03-26")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		data, _ := io.ReadAll(resp.Body)

		return resp, string(data)
	}

	resp, body := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"at-chats","version":"1"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize: %d %s", resp.StatusCode, body)
	}

	session = resp.Header.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("initialize returned no session ID")
	}

	if !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), "Mcp-Session-Id") {
		t.Fatal("session ID is not exposed to browsers")
	}

	if resp, body := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized: %d %s", resp.StatusCode, body)
	}

	resp, body = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "dataport_list_connections") {
		t.Fatalf("tools/list: %d %s", resp.StatusCode, body)
	}
}
