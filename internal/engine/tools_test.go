package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"CREATE USER bob IDENTIFIED BY 'se''cret';":               "CREATE USER bob IDENTIFIED BY '***';",
		`CREATE USER app IDENTIFIED BY "pw"`:                      `CREATE USER app IDENTIFIED BY "***"`,
		"ALTER ROLE x WITH LOGIN PASSWORD 'a'; SELECT 'a'":        "ALTER ROLE x WITH LOGIN PASSWORD '***'; SELECT 'a'",
		"CREATE LOGIN l WITH PASSWORD = 'x1', CHECK_POLICY = OFF": "CREATE LOGIN l WITH PASSWORD = '***', CHECK_POLICY = OFF",
		"select 1": "select 1",
	}

	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapType(t *testing.T) {
	cases := []struct{ from, to, typ, want string }{
		{"postgres", "postgres", "jsonb", "jsonb"},
		{"postgres", "mysql", "text[]", "longtext"},
		{"generic", "postgres", "varchar(1); DROP TABLE x; --", "varchar(1)"},
		{"postgres", "mysql", "numeric(10, 2)", "decimal(10,2)"},
		{"postgres", "mssql", "_int4", "nvarchar(max)"},
		{"clickhouse", "postgres", "Array(String)", "text"},
		{"postgres", "mysql", "character varying(40)", "varchar(40)"},
		{"postgres", "mssql", "text", "nvarchar(max)"},
		{"mysql", "postgres", "tinyint(1)", "smallint"},
		{"mysql", "postgres", "datetime", "timestamp"},
		{"mssql", "postgres", "uniqueidentifier", "uuid"},
		{"postgres", "oracle", "numeric(10,2)", "NUMBER(10,2)"},
		{"postgres", "sqlite", "bigint", "INTEGER"},
		{"postgres", "clickhouse", "timestamp with time zone", "DateTime64(6)"},
		{"clickhouse", "postgres", "Nullable(Int32)", "integer"},
		{"generic", "mysql", "bool", "tinyint(1)"},
	}

	for _, c := range cases {
		if got := MapType(c.from, c.to, c.typ); got != c.want {
			t.Errorf("MapType(%s→%s, %q) = %q, want %q", c.from, c.to, c.typ, got, c.want)
		}
	}
}

func TestLiteral(t *testing.T) {
	pg, _ := GetDriver("postgres")
	my, _ := GetDriver("mysql")
	ms, _ := GetDriver("mssql")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	cases := []struct {
		d    Driver
		typ  string
		v    any
		want string
	}{
		{pg, "text", "it's", "'it''s'"},
		{my, "text", `a\b'`, `'a\\b'''`},
		{ms, "nvarchar", "ç", "N'ç'"},
		{pg, "bytea", []byte{1, 2}, `'\x0102'::bytea`},
		{ms, "varbinary", []byte{1, 2}, "0x0102"},
		{pg, "bool", true, "true"},
		{my, "tinyint", true, "1"},
		{pg, "date", at, "'2026-01-02'"},
		{pg, "timestamptz", at, "'2026-01-02 03:04:05+00:00'"},
		{pg, "int8", nil, "NULL"},
	}

	for _, c := range cases {
		if got := Literal(c.d, c.typ, c.v); got != c.want {
			t.Errorf("Literal(%s, %s, %#v) = %s, want %s", c.d.Type(), c.typ, c.v, got, c.want)
		}
	}
}

func TestKillSQL(t *testing.T) {
	for _, typ := range []string{"postgres", "mysql", "mssql", "oracle", "clickhouse", "trino"} {
		d, _ := GetDriver(typ)
		p := d.(ProcessLister)

		for _, bad := range []string{"", "1; DROP TABLE t", "' OR 1=1 --", "a b"} {
			if stmt, err := p.KillSQL(bad, true); err == nil {
				t.Errorf("%s accepted %q: %s", typ, bad, stmt)
			}
		}
	}

	pg, _ := GetDriver("postgres")
	if s, _ := pg.(ProcessLister).KillSQL("42", false); s != "SELECT pg_cancel_backend(42)" {
		t.Fatal(s)
	}

	ora, _ := GetDriver("oracle")
	if s, _ := ora.(ProcessLister).KillSQL("12,345", true); !strings.Contains(s, "'12,345'") {
		t.Fatal(s)
	}
}

func TestCreateTableSQL(t *testing.T) {
	ch, _ := GetDriver("clickhouse")
	got := CreateTableSQL("postgres", ch, "db", "t", []ColumnInfo{{Name: "id", Type: "bigint", PrimaryKey: true}, {Name: "n", Type: "text", Nullable: true}})

	want := "CREATE TABLE `db`.`t` (\n  `id` Int64,\n  `n` Nullable(String)\n) ENGINE = MergeTree ORDER BY (`id`)"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestTransferSamePoolOfOne(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 1}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "x", DBType: "postgres"}

	err := m.Transfer(context.Background(), c, "", c, "", TransferRequest{Tables: []string{"t"}, Data: true}, func(TransferProgress) {})
	if err == nil || !strings.Contains(err.Error(), "at least two") {
		t.Fatalf("got %v", err)
	}
}
