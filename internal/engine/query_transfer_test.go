package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestQueryTransfer(t *testing.T) {
	root := t.TempDir()
	for file, seed := range map[string]string{
		"source.db": `CREATE TABLE source (id INTEGER, name TEXT); INSERT INTO source VALUES (1,'ada'), (2,'linus'), (3,'grace');`,
		"target.db": `CREATE TABLE target (id INTEGER PRIMARY KEY, name TEXT NOT NULL); INSERT INTO target VALUES(2,'existing');`,
	} {
		db, err := sql.Open("sqlite", filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(seed); err != nil {
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck
	}
	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer ConfigureFiles("") //nolint:errcheck
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()
	src := &store.Connection{ID: "src", DBType: "sqlite", Database: "source.db", ReadOnly: true}
	dst := &store.Connection{ID: "dst", DBType: "sqlite", Database: "target.db"}
	req := QueryTransferRequest{SQL: "SELECT id, name FROM source WHERE id > ?", Args: []any{0}, Target: notebook.Transfer{ConnectionID: "dst", Table: "target", Batch: 3, SkipError: "UNIQUE constraint", Mappings: []notebook.Mapping{{Source: "id", Target: "id", Type: "number"}, {Source: "name", Target: "name", Type: "string", Template: `{{ . | upper }}`}}}}
	result, err := m.QueryTransfer(context.Background(), src, "", dst, "", req)
	if err != nil || result.Inserted != 2 || result.Skipped != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	got, err := m.Execute(context.Background(), dst, "", ExecRequest{SQL: "SELECT name FROM target ORDER BY id", ReadOnly: true, MaxRows: 10})
	if err != nil || got.FirstError() != "" || len(got.Results[0].Rows) != 3 || got.Results[0].Rows[0][0] != "ADA" {
		t.Fatalf("%+v %v", got, err)
	}
	// A non-matching error must roll back the whole target transaction,
	// including the optional DELETE and the earlier successful batches.
	req.Target.Wipe = true
	req.Target.SkipError = ""
	req.Target.Batch = 1
	req.Target.Mappings[1].Template = "{{ . | lower }}"
	req.SQL = "SELECT id, CASE WHEN id=3 THEN NULL ELSE name END AS name FROM source"
	req.Args = nil
	result, err = m.QueryTransfer(context.Background(), src, "", dst, "", req)
	if err == nil || result.Inserted != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	got, err = m.Execute(context.Background(), dst, "", ExecRequest{SQL: "SELECT name FROM target ORDER BY id", ReadOnly: true, MaxRows: 10})
	if err != nil || got.Results[0].Rows[0][0] != "ADA" {
		t.Fatal("failed transfer changed target", got, err)
	}
	req.SQL = "DELETE FROM source"
	if _, err := m.QueryTransfer(context.Background(), src, "", dst, "", req); err == nil {
		t.Fatal("write source allowed")
	}
	req.SQL = "SELECT id, name FROM source"
	req.Target.Mappings[1].Template = `{{- template "value" . -}}`
	if _, err := m.QueryTransfer(context.Background(), src, "", dst, "", req); err == nil {
		t.Fatal("recursive value template allowed")
	}
}

func TestParameterizedEngine(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:errcheck
	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer ConfigureFiles("") //nolint:errcheck
	m := NewManager(store.Limits{}, time.Minute)
	defer m.Close()
	c := &store.Connection{ID: "p", DBType: "sqlite", Database: "params.db"}
	dangerous := "'; DROP TABLE t; --"
	r, err := m.Execute(context.Background(), c, "", ExecRequest{SQL: "SELECT ? AS value", Args: []any{dangerous}, ReadOnly: true, MaxRows: 1})
	if err != nil || r.FirstError() != "" || r.Results[0].Rows[0][0] != dangerous {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := m.Execute(context.Background(), c, "", ExecRequest{SQL: "SELECT ?; SELECT 2", Args: []any{dangerous}, ReadOnly: true, MaxRows: 1}); err == nil {
		t.Fatal("multiple parameterized statements accepted")
	}
}

func TestTransferMappings(t *testing.T) {
	columns := []string{"id", "name", "note"}
	targets := func(ms []notebook.Mapping) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Source+">"+m.Target)
		}
		return out
	}
	got, err := transferMappings(notebook.Transfer{KeepUnmapped: true, Mappings: []notebook.Mapping{{Source: "name", Target: "first"}, {Source: "name", Target: "last"}}}, columns)
	if err != nil || !slices.Equal(targets(got), []string{"id>id", "name>first", "name>last", "note>note"}) {
		t.Fatalf("%v %v", targets(got), err)
	}
	if _, err := transferMappings(notebook.Transfer{KeepUnmapped: true, Mappings: []notebook.Mapping{{Source: "name", Target: "id"}}}, columns); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if _, err := transferMappings(notebook.Transfer{KeepUnmapped: true, Mappings: []notebook.Mapping{{Source: "missing", Target: "x"}}}, columns); err == nil {
		t.Fatal("missing source accepted")
	}
	got, err = transferMappings(notebook.Transfer{}, columns)
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
}
