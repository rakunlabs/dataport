//go:build integration

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Runs against make env: postgres on :55432, mysql on :53306.
func TestExplainIntegration(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	ctx := context.Background()
	pg := &store.Connection{ID: "pg", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres", Options: map[string]string{"sslmode": "disable"}}

	if _, err := m.Execute(ctx, pg, "pw", ExecRequest{SQL: "CREATE TABLE IF NOT EXISTS dp_explain (id int primary key, v text)"}); err != nil {
		t.Skipf("postgres not available: %v", err)
	}

	plan, err := m.Explain(ctx, pg, "pw", ExplainRequest{SQL: "SELECT * FROM dp_explain WHERE id = 1", Options: ExplainOptions{Analyze: true, Buffers: true, Costs: true, Timing: true, Format: "json"}, Timeout: 10 * time.Second})
	if err != nil || !strings.Contains(plan.Result.Rows[0][0].(string), "Actual Total Time") {
		t.Fatalf("analyze json: %+v %v", plan, err)
	}

	// An analyzed write is rolled back.
	if _, err := m.Explain(ctx, pg, "pw", ExplainRequest{SQL: "INSERT INTO dp_explain VALUES (99, 'x')", Options: ExplainOptions{Analyze: true}}); err != nil {
		t.Fatal(err)
	}

	n, err := m.Count(ctx, pg, "pw", CountRequest{SQL: "SELECT * FROM dp_explain WHERE id = 99"})
	if err != nil || n.Count != 0 {
		t.Fatalf("analyzed insert was kept: %+v %v", n, err)
	}

	// Read-only grants analyze reads only.
	if _, err := m.Explain(ctx, pg, "pw", ExplainRequest{SQL: "DELETE FROM dp_explain", Options: ExplainOptions{Analyze: true}, ReadOnly: true}); err == nil {
		t.Fatal("read-only grant analyzed a delete")
	}

	my := &store.Connection{ID: "my", DBType: "mysql", Host: "127.0.0.1", Port: 53306, Database: "app", Username: "root"}
	if _, err := m.Execute(ctx, my, "pw", ExecRequest{SQL: "CREATE TABLE IF NOT EXISTS dp_explain (id int primary key)"}); err != nil {
		t.Skipf("mysql not available: %v", err)
	}

	for _, o := range []ExplainOptions{{}, {Analyze: true}, {Format: "json"}} {
		plan, err := m.Explain(ctx, my, "pw", ExplainRequest{SQL: "SELECT * FROM dp_explain WHERE id > 1", Options: o})
		if err != nil || len(plan.Result.Rows) == 0 {
			t.Fatalf("mysql %+v: %+v %v", o, plan, err)
		}
	}

	if n, err := m.Count(ctx, my, "pw", CountRequest{SQL: "SELECT * FROM dp_explain ORDER BY id"}); err != nil || n.Count != 0 {
		t.Fatalf("mysql count: %+v %v", n, err)
	}
}
