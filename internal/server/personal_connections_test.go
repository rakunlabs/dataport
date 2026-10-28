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

func connectionHTTP(t *testing.T, env *mcpEnv, user *store.User, method, path, id string, body any, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, user))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	handler(w, r)

	return w
}

func setConnectionSettings(t *testing.T, env *mcpEnv, create, files bool) {
	t.Helper()

	next := env.srv.base
	next.Connections.UserCreate, next.Connections.UserFiles = create, files
	env.srv.useSettings(&next, false)
}

func TestPersonalConnections(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()
	alice, bob := env.alice, env.bob
	body := map[string]any{"name": "mine", "db_type": "sqlite", "database": "shop.db", "read_only": false}

	w := connectionHTTP(t, env, alice, "POST", "/my-connections", "", body, env.srv.createPersonalConnection)
	if w.Code != http.StatusForbidden {
		t.Fatalf("created while turned off: %d %s", w.Code, w.Body.String())
	}

	setConnectionSettings(t, env, true, false)

	w = connectionHTTP(t, env, alice, "POST", "/my-connections", "", body, env.srv.createPersonalConnection)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "database files") {
		t.Fatalf("file database allowed: %d %s", w.Code, w.Body.String())
	}

	setConnectionSettings(t, env, true, true)

	w = connectionHTTP(t, env, alice, "POST", "/my-connections", "", body, env.srv.createPersonalConnection)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}

	var mine store.Connection
	_ = json.Unmarshal(w.Body.Bytes(), &mine)

	if mine.OwnerID != alice.ID {
		t.Fatalf("owner %q", mine.OwnerID)
	}

	if g, err := env.st.GetGrant(ctx, alice, mine.ID); err != nil || g.ReadOnly {
		t.Fatalf("owner grant %+v %v", g, err)
	}

	// Nobody else, admins neither, reaches it or changes it.
	admin := &store.User{ID: "admin", Role: store.RoleAdmin}
	for _, u := range []*store.User{bob, admin} {
		if _, err := env.st.GetGrant(ctx, u, mine.ID); err == nil {
			t.Fatalf("%s reached a personal connection", u.Username)
		}
	}

	if w := connectionHTTP(t, env, bob, "DELETE", "/my-connections/x", mine.ID, nil, env.srv.deletePersonalConnection); w.Code != http.StatusNotFound {
		t.Fatalf("bob deleted alice's connection: %d", w.Code)
	}

	if w := connectionHTTP(t, env, admin, "PUT", "/admin/connections/x", mine.ID, body, env.srv.adminUpdateConnection); w.Code != http.StatusNotFound {
		t.Fatalf("admin changed a personal connection: %d", w.Code)
	}

	// Turned off, it stays but is neither listed nor usable.
	setConnectionSettings(t, env, false, false)

	if _, err := env.st.GetGrant(ctx, alice, mine.ID); err == nil {
		t.Fatal("personal connection usable while turned off")
	}

	grants, _ := env.st.ListGrants(ctx, alice)
	for _, g := range grants {
		if g.Connection.ID == mine.ID {
			t.Fatal("personal connection listed while turned off")
		}
	}

	if _, err := env.st.GetConnection(ctx, mine.ID); err != nil {
		t.Fatal("personal connection removed when turned off")
	}
}

func TestNotebookOnlyConnection(t *testing.T) {
	env := newMCPEnv(t, "")
	ctx := context.Background()

	c := &store.Connection{Name: "reports", DBType: "sqlite", Database: "shop.db", NotebookOnly: true, HideWorkspace: true, HideMCP: true,
		Access: []store.Access{{SubjectType: "user", SubjectID: env.alice.ID}}}
	if err := env.st.CreateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}

	// Queries and the schema browser are refused.
	w := connectionHTTP(t, env, env.alice, "GET", "/connections/x/schemas", c.ID, nil, env.srv.schemas)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "only be used in notebooks") {
		t.Fatalf("schemas: %d %s", w.Code, w.Body.String())
	}

	// Notebook cells run on it.
	doc := notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "names", ConnectionID: c.ID, SQL: "SELECT name FROM customers WHERE id = 1", Enabled: true, Output: true}}}
	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks", saveNotebookRequest{Name: "Report", Path: "report", Document: doc})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}

	var n store.Notebook
	_ = json.Unmarshal(w.Body.Bytes(), &n)

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Ada") {
		t.Fatal(w.Code, w.Body.String())
	}

	// A connection kept out of notebooks cannot go into a cell.
	c.NotebookOnly, c.HideNotebooks = false, true
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks", saveNotebookRequest{Name: "Other", Path: "other", Document: doc})
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}

	w = notebookHTTP(t, env, env.alice, "POST", "/notebooks/"+n.ID+"/run", notebookRunRequest{Version: n.Version})
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
}
