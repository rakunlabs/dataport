//go:build duckdb

package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestDuckDBEngine(t *testing.T) {
	root := t.TempDir()

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	seed, err := openDuckDB(filepath.Join(root, "a.duckdb"), false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := seed.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL);
CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id));
INSERT INTO users VALUES (1, 'a'), (2, 'b');`); err != nil {
		t.Fatal(err)
	}

	_ = seed.Close()

	m := NewManager(store.Limits{MaxOpen: 1}, time.Minute)
	defer m.Close()

	ctx := context.Background()
	c := &store.Connection{ID: "d", DBType: "duckdb", Database: "a.duckdb", UpdatedAt: 1}

	res, err := m.Execute(ctx, c, "", ExecRequest{SQL: "select * from users order by id", ReadOnly: true, MaxRows: 10, Timeout: 5 * time.Second})
	if err != nil || res.FirstError() != "" || len(res.Results[0].Rows) != 2 {
		t.Fatalf("select: %v %+v", err, res)
	}

	// A UUID, which the driver reads as 16 bytes, shows as text.
	res, err = m.Execute(ctx, c, "", ExecRequest{SQL: "select '12345678-9abc-def0-1234-56789abcdef0'::uuid as u", ReadOnly: true, MaxRows: 1, Timeout: 5 * time.Second})
	if err != nil || res.FirstError() != "" || res.Results[0].Rows[0][0] != "12345678-9abc-def0-1234-56789abcdef0" {
		t.Fatalf("uuid: %v %+v %+v", err, res.Results[0].Columns, res.Results[0].Rows)
	}

	res, err = m.Execute(ctx, c, "", ExecRequest{SQL: "select * from read_csv('/etc/passwd')", MaxRows: 10, Timeout: 5 * time.Second})
	if err == nil && res.FirstError() == "" {
		t.Fatal("external access was allowed")
	}

	st, err := m.Structure(ctx, c, "", "main", "orders")
	if err != nil || !st.Columns[0].PrimaryKey || len(st.ForeignKeys) != 1 || st.DDL == "" {
		t.Fatalf("structure: %v %+v", err, st)
	}
}
