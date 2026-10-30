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
		{"mysql procedure body", DialectMySQL, "create procedure p()\nbegin\n  declare x int;\n  set x = 1;\n  select x;\nend;\nselect 2",
			[]string{"create procedure p()\nbegin\n  declare x int;\n  set x = 1;\n  select x;\nend", "select 2"}},
		{"mysql nested blocks", DialectMySQL, "create procedure p()\nbegin\n  if x > 1 then\n    select 1;\n  end if;\n  case x when 1 then select 2; end case;\n  while x < 3 do set x = x + 1; end while;\nend; select 3",
			[]string{"create procedure p()\nbegin\n  if x > 1 then\n    select 1;\n  end if;\n  case x when 1 then select 2; end case;\n  while x < 3 do set x = x + 1; end while;\nend", "select 3"}},
		{"mysql trigger body", DialectMySQL, "create trigger tr before insert on t for each row\nbegin\n  set new.a = 1;\nend; select 1",
			[]string{"create trigger tr before insert on t for each row\nbegin\n  set new.a = 1;\nend", "select 1"}},
		{"mysql trigger without body", DialectMySQL, "create trigger tr before insert on t for each row set new.a = 1; select 1",
			[]string{"create trigger tr before insert on t for each row set new.a = 1", "select 1"}},
		{"mysql end in names", DialectMySQL, "create procedure p()\nbegin\n  select `end`, t.end from t;\nend; select 2",
			[]string{"create procedure p()\nbegin\n  select `end`, t.end from t;\nend", "select 2"}},
		{"create table if not exists", DialectMySQL, "create table if not exists t (a int); select 1", []string{"create table if not exists t (a int)", "select 1"}},
		{"sqlite trigger body", DialectSQLite, "create trigger tr after insert on t\nbegin\n  update u set n = n + 1;\n  delete from v;\nend; select 1",
			[]string{"create trigger tr after insert on t\nbegin\n  update u set n = n + 1;\n  delete from v;\nend", "select 1"}},
		{"mssql procedure body", DialectMSSQL, "create procedure p as\nbegin\n  select 1;\n  select 2;\nend; select 3",
			[]string{"create procedure p as\nbegin\n  select 1;\n  select 2;\nend", "select 3"}},
		{"pg begin atomic", DialectPostgres, "create function f() returns int language sql\nbegin atomic\n  select 1;\nend; select 2",
			[]string{"create function f() returns int language sql\nbegin atomic\n  select 1;\nend", "select 2"}},
		{"case outside a routine", DialectPostgres, "create view v as select case when a then 1 end as x from t; select 2",
			[]string{"create view v as select case when a then 1 end as x from t", "select 2"}},
		{"transaction begin", DialectPostgres, "begin; select 1; commit;", []string{"begin", "select 1", "commit"}},
		// A body without END must not take the rest of the script.
		{"unclosed body", DialectMySQL, "create procedure p()\nbegin\n  select 1;\nselect 2; select 3",
			[]string{"create procedure p()\nbegin\n  select 1", "select 2", "select 3"}},

		// Only routines have bodies: in other CREATEs begin, case and end
		// are names, and must not hide the statements after them.
		{"begin as a column", DialectSQLite, "create index ix on shifts (begin);\ndelete from shifts;\nselect id as end from shifts;",
			[]string{"create index ix on shifts (begin)", "delete from shifts", "select id as end from shifts"}},
		{"begin column then vacuum", DialectSQLite, "create table t (id int, begin int);\nvacuum into '/tmp/x';",
			[]string{"create table t (id int, begin int)", "vacuum into '/tmp/x'"}},
		{"begin column then a procedure", DialectMySQL, "create table t (id int, begin int);\ncreate procedure p() begin select 1; select 2; end;",
			[]string{"create table t (id int, begin int)", "create procedure p() begin select 1; select 2; end"}},
		{"oracle q quote", DialectOracle, "select q'[it's; ok]' from dual; select 2", []string{"select q'[it's; ok]' from dual", "select 2"}},

		// Inside a body: what opens a block and what does not.
		{"two cases on a line", DialectMySQL, "create procedure p() begin select case when a then 1 end, case when b then 2 end from t; select 3; end; select 4",
			[]string{"create procedure p() begin select case when a then 1 end, case when b then 2 end from t; select 3; end", "select 4"}},
		{"if exists and if function", DialectMySQL, "create procedure p() begin drop table if exists t; create table if not exists t (a int); select if(a, 1, 2) from t; end; select 4",
			[]string{"create procedure p() begin drop table if exists t; create table if not exists t (a int); select if(a, 1, 2) from t; end", "select 4"}},
		{"labelled loop", DialectMySQL, "create procedure p() begin lbl: loop leave lbl; end loop lbl; end; select 4",
			[]string{"create procedure p() begin lbl: loop leave lbl; end loop lbl; end", "select 4"}},
		{"mariadb labelled for", DialectMySQL, "create procedure p() begin lbl: for i in 1..3 do select i; end for lbl; end; select 4",
			[]string{"create procedure p() begin lbl: for i in 1..3 do select i; end for lbl; end", "select 4"}},
		{"select for update in a body", DialectMySQL, "create procedure p() begin select * from t for update; end; select 4",
			[]string{"create procedure p() begin select * from t for update; end", "select 4"}},
		{"mysql definer", DialectMySQL, "create definer=`root`@`%` procedure p() begin select 1; end; select 2",
			[]string{"create definer=`root`@`%` procedure p() begin select 1; end", "select 2"}},
		{"mssql if else and while", DialectMSSQL, "create procedure p as begin if 1 = 1 select 1; else select 2; while 1 = 0 begin select 3; end; end; select 4",
			[]string{"create procedure p as begin if 1 = 1 select 1; else select 2; while 1 = 0 begin select 3; end; end", "select 4"}},
		{"mssql transaction in a body", DialectMSSQL, "create procedure p as begin begin tran; update t set a = 1; commit; begin transaction; commit; end; select 4",
			[]string{"create procedure p as begin begin tran; update t set a = 1; commit; begin transaction; commit; end", "select 4"}},
		{"mssql try catch", DialectMSSQL, "create procedure p as begin begin try select 1; end try begin catch select 2; end catch; end; select 4",
			[]string{"create procedure p as begin begin try select 1; end try begin catch select 2; end catch; end", "select 4"}},
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
