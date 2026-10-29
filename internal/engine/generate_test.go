package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestGenerate(t *testing.T) {
	root := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(root, "gen.db"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`PRAGMA foreign_keys = ON;
CREATE TABLE customers (id INTEGER PRIMARY KEY, code VARCHAR(5) NOT NULL);
INSERT INTO customers (code) VALUES ('a'), ('b'), ('c');
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  email VARCHAR(80) NOT NULL,
  first_name TEXT,
  status VARCHAR(10) DEFAULT 'new',
  total NUMERIC(8,2),
  paid BOOLEAN,
  created_at TIMESTAMP,
  note TEXT,
  label TEXT,
  doubled INTEGER GENERATED ALWAYS AS (customer_id * 2) VIRTUAL
);`)
	if err != nil {
		t.Fatal(err)
	}

	db.Close() //nolint:errcheck

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer ConfigureFiles("") //nolint:errcheck

	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "gen", DBType: "sqlite", Database: "gen.db"}
	ctx := context.Background()

	req := GenerateRequest{
		Table: "orders", Rows: 250, Seed: 7,
		Columns: []GenColumn{
			{Name: "note", Generator: "null"},
			{Name: "label", Generator: "template", Template: `{{ .row.first_name | upper }}-{{ randInt 1 9 }}-{{ fake "word" }}`},
		},
	}

	plan, err := m.GeneratePreview(ctx, c, "", req)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"id": "skip", "customer_id": "fk", "email": "email", "first_name": "first_name", "status": "skip",
		"total": "decimal", "paid": "bool", "created_at": "timestamp", "note": "null", "label": "template", "doubled": "skip",
	}
	for _, col := range plan.Columns {
		if want[col.Name] != col.Generator {
			t.Errorf("column %s: generator %q, want %q", col.Name, col.Generator, want[col.Name])
		}
	}

	if len(plan.Rows) != previewRows || len(plan.Rows[0]) != 8 {
		t.Fatalf("preview rows: %d × %d", len(plan.Rows), len(plan.Rows[0]))
	}

	again, err := m.GeneratePreview(ctx, c, "", req)
	if err != nil || again.Rows[3][2] != plan.Rows[3][2] {
		t.Fatalf("the same seed should give the same rows: %v %v", again.Rows[3], plan.Rows[3])
	}

	var progress int64

	n, err := m.Generate(ctx, c, "", req, func(p GenerateProgress) { progress = p.Rows })
	if err != nil || n != 250 {
		t.Fatalf("generate: %d %v", n, err)
	}

	_ = progress

	got, err := m.Execute(ctx, c, "", ExecRequest{
		SQL: `SELECT COUNT(*), COUNT(DISTINCT customer_id), SUM(customer_id NOT IN (SELECT id FROM customers)),
MIN(status), MAX(length(email)), SUM(note IS NULL), MIN(label) FROM orders`,
		ReadOnly: true, MaxRows: 1,
	})
	if err != nil || got.FirstError() != "" {
		t.Fatalf("%+v %v", got, err)
	}

	row := got.Results[0].Rows[0]
	if row[0] != int64(250) || row[1] != int64(3) || row[2] != int64(0) || row[3] != "new" || row[5] != int64(250) {
		t.Fatalf("unexpected rows: %v", row)
	}

	if label, _ := row[6].(string); !strings.Contains(label, "-") || label != strings.ToUpper(label[:strings.Index(label, "-")])+label[strings.Index(label, "-"):] {
		t.Fatalf("label: %v", row[6])
	}

	// A generator that cannot fit the column is refused before anything runs.
	bad := []GenerateRequest{
		{Table: "orders", Rows: 1, Columns: []GenColumn{{Name: "email", Generator: "null"}}},
		{Table: "orders", Rows: 1, Columns: []GenColumn{{Name: "doubled", Generator: "int"}}},
		{Table: "orders", Rows: 1, Columns: []GenColumn{{Name: "nope", Generator: "int"}}},
		{Table: "orders", Rows: 1, Columns: []GenColumn{{Name: "label", Generator: "template", Template: `{{ env "HOME" }}`}}},
		{Table: "orders", Rows: 1, Columns: []GenColumn{{Name: "label", Generator: "template", Template: `{{ range .row }}x{{ end }}`}}},
		{Table: "orders", Rows: MaxGenerateRows + 1},
	}
	for i, r := range bad {
		if _, err := m.Generate(ctx, c, "", r, func(GenerateProgress) {}); err == nil {
			t.Errorf("bad request %d was accepted", i)
		}
	}

	// Pools made in another language replace the built-in values, also
	// inside templates; generators without pools keep theirs.
	pooled := GenerateRequest{
		Table: "orders", Rows: 20, Seed: 3,
		Columns: []GenColumn{{Name: "label", Generator: "template", Template: `{{ fake "city" }}`}},
		Pools:   map[string][]string{"first_name": {"Ayşe", "Mehmet"}, "city": {"İzmir"}},
	}

	lp, err := m.GeneratePreview(ctx, c, "", pooled)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range lp.Rows {
		if r[2] != "Ayşe" && r[2] != "Mehmet" || r[len(r)-1] != "İzmir" {
			t.Fatalf("pooled row: %v", r)
		}
	}

	pooled.Pools = map[string][]string{"email": {"x@y.z"}}
	if _, err := m.GeneratePreview(ctx, c, "", pooled); err == nil {
		t.Error("a pool for a generator that takes none was accepted")
	}
}

func TestGuessColumn(t *testing.T) {
	cases := []struct {
		col  ColumnInfo
		want string
	}{
		{ColumnInfo{Name: "id", Type: "bigint", PrimaryKey: true, Default: "nextval('x_seq'::regclass)"}, "skip"},
		{ColumnInfo{Name: "id", Type: "uuid", PrimaryKey: true}, "uuid"},
		{ColumnInfo{Name: "id", Type: "int", PrimaryKey: true}, "sequence"},
		{ColumnInfo{Name: "EmailAddress", Type: "nvarchar(120)"}, "email"},
		{ColumnInfo{Name: "phone_number", Type: "varchar(20)"}, "phone"},
		{ColumnInfo{Name: "country", Type: "char(2)"}, "country_code"},
		{ColumnInfo{Name: "description", Type: "varchar(100)"}, "sentence"},
		{ColumnInfo{Name: "description", Type: "text"}, "paragraph"},
		{ColumnInfo{Name: "is_active", Type: "boolean"}, "bool"},
		{ColumnInfo{Name: "birth_date", Type: "date"}, "date"},
		{ColumnInfo{Name: "price", Type: "numeric(10,2)"}, "decimal"},
		{ColumnInfo{Name: "created_at", Type: "timestamp", Default: "now()"}, "skip"},
		{ColumnInfo{Name: "payload", Type: "jsonb"}, "json"},
		{ColumnInfo{Name: "x", Type: "varchar(8)"}, "word"},
		{ColumnInfo{Name: "id", Type: "int", Extra: "AUTO_INCREMENT", PrimaryKey: true}, "skip"},
	}

	for _, c := range cases {
		if got := GuessColumn("postgres", c.col, "", nil, true); got.Generator != c.want {
			t.Errorf("%s %s: %q, want %q", c.col.Name, c.col.Type, got.Generator, c.want)
		}
	}
}

func TestFitValue(t *testing.T) {
	cut := fitValue("postgres", "varchar(5)", "varchar", 5)
	if got := cut("abcdefgh"); got != "abcde" {
		t.Errorf("cut: %v", got)
	}

	if got := fitValue("mysql", "tinyint(1)", "bool", 0)(true); got != 1 {
		t.Errorf("mysql bool: %v", got)
	}

	if got := fitValue("postgres", "integer", "int", 0)("42"); got != int64(42) {
		t.Errorf("int: %v %T", got, got)
	}

	day := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if got := fitValue("sqlite", "DATE", "date", 0)(day); got != "2001-02-03" {
		t.Errorf("sqlite date: %v", got)
	}

	if got := fitValue("sqlite", "TIMESTAMP", "timestamp", 0)(day); got != "2001-02-03 04:05:06" {
		t.Errorf("sqlite timestamp: %v", got)
	}
}
