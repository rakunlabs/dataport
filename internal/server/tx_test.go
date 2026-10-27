package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestManualTxHTTP(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, srv := env.st, env.srv

	token, err := srv.auth.CreateSession(ctx, env.alice.ID, "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	conns, err := st.ListConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var shop string

	for _, c := range conns {
		if c.Name == "shop" {
			shop = c.ID
		}
	}

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()

		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})

		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)

		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	query := "/connections/" + shop + "/query"
	count := func() float64 {
		t.Helper()

		_, out := do("POST", query, map[string]any{"sql": "SELECT COUNT(*) FROM customers"})

		return out["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0].(float64)
	}

	code, out := do("POST", query, map[string]any{"sql": "INSERT INTO customers (id, name) VALUES (10, 'Grace')", "tx_id": "t1"})
	if code != http.StatusOK || out["tx_open"] != true || out["committed"] != false {
		t.Fatalf("run in tx: %d %v", code, out)
	}

	// Outside the transaction the row is not there yet.
	if n := count(); n != 2 {
		t.Fatalf("rows before commit: %v", n)
	}

	code, out = do("POST", "/connections/"+shop+"/tx", map[string]any{"tx_id": "t1", "commit": true})
	if code != http.StatusOK || out["committed"] != true {
		t.Fatalf("commit: %d %v", code, out)
	}

	if n := count(); n != 3 {
		t.Fatalf("rows after commit: %v", n)
	}

	// A transaction the server no longer holds is reported, not replaced.
	code, out = do("POST", query, map[string]any{"sql": "SELECT 1", "tx_id": "t1", "tx_continue": true})
	if code != http.StatusGone || out["tx_lost"] != true {
		t.Fatalf("lost tx: %d %v", code, out)
	}

	logs, err := st.ListQueryLog(ctx, store.QueryLogFilter{UserID: env.alice.ID})
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, l := range logs {
		if l.SQL == "COMMIT" && l.Status == "ok" {
			found = true
		}
	}

	if !found {
		t.Fatal("the commit is not in the query log")
	}
}
