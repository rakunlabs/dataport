package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestMCPNotebooks(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, alice, bob := env.st, env.alice, env.bob

	token := func(u *store.User, readOnly bool, scope string) string {
		raw, h, err := auth.NewAccessToken()
		if err != nil {
			t.Fatal(err)
		}

		if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: "t", TokenHash: h, ReadOnly: readOnly, ConnectionID: scope}); err != nil {
			t.Fatal(err)
		}

		return raw
	}

	connect := func(raw string, elicit func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{ElicitationHandler: elicit})

		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   env.httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: raw, next: http.DefaultTransport}},
			MaxRetries: -1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = session.Close() })

		return session
	}

	call := func(s *mcp.ClientSession, name string, args map[string]any) (string, map[string]any) {
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

		msg := ""
		if res.IsError && len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				msg = tc.Text
			}

			if msg == "" {
				msg = "error"
			}
		}

		return msg, out
	}

	rw := connect(token(alice, false, ""), nil)

	tools, err := rw.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}

	for _, want := range []string{"list_notebooks", "get_notebook", "run_notebook", "notebook_runs", "cancel_notebook_run", "save_notebook", "publish_notebook", "share_notebook", "delete_notebook"} {
		if !names[mcpToolPrefix+want] {
			t.Errorf("missing tool %s", want)
		}
	}

	// A notebook passing rows from one cell to the next, with mugo.
	cells := []map[string]any{
		{"path": "seed", "connection": "shop", "template": true, "output": false, "sql": "SELECT id FROM customers WHERE name = {{ .data.name | trim | param }}"},
		{"path": "detail", "connection": "shop", "template": true, "dependencies": []string{"seed"}, "sql": "SELECT name FROM customers WHERE id = {{ param (index .cells.seed 0).id }}"},
	}
	params := []map[string]any{{"name": "name", "type": "string", "required": true}}

	msg, out := call(rw, "dataport_save_notebook", map[string]any{"name": "Report", "path": "report", "cells": cells, "parameters": params})
	if msg != "" || out["version"].(float64) != 1 {
		t.Fatalf("create: %s %v", msg, out)
	}

	id := out["id"].(string)

	if msg, _ := call(rw, "dataport_save_notebook", map[string]any{"name": "Dup", "path": "report", "cells": cells}); !strings.Contains(msg, "exists") {
		t.Fatalf("duplicate path: %q", msg)
	}

	if msg, _ := call(rw, "dataport_save_notebook", map[string]any{"name": "Hidden", "path": "hidden", "cells": []map[string]any{{"path": "a", "connection": "hidden", "sql": "SELECT 1"}}}); !strings.Contains(msg, "unknown connection") {
		t.Fatalf("another user's connection: %q", msg)
	}

	_, out = call(rw, "dataport_list_notebooks", nil)
	if list := out["notebooks"].([]any); len(list) != 1 || list[0].(map[string]any)["permission"] != "owner" {
		t.Fatalf("list: %v", out)
	}

	_, out = call(rw, "dataport_get_notebook", map[string]any{"notebook": "report"})
	got := out["cells"].([]any)
	if len(got) != 2 || got[0].(map[string]any)["connection"] != "shop" || got[1].(map[string]any)["dependencies"].([]any)[0] != "seed" {
		t.Fatalf("get: %v", out)
	}

	msg, out = call(rw, "dataport_run_notebook", map[string]any{"notebook": id, "inputs": map[string]any{"name": " Ada "}})
	if msg != "" || out["status"] != "completed" {
		t.Fatalf("run: %s %v", msg, out)
	}

	ran := out["cells"].([]any)
	if len(ran) != 2 || ran[0].(map[string]any)["results"] != nil {
		t.Fatalf("seed output should be hidden: %v", ran)
	}

	rows := ran[1].(map[string]any)["results"].([]any)[0].(map[string]any)["rows"].([]any)
	if rows[0].([]any)[0] != "Ada" {
		t.Fatalf("dependency rows: %v", rows)
	}

	if msg, _ := call(rw, "dataport_run_notebook", map[string]any{"notebook": "report"}); !strings.Contains(msg, "required") {
		t.Fatalf("missing input: %q", msg)
	}

	// Updates need the current version and replace the document.
	update := map[string]any{"notebook": "report", "name": "Report", "path": "report", "cells": cells[:1], "parameters": params}
	if msg, _ := call(rw, "dataport_save_notebook", update); !strings.Contains(msg, "version is required") {
		t.Fatalf("no version: %q", msg)
	}

	update["version"] = 7
	if msg, _ := call(rw, "dataport_save_notebook", update); !strings.Contains(msg, "changed") {
		t.Fatalf("stale version: %q", msg)
	}

	update["version"] = 1
	if msg, out := call(rw, "dataport_save_notebook", update); msg != "" || out["version"].(float64) != 2 {
		t.Fatalf("update: %s %v", msg, out)
	}

	if msg, _ := call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "version": 1, "inputs": map[string]any{"name": "Ada"}}); !strings.Contains(msg, "version 2") {
		t.Fatalf("stale run: %q", msg)
	}

	// Unsafe templates are refused when the notebook runs.
	update["version"] = 2
	update["cells"] = []map[string]any{{"path": "bad", "connection": "shop", "template": true, "sql": "SELECT '{{ .data.name }}'"}}
	if msg, _ := call(rw, "dataport_save_notebook", update); msg != "" {
		t.Fatalf("save: %q", msg)
	}

	if msg, _ := call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "x"}}); !strings.Contains(msg, "param") {
		t.Fatalf("printed value ran: %q", msg)
	}

	// A production write needs the user's confirmation of the exact SQL.
	write := []map[string]any{{"path": "rename", "connection": "shop-prod", "write": true, "template": true, "sql": "UPDATE customers SET name = {{ param .data.name }} WHERE id = 2"}}
	update["version"], update["cells"] = 3, write

	if msg, _ := call(rw, "dataport_save_notebook", update); msg != "" {
		t.Fatalf("save write: %q", msg)
	}

	if msg, _ := call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "Changed"}}); !strings.Contains(msg, "confirmation") {
		t.Fatalf("production write without elicitation: %q", msg)
	}

	msg, out = call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "preview": true, "inputs": map[string]any{"name": "Changed"}})
	if out["cells"] == nil {
		t.Fatalf("preview: %q %v", msg, out)
	}

	if c := out["cells"].([]any)[0].(map[string]any); c["status"] != "preview" || !strings.Contains(c["sql"].(string), "UPDATE customers") {
		t.Fatalf("preview: %v", out)
	}

	asked := ""
	answer := "decline"
	confirming := connect(token(alice, false, ""), func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = req.Params.Message
		if answer == "accept" {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		}

		return &mcp.ElicitResult{Action: answer}, nil
	})

	if msg, _ := call(confirming, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "Changed"}}); !strings.Contains(msg, "did not confirm") ||
		!strings.Contains(asked, "UPDATE customers") || !strings.Contains(asked, `"Changed"`) {
		t.Fatalf("declined: %q asked %q", msg, asked)
	}

	answer = "accept"
	if msg, out := call(confirming, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "Changed"}}); msg != "" || out["status"] != "completed" {
		t.Fatalf("confirmed: %s %v", msg, out)
	}

	g, pw, err := env.srv.resolveGrant(ctx, alice, mustConnection(t, st, alice, "shop").ID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := env.srv.execute(ctx, alice, g, pw, execInput{SQL: "SELECT name FROM customers WHERE id = 2", ReadOnly: true, Source: "test"})
	if err != nil || res.Results[0].Rows[0][0] != "Changed" {
		t.Fatalf("confirmed write did not run: %v %v", res, err)
	}

	// Read-only tokens can read and run read-only cells, nothing more.
	ro := connect(token(alice, true, ""), nil)

	tools, _ = ro.ListTools(ctx, nil)
	for _, tool := range tools.Tools {
		switch tool.Name {
		case mcpToolPrefix + "save_notebook", mcpToolPrefix + "publish_notebook", mcpToolPrefix + "share_notebook", mcpToolPrefix + "delete_notebook":
			t.Fatalf("read-only token has %s", tool.Name)
		}
	}

	if msg, _ := call(ro, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "x"}}); !strings.Contains(msg, "only read") {
		t.Fatalf("read-only token wrote: %q", msg)
	}

	// A token scoped to one connection only sees notebooks within it.
	scoped := connect(token(alice, false, mustConnection(t, st, alice, "shop").ID), nil)
	if _, out := call(scoped, "dataport_list_notebooks", nil); len(out["notebooks"].([]any)) != 0 {
		t.Fatalf("scoped token sees a production notebook: %v", out)
	}

	if msg, _ := call(scoped, "dataport_get_notebook", map[string]any{"notebook": "report"}); !strings.Contains(msg, "unknown notebook") {
		t.Fatalf("scoped token read it: %q", msg)
	}

	// Sharing grants the notebook, never the database.
	bobs := connect(token(bob, false, ""), nil)
	if msg, _ := call(bobs, "dataport_get_notebook", map[string]any{"notebook": "report"}); !strings.Contains(msg, "unknown notebook") {
		t.Fatalf("bob read an unshared notebook: %q", msg)
	}

	if msg, out := call(rw, "dataport_share_notebook", map[string]any{"notebook": "report", "shares": []map[string]any{{"user": "bob", "permission": "run"}}}); msg != "" || len(out["shares"].([]any)) != 1 {
		t.Fatalf("share: %s %v", msg, out)
	}

	if _, out := call(bobs, "dataport_get_notebook", map[string]any{"notebook": "report"}); out["permission"] != "run" || !strings.Contains(out["cells"].([]any)[0].(map[string]any)["connection"].(string), "not shared with you") {
		t.Fatalf("bob view: %v", out)
	}

	if msg, _ := call(bobs, "dataport_run_notebook", map[string]any{"notebook": "report", "inputs": map[string]any{"name": "x"}}); !strings.Contains(msg, "connection unavailable") {
		t.Fatalf("bob used alice's database: %q", msg)
	}

	for tool, args := range map[string]map[string]any{
		"dataport_publish_notebook": {"notebook": "report", "version": 4},
		"dataport_share_notebook":   {"notebook": "report", "shares": []any{}},
		"dataport_delete_notebook":  {"notebook": "report"},
	} {
		if msg, _ := call(bobs, tool, args); !strings.Contains(msg, "needs owner") {
			t.Fatalf("%s as runner: %q", tool, msg)
		}
	}

	if msg, _ := call(bobs, "dataport_save_notebook", map[string]any{"notebook": "report", "version": 4, "name": "x", "path": "report", "cells": write}); !strings.Contains(msg, "needs edit") {
		t.Fatalf("save as runner: %q", msg)
	}

	// Publishing serves a snapshot; a draft saved later does not change it.
	if msg, out := call(rw, "dataport_publish_notebook", map[string]any{"notebook": "report", "version": 4}); msg != "" || out["published_version"].(float64) != 4 || out["endpoint"] != "/api/v1/run/report" {
		t.Fatalf("publish: %s %v", msg, out)
	}

	read := []map[string]any{{"path": "count", "connection": "shop", "sql": "SELECT count(*) AS n FROM customers"}}
	update["version"], update["cells"], update["parameters"] = 4, read, []any{}

	if msg, _ := call(rw, "dataport_save_notebook", update); msg != "" {
		t.Fatalf("save draft: %q", msg)
	}

	if msg, out := call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "published": true, "preview": true, "inputs": map[string]any{"name": "x"}}); msg != "" || out["cells"].([]any)[0].(map[string]any)["path"] != "rename" {
		t.Fatalf("published snapshot: %s %v", msg, out)
	}

	// Background runs return at once and show up in the run list.
	_, out = call(rw, "dataport_run_notebook", map[string]any{"notebook": "report", "background": true})
	if out["status"] != "running" || out["run_id"] == "" {
		t.Fatalf("background: %v", out)
	}

	runID := out["run_id"].(string)
	deadline := time.Now().Add(5 * time.Second)

	for {
		_, out = call(rw, "dataport_notebook_runs", map[string]any{"notebook": "report", "run": runID})
		run := out["runs"].([]any)[0].(map[string]any)

		if run["status"] == "completed" {
			if run["cells"].([]any)[0].(map[string]any)["path"] != "count" {
				t.Fatalf("background cells: %v", run)
			}

			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("background run did not finish: %v", run)
		}

		time.Sleep(20 * time.Millisecond)
	}

	if msg, _ := call(rw, "dataport_cancel_notebook_run", map[string]any{"notebook": "report", "run": runID}); !strings.Contains(msg, "already completed") {
		t.Fatalf("cancel finished run: %q", msg)
	}

	if msg, _ := call(bobs, "dataport_notebook_runs", map[string]any{"notebook": "report", "run": runID}); !strings.Contains(msg, "unknown run") {
		t.Fatalf("bob saw alice's run: %q", msg)
	}

	if _, out := call(rw, "dataport_notebook_runs", map[string]any{"notebook": "report"}); len(out["runs"].([]any)) < 3 {
		t.Fatalf("run list: %v", out)
	}

	logs, err := st.ListQueryLog(ctx, store.QueryLogFilter{UserID: alice.ID, Source: "mcp"})
	if err != nil || len(logs) == 0 {
		t.Fatalf("notebook statements are not logged as mcp: %v %d", err, len(logs))
	}

	if msg, out := call(rw, "dataport_delete_notebook", map[string]any{"notebook": "report"}); msg != "" || out["deleted"] != "report" {
		t.Fatalf("delete: %s %v", msg, out)
	}

	if _, out := call(rw, "dataport_list_notebooks", nil); len(out["notebooks"].([]any)) != 0 {
		t.Fatalf("deleted notebook listed: %v", out)
	}
}

func mustConnection(t *testing.T, st *store.Store, u *store.User, name string) *store.Connection {
	t.Helper()

	grants, err := st.ListGrants(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}

	for _, g := range grants {
		if g.Connection.Name == name {
			return g.Connection
		}
	}

	t.Fatalf("no connection %s", name)

	return nil
}
