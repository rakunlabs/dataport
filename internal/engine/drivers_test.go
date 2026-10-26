package engine

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestOracleSplit(t *testing.T) {
	d := oracleDriver{}

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", "select 1 from dual; select 2 from dual;", []string{"select 1 from dual", "select 2 from dual"}},
		{"block", "begin\n  null;\n  null;\nend;\n/\nselect 1 from dual", []string{"begin\n  null;\n  null;\nend;", "select 1 from dual"}},
		{"block at end", "declare x number; begin x := 1; end;", []string{"declare x number; begin x := 1; end;"}},
		{"procedure", "create or replace procedure p as begin null; end;\n/\n", []string{"create or replace procedure p as begin null; end;"}},
		{"q quote", "select q'[a;b]' from dual; select 2 from dual", []string{"select q'[a;b]' from dual", "select 2 from dual"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Split(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Split() = %#v, want %#v", got, tt.want)
			}
		})
	}

	if _, err := Validate(d, "begin null; end;", false); err != nil {
		t.Fatalf("a PL/SQL block is not a transaction: %v", err)
	}

	if _, err := Validate(d, "select * from t@remote", true); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("database link on read-only: %v", err)
	}

	if _, err := Validate(d, "select 'a@b' from dual", true); err != nil {
		t.Fatalf("@ in a string is not a link: %v", err)
	}
}

func TestODBCConnString(t *testing.T) {
	got := odbcConnString("DSN=bas;", "me", "p;w")
	if want := "DSN=bas;UID=me;PWD={p;w}"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	if got := odbcConnString("DSN=x", "", "a}b c"); got != "DSN=x;PWD={a}}b c}" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveFile(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	outside := t.TempDir()

	for _, p := range []string{filepath.Join(root, "a.db"), filepath.Join(data, "dataport.db"), filepath.Join(outside, "x.db")} {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, nil, 0o600)
	}

	_ = os.Symlink(filepath.Join(outside, "x.db"), filepath.Join(root, "link.db"))

	if err := ConfigureFiles(root, data); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	ok := []string{"a.db", filepath.Join(root, "a.db")}
	bad := []string{"../x.db", filepath.Join(outside, "x.db"), "link.db", "data/dataport.db", "missing.db", "a.db?mode=rwc", "a%2edb", ""}

	for _, p := range ok {
		if _, err := resolveFile(p, false); err != nil {
			t.Errorf("resolveFile(%q) = %v, want ok", p, err)
		}
	}

	for _, p := range bad {
		if got, err := resolveFile(p, false); err == nil {
			t.Errorf("resolveFile(%q) = %q, want an error", p, got)
		}
	}

	_ = ConfigureFiles("")

	if _, err := resolveFile("a.db", false); !errors.Is(err, ErrFilesDisabled) {
		t.Errorf("disabled: %v", err)
	}
}

func TestSQLiteEngine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "shop.db")

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := seed.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER REFERENCES customers(id), total REAL);
CREATE INDEX orders_customer ON orders(customer_id);
INSERT INTO customers VALUES (1, 'Ada'), (2, 'Linus');`); err != nil {
		t.Fatal(err)
	}

	_ = seed.Close()

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	ctx := context.Background()
	c := &store.Connection{ID: "s", DBType: "sqlite", Database: "shop.db", UpdatedAt: 1}

	if err := CheckConnection(c); err != nil {
		t.Fatal(err)
	}

	if err := m.Test(ctx, c, ""); err != nil {
		t.Fatal(err)
	}

	run := func(q string, ro bool) (*ExecResult, error) {
		return m.Execute(ctx, c, "", ExecRequest{SQL: q, ReadOnly: ro, MaxRows: 10, Timeout: 5 * time.Second})
	}

	res, err := run("select id, name from customers order by id", true)
	if err != nil || res.FirstError() != "" || len(res.Results[0].Rows) != 2 {
		t.Fatalf("select: %v %+v", err, res)
	}

	// The keyword check lets a WITH through; query_only stops the write.
	res, err = run("with x as (select 1) insert into customers select 3, 'x' from x", true)
	if err == nil && res.FirstError() == "" {
		t.Fatal("read-only run wrote")
	}

	if _, err := run("attach database 'other.db' as o", false); err == nil {
		t.Fatal("ATTACH was allowed")
	}

	res, err = run("insert into orders (customer_id, total) values (1, 9.5); update customers set name = 'Ada L.' where id = 1", false)
	if err != nil || !res.Committed {
		t.Fatalf("write: %v %+v", err, res)
	}

	schemas, err := m.Schemas(ctx, c, "")
	if err != nil || !reflect.DeepEqual(schemas, []string{"main"}) {
		t.Fatalf("schemas: %v %v", schemas, err)
	}

	objects, err := m.Objects(ctx, c, "", "main")
	if err != nil || len(objects) != 2 {
		t.Fatalf("objects: %v %v", objects, err)
	}

	st, err := m.Structure(ctx, c, "", "main", "orders")
	if err != nil {
		t.Fatal(err)
	}

	if !st.Columns[0].PrimaryKey || len(st.ForeignKeys) != 1 || st.ForeignKeys[0].RefTable != "customers" || len(st.Indexes) == 0 || st.DDL == "" {
		t.Fatalf("structure: %+v", st)
	}

	edit, err := m.ApplyEdits(ctx, c, "", EditRequest{
		Schema: "main", Table: "customers", Timeout: 5 * time.Second,
		Edits: []RowEdit{{Op: "update", Key: map[string]any{"id": 2}, Values: map[string]any{"name": "Linus T."}}},
	})
	if err != nil || edit.Affected != 1 {
		t.Fatalf("edit: %v %+v", err, edit)
	}
}
