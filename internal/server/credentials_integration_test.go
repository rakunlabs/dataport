//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Run with: go test -tags integration ./internal/server/ against the
// postgres from env/compose.yaml.

type client struct {
	t      *testing.T
	h      http.Handler
	cookie string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()

	var payload string
	if body != nil {
		b, _ := json.Marshal(body)
		payload = string(b)
	}

	r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: cookieName, Value: c.cookie})

	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	return w.Code, out
}

func TestIntegrationUserCredentials(t *testing.T) {
	ctx := context.Background()

	en := engine.NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer en.Close()

	pg := &store.Connection{ID: "probe", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres",
		Username: "postgres", Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}
	if err := en.Test(ctx, pg, "pw"); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	// Two database accounts that see different things.
	res, err := en.Execute(ctx, pg, "pw", engine.ExecRequest{MaxRows: 10, Timeout: 10 * time.Second, SQL: `
DROP TABLE IF EXISTS dp_cred_t;
DROP ROLE IF EXISTS dp_alice;
DROP ROLE IF EXISTS dp_bob;
CREATE ROLE dp_alice LOGIN PASSWORD 'alice-pw';
CREATE ROLE dp_bob LOGIN PASSWORD 'bob-pw';
CREATE TABLE dp_cred_t (id int);
GRANT SELECT ON dp_cred_t TO dp_alice`})
	if err != nil || res.FirstError() != "" {
		t.Fatalf("setup: %v %s", err, res.FirstError())
	}

	st, err := store.New(ctx, config.Store{Type: "sqlite", TablePrefix: "dp_", DSN: "file:" + filepath.Join(t.TempDir(), "t.db")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ci, _ := crypto.New("k")
	au := auth.New(st, time.Hour)

	srv, err := New(&config.Config{Query: config.Query{DefaultMaxRows: 100, DefaultTimeout: 10 * time.Second, MaxRowsLimit: 1000}}, st, au, ci, en, nil)
	if err != nil {
		t.Fatal(err)
	}

	login := func(name string, role string) *client {
		hash, _ := auth.HashPassword("x-password")
		u := &store.User{Username: name, PasswordHash: hash, Role: role}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}

		token, err := au.CreateSession(ctx, u.ID, "", "")
		if err != nil {
			t.Fatal(err)
		}

		return &client{t: t, h: srv.mux, cookie: token}
	}

	admin := login("root", store.RoleAdmin)
	alice := login("alice", store.RoleUser)
	bob := login("bob", store.RoleUser)

	code, created := admin.do("POST", "/admin/connections", map[string]any{
		"name": "own", "db_type": "postgres", "host": "127.0.0.1", "port": 55432, "database": "postgres",
		"username": "postgres", "password": "pw", "options": map[string]string{"sslmode": "disable"},
		"user_credentials": true, "access": []map[string]any{
			{"subject_type": "user", "subject_id": userID(t, st, "alice"), "read_only": true},
			{"subject_type": "user", "subject_id": userID(t, st, "bob"), "read_only": true},
		},
	})
	if code != http.StatusCreated || created["has_password"] != false || created["username"] != "" {
		t.Fatalf("create: %d %v (the shared account must not be stored)", code, created)
	}

	id := created["id"].(string)
	query := map[string]any{"sql": "SELECT current_user, count(*) FROM dp_cred_t"}

	// Without an account the user is asked for one.
	if code, body := alice.do("POST", "/connections/"+id+"/query", query); code != http.StatusPreconditionRequired || body["credentials_required"] != true {
		t.Fatalf("no account: %d %v", code, body)
	}

	// A wrong password is refused and not saved.
	if code, body := alice.do("PUT", "/connections/"+id+"/credentials", map[string]any{"username": "dp_alice", "password": "nope"}); code != http.StatusBadRequest ||
		body["message"] != "the database refused your username or password" {
		t.Fatalf("wrong password: %d %v", code, body)
	}

	if code, _ := alice.do("PUT", "/connections/"+id+"/credentials", map[string]any{"username": "dp_alice", "password": "alice-pw"}); code != http.StatusNoContent {
		t.Fatalf("save alice: %d", code)
	}

	if code, _ := bob.do("PUT", "/connections/"+id+"/credentials", map[string]any{"username": "dp_bob", "password": "bob-pw"}); code != http.StatusNoContent {
		t.Fatalf("save bob: %d", code)
	}

	// Each user runs as their own database account, on separate pools.
	code, body := alice.do("POST", "/connections/"+id+"/query", query)
	if code != http.StatusOK || !strings.Contains(mustJSON(body), `"dp_alice"`) {
		t.Fatalf("alice query: %d %v", code, body)
	}

	code, body = bob.do("POST", "/connections/"+id+"/query", query)
	if code != http.StatusOK || !strings.Contains(mustJSON(body), "permission denied") {
		t.Fatalf("bob must hit his own permissions: %d %v", code, body)
	}

	// Own limits: validated, saved without retyping the password, applied
	// over the connection's.
	if code, body := alice.do("PUT", "/connections/"+id+"/credentials", map[string]any{
		"username": "dp_alice", "limits": map[string]any{"max_open": 500},
	}); code != http.StatusBadRequest {
		t.Fatalf("limits must be validated: %d %v", code, body)
	}

	if code, body := alice.do("PUT", "/connections/"+id+"/credentials", map[string]any{
		"username": "dp_alice", "limits": map[string]any{"max_open": 1, "max_idle_time_sec": 60},
	}); code != http.StatusNoContent {
		t.Fatalf("limits without password: %d %v", code, body)
	}

	if list := listConnections(t, alice); list[0]["my_limits"].(map[string]any)["max_open"] != float64(1) || list[0]["limits"] == nil {
		t.Fatalf("my limits: %v", list[0])
	}

	if code, _ := alice.do("POST", "/connections/"+id+"/query", query); code != http.StatusOK {
		t.Fatalf("query with own limits: %d", code)
	}

	// The list tells each user their own username only.
	_, _ = alice.do("GET", "/connections", nil)
	if list := listConnections(t, alice); list[0]["credential_user"] != "dp_alice" || list[0]["user_credentials"] != true {
		t.Fatalf("alice list: %v", list)
	}

	// A password changed at the database sends the user back to the dialog.
	if res, err := en.Execute(ctx, pg, "pw", engine.ExecRequest{MaxRows: 1, Timeout: 5 * time.Second,
		SQL: "ALTER ROLE dp_alice PASSWORD 'changed'"}); err != nil || res.FirstError() != "" {
		t.Fatal(err, res.FirstError())
	}

	en.ForgetUser(id, userID(t, st, "alice"))

	if code, body := alice.do("POST", "/connections/"+id+"/query", query); code != http.StatusPreconditionRequired || body["credentials_required"] != true {
		t.Fatalf("changed password: %d %v", code, body)
	}

	// Forgetting removes the account.
	if code, _ := bob.do("DELETE", "/connections/"+id+"/credentials", nil); code != http.StatusNoContent {
		t.Fatalf("forget: %d", code)
	}

	if code, _ := bob.do("POST", "/connections/"+id+"/query", query); code != http.StatusPreconditionRequired {
		t.Fatalf("after forget: %d", code)
	}

	// Switching back to a shared account drops the users' accounts.
	created["user_credentials"] = false
	created["username"] = "postgres"
	created["password"] = "pw"
	delete(created, "id")
	delete(created, "has_password")
	delete(created, "created_at")
	delete(created, "updated_at")

	if code, body := admin.do("PUT", "/admin/connections/"+id, created); code != http.StatusOK {
		t.Fatalf("switch back: %d %v", code, body)
	}

	if _, err := st.GetCredential(ctx, id, userID(t, st, "alice")); err == nil {
		t.Fatal("user accounts must be removed with a shared account")
	}

	code, body = alice.do("POST", "/connections/"+id+"/query", query)
	if code != http.StatusOK || !strings.Contains(mustJSON(body), `"postgres"`) {
		t.Fatalf("shared account: %d %v", code, body)
	}

	_, _ = en.Execute(ctx, pg, "pw", engine.ExecRequest{MaxRows: 1, Timeout: 5 * time.Second,
		SQL: "DROP TABLE IF EXISTS dp_cred_t; DROP ROLE IF EXISTS dp_alice; DROP ROLE IF EXISTS dp_bob"})
}

func userID(t *testing.T, st *store.Store, name string) string {
	t.Helper()

	u, err := st.GetUserByUsername(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}

	return u.ID
}

func listConnections(t *testing.T, c *client) []map[string]any {
	t.Helper()

	r := httptest.NewRequest("GET", "/api/v1/connections", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: c.cookie})

	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)

	var out []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}

	return out
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)

	return string(b)
}
