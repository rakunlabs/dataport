package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// fakePushTarget keeps the items of one push collection.
type fakePushTarget struct {
	mu      sync.Mutex
	created bool
	items   map[string]pushItem
	pushes  int
	auth    string
}

func (f *fakePushTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.auth = r.Header.Get("Authorization")
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sources/docs":
		if !f.created {
			reply(http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		reply(http.StatusOK, map[string]any{"source": map[string]any{"type": "push", "page_count": len(f.items)}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sources":
		f.created = true
		reply(http.StatusAccepted, map[string]any{})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sources/docs/items":
		var in struct {
			Items []pushItem `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Items) > pushBatchItems {
			reply(http.StatusBadRequest, map[string]string{"error": "batch too large"})
			return
		}
		f.pushes++
		for _, it := range in.Items {
			f.items[it.ID] = it
		}
		reply(http.StatusOK, map[string]int{"imported": len(in.Items), "changed": len(in.Items)})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sources/docs/items/prune":
		var in struct {
			Keep []string `json:"keep"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		keep := map[string]bool{}
		for _, id := range in.Keep {
			keep[id] = true
		}
		n := 0
		for id := range f.items {
			if !keep[id] {
				delete(f.items, id)
				n++
			}
		}
		reply(http.StatusOK, map[string]int{"removed": n})
	default:
		reply(http.StatusNotFound, map[string]string{"error": "no route"})
	}
}

func TestDocExport(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	fake := &fakePushTarget{items: map[string]pushItem{}}
	ks := httptest.NewServer(fake)
	t.Cleanup(ks.Close)

	token := "secret-token"
	cfg, msg := s.checkDocExport(ctx, docExportIn{
		Enabled: true, URL: ks.URL + "/", Collection: "docs", Token: &token,
		Targets: []docExportTarget{{ConnectionID: k.ConnectionID, UserID: env.alice.ID}},
	}, docExportConfig{})
	if msg != "" {
		t.Fatal(msg)
	}
	raw, _ := json.Marshal(cfg)
	if err := env.st.SetSetting(ctx, docExportSetting, string(raw)); err != nil {
		t.Fatal(err)
	}

	s.exportDocs(ctx, 0)

	status := s.docExportStatusNow(ctx)
	if status.Error != "" || len(status.Failed) > 0 || status.Tables != 3 || status.Sent != 3 {
		t.Fatalf("first export: %+v", status)
	}
	if fake.auth != "Bearer "+token {
		t.Fatalf("auth header: %q", fake.auth)
	}
	orders, ok := fake.items["dataport/"+k.ConnectionID+"/main/orders"]
	if !ok {
		t.Fatalf("items: %v", fake.items)
	}
	for _, want := range []string{"# orders", "connection: shop (sqlite)", "customer_id INTEGER NOT NULL -> customers.id", "trigger orders_audit", "# related tables"} {
		if !strings.Contains(orders.Content, want) {
			t.Fatalf("orders item lacks %q:\n%s", want, orders.Content)
		}
	}
	if _, ok := fake.items["dataport/"+k.ConnectionID+"/main/_schema"]; !ok {
		t.Fatal("schema overview missing")
	}

	// Nothing changed: nothing is sent again.
	pushes := fake.pushes
	s.exportDocs(ctx, 0)
	if status := s.docExportStatusNow(ctx); fake.pushes != pushes || status.Sent != 0 || status.Error != "" {
		t.Fatalf("unchanged export sent %d: %+v", fake.pushes-pushes, status)
	}

	// A dropped table is pruned from the target after the refresh.
	g, pw, err := s.resolveGrant(ctx, env.alice, k.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.engine.Execute(ctx, g.Connection, pw, engineExec("CREATE TABLE extra (id INTEGER PRIMARY KEY)")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, k, true); err != nil {
		t.Fatal(err)
	}
	s.exportDocs(ctx, 0)
	// Only the new table and the schema overview changed.
	if status := s.docExportStatusNow(ctx); status.Sent != 2 || len(fake.items) != 4 {
		t.Fatalf("after add: %+v %d", status, len(fake.items))
	}
	if _, err := s.engine.Execute(ctx, g.Connection, pw, engineExec("DROP TABLE extra")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, k, true); err != nil {
		t.Fatal(err)
	}
	s.exportDocs(ctx, 0)
	if status := s.docExportStatusNow(ctx); status.Removed != 1 || len(fake.items) != 3 {
		t.Fatalf("after drop: %+v %d", status, len(fake.items))
	}

	// A user without access fails the target and prunes nothing.
	cfg.Targets[0].UserID = env.bob.ID
	raw, _ = json.Marshal(cfg)
	_ = env.st.SetSetting(ctx, docExportSetting, string(raw))
	s.exportDocs(ctx, 0)
	if status := s.docExportStatusNow(ctx); len(status.Failed) != 1 || len(fake.items) != 3 {
		t.Fatalf("no access: %+v %d", status, len(fake.items))
	}
	if strings.Contains(strings.Join(s.docExportStatusNow(ctx).Failed, " "), "shop.db") {
		t.Fatal("status names the database file")
	}
}

func TestDocExportValidation(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)
	ok := docExportTarget{ConnectionID: k.ConnectionID, UserID: env.alice.ID}

	for name, in := range map[string]docExportIn{
		"no url":         {Enabled: true},
		"bad scheme":     {URL: "ftp://x"},
		"credentials":    {URL: "http://u:p@x"},
		"bad collection": {URL: "http://x", Collection: "Bad Name"},
		"no connection":  {URL: "http://x", Targets: []docExportTarget{{ConnectionID: "nope", UserID: env.alice.ID}}},
		"no user":        {URL: "http://x", Targets: []docExportTarget{{ConnectionID: k.ConnectionID, UserID: "nope"}}},
		"twice":          {URL: "http://x", Targets: []docExportTarget{ok, ok}},
	} {
		if _, msg := s.checkDocExport(ctx, in, docExportConfig{}); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}

	personal := &store.Connection{Name: "mine", DBType: "sqlite", Database: "shop.db", OwnerID: env.alice.ID}
	if err := env.st.CreateConnection(ctx, personal); err != nil {
		t.Fatal(err)
	}
	if _, msg := s.checkDocExport(ctx, docExportIn{URL: "http://x", Targets: []docExportTarget{{ConnectionID: personal.ID, UserID: env.alice.ID}}}, docExportConfig{}); msg == "" {
		t.Error("personal connection accepted")
	}

	// A saved token is kept unless a new one is given, and never shown.
	cfg, msg := s.checkDocExport(ctx, docExportIn{URL: "http://x"}, docExportConfig{TokenEnc: "v1:kept"})
	if msg != "" || cfg.TokenEnc != "v1:kept" || cfg.Collection != docExportDefaultCollection {
		t.Fatalf("kept token: %q %+v", msg, cfg)
	}
}

func engineExec(sql string) engine.ExecRequest {
	return engine.ExecRequest{SQL: sql, MaxRows: 1}
}
