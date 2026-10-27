package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return b.next.RoundTrip(r)
}

// mcpEnv is a server with two users and three SQLite connections on the
// same file: alice has shop and shop-prod, bob has hidden.
type mcpEnv struct {
	st         *store.Store
	srv        *Server
	httpSrv    *httptest.Server
	alice, bob *store.User
}

func newMCPEnv(t *testing.T, basePath string) *mcpEnv {
	t.Helper()

	ctx := context.Background()
	files := t.TempDir()

	seed, err := sql.Open("sqlite", filepath.Join(files, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := seed.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers(id), code TEXT, product_id INTEGER);
CREATE UNIQUE INDEX orders_code ON orders(code);
CREATE TRIGGER orders_audit AFTER INSERT ON orders BEGIN SELECT 1; END;
INSERT INTO customers VALUES (1, 'Ada'), (2, '` + strings.Repeat("x", 50) + `');`); err != nil {
		t.Fatal(err)
	}

	_ = seed.Close()

	if err := engine.ConfigureFiles(files); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.ConfigureFiles("") })

	st, err := store.New(ctx, config.Store{Type: "sqlite", TablePrefix: "dataport_", DSN: "file:" + filepath.Join(t.TempDir(), "t.db")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	en := engine.NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	t.Cleanup(en.Close)

	ci, _ := crypto.New("k")
	au := auth.New(st)

	cfg := &config.Config{
		Server: config.Server{BasePath: basePath},
		Query:  config.Query{DefaultMaxRows: 100, DefaultTimeout: 10 * time.Second, MaxRowsLimit: 1000},
		MCP:    config.MCP{Enabled: true, MaxRows: 50, MaxCellChars: 10, MaxSchemaTables: 10},
	}

	srv, err := New(cfg, st, au, ci, en, nil)
	if err != nil {
		t.Fatal(err)
	}

	hash, _ := auth.HashPassword("x-password")

	alice := &store.User{Username: "alice", PasswordHash: hash, Role: store.RoleUser}
	bob := &store.User{Username: "bob", PasswordHash: hash, Role: store.RoleUser}

	for _, u := range []*store.User{alice, bob} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []*store.Connection{
		{Name: "shop", DBType: "sqlite", Database: "shop.db", Access: []store.Access{{SubjectType: "user", SubjectID: alice.ID}}},
		{Name: "shop-prod", DBType: "sqlite", Database: "shop.db", Production: true, Access: []store.Access{{SubjectType: "user", SubjectID: alice.ID}}},
		{Name: "hidden", DBType: "sqlite", Database: "shop.db", Access: []store.Access{{SubjectType: "user", SubjectID: bob.ID}}},
	} {
		if err := st.CreateConnection(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	httpSrv := httptest.NewServer(srv.mux)
	// Sessions keep an SSE stream open; Cleanup closes them first.
	t.Cleanup(func() {
		httpSrv.CloseClientConnections()
		httpSrv.Close()
	})

	return &mcpEnv{st: st, srv: srv, httpSrv: httpSrv, alice: alice, bob: bob}
}

func TestMCP(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, httpSrv, alice := env.st, env.httpSrv, env.alice

	newToken := func(u *store.User, readOnly bool) string {
		raw, h, err := auth.NewAccessToken()
		if err != nil {
			t.Fatal(err)
		}

		if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: "t", TokenHash: h, ReadOnly: readOnly}); err != nil {
			t.Fatal(err)
		}

		return raw
	}

	connect := func(token string, elicit func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{ElicitationHandler: elicit})

		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
			MaxRetries: -1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = session.Close() })

		return session
	}

	call := func(s *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
		t.Helper()

		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		var out map[string]any
		if res.StructuredContent != nil {
			data, _ := json.Marshal(res.StructuredContent)
			_ = json.Unmarshal(data, &out)
		}

		return res, out
	}

	errText := func(res *mcp.CallToolResult) string {
		if !res.IsError || len(res.Content) == 0 {
			return ""
		}

		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			return tc.Text
		}

		return "error"
	}

	// Without a token the endpoint is closed.
	if resp, err := http.Post(httpSrv.URL+"/mcp", "application/json", strings.NewReader("{}")); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %v %v", resp.StatusCode, err)
	}

	rw := connect(newToken(alice, false), nil)

	tools, err := rw.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}

	if !names["dataport_query"] || !names["dataport_execute"] || !names["dataport_schema_context"] {
		t.Fatalf("tools: %v", names)
	}

	_, out := call(rw, "dataport_list_connections", nil)
	if conns := out["connections"].([]any); len(conns) != 2 {
		t.Fatalf("alice sees %v, want only her two connections", conns)
	}

	if res, _ := call(rw, "dataport_list_tables", map[string]any{"connection": "hidden"}); !strings.Contains(errText(res), "unknown connection") {
		t.Fatalf("another user's connection: %v", errText(res))
	}

	_, out = call(rw, "dataport_schema_context", map[string]any{"connection": "shop"})
	if !strings.Contains(out["compact"].(string), "customers(id INTEGER pk, name TEXT not null)") {
		t.Fatalf("schema context: %v", out["compact"])
	}

	// table_docs tells code what a table holds and how it joins.
	res, out := call(rw, "dataport_table_docs", map[string]any{"connection": "shop", "tables": []string{"ORDERS", "nope"}})
	doc := errText(res)
	if doc == "" {
		doc, _ = out["doc"].(string)
	}

	for _, want := range []string{
		"# orders", "customer_id INTEGER NOT NULL -> customers.id", `references: JOIN "customers" ON "orders"."customer_id" = "customers"."id"`,
		"unique keys (upsert targets): (code)", "trigger orders_audit", "not found: nope", "# related tables\ncustomers(id INTEGER PK, name TEXT)",
		"design notes: unindexed_foreign_key(customer_id)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("table docs lack %q:\n%s", want, doc)
		}
	}

	_, out = call(rw, "dataport_schema_report", map[string]any{"connection": "shop"})
	if r := out["report"].(string); !strings.Contains(r, "customers, 2 columns") || !strings.Contains(r, "referenced by orders") {
		t.Errorf("schema report:\n%s", r)
	}

	_, out = call(rw, "dataport_query", map[string]any{"connection": "shop", "sql": "select id, name from customers order by id"})
	first := out["results"].([]any)[0].(map[string]any)

	if first["row_count"].(float64) != 2 || first["cells_truncated"].(float64) != 1 {
		t.Fatalf("query: %v", first)
	}

	// The query tool never writes, whatever the grant.
	if res, _ := call(rw, "dataport_query", map[string]any{"connection": "shop", "sql": "delete from customers"}); !res.IsError {
		t.Fatal("query tool ran a delete")
	}

	_, out = call(rw, "dataport_execute", map[string]any{"connection": "shop", "sql": "update customers set name = 'Ada L.' where id = 1"})
	if out["committed"] != true {
		t.Fatalf("execute: %v", out)
	}

	// Generated rows: a preview writes nothing, then rows go in with the
	// foreign key taken from customers.
	_, out = call(rw, "dataport_generate_rows", map[string]any{"connection": "shop", "table": "orders", "rows": 0, "seed": 3})
	if out["inserted"].(float64) != 0 || len(out["sample"].([]any)) == 0 {
		t.Fatalf("generate preview: %v", out)
	}

	_, out = call(rw, "dataport_generate_rows", map[string]any{
		"connection": "shop", "table": "orders", "rows": 40,
		"columns": []map[string]any{{"name": "code", "generator": "template", "template": "ORD-{{ .n }}"}},
	})
	if out["inserted"].(float64) != 40 {
		t.Fatalf("generate: %v", out)
	}

	_, out = call(rw, "dataport_query", map[string]any{"connection": "shop", "sql": "select count(*), sum(customer_id not in (1, 2)), max(code) from orders"})
	if row := out["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any); row[0].(float64) != 40 || row[1].(float64) != 0 || row[2] != "ORD-9" {
		t.Fatalf("generated rows: %v", row)
	}

	if res, _ := call(rw, "dataport_generate_rows", map[string]any{"connection": "shop-prod", "table": "orders", "rows": 1}); !strings.Contains(errText(res), "confirmation") {
		t.Fatalf("generate on production without elicitation: %v", errText(res))
	}

	// Production writes need the user's confirmation through the client.
	if res, _ := call(rw, "dataport_execute", map[string]any{"connection": "shop-prod", "sql": "delete from customers where id = 2"}); !strings.Contains(errText(res), "confirmation") {
		t.Fatalf("production without elicitation: %v", errText(res))
	}

	asked := ""
	confirming := connect(newToken(alice, false), func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = req.Params.Message

		return &mcp.ElicitResult{Action: "decline"}, nil
	})

	if res, _ := call(confirming, "dataport_execute", map[string]any{"connection": "shop-prod", "sql": "delete from customers where id = 2"}); !strings.Contains(errText(res), "did not confirm") ||
		!strings.Contains(asked, "delete from customers where id = 2") {
		t.Fatalf("declined: %q, asked %q", errText(res), asked)
	}

	// A read-only token has no write tool and cannot write through query.
	ro := connect(newToken(alice, true), nil)

	tools, err = ro.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range tools.Tools {
		if tool.Name == "dataport_execute" || tool.Name == "dataport_generate_rows" {
			t.Fatal("read-only token has a write tool: " + tool.Name)
		}
	}

	_, out = call(ro, "dataport_list_connections", nil)
	for _, c := range out["connections"].([]any) {
		if c.(map[string]any)["writable"] == true {
			t.Fatal("read-only token reports a writable connection")
		}
	}

	logs, err := st.ListQueryLog(ctx, store.QueryLogFilter{UserID: alice.ID, Source: "mcp"})
	if err != nil || len(logs) < 3 {
		t.Fatalf("mcp statements are not logged: %v %d", err, len(logs))
	}
}
