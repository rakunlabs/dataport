package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

// UPDATE or DELETE without WHERE asks for confirmation on any connection,
// and runs once confirmed.
func TestUnboundedNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")

	token, err := env.srv.auth.CreateSession(ctx, env.alice.ID, "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	conns, err := env.st.ListConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var shop string

	for _, c := range conns {
		if c.Name == "shop" {
			shop = c.ID

			if c.Production {
				t.Fatal("the test needs a connection that is not production")
			}
		}
	}

	do := func(body map[string]any) (int, map[string]any) {
		t.Helper()

		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/v1/connections/"+shop+"/query", strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})

		w := httptest.NewRecorder()
		env.srv.mux.ServeHTTP(w, r)

		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	count := func() float64 {
		t.Helper()

		_, out := do(map[string]any{"sql": "SELECT COUNT(*) FROM customers"})

		return out["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0].(float64)
	}

	before := count()

	code, out := do(map[string]any{"sql": "SELECT 1; DELETE FROM customers"})
	if code != http.StatusConflict || out["confirmation_required"] != true || out["production"] != false {
		t.Fatalf("delete without where: %d %v", code, out)
	}

	if list, _ := out["unbounded"].([]any); len(list) != 1 || list[0] != "DELETE FROM customers" {
		t.Fatalf("unbounded: %v", out["unbounded"])
	}

	if n := count(); n != before {
		t.Fatalf("rows changed before confirming: %v -> %v", before, n)
	}

	// A WHERE runs without asking.
	if code, out := do(map[string]any{"sql": "DELETE FROM customers WHERE id = -1"}); code != http.StatusOK {
		t.Fatalf("delete with where: %d %v", code, out)
	}

	if code, out := do(map[string]any{"sql": "DELETE FROM customers", "confirm": true}); code != http.StatusOK {
		t.Fatalf("confirmed delete: %d %v", code, out)
	}

	if n := count(); n != 0 {
		t.Fatalf("rows after confirmed delete: %v", n)
	}
}

// A notebook write cell without WHERE asks for the exact SQL on any
// connection, and runs with that confirmation.
func TestUnboundedNotebookCell(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()

	grants, err := env.st.ListGrants(ctx, env.alice)
	if err != nil {
		t.Fatal(err)
	}

	var shop string

	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
	}

	doc := notebook.Document{Cells: []notebook.Cell{{ID: "w", Path: "wipe", ConnectionID: shop, SQL: "UPDATE customers SET name = 'x'", Enabled: true, Write: true, Output: true}}}

	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks", saveNotebookRequest{Name: "Wipe", Path: "wipe", Document: doc})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}

	var n store.Notebook
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})

	var out notebookResult
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	if w.Code == http.StatusOK || len(out.Cells) != 1 || out.Cells[0].Status != "confirmation_required" || out.Cells[0].Confirmation == "" {
		t.Fatalf("unconfirmed run: %d %s", w.Code, w.Body.String())
	}

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Confirmations: map[string]string{"wipe": out.Cells[0].Confirmation}})
	if w.Code != http.StatusOK {
		t.Fatalf("confirmed run: %d %s", w.Code, w.Body.String())
	}

	// The endpoint caller cannot confirm, so the cell is refused there.
	write := notebook.Cell{ID: "w", Path: "w", ConnectionID: shop, SQL: "DELETE FROM customers", Enabled: true, Write: true}
	if _, err := env.srv.cellAccess(ctx, env.alice, write, runPolicy{noProductionWrites: true}, false); err == nil {
		t.Fatal("unconfirmed delete without where accepted")
	}
}

// A preview tells which write cells need a confirmation, so the UI asks
// before any cell runs; a bounded write needs none.
func TestUnboundedNotebookPreview(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()

	grants, err := env.st.ListGrants(ctx, env.alice)
	if err != nil {
		t.Fatal(err)
	}

	var shop string

	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
	}

	doc := notebook.Document{Cells: []notebook.Cell{
		{ID: "a", Path: "insert", ConnectionID: shop, SQL: "INSERT INTO customers (id, name) VALUES (50, 'x')", Enabled: true, Write: true},
		{ID: "b", Path: "wipe", ConnectionID: shop, SQL: "UPDATE customers SET name = 'y'", Enabled: true, Write: true},
	}}

	w := notebookHTTP(t, env, env.alice, "POST", "/notebooks", saveNotebookRequest{Name: "Two", Path: "two", Document: doc})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}

	var n store.Notebook
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version, Preview: true})

	var out notebookResult
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	if w.Code != http.StatusOK || len(out.Cells) != 2 || out.Cells[0].NeedsConfirmation || !out.Cells[1].NeedsConfirmation {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
}
