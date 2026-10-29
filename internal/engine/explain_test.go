package engine

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestExplainSQL(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{postgresExplain("SELECT 1", ExplainOptions{Costs: true}), "EXPLAIN SELECT 1"},
		{postgresExplain("SELECT 1", ExplainOptions{}), "EXPLAIN (COSTS OFF) SELECT 1"},
		{
			postgresExplain("SELECT 1", ExplainOptions{Analyze: true, Verbose: true, Costs: true, Buffers: true, Timing: true, WAL: true, Format: "json"}),
			"EXPLAIN (ANALYZE, VERBOSE, BUFFERS, WAL, FORMAT JSON) SELECT 1",
		},
		// TIMING and WAL are left out without ANALYZE, which they need.
		{postgresExplain("SELECT 1", ExplainOptions{Costs: true, WAL: true}), "EXPLAIN SELECT 1"},
		{mysqlExplain(false, "SELECT 1", ExplainOptions{}), "EXPLAIN FORMAT=TREE SELECT 1"},
		{mysqlExplain(false, "SELECT 1", ExplainOptions{Analyze: true}), "EXPLAIN ANALYZE SELECT 1"},
		{mysqlExplain(true, "SELECT 1", ExplainOptions{Analyze: true, Format: "json"}), "ANALYZE FORMAT=JSON SELECT 1"},
	}

	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestExplainStatement(t *testing.T) {
	d, _ := GetDriver("postgres")

	if s, err := ExplainStatement(d, "SELECT 1;\n", ExplainOptions{}, true); err != nil || s != "SELECT 1" {
		t.Fatalf("%q %v", s, err)
	}

	for _, bad := range []string{"SELECT 1; SELECT 2", "EXPLAIN SELECT 1", "COMMIT", ""} {
		if _, err := ExplainStatement(d, bad, ExplainOptions{}, false); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}

	// A read-only grant may only analyze reads.
	if _, err := ExplainStatement(d, "DELETE FROM t", ExplainOptions{Analyze: true}, true); err == nil {
		t.Error("analyzing a write on a read-only grant must fail")
	}

	if _, err := ExplainStatement(d, "DELETE FROM t", ExplainOptions{}, true); err != nil {
		t.Errorf("an estimated plan of a write may be shown: %v", err)
	}

	// ClickHouse cannot roll back, so analyze-like runs stay reads.
	ch, _ := GetDriver("clickhouse")
	if _, err := ExplainStatement(ch, "INSERT INTO t VALUES (1)", ExplainOptions{Analyze: true}, false); err == nil {
		t.Error("analyzing a write without transactions must fail")
	}
}

func TestCountSQL(t *testing.T) {
	pg, _ := GetDriver("postgres")

	q, err := CountSQL(pg, "SELECT * FROM t -- all\n;")
	if err != nil || q != "SELECT COUNT(*) FROM (\nSELECT * FROM t -- all\n) AS dp_count" {
		t.Fatalf("%q %v", q, err)
	}

	ora, _ := GetDriver("oracle")
	if q, _ := CountSQL(ora, "SELECT 1 FROM dual"); !strings.HasSuffix(q, ") dp_count") {
		t.Fatalf("oracle: %q", q)
	}

	for _, bad := range []string{"DELETE FROM t", "SHOW TABLES", "SELECT 1; SELECT 2", "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x"} {
		if _, err := CountSQL(pg, bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestExplainAndCountSQLite(t *testing.T) {
	root := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(root, "x.db"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO t VALUES (1,'a'),(2,'b'),(3,'c');`); err != nil {
		t.Fatal(err)
	}

	_ = db.Close()

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "x", DBType: "sqlite", Database: "x.db"}
	ctx := context.Background()

	plan, err := m.Explain(ctx, c, "", ExplainRequest{SQL: "SELECT * FROM t WHERE id = 2", Timeout: 5 * time.Second})
	if err != nil || plan.Result == nil || len(plan.Result.Rows) == 0 || !strings.HasPrefix(plan.SQL, "EXPLAIN QUERY PLAN") {
		t.Fatalf("explain: %+v %v", plan, err)
	}

	n, err := m.Count(ctx, c, "", CountRequest{SQL: "SELECT * FROM t WHERE id > 1 ORDER BY id;", Timeout: 5 * time.Second})
	if err != nil || n.Count != 2 {
		t.Fatalf("count: %+v %v", n, err)
	}

	if _, err := m.Count(ctx, c, "", CountRequest{SQL: "DELETE FROM t"}); !errors.Is(err, ErrNotCountable) {
		t.Fatalf("counting a write: %v", err)
	}
}
