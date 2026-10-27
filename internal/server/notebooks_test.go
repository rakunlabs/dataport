package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

func notebookHTTP(t *testing.T, env *mcpEnv, user *store.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, user))
	w := httptest.NewRecorder()
	return callNotebookRoute(env.srv, w, r, path)
}
func callNotebookRoute(s *Server, w *httptest.ResponseRecorder, r *http.Request, path string) *httptest.ResponseRecorder {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 1 {
		r.SetPathValue("note", parts[1])
	}
	switch {
	case path == "/notebooks":
		s.saveNotebook(w, r)
	case strings.HasSuffix(path, "/run"):
		s.runNotebook(w, r)
	case strings.HasSuffix(path, "/publish"):
		s.publishNotebook(w, r)
	default:
		s.getNotebook(w, r)
	}
	return w
}

func TestNotebookExecutionAndPublishing(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, err := env.st.ListGrants(ctx, env.alice)
	if err != nil {
		t.Fatal(err)
	}
	var shop, prod string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
		if g.Connection.Name == "shop-prod" {
			prod = g.Connection.ID
		}
	}
	doc := notebook.Document{Cells: []notebook.Cell{
		{ID: "b", Path: "result", ConnectionID: shop, SQL: `SELECT name FROM customers WHERE id = {{ param (index .cells.seed 0).id }}`, Enabled: true, Output: true, Template: true, Dependencies: []string{"seed"}},
		{ID: "a", Path: "seed", ConnectionID: shop, SQL: `SELECT {{ param .data.id }} AS id`, Enabled: true, Output: false, Template: true},
	}, Parameters: []notebook.Parameter{{Name: "id", Type: "number", Required: true}}}
	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks", saveNotebookRequest{Name: "Report", Path: "report", Document: doc})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	var n store.Notebook
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Data: map[string]any{"id": 1.0}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out notebookResult
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Cells) != 2 || out.Cells[0].Path != "seed" || out.Cells[0].Result != nil || out.Cells[1].Result.Results[0].Rows[0][0] != "Ada" {
		t.Fatalf("%s", w.Body.String())
	}
	history, err := env.st.GetNotebookRun(ctx, out.ID)
	if err != nil || strings.Contains(string(history.Info), "Ada") || strings.Contains(string(history.Info), "SELECT") {
		t.Fatalf("%s %v", history.Info, err)
	}
	w = notebookHTTP(t, env, env.bob, "GET", "/notebooks/"+n.ID, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	if err := env.st.PublishNotebook(ctx, n.ID, n.Version, true); err != nil {
		t.Fatal(err)
	}
	raw, hash, _ := auth.NewAccessToken()
	token := &store.AccessToken{UserID: env.alice.ID, TokenHash: hash, Name: "endpoint", ReadOnly: true, ConnectionID: shop}
	if err := env.st.CreateAccessToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	request := func(method, url, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		env.srv.mux.ServeHTTP(w, r)
		return w
	}
	w = request("GET", "/api/v1/run/report?id=1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Ada") {
		t.Fatal(w.Code, w.Body.String())
	}
	n.Document.Cells[0].SQL = "SELECT 'draft'"
	if err := env.st.SaveNotebook(ctx, &n); err != nil {
		t.Fatal(err)
	}
	// The SQL shown to a caller is part of the execution contract. A stale
	// client must not silently run a newer saved draft.
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version - 1, Data: map[string]any{"id": 1.0}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "reload before running") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/run/report", `{"id":1}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "draft") {
		t.Fatal(w.Code, w.Body.String())
	}
	n.Document = notebook.Document{Cells: []notebook.Cell{{ID: "write", Path: "write", ConnectionID: prod, SQL: "UPDATE customers SET name='changed' WHERE id=1", Enabled: true, Write: true, Output: true}}}
	if err := env.st.SaveNotebook(ctx, &n); err != nil {
		t.Fatal(err)
	}
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Preview: true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Cells) != 1 || out.Cells[0].Confirmation == "" {
		t.Fatal(w.Body.String())
	}
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Confirmations: map[string]string{"write": out.Cells[0].Confirmation}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := env.st.PublishNotebook(ctx, n.ID, n.Version, true); err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/v1/run/report", `{}`)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := env.st.DeleteAccessToken(ctx, env.alice.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/v1/run/report", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestNotebookSharingDoesNotGrantDatabaseAccess(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var id string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			id = g.Connection.ID
		}
	}
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "private db", Path: "private-db", Document: notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "a", ConnectionID: id, SQL: "select 1", Enabled: true}}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetNotebookAccess(ctx, n.ID, []store.NotebookAccess{{SubjectType: "user", SubjectID: env.bob.ID, Permission: "run"}}); err != nil {
		t.Fatal(err)
	}
	w := notebookHTTP(t, env, env.bob, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = notebookHTTP(t, env, env.bob, "POST", "/notebooks/"+n.ID+"/publish", map[string]any{"version": 1, "publish": true})
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNotebookBackgroundCancellation(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var id string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			id = g.Connection.ID
		}
	}
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Long read", Path: "long-read", Document: notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "a", ConnectionID: id, SQL: `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n`, Enabled: true}}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Background: true})
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	var run store.NotebookRun
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	// Simulate cancellation handled by a different replica: it only updates
	// the shared store, with no access to this worker's in-memory engine.
	if err := env.st.CancelNotebookRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("background worker did not acknowledge cancellation")
		case <-tick.C:
			state, err := env.st.GetNotebookRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status != "running" {
				if state.Status != "cancelled" {
					t.Fatalf("%+v", state)
				}
				return
			}
		}
	}
}

func TestNotebookEndpointBasePathAndTokenScope(t *testing.T) {
	env := newMCPEnv(t, "/dataport")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var shop, prod string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
		if g.Connection.Name == "shop-prod" {
			prod = g.Connection.ID
		}
	}
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Read", Path: "read", Document: notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "a", ConnectionID: shop, SQL: "SELECT 1", Enabled: true, Output: true}}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := env.st.PublishNotebook(ctx, n.ID, n.Version, true); err != nil {
		t.Fatal(err)
	}
	raw, hash, _ := auth.NewAccessToken()
	if err := env.st.CreateAccessToken(ctx, &store.AccessToken{UserID: env.alice.ID, Name: "scoped", TokenHash: hash, ConnectionID: prod}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/dataport/api/v1/run/read/a", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "/dataport/api/v1/run/read/a", nil)
	w = httptest.NewRecorder()
	env.srv.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNotebookNullCollectionsAndMetadata(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var id string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			id = g.Connection.ID
		}
	}
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Minimal API notebook", Path: "minimal", Document: notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "a", ConnectionID: id, SQL: "SELECT 1", Enabled: true}}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetNotebookAccess(ctx, n.ID, []store.NotebookAccess{{SubjectType: "user", SubjectID: env.bob.ID, Permission: "run"}}); err != nil {
		t.Fatal(err)
	}
	w := notebookHTTP(t, env, env.bob, "GET", "/notebooks/"+n.ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"dependencies":[]`) || !strings.Contains(w.Body.String(), `"parameters":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	meta := notebookRunMetadata(notebookResult{Error: "invalid input: secret", Cells: []notebookCellResult{{Path: "a", Error: "invalid input: secret", SQL: "secret", Args: []any{"secret"}}}})
	b, _ := json.Marshal(meta)
	if strings.Contains(string(b), "secret") {
		t.Fatal("sensitive error leaked to run history", string(b))
	}
}

func TestNotebookConfirmationBindsValues(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var shop, prod string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
		if g.Connection.Name == "shop-prod" {
			prod = g.Connection.ID
		}
	}
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Confirm", Path: "confirm-values", Document: notebook.Document{Parameters: []notebook.Parameter{{Name: "id", Type: "number", Required: true}, {Name: "name", Type: "string", Required: true}}, Cells: []notebook.Cell{
		{ID: "seed", Path: "seed", ConnectionID: shop, SQL: `SELECT {{ param .data.id }} AS id`, Template: true, Enabled: true},
		{ID: "write", Path: "write", ConnectionID: prod, SQL: `UPDATE customers SET name = {{ param .data.name }} WHERE id = {{ param (index .cells.seed 0).id }}`, Template: true, Write: true, Enabled: true, Dependencies: []string{"seed"}},
	}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"id": 1.0, "name": "approved"}
	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Preview: true, Data: data})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview notebookResult
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	confirmation := map[string]string{"write": preview.Cells[1].Confirmation}
	for _, changed := range []map[string]any{{"id": 2.0, "name": "approved"}, {"id": 1.0, "name": "not-approved"}} {
		w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Data: changed, Confirmations: confirmation})
		if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	g, pw, err := env.srv.resolveGrant(ctx, env.alice, shop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := env.srv.execute(ctx, env.alice, g, pw, execInput{SQL: "SELECT name FROM customers WHERE id=1", ReadOnly: true, Source: "notebook"})
	if err != nil || result.Results[0].Rows[0][0] != "Ada" {
		t.Fatal("unapproved write ran", result, err)
	}
}

func TestNotebookPrecheckMatchesCellChecks(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	grants, _ := env.st.ListGrants(ctx, env.alice)
	var shop, prod string
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
		if g.Connection.Name == "shop-prod" {
			prod = g.Connection.ID
		}
	}
	// A production transfer target is refused before any cell commits, for
	// workspace runs as well as token callers.
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Transfer", Path: "transfer", Document: notebook.Document{Cells: []notebook.Cell{
		{ID: "w", Path: "w", ConnectionID: shop, SQL: "UPDATE customers SET name = 'changed' WHERE id = 1", Enabled: true, Write: true},
		{ID: "t", Path: "t", ConnectionID: shop, SQL: "SELECT id, name FROM customers", Enabled: true, Write: true, Transfer: &notebook.Transfer{ConnectionID: prod, Table: "customers", Batch: 10}},
	}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
	g, pw, err := env.srv.resolveGrant(ctx, env.alice, shop)
	if err != nil {
		t.Fatal(err)
	}
	result, err := env.srv.execute(ctx, env.alice, g, pw, execInput{SQL: "SELECT name FROM customers WHERE id=1", ReadOnly: true, Source: "notebook"})
	if err != nil || result.Results[0].Rows[0][0] != "Ada" {
		t.Fatal("earlier cell ran before the refusal", result, err)
	}
	// A transfer only reads its source, so a production source needs no
	// confirmation, before the run or when the cell runs.
	transfer := notebook.Cell{ID: "t", Path: "t", ConnectionID: prod, SQL: "SELECT 1", Enabled: true, Write: true, Transfer: &notebook.Transfer{ConnectionID: shop, Table: "customers", Batch: 10}}
	access, err := env.srv.cellAccess(ctx, env.alice, transfer, runPolicy{noProductionWrites: true}, false)
	if err != nil || needsConfirmation(transfer, access) {
		t.Fatal("transfer from production treated as a production write", err)
	}
	write := notebook.Cell{ID: "w", Path: "w", ConnectionID: prod, SQL: "DELETE FROM customers", Enabled: true, Write: true}
	if _, err := env.srv.cellAccess(ctx, env.alice, write, runPolicy{noProductionWrites: true}, false); err == nil {
		t.Fatal("unconfirmed production write accepted")
	}
}

func TestNotebookSharingDeduplicates(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Shared", Path: "shared", Document: notebook.Document{Cells: []notebook.Cell{}}}
	if err := env.st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}
	err := env.srv.setNotebookAccess(ctx, n, []store.NotebookAccess{
		{SubjectType: store.SubjectUser, SubjectID: env.bob.ID, Permission: "view"},
		{SubjectType: store.SubjectUser, SubjectID: env.bob.ID, Permission: "edit"},
		{SubjectType: store.SubjectUser, SubjectID: env.bob.ID, Permission: "run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	access, err := env.st.NotebookAccess(ctx, n.ID)
	if err != nil || len(access) != 1 || access[0].Permission != "edit" {
		t.Fatalf("%+v %v", access, err)
	}
}
