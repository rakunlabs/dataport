//go:build integration

package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func pgConn() (*store.Connection, string) {
	return &store.Connection{ID: "pgtools", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}, env("PG_PASSWORD", "pw")
}

func TestIntegrationPostgresTools(t *testing.T) {
	ctx := context.Background()
	m := NewManager(store.Limits{MaxOpen: 3}, time.Minute)
	defer m.Close()

	c, pw := pgConn()
	if err := m.Test(ctx, c, pw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	run(t, m, c, pw, `DROP SCHEMA IF EXISTS dp_tools CASCADE; CREATE SCHEMA dp_tools;
CREATE TABLE dp_tools.items (id int PRIMARY KEY, name text, price numeric(8,2), at timestamptz, ok boolean, data bytea, meta jsonb);
INSERT INTO dp_tools.items VALUES
  (1, 'Çay ''demli''', 12.5, '2026-01-02 03:04:05+00', true, '\x0102', '{"a": 1}'),
  (2, NULL, NULL, NULL, NULL, NULL, NULL);
CREATE TABLE dp_tools.tags (id bigint PRIMARY KEY, label varchar(20) NOT NULL);
INSERT INTO dp_tools.tags VALUES (10, 'x'), (11, 'y');`, false)

	// process list shows our own session
	pl, err := m.Processes(ctx, c, pw)
	if err != nil || pl.Self == "" || len(pl.Result.Rows) == 0 || !pl.CanCancel {
		t.Fatalf("processes: %+v %v", pl, err)
	}

	if _, err := m.KillProcess(ctx, c, pw, "1; DROP TABLE x", true); err == nil {
		t.Fatal("bad session id must be refused")
	}

	// users
	users, err := m.Users(ctx, c, pw)
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v %v", users, err)
	}

	grants, err := m.Grants(ctx, c, pw, "postgres", "")
	if err != nil || len(grants) == 0 {
		t.Fatalf("grants: %v %v", grants, err)
	}

	// SQL export runs back into a fresh schema
	var buf bytes.Buffer

	stats, err := m.Export(ctx, c, pw, ExportRequest{Schema: "dp_tools", Tables: []string{"items", "tags"}, Format: "sql", Structure: true, Data: true, Timeout: 10 * time.Second}, &buf)
	if err != nil || stats.Rows != 4 {
		t.Fatalf("export sql: %v %+v", err, stats)
	}

	script := strings.ReplaceAll(buf.String(), "dp_tools", "dp_tools_copy")
	run(t, m, c, pw, "DROP SCHEMA IF EXISTS dp_tools_copy CASCADE; CREATE SCHEMA dp_tools_copy;", false)

	res := run(t, m, c, pw, script, false)
	if res.FirstError() != "" {
		t.Fatalf("export script fails: %s\n%s", res.FirstError(), buf.String())
	}

	back := run(t, m, c, pw, "SELECT name, price::text, data, meta->>'a', ok FROM dp_tools_copy.items ORDER BY id", true).Results[0]
	if back.Rows[0][0] != "Çay 'demli'" || back.Rows[0][1] != "12.50" || back.Rows[0][2] != `\x0102` || back.Rows[0][3] != "1" || back.Rows[0][4] != true {
		t.Fatalf("round trip: %#v", back.Rows)
	}

	// CSV and JSON of several tables come as a zip
	buf.Reset()

	if _, err := m.Export(ctx, c, pw, ExportRequest{Schema: "dp_tools", Tables: []string{"items", "tags"}, Format: "csv", Timeout: 10 * time.Second}, &buf); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || len(zr.File) != 2 || zr.File[0].Name != "items.csv" {
		t.Fatalf("zip: %v", err)
	}

	buf.Reset()

	if _, err := m.Export(ctx, c, pw, ExportRequest{Schema: "dp_tools", Tables: []string{"items"}, Format: "json", Timeout: 10 * time.Second}, &buf); err != nil {
		t.Fatal(err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil || len(rows) != 2 || rows[0]["name"] != "Çay 'demli'" {
		t.Fatalf("json export: %v %s", err, buf.String())
	}

	// import creates a table and converts text values
	imp, err := m.Import(ctx, c, pw, ImportRequest{
		Schema: "dp_tools", Table: "imported", Columns: []string{"id", "name", "amount", "seen", "flag"},
		Create: []ColumnInfo{{Name: "id", Type: "bigint", PrimaryKey: true}, {Name: "name", Type: "text", Nullable: true},
			{Name: "amount", Type: "decimal", Nullable: true}, {Name: "seen", Type: "timestamp", Nullable: true}, {Name: "flag", Type: "bool", Nullable: true}},
		Rows:    [][]any{{"1", "a", "1.5", "2026-01-01 10:00:00", "yes"}, {"2", "", "", "", "0"}},
		Timeout: 10 * time.Second,
	})
	if err != nil || imp.Inserted != 2 {
		t.Fatalf("import: %v %+v", err, imp)
	}

	got := run(t, m, c, pw, "SELECT name, amount::text, flag FROM dp_tools.imported ORDER BY id", true).Results[0]
	if got.Rows[0][1] != "1.5" || got.Rows[0][2] != true || got.Rows[1][0] != "" || got.Rows[1][1] != nil || got.Rows[1][2] != false {
		t.Fatalf("imported: %#v", got.Rows)
	}

	// a failing batch leaves nothing behind
	if _, err := m.Import(ctx, c, pw, ImportRequest{Schema: "dp_tools", Table: "imported", Columns: []string{"id"}, Rows: [][]any{{"3"}, {"1"}}, Timeout: 5 * time.Second}); err == nil {
		t.Fatal("duplicate key must fail")
	}

	if n := run(t, m, c, pw, "SELECT count(*) FROM dp_tools.imported", true).Results[0].Rows[0][0]; n != int64(2) {
		t.Fatalf("failed batch kept rows: %v", n)
	}

	// transfer into SQLite, creating the tables
	dir := t.TempDir()
	if err := ConfigureFiles(dir); err != nil {
		t.Fatal(err)
	}

	seed, _ := sql.Open("sqlite", filepath.Join(dir, "copy.db"))
	if _, err := seed.Exec("CREATE TABLE seed (x int); DROP TABLE seed"); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()

	lite := &store.Connection{ID: "lite", DBType: "sqlite", Database: filepath.Join(dir, "copy.db"), UpdatedAt: 1}

	var progress []TransferProgress

	err = m.Transfer(ctx, c, pw, lite, "", TransferRequest{Schema: "dp_tools", Tables: []string{"items", "tags"}, Create: true, Data: true, Timeout: 10 * time.Second},
		func(p TransferProgress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range progress {
		if p.Error != "" {
			t.Fatalf("transfer %s: %s", p.Table, p.Error)
		}
	}

	db, _ := sql.Open("sqlite", filepath.Join(dir, "copy.db"))
	defer db.Close()

	var (
		name  string
		price float64
		blob  []byte
	)

	if err := db.QueryRow("SELECT name, price, data FROM items WHERE id = 1").Scan(&name, &price, &blob); err != nil || name != "Çay 'demli'" || price != 12.5 || len(blob) != 2 {
		t.Fatalf("sqlite copy: %v %q %v %v", err, name, price, blob)
	}

	// and back to postgres into an existing table, emptied first
	err = m.Transfer(ctx, lite, "", c, pw, TransferRequest{Tables: []string{"tags"}, TargetSchema: "dp_tools", Truncate: true, Data: true, Timeout: 10 * time.Second},
		func(p TransferProgress) {
			if p.Error != "" {
				t.Errorf("back: %s", p.Error)
			}
		})
	if err != nil {
		t.Fatal(err)
	}

	if n := run(t, m, c, pw, "SELECT count(*) FROM dp_tools.tags", true).Results[0].Rows[0][0]; n != int64(2) {
		t.Fatalf("tags after round trip: %v", n)
	}

	m.Disconnect(c)

	if !m.Connected(c) {
		t.Fatal("a shared pool stays open after disconnect")
	}

	run(t, m, c, pw, "DROP SCHEMA dp_tools CASCADE; DROP SCHEMA dp_tools_copy CASCADE", false)
	_ = os.Remove(filepath.Join(dir, "copy.db"))
}

func TestIntegrationMySQLTools(t *testing.T) {
	ctx := context.Background()
	m := NewManager(store.Limits{MaxOpen: 3}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "mytools", DBType: "mysql", Host: "127.0.0.1", Port: 53306, Database: "app", Username: "root", UpdatedAt: 1}
	pw := env("MYSQL_PASSWORD", "pw")

	var err error
	for range 30 {
		if err = m.Test(ctx, c, pw); err == nil {
			break
		}

		time.Sleep(2 * time.Second)
	}

	if err != nil {
		t.Skip("mysql is not reachable:", err)
	}

	pl, err := m.Processes(ctx, c, pw)
	if err != nil || pl.Self == "" || len(pl.Result.Rows) == 0 {
		t.Fatalf("processes: %+v %v", pl, err)
	}

	users, err := m.Users(ctx, c, pw)
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v %v", users, err)
	}

	grants, err := m.Grants(ctx, c, pw, "root", "%")
	if err != nil || len(grants) == 0 {
		t.Fatalf("grants: %v %v", grants, err)
	}

	// postgres → mysql transfer, creating the table
	pg, ppw := pgConn()
	if err := m.Test(ctx, pg, ppw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	run(t, m, pg, ppw, `DROP SCHEMA IF EXISTS dp_xfer CASCADE; CREATE SCHEMA dp_xfer;
CREATE TABLE dp_xfer.items (id int PRIMARY KEY, name varchar(30), price numeric(8,2), at timestamp, ok boolean, data bytea, uid uuid);
INSERT INTO dp_xfer.items VALUES (1, 'Çay', 12.5, '2026-01-02 03:04:05', true, '\x0102', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'), (2, NULL, NULL, NULL, false, NULL, NULL);`, false)
	run(t, m, c, pw, "DROP TABLE IF EXISTS items", false)

	err = m.Transfer(ctx, pg, ppw, c, pw, TransferRequest{Schema: "dp_xfer", Tables: []string{"items"}, TargetSchema: "app", Create: true, Data: true, Timeout: 20 * time.Second},
		func(p TransferProgress) {
			if p.Error != "" {
				t.Errorf("transfer: %s", p.Error)
			}
		})
	if err != nil {
		t.Fatal(err)
	}

	got := run(t, m, c, pw, "SELECT name, price, ok, HEX(data), uid FROM items ORDER BY id", true).Results[0]
	if got.Rows[0][0] != "Çay" || got.Rows[0][1] != "12.50" || got.Rows[0][3] != "0102" || got.Rows[0][4] != "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" {
		t.Fatalf("mysql copy: %#v", got.Rows)
	}

	var buf bytes.Buffer
	if _, err := m.Export(ctx, c, pw, ExportRequest{Schema: "app", Tables: []string{"items"}, Format: "sql", Structure: true, Data: true, Timeout: 10 * time.Second}, &buf); err != nil {
		t.Fatal(err)
	}

	run(t, m, c, pw, "DROP TABLE items", false)

	if res := run(t, m, c, pw, buf.String(), false); res.FirstError() != "" {
		t.Fatalf("mysql export script: %s\n%s", res.FirstError(), buf.String())
	}

	run(t, m, pg, ppw, "DROP SCHEMA dp_xfer CASCADE", false)
	run(t, m, c, pw, "DROP TABLE items", false)
}
