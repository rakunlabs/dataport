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

func TestServerSettings(t *testing.T) {
	ctx := context.Background()

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
		Query: config.Query{DefaultMaxRows: 100, MaxRowsLimit: 1000, PoolMaxOpen: 2},
		MCP:   config.MCP{Enabled: true, MaxRows: 50, TokenTTL: 90 * 24 * time.Hour},
	}

	srv, err := New(cfg, st, au, ci, en, nil)
	if err != nil {
		t.Fatal(err)
	}

	hash, _ := auth.HashPassword("x-password")
	admin := &store.User{Username: "root", PasswordHash: hash, Role: store.RoleAdmin}
	user := &store.User{Username: "u", PasswordHash: hash, Role: store.RoleUser}

	for _, u := range []*store.User{admin, user} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	do := func(u *store.User, method string, body any) (int, []settingView) {
		t.Helper()

		token, err := au.CreateSession(ctx, u.ID, "", "", false)
		if err != nil {
			t.Fatal(err)
		}

		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1/admin/settings", strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})

		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)

		var out []settingView
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	find := func(list []settingView, key string) settingView {
		for _, v := range list {
			if v.Key == key {
				return v
			}
		}

		t.Fatalf("no setting %s", key)

		return settingView{}
	}

	if code, _ := do(user, http.MethodGet, nil); code != http.StatusForbidden {
		t.Fatalf("a user read the settings: %d", code)
	}

	code, list := do(admin, http.MethodGet, nil)
	if code != http.StatusOK || find(list, "query.default_max_rows").Value != float64(100) || find(list, "mcp.token_ttl").Value != float64(90*24*3600) {
		t.Fatalf("get: %d %+v", code, list)
	}

	// A saved value wins over the configuration and applies at once.
	code, list = do(admin, http.MethodPut, map[string]any{"query.default_max_rows": 500, "query.pool_max_open": 9, "mcp.max_rows": 7, "mcp.enabled": false})
	if code != http.StatusOK || !find(list, "query.default_max_rows").Saved || find(list, "query.default_max_rows").Config != float64(100) {
		t.Fatalf("put: %d %+v", code, list)
	}

	if srv.queryCfg().DefaultMaxRows != 500 || srv.mcpCfg().MaxRows != 7 || en.Limits(&store.Connection{}).MaxOpen != 9 {
		t.Fatalf("not applied: %+v %+v", srv.queryCfg(), srv.mcpCfg())
	}

	// Turning MCP off needs a restart; the running server keeps it on.
	if v := find(list, "mcp.enabled"); v.Value != false || v.Running != true || !srv.mcpCfg().Enabled {
		t.Fatalf("restart setting: %+v", v)
	}

	// Out of range values and unknown keys are refused.
	for _, body := range []map[string]any{{"query.pool_max_open": 0}, {"query.file_dir": "/"}, {"mcp.enabled": 1}} {
		if code, _ := do(admin, http.MethodPut, body); code != http.StatusBadRequest {
			t.Errorf("%v accepted: %d", body, code)
		}
	}

	// null goes back to the configuration.
	code, list = do(admin, http.MethodPut, map[string]any{"query.default_max_rows": nil})
	if code != http.StatusOK || find(list, "query.default_max_rows").Saved || srv.queryCfg().DefaultMaxRows != 100 {
		t.Fatalf("reset: %d %+v", code, srv.queryCfg())
	}

	// A new server reads the saved values at start, restart ones included.
	again, err := New(&config.Config{Query: cfg.Query, MCP: config.MCP{Enabled: true, MaxRows: 50}}, st, au, ci, en, nil)
	if err != nil {
		t.Fatal(err)
	}

	if again.mcpCfg().MaxRows != 7 || again.mcpCfg().Enabled || again.queryCfg().DefaultMaxRows != 100 {
		t.Fatalf("restart: %+v %+v", again.mcpCfg(), again.queryCfg())
	}
}
