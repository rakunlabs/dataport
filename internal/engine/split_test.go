package engine

import (
	"errors"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		in      string
		want    []string
	}{
		{"single", DialectPostgres, "select 1", []string{"select 1"}},
		{"two", DialectPostgres, "select 1; select 2;", []string{"select 1", "select 2"}},
		{"empty parts", DialectPostgres, ";; select 1 ;  ;", []string{"select 1"}},
		{"semicolon in string", DialectPostgres, "select 'a;b'; select 2", []string{"select 'a;b'", "select 2"}},
		{"doubled quote", DialectPostgres, "select 'it''s;'; select 2", []string{"select 'it''s;'", "select 2"}},
		{"identifier", DialectPostgres, `select "a;b" from t; select 2`, []string{`select "a;b" from t`, "select 2"}},
		{"line comment", DialectPostgres, "select 1 -- x; y\n; select 2", []string{"select 1 -- x; y", "select 2"}},
		{"block comment", DialectPostgres, "select /* ; */ 1; select 2", []string{"select /* ; */ 1", "select 2"}},
		{"nested block pg", DialectPostgres, "select /* a /* ; */ ; */ 1; select 2", []string{"select /* a /* ; */ ; */ 1", "select 2"}},
		{"only comment", DialectPostgres, "-- nothing here\n/* x */", nil},
		{"dollar", DialectPostgres, "do $$ begin perform 1; end $$; select 2", []string{"do $$ begin perform 1; end $$", "select 2"}},
		{"dollar tag", DialectPostgres, "select $fn$ a;b $fn$; select 2", []string{"select $fn$ a;b $fn$", "select 2"}},
		{"positional param not dollar quote", DialectPostgres, "select $1; select 2", []string{"select $1", "select 2"}},
		{"pg backslash is literal", DialectPostgres, `select 'a\'; select 2`, []string{`select 'a\'`, "select 2"}},
		{"pg escape string", DialectPostgres, `select E'a\';b'; select 2`, []string{`select E'a\';b'`, "select 2"}},
		{"mysql backslash", DialectMySQL, `select 'a\';b'; select 2`, []string{`select 'a\';b'`, "select 2"}},
		{"mysql backtick", DialectMySQL, "select `a;b` from t; select 2", []string{"select `a;b` from t", "select 2"}},
		{"mysql hash comment", DialectMySQL, "select 1 # ; x\n; select 2", []string{"select 1 # ; x", "select 2"}},
		{"mssql bracket", DialectMSSQL, "select [a;b]] c] from t; select 2", []string{"select [a;b]] c] from t", "select 2"}},
		{"mssql backslash literal", DialectMSSQL, `select 'a\'; select 2`, []string{`select 'a\'`, "select 2"}},
		{"clickhouse backslash", DialectClickHouse, `select 'a\';b'; select 2`, []string{`select 'a\';b'`, "select 2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.in, tt.dialect)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Split(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFirstKeyword(t *testing.T) {
	tests := map[string]string{
		"select 1":                      "SELECT",
		"  -- c\n /* x */ (select 1)":   "SELECT",
		"with a as (select 1) select *": "WITH",
		"Insert into t values (1)":      "INSERT",
		"":                              "",
	}

	for in, want := range tests {
		if got := FirstKeyword(in, DialectPostgres); got != want {
			t.Errorf("FirstKeyword(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	pg, _ := GetDriver("postgres")
	my, _ := GetDriver("mysql")
	ms, _ := GetDriver("mssql")

	tests := []struct {
		name     string
		d        Driver
		sql      string
		readOnly bool
		wantErr  bool
		readErr  bool
	}{
		{"select ro", pg, "select 1; select 2", true, false, false},
		{"update ro", pg, "select 1; update t set a = 1", true, true, true},
		{"update rw", pg, "update t set a = 1", false, false, false},
		{"commit blocked", pg, "update t set a = 1; commit; update t set a = 2", false, true, false},
		{"begin blocked", pg, "begin; select 1", true, true, false},
		{"set ro blocked", pg, "set transaction read write", true, true, true},
		{"empty", pg, " -- nothing ", true, true, false},
		{"mysql outfile", my, "select * from t into outfile '/tmp/x'", true, true, true},
		{"mysql versioned comment", my, "select 1 /*!50000 , sleep(1) */", true, true, true},
		{"mysql show", my, "show tables", true, false, false},
		{"mssql select into", ms, "select * into t2 from t", true, true, true},
		{"mssql exec", ms, "select 1; exec sp_who", true, true, true},
		{"mssql select", ms, "select top 10 * from [dbo].[t]", true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.d, tt.sql, tt.readOnly)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.readErr && !errors.Is(err, ErrReadOnly) {
				t.Fatalf("Validate() error = %v, want ErrReadOnly", err)
			}
		})
	}
}

func TestReturnsRows(t *testing.T) {
	pg, _ := GetDriver("postgres")
	my, _ := GetDriver("mysql")

	risks := []struct {
		d    Driver
		sql  string
		want Risk
	}{
		{pg, "select 1; show search_path", RiskRead},
		{pg, "select 1; update t set a = 1", RiskWrite},
		{pg, "with x as (delete from t returning *) select * from x", RiskWrite},
		{pg, "insert into t values (1); drop table t", RiskDDL},
		{pg, "set search_path = x", RiskWrite},
		{my, "select * from t into outfile '/x'", RiskWrite},
	}

	for _, tt := range risks {
		if got := Classify(tt.d, tt.sql); got != tt.want {
			t.Errorf("Classify(%q) = %s, want %s", tt.sql, got, tt.want)
		}
	}

	if !returnsRows("insert into t values (1) returning id", "INSERT") {
		t.Error("insert returning should return rows")
	}

	if returnsRows("insert into t (returning_x) values (1)", "INSERT") {
		t.Error("column named returning_x is not a RETURNING clause")
	}

	if returnsRows("create table t (a int)", "CREATE") {
		t.Error("create should not return rows")
	}
}
