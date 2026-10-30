package engine

import "testing"

func TestUnbounded(t *testing.T) {
	pg, _ := GetDriver("postgres")
	my, _ := GetDriver("mysql")
	ms, _ := GetDriver("mssql")
	or, _ := GetDriver("oracle")
	ch, _ := GetDriver("clickhouse")

	tests := []struct {
		name string
		d    Driver
		sql  string
		want int
	}{
		{"update without where", pg, "update t set a = 1", 1},
		{"delete without where", pg, "delete from t", 1},
		{"update with where", pg, "update t set a = 1 where id = 2", 0},
		{"delete with where", pg, "delete from t where id = 2", 0},
		{"where only in a subquery", pg, "update t set a = (select max(b) from u where u.id = 1)", 1},
		{"delete where in subquery", pg, "delete from t where id in (select id from u)", 0},
		{"update from with where", pg, "update t set a = s.a from s where s.id = t.id", 0},
		{"returning without where", pg, "delete from t returning *", 1},
		{"cte delete without where", pg, "with d as (delete from t returning *) select * from d", 1},
		{"cte delete with where", pg, "with d as (delete from t where a returning *) select * from d", 0},
		{"one of several", pg, "update t set a = 1 where id = 1; delete from u; select 1", 1},
		{"two", pg, "update t set a = 1; delete from u", 2},
		{"select for update", pg, "select * from t for update", 0},
		{"on delete cascade", pg, "create table t (id int references u on delete cascade on update no action)", 0},
		{"on conflict do update", pg, "insert into t values (1) on conflict (id) do update set a = 1", 0},
		{"merge actions", pg, "merge into t using s on t.id = s.id when matched then update set a = 1 when matched then delete", 0},
		{"trigger body not run", pg, "create trigger tr after insert on t for each row execute function f()", 0},
		{"rule", pg, "create rule r as on insert to t do also delete from u", 0},
		{"grant update", pg, "grant update, delete on t to bob", 0},
		{"column named update", pg, "select t.update, t.delete from t", 0},
		{"where in a string", pg, "update t set a = 'where'", 1},
		{"where in a comment", pg, "delete from t -- where id = 1", 1},
		{"mysql procedure is a definition", my, "create procedure p() begin delete from t; end", 0},
		{"mysql multi-table delete", my, "delete t1 from t1 join t2 on t1.id = t2.id", 1},
		{"mysql limit is not a where", my, "delete from t limit 10", 1},
		{"mssql batch without semicolons", ms, "update t set a = 1\nselect 1", 1},
		{"mssql batch bounded", ms, "update t set a = 1 where b = 2\nselect 1", 0},
		{"mssql set after update", ms, "declare @x int\nupdate t set a = 1\nset @x = 2", 1},
		{"mssql set list", ms, "update t set a = 1, b = 2 where c = 3", 0},
		{"mssql update statistics", ms, "update statistics t", 0},
		{"mssql truncate is ddl, not here", ms, "truncate table t", 0},

		// T-SQL batches: a statement before without ";" must not hide one.
		{"mssql set nocount", ms, "SET NOCOUNT ON\nDELETE FROM t", 1},
		{"mssql set options", ms, "SET ANSI_NULLS ON\nSET QUOTED_IDENTIFIER ON\nDELETE FROM dbo.t", 1},
		{"mssql after a string", ms, "SET @x = 'a'\nDELETE FROM t", 1},
		{"mssql after a select", ms, "SELECT * FROM u WHERE name = 'x'\nDELETE FROM t", 1},
		{"mssql after a list", ms, "SELECT 'a', 'b'\nUPDATE t SET a = 1", 1},
		{"mssql after a definition", ms, "CREATE TABLE #x (a int)\nDELETE FROM t", 1},
		{"mssql after alter", ms, "ALTER TABLE t ADD c int\nUPDATE t SET c = 1", 1},
		{"mssql after grant", ms, "GRANT SELECT ON t TO bob\nDELETE FROM t", 1},
		{"mssql procedure body not run", ms, "CREATE PROCEDURE p AS DELETE FROM t", 0},

		// CASE … END and functions named like statements are no new statement.
		{"case end before where", pg, "UPDATE t SET a = CASE WHEN b = 1 THEN 1 END WHERE id = 1", 0},
		{"mssql case end before where", ms, "UPDATE t SET a = CASE WHEN b = 1 THEN 1 END WHERE id = 1", 0},
		{"mysql if function", my, "UPDATE t SET a = IF(b > 1, 1, 2) WHERE id = 1", 0},
		{"mysql insert function", my, "UPDATE t SET a = INSERT(a, 1, 2, 'x') WHERE id = 1", 0},
		{"mysql truncate function", my, "UPDATE t SET a = TRUNCATE(b, 2) WHERE id = 1", 0},
		{"mssql join on case", ms, "UPDATE t SET a = 1 FROM t JOIN u ON CASE WHEN u.x = 1 THEN 1 END = 1 WHERE t.id = 1", 0},
		{"mysql on duplicate key update", my, "INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = a + 1", 0},
		{"for no key update skip locked", pg, "SELECT * FROM t FOR NO KEY UPDATE SKIP LOCKED", 0},

		// Bodies that run at once.
		{"pg do block", pg, "DO $$ BEGIN DELETE FROM t; END $$", 1},
		{"pg do block if", pg, "DO $$ BEGIN IF true THEN DELETE FROM t; END IF; END $$", 1},
		{"pg do block bounded", pg, "DO $$ BEGIN DELETE FROM t WHERE a = 1; END $$", 0},
		{"pg function body not run", pg, "CREATE FUNCTION f() RETURNS void AS $$ DELETE FROM t $$ LANGUAGE sql", 0},
		{"oracle block", or, "BEGIN IF x > 0 THEN DELETE FROM t; END IF; END;", 1},

		// Strings and other scripts do not make or hide keywords.
		{"dollar string with a statement", pg, "SELECT $$ update t set a = 1 $$", 0},
		{"dollar string hides no where", pg, "UPDATE t SET body = $$ where $$", 1},
		{"letters of other scripts", pg, "SELECT ĩupdate FROM t", 0},
		{"letters of other scripts hide no where", pg, "UPDATE t SET ĩwhere = 1", 1},
		{"prepare", pg, "PREPARE p AS DELETE FROM t", 1},

		// Known limits: dynamic SQL is not read, and ClickHouse requires a
		// WHERE, so WHERE 1 says every row is meant.
		{"dynamic sql", pg, "EXECUTE 'DELETE FROM t'", 0},
		{"clickhouse mutation", ch, "ALTER TABLE t DELETE WHERE 1", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unbounded(tt.d, tt.sql); len(got) != tt.want {
				t.Fatalf("Unbounded(%q) = %q, want %d", tt.sql, got, tt.want)
			}
		})
	}
}
