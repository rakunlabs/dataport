package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestMCPAdminDocExport(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	admin := &store.User{Username: "admin", Role: store.RoleAdmin}
	if err := env.st.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}

	fake := &fakePushTarget{items: map[string]pushItem{}}
	ks := httptest.NewServer(fake)
	t.Cleanup(ks.Close)

	cfg, msg := s.checkDocExport(ctx, docExportIn{
		Enabled: true, URL: ks.URL, Collection: "docs",
		Targets: []docExportTarget{{ConnectionID: k.ConnectionID, UserID: env.alice.ID}},
	}, docExportConfig{})
	if msg != "" {
		t.Fatal(msg)
	}
	raw, _ := json.Marshal(cfg)
	if err := env.st.SetSetting(ctx, docExportSetting, string(raw)); err != nil {
		t.Fatal(err)
	}

	connect := func(u *store.User, readOnly bool, connectionID string) *mcp.ClientSession {
		t.Helper()

		token, h, err := auth.NewAccessToken()
		if err != nil {
			t.Fatal(err)
		}
		if err := env.st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: "t", TokenHash: h, ReadOnly: readOnly, ConnectionID: connectionID}); err != nil {
			t.Fatal(err)
		}

		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   env.httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
			MaxRetries: -1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })

		return session
	}

	adminTools := func(session *mcp.ClientSession) []string {
		t.Helper()

		res, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		var names []string
		for _, tool := range res.Tools {
			if strings.Contains(tool.Name, "docs_export_") || strings.Contains(tool.Name, "lookup_tables") {
				names = append(names, strings.TrimPrefix(tool.Name, mcpToolPrefix))
			}
		}
		slices.Sort(names)

		return names
	}

	call := func(session *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
		t.Helper()

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpToolPrefix + name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			text := "error"
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
			return nil, text
		}

		var out map[string]any
		data, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(data, &out)

		return out, ""
	}

	// Users, read-only admin tokens and tokens of one connection do not get
	// the tools that change settings.
	if got := adminTools(connect(env.alice, false, "")); len(got) != 0 {
		t.Fatalf("a user sees admin tools: %v", got)
	}
	if got := adminTools(connect(admin, false, k.ConnectionID)); len(got) != 0 {
		t.Fatalf("a token of one connection sees admin tools: %v", got)
	}
	if got := adminTools(connect(admin, true, "")); !slices.Equal(got, []string{"docs_export_settings", "docs_lookup_tables"}) {
		t.Fatalf("read-only admin tools: %v", got)
	}

	session := connect(admin, false, "")
	if got := adminTools(session); !slices.Equal(got, []string{"docs_export_push", "docs_export_set_description", "docs_export_set_note", "docs_export_settings", "docs_lookup_tables", "docs_set_lookup_tables"}) {
		t.Fatalf("admin tools: %v", got)
	}

	if _, e := call(session, "docs_export_set_note", map[string]any{"connection": "SHOP", "schema": "main", "note": "Owned by the orders service (shop/orders)."}); e != "" {
		t.Fatal(e)
	}
	if _, e := call(session, "docs_export_set_note", map[string]any{"connection": "shop", "note": "Shop database."}); e != "" {
		t.Fatal(e)
	}
	if _, e := call(session, "docs_export_set_description", map[string]any{"description": "Schemas of the shop databases."}); e != "" {
		t.Fatal(e)
	}

	// Mistakes are refused with the rules of the admin UI.
	if _, e := call(session, "docs_export_set_note", map[string]any{"connection": "hidden", "note": "x"}); !strings.Contains(e, "does not include a connection named hidden") {
		t.Fatalf("unknown connection: %q", e)
	}
	if _, e := call(session, "docs_export_set_note", map[string]any{"connection": "shop", "note": strings.Repeat("x", docExportMaxNote+1)}); !strings.Contains(e, "longer than") {
		t.Fatalf("overlong note: %q", e)
	}

	out, e := call(session, "docs_export_settings", nil)
	if e != "" {
		t.Fatal(e)
	}
	conns, _ := out["connections"].([]any)
	if out["description"] != "Schemas of the shop databases." || out["default_description"] != false || len(conns) != 1 {
		t.Fatalf("settings: %+v", out)
	}
	shop, _ := conns[0].(map[string]any)
	notes, _ := shop["notes"].(map[string]any)
	if shop["connection"] != "shop" || shop["as_user"] != "alice" || notes["main"] != "Owned by the orders service (shop/orders)." || notes[""] != "Shop database." {
		t.Fatalf("connection settings: %+v", shop)
	}

	// The token and the targets survive a change made over MCP.
	saved, _ := s.loadDocExport(ctx)
	if saved.URL != ks.URL || len(saved.Targets) != 1 || saved.Targets[0].UserID != env.alice.ID {
		t.Fatalf("saved: %+v", saved)
	}

	// Tables whose rows the documentation shows.
	if _, e := call(session, "docs_set_lookup_tables", map[string]any{"connection": "Shop", "tables": []string{"main.customers", " "}}); e != "" {
		t.Fatal(e)
	}
	env.srv.notebookWorkers.Wait()
	if _, e := call(session, "docs_set_lookup_tables", map[string]any{"connection": "nope", "tables": []string{"x"}}); !strings.Contains(e, "no connection named nope") {
		t.Fatalf("unknown connection: %q", e)
	}
	if _, e := call(session, "docs_set_lookup_tables", map[string]any{"connection": "shop", "tables": []string{"[bad"}}); !strings.Contains(e, "invalid table pattern") {
		t.Fatalf("bad pattern: %q", e)
	}
	lk, e := call(session, "docs_lookup_tables", map[string]any{"connection": "shop"})
	if e != "" {
		t.Fatal(e)
	}
	lconns, _ := lk["connections"].([]any)
	if len(lconns) != 1 || lconns[0].(map[string]any)["tables"].([]any)[0] != "main.customers" {
		t.Fatalf("lookup tables: %+v", lk)
	}

	// Removing a note.
	if _, e := call(session, "docs_export_set_note", map[string]any{"connection": "shop", "note": ""}); e != "" {
		t.Fatal(e)
	}

	if _, e := call(session, "docs_export_push", nil); e != "" {
		t.Fatal(e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st := s.docExportStatusNow(ctx); !st.Running && st.FinishedAt > 0 {
			if st.Error != "" || len(st.Failed) > 0 {
				t.Fatalf("push: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("push did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	orders := fake.items["dataport/"+k.ConnectionID+"/main/orders"].Content
	if !strings.Contains(orders, "about this schema: Owned by the orders service (shop/orders).\n") || strings.Contains(orders, "Shop database.") {
		t.Fatalf("pushed note:\n%s", orders)
	}
	if customers := fake.items["dataport/"+k.ConnectionID+"/main/customers"].Content; !strings.Contains(customers, "values (id | name):\n  1 | Ada\n") {
		t.Fatalf("chosen rows not pushed:\n%s", customers)
	}
	if fake.description != "Schemas of the shop databases." {
		t.Fatalf("collection description %q", fake.description)
	}
}
