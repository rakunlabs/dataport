package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func testCell(path string, deps ...string) Cell {
	return Cell{ID: path, Path: path, ConnectionID: "db", SQL: "select 1", Enabled: true, Dependencies: deps}
}

func TestDependencyPlan(t *testing.T) {
	d := Document{Cells: []Cell{testCell("last", "middle", "first"), testCell("middle", "first"), testCell("first")}}
	p, err := d.Plan("", true)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range p {
		paths = append(paths, c.Path)
	}
	if !reflect.DeepEqual(paths, []string{"first", "middle", "last"}) {
		t.Fatal(paths)
	}
	p, err = d.Plan("middle", true)
	if err != nil || len(p) != 2 {
		t.Fatalf("%v %v", p, err)
	}
	d.Cells[2].Dependencies = []string{"last"}
	if err := d.Validate(); err == nil {
		t.Fatal("cycle accepted")
	}
	d.Cells[2].Dependencies = nil
	d.Cells[2].Enabled = false
	if _, err := d.Plan("last", true); err == nil {
		t.Fatal("disabled dependency accepted")
	}
	d.Cells[2].Enabled = true
	d.Cells[2].Write = true
	if err := d.Validate(); err == nil {
		t.Fatal("write dependency accepted")
	}
	d.Cells[2].Write = false
	d.Cells[0].Dependencies = []string{"missing"}
	if err := d.Validate(); err == nil {
		t.Fatal("missing dependency accepted")
	}
}

func TestValidationAndInputs(t *testing.T) {
	d := Document{Cells: []Cell{testCell("a")}, Parameters: []Parameter{{Name: "id", Type: "number", Required: true}, {Name: "status", Type: "string", Default: json.RawMessage(`"active"`)}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	values, err := d.Inputs(map[string]any{"id": float64(2)})
	if err != nil || values["status"] != "active" {
		t.Fatalf("%v %v", values, err)
	}
	for _, in := range []map[string]any{{}, {"id": "2"}, {"id": 2.0, "extra": true}, {"id": nil}} {
		if _, err := d.Inputs(in); err == nil {
			t.Fatal("accepted", in)
		}
	}
	d.Cells = append(d.Cells, testCell("a"))
	if err := d.Validate(); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestSafeRender(t *testing.T) {
	data := map[string]any{"name": "'; DROP TABLE users; --", "ids": []any{1.0, 2.0}, "table": `a"b`}
	ph := func(n int) string { return fmt.Sprintf("$%d", n) }
	sql, args, err := Render(`SELECT * FROM "a""b" WHERE name = {{ param .data.name }} AND id IN ({{range $i, $v := .data.ids}}{{if $i}},{{end}}{{param $v}}{{end}})`, data, nil, ph)
	if err != nil || sql != `SELECT * FROM "a""b" WHERE name = $1 AND id IN ($2,$3)` || len(args) != 3 || args[0] != data["name"] {
		t.Fatalf("%q %#v %v", sql, args, err)
	}
	for _, unsafe := range []string{`SELECT {{ .data.name }}`, `SELECT {{ printf "%s" .data.name }}`, `{{define "x"}}{{template "x"}}{{end}}{{template "x"}}`, `{{ template "sql" }}`, `{{ env "HOME" }}`, `SELECT '{{ ident .data.name }}'`, `-- {{ ident .data.name }}`, `SELECT {{ param (printf "%1000000000s" .data.name) }}`, `{{ range 1000000000000 }}{{end}}SELECT 1`} {
		if _, _, err := Render(unsafe, data, nil, ph); err == nil {
			t.Fatal("unsafe template accepted", unsafe)
		}
	}
	if _, _, err := Render(`SELECT {{ param .data.missing }}`, data, nil, ph); err == nil {
		t.Fatal("missing value accepted")
	}
}

func TestRenderRangeBindsValues(t *testing.T) {
	data := map[string]any{"ids": []any{"x", "y"}, "named": map[string]any{"b": 2.0, "a": 1.0}}
	ph := func(int) string { return "?" }
	for _, tt := range []struct {
		sql  string
		args []any
	}{
		{`{{range .data.ids}}{{param .}}{{end}}`, []any{"x", "y"}},
		{`{{range $v := .data.ids}}{{param $v}}{{end}}`, []any{"x", "y"}},
		{`{{range $i, $v := .data.ids}}{{param $i}}{{param $v}}{{end}}`, []any{int64(0), "x", int64(1), "y"}},
		{`{{range .data.named}}{{param .}}{{end}}`, []any{1.0, 2.0}},
		{`{{range $v := .data.named}}{{param $v}}{{end}}`, []any{1.0, 2.0}},
		{`{{range $k, $v := .data.named}}{{param $k}}{{param $v}}{{end}}`, []any{"a", 1.0, "b", 2.0}},
	} {
		_, args, err := Render(tt.sql, data, nil, ph)
		if err != nil || !reflect.DeepEqual(args, tt.args) {
			t.Errorf("%s: %#v %v", tt.sql, args, err)
		}
	}
}

func TestRenderRefusesUnprintedParam(t *testing.T) {
	data := map[string]any{"id": 1.0}
	ph := func(int) string { return "?" }
	for _, sql := range []string{
		`SELECT {{ $x := param .data.id }}1`,
		`SELECT {{ if param .data.id }}1{{ end }}`,
		`SELECT {{ with param .data.id }}1{{ end }}`,
		`SELECT {{ range (list (param .data.id)) }}1{{ end }}`,
		`SELECT {{ param (param .data.id) }}`,
		`SELECT {{ param .data.id | param }}`,
	} {
		if _, _, err := Render(sql, data, nil, ph); err == nil {
			t.Errorf("accepted %s", sql)
		}
	}
}

func TestRenderCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, _, err := RenderContext(ctx, `SELECT {{range .data.ids}}{{param .}}{{end}}`, map[string]any{"ids": []any{1.0, 2.0, 3.0}}, nil, func(n int) string { cancel(); return "?" })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("render did not stop on cancellation: %v", err)
	}
}
