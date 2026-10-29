package notebook

import (
	"fmt"
	"strings"
	"testing"
	"text/template"
)

func TestMugoFunctions(t *testing.T) {
	ph := func(n int) string { return fmt.Sprintf("$%d", n) }
	data := map[string]any{"name": "  ada lovelace  ", "id": "42", "price": "19.90", "tags": []any{"b", "a"}, "doc": `{"status":"active"}`}

	tests := []struct {
		name, sql string
		args      []any
	}{
		{"sprig pipeline", `SELECT {{ .data.name | trim | upper | param }}`, []any{"ADA LOVELACE"}},
		{"sprig default", `SELECT {{ param (.data.missing | default "none") }}`, nil},
		{"cast namespace", `SELECT {{ param (cast.ToInt .data.id) }}`, []any{int64(42)}},
		{"float arithmetic", `SELECT {{ param (mulf .data.price 3) }}`, []any{59.7}},
		{"integer arithmetic", `SELECT {{ param (add .data.id 1) }}`, []any{int64(43)}},
		{"json decode", `SELECT {{ param (fromJson .data.doc).status }}`, []any{"active"}},
		{"join list", `SELECT {{ param (.data.tags | sortAlpha | join ",") }}`, []any{"a,b"}},
		{"crypto hash", `SELECT {{ param (crypto.SHA256 "x") }}`, []any{"2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"}},
		{"bounded replace", `SELECT {{ param (replace " " "_" .data.name | trim) }}`, []any{"__ada_lovelace__"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := Render(tt.sql, data, nil, ph)
			if tt.name == "sprig default" {
				// missingkey=error refuses unknown keys even with default;
				// templates must use index or hasKey for optional values.
				if err == nil {
					t.Fatal("missing key accepted")
				}

				return
			}

			if err != nil || sql != "SELECT $1" || fmt.Sprint(args) != fmt.Sprint(tt.args) {
				t.Fatalf("%q %#v %v", sql, args, err)
			}
		})
	}

	_, args, err := Render(`SELECT {{ param (get .data "missing" | default "none") }}`, data, nil, ph)
	if err != nil || args[0] != "none" {
		t.Fatalf("optional value: %#v %v", args, err)
	}
}

// Functions mugo offers that reach the host, run commands, read files or
// grow output without bound must never be callable from a notebook.
func TestMugoDangerousFunctionsBlocked(t *testing.T) {
	ph := func(int) string { return "?" }

	for _, sql := range []string{
		`SELECT {{ param (env "HOME") }}`,
		`SELECT {{ param (expandenv "$HOME") }}`,
		`SELECT {{ param (getHostByName "example.com") }}`,
		`SELECT {{ param (exec.Exec "id") }}`,
		`SELECT {{ param (file.Read "/etc/passwd") }}`,
		`SELECT {{ param (os.ReadFile "/etc/passwd") }}`,
		`SELECT {{ param (execTemplate "sql" .) }}`,
		`SELECT {{ param (repeat 100000000 "x") }}`,
		`SELECT {{ range (until 100000000) }}{{ param . }}{{ end }}`,
		`SELECT {{ param (seq 1 100000000) }}`,
		`SELECT {{ param (indent 100000000 "x") }}`,
		`SELECT {{ param (genPrivateKey "rsa") }}`,
		`SELECT {{ param (randBytes 100000000) }}`,
		`SELECT {{ param (math.Pow "10" "100000000") }}`,
		`SELECT {{ param (math.Add "1e999999999" "1") }}`,
		`SELECT {{ param (math.Round "1" "1000000000") }}`,
		`SELECT {{ param (printf "%s" "x") }}`,
		`SELECT {{ $c := cast }}{{ param ($c.ToInt "1") }}`,
		`SELECT {{ param (cast.ToStringMapE .data) }}`,
		`SELECT {{ .data.name | upper }}`,
		`SELECT {{ param (join .data.sep .data.many) }}`,
	} {
		if _, _, err := Render(sql, map[string]any{"name": "x", "sep": strings.Repeat("x", 1<<20), "many": []any{"a", "b", "c"}}, nil, ph); err == nil {
			t.Errorf("accepted %s", sql)
		}
	}
}

func TestMappingTemplateUsesMugo(t *testing.T) {
	parse := func(s string) (*template.Template, error) {
		tpl, err := template.New("value").Funcs(TemplateFuncs()).Parse(s)
		if err != nil {
			return nil, err
		}

		return tpl, ValidateMappingTemplate(tpl.Tree.Root) //nolint:staticcheck
	}

	tpl, err := parse(`{{ . | trim | snakecase }}`)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	if err := tpl.Execute(&b, " Ada Lovelace "); err != nil || b.String() != "ada_lovelace" {
		t.Fatalf("%q %v", b.String(), err)
	}

	for _, bad := range []string{`{{ env "HOME" }}`, `{{ range until 5 }}{{ . }}{{ end }}`, `{{ repeat 5 . }}`, `{{ exec.Exec "id" }}`} {
		if _, err := parse(bad); err == nil {
			t.Errorf("mapping accepted %s", bad)
		}
	}
}
