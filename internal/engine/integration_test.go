//go:build integration

package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Run with: go test -tags integration ./internal/engine/ -run Integration
// against the databases from env/compose.yaml.

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func run(t *testing.T, m *Manager, c *store.Connection, pw, sql string, ro bool) *ExecResult {
	t.Helper()

	res, err := m.Execute(context.Background(), c, pw, ExecRequest{SQL: sql, ReadOnly: ro, MaxRows: 100, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}

	return res
}

func TestIntegrationMSSQL(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "ms", DBType: "mssql", Host: "127.0.0.1", Port: 51433, Database: "master", Username: "sa",
		Options: map[string]string{"encrypt": "disable"}, UpdatedAt: 1}
	pw := env("MSSQL_PASSWORD", "Str0ng!Passw0rd")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("sql server is not reachable:", err)
	}

	run(t, m, c, pw, "IF OBJECT_ID('dbo.dp_items') IS NOT NULL DROP TABLE dbo.dp_items; "+
		"CREATE TABLE dbo.dp_items (id INT PRIMARY KEY, name NVARCHAR(50), price DECIMAL(10,2), "+
		"uid UNIQUEIDENTIFIER DEFAULT NEWID(), at DATETIME2 DEFAULT SYSUTCDATETIME(), blob VARBINARY(10)); "+
		"INSERT INTO dbo.dp_items (id, name, price, blob) VALUES (1, N'Çay', 12.50, 0x0102), (2, N'Kahve', 30, NULL)", false)

	res := run(t, m, c, pw, "SELECT id, name, price, uid, at, blob FROM dbo.dp_items ORDER BY id", true)
	rs := res.Results[0]
	if rs.Error != "" || len(rs.Rows) != 2 {
		t.Fatalf("select: %+v", rs)
	}

	row := rs.Rows[0]
	t.Logf("row: %#v types: %+v", row, rs.Columns)

	if row[1] != "Çay" || row[2] != "12.50" || row[5] != `\x0102` {
		t.Fatalf("unexpected values %#v", row)
	}

	if s, _ := row[3].(string); len(s) != 36 {
		t.Fatalf("uniqueidentifier should be a UUID string, got %#v", row[3])
	}

	if _, err := m.Execute(context.Background(), c, pw, ExecRequest{SQL: "SELECT * INTO dbo.x FROM dbo.dp_items", ReadOnly: true, MaxRows: 1, Timeout: time.Second}); err == nil {
		t.Fatal("select into must be refused when read-only")
	}

	// Read-only runs are rolled back, so a write that slips through is undone.
	ro := run(t, m, c, pw, "SELECT 1", true)
	if ro.Committed {
		t.Fatal("read-only run must not commit")
	}

	start := time.Now()
	res, err := m.Execute(context.Background(), c, pw, ExecRequest{SQL: "WAITFOR DELAY '00:00:20'", MaxRows: 1, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	if res.Results[0].Error != "query timed out" || time.Since(start) > 6*time.Second {
		t.Fatalf("timeout: %+v after %s", res.Results[0], time.Since(start))
	}

	schemas, err := m.Schemas(context.Background(), c, pw)
	if err != nil || len(schemas) == 0 || schemas[0] != "dbo" {
		t.Fatalf("schemas %v %v", schemas, err)
	}

	cols, err := m.Columns(context.Background(), c, pw, "dbo", "dp_items")
	if err != nil || len(cols) != 6 || !cols[0].PrimaryKey || cols[1].Type != "nvarchar(50)" {
		t.Fatalf("columns %+v %v", cols, err)
	}

	edit, err := m.ApplyEdits(context.Background(), c, pw, EditRequest{Schema: "dbo", Table: "dp_items", Timeout: 5 * time.Second, Edits: []RowEdit{
		{Op: "update", Key: map[string]any{"id": 1}, Values: map[string]any{"name": "Çay (demlik)"}},
		{Op: "insert", Values: map[string]any{"id": 3, "name": "Su"}},
		{Op: "delete", Key: map[string]any{"id": 2}},
	}})
	if err != nil || edit.Affected != 3 {
		t.Fatalf("edits %+v %v", edit, err)
	}

	res = run(t, m, c, pw, "SELECT STRING_AGG(name, ',') WITHIN GROUP (ORDER BY id) FROM dbo.dp_items", true)
	if got := res.Results[0].Rows[0][0]; got != "Çay (demlik),Su" {
		t.Fatalf("after edits: %v", got)
	}

	_, err = m.ApplyEdits(context.Background(), c, pw, EditRequest{Schema: "dbo", Table: "dp_items", Timeout: 5 * time.Second, Edits: []RowEdit{
		{Op: "update", Key: map[string]any{"id": 1}, Values: map[string]any{"name": "rolled back"}},
		{Op: "delete", Key: map[string]any{"id": 99}},
	}})
	if err == nil || !strings.Contains(err.Error(), "matched 0 rows") {
		t.Fatalf("stale edit must fail: %v", err)
	}

	res = run(t, m, c, pw, "SELECT name FROM dbo.dp_items WHERE id = 1", true)
	if got := res.Results[0].Rows[0][0]; got != "Çay (demlik)" {
		t.Fatalf("failed batch must roll back, got %v", got)
	}
}

func TestIntegrationClickHouse(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "ch", DBType: "clickhouse", Host: "127.0.0.1", Port: 59000, Database: "app", Username: "ch", UpdatedAt: 1}
	pw := env("CLICKHOUSE_PASSWORD", "pw")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("clickhouse is not reachable:", err)
	}

	res := run(t, m, c, pw, "DROP TABLE IF EXISTS events; "+
		"CREATE TABLE events (id UInt64, kind LowCardinality(String), tags Array(String), at DateTime64(3), amount Nullable(Decimal(10,2))) ENGINE = MergeTree ORDER BY id; "+
		"INSERT INTO events VALUES (1, 'click', ['a','b'], now64(3), 1.5), (18446744073709551615, 'view', [], now64(3), NULL)", false)
	if res.Transactional {
		t.Fatal("clickhouse must report non-transactional runs")
	}

	res = run(t, m, c, pw, "SELECT id, kind, tags, at, amount FROM events ORDER BY id", true)
	rs := res.Results[0]
	if rs.Error != "" || len(rs.Rows) != 2 {
		t.Fatalf("select: %+v", rs)
	}

	t.Logf("rows: %#v types: %+v", rs.Rows, rs.Columns)

	if rs.Rows[1][0] != "18446744073709551615" {
		t.Fatalf("UInt64 above 2^53 must be a string, got %#v", rs.Rows[1][0])
	}

	ro := run(t, m, c, pw, "INSERT INTO events (id, kind) VALUES (5, 'x')", false)
	if ro.Results[0].Error != "" {
		t.Fatalf("write on read-write: %s", ro.Results[0].Error)
	}

	if _, err := m.Execute(context.Background(), c, pw, ExecRequest{SQL: "INSERT INTO events (id, kind) VALUES (6, 'x')", ReadOnly: true, MaxRows: 1, Timeout: time.Second}); err == nil {
		t.Fatal("insert must be refused when read-only")
	}

	// The server-side readonly setting stops writes the keyword check misses.
	res = run(t, m, c, pw, "SELECT * FROM events; SELECT 1", true)
	if res.Results[0].Error != "" {
		t.Fatal(res.Results[0].Error)
	}

	res = run(t, m, c, pw, "WITH x AS (SELECT 1) SELECT getSetting('readonly')", true)
	if v := res.Results[0].Rows[0][0]; v != "2" && v != uint64(2) && v != int64(2) && v != uint8(2) {
		t.Fatalf("readonly setting = %#v", v)
	}

	start := time.Now()
	res, err := m.Execute(context.Background(), c, pw, ExecRequest{SQL: "SELECT sleepEachRow(1) FROM numbers(30) SETTINGS max_block_size = 1", MaxRows: 100, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	if res.Results[0].Error == "" || time.Since(start) > 6*time.Second {
		t.Fatalf("timeout: %+v after %s", res.Results[0], time.Since(start))
	}

	objs, err := m.Objects(context.Background(), c, pw, "app")
	if err != nil || len(objs) != 1 || objs[0].Name != "events" {
		t.Fatalf("objects %+v %v", objs, err)
	}

	cols, err := m.Columns(context.Background(), c, pw, "app", "events")
	if err != nil || len(cols) != 5 || !cols[0].PrimaryKey || !cols[4].Nullable {
		t.Fatalf("columns %+v %v", cols, err)
	}
}

func TestIntegrationPostgresEdits(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "pg", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}
	pw := env("PG_PASSWORD", "pw")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	run(t, m, c, pw, "DROP TABLE IF EXISTS dp_edit; CREATE TABLE dp_edit (id int PRIMARY KEY, name text, born date, meta jsonb, ok boolean, n numeric(6,2))", false)

	// Values arrive from the browser as JSON: numbers, strings, booleans.
	edit, err := m.ApplyEdits(context.Background(), c, pw, EditRequest{Schema: "public", Table: "dp_edit", Timeout: 5 * time.Second, Edits: []RowEdit{
		{Op: "insert", Values: map[string]any{"id": float64(1), "name": "a", "born": "2020-01-02", "meta": `{"k":1}`, "ok": true, "n": "1.50"}},
		{Op: "insert", Values: map[string]any{"id": "2", "name": nil, "born": nil, "meta": nil, "ok": "false", "n": float64(3)}},
	}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	t.Log(edit.Statements)

	if _, err := m.ApplyEdits(context.Background(), c, pw, EditRequest{Schema: "public", Table: "dp_edit", Timeout: 5 * time.Second, Edits: []RowEdit{
		{Op: "update", Key: map[string]any{"id": float64(1)}, Values: map[string]any{"born": "2021-03-04", "meta": `{"k":2}`, "n": "9.99"}},
		{Op: "delete", Key: map[string]any{"id": "2"}},
	}}); err != nil {
		t.Fatalf("update: %v", err)
	}

	res := run(t, m, c, pw, "SELECT id, born::text, meta->>'k', n::text FROM dp_edit", true)
	if got := res.Results[0].Rows; len(got) != 1 || got[0][1] != "2021-03-04" || got[0][2] != "2" || got[0][3] != "9.99" {
		t.Fatalf("rows %#v", got)
	}
}

func TestIntegrationStructure(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 4}, time.Minute)
	defer m.Close()

	cases := []struct {
		name, schema string
		conn         *store.Connection
		pw, setup    string
		cleanup      string
		fk           bool
	}{
		{
			name: "postgres", schema: "public", pw: env("PG_PASSWORD", "pw"), fk: true,
			conn: &store.Connection{ID: "pg-s", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
				Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1},
			cleanup: "DROP TABLE IF EXISTS dp_child; DROP TABLE IF EXISTS dp_parent",
			setup: "DROP TABLE IF EXISTS dp_child; DROP TABLE IF EXISTS dp_parent; " +
				"CREATE TABLE dp_parent (id int PRIMARY KEY, code text UNIQUE); " +
				"CREATE TABLE dp_child (id serial PRIMARY KEY, parent_id int NOT NULL REFERENCES dp_parent(id) ON DELETE CASCADE, name text DEFAULT 'x', " +
				"n bigint GENERATED BY DEFAULT AS IDENTITY, twice int GENERATED ALWAYS AS (parent_id * 2) STORED); " +
				"CREATE INDEX dp_child_name ON dp_child (name, parent_id)",
		},
		{
			name: "mysql", schema: "app", pw: env("MYSQL_PASSWORD", "pw"), fk: true,
			conn:    &store.Connection{ID: "my-s", DBType: "mysql", Host: "127.0.0.1", Port: 53306, Database: "app", Username: "root", UpdatedAt: 1},
			cleanup: "DROP TABLE IF EXISTS dp_child; DROP TABLE IF EXISTS dp_parent",
			setup: "DROP TABLE IF EXISTS dp_child; DROP TABLE IF EXISTS dp_parent; " +
				"CREATE TABLE dp_parent (id int PRIMARY KEY, code varchar(20) UNIQUE); " +
				"CREATE TABLE dp_child (id int AUTO_INCREMENT PRIMARY KEY, parent_id int NOT NULL, name varchar(20) DEFAULT 'x', " +
				"INDEX dp_child_name (name, parent_id), CONSTRAINT dp_child_fk FOREIGN KEY (parent_id) REFERENCES dp_parent(id) ON DELETE CASCADE)",
		},
		{
			name: "mssql", schema: "dbo", pw: env("MSSQL_PASSWORD", "Str0ng!Passw0rd"), fk: true,
			conn: &store.Connection{ID: "ms-s", DBType: "mssql", Host: "127.0.0.1", Port: 51433, Database: "master", Username: "sa",
				Options: map[string]string{"encrypt": "disable"}, UpdatedAt: 1},
			cleanup: "DROP TABLE IF EXISTS dbo.dp_child; DROP TABLE IF EXISTS dbo.dp_parent",
			setup: "IF OBJECT_ID('dbo.dp_child') IS NOT NULL DROP TABLE dbo.dp_child; IF OBJECT_ID('dbo.dp_parent') IS NOT NULL DROP TABLE dbo.dp_parent; " +
				"CREATE TABLE dbo.dp_parent (id INT PRIMARY KEY, code NVARCHAR(20) UNIQUE); " +
				"CREATE TABLE dbo.dp_child (id INT IDENTITY PRIMARY KEY, parent_id INT NOT NULL, name NVARCHAR(20) DEFAULT 'x', twice AS (parent_id * 2), " +
				"CONSTRAINT dp_child_fk FOREIGN KEY (parent_id) REFERENCES dbo.dp_parent(id) ON DELETE CASCADE); " +
				"CREATE INDEX dp_child_name ON dbo.dp_child (name, parent_id)",
		},
		{
			name: "clickhouse", schema: "app", pw: env("CLICKHOUSE_PASSWORD", "pw"),
			conn:    &store.Connection{ID: "ch-s", DBType: "clickhouse", Host: "127.0.0.1", Port: 59000, Database: "app", Username: "ch", UpdatedAt: 1},
			cleanup: "DROP TABLE IF EXISTS dp_child",
			setup: "DROP TABLE IF EXISTS dp_child; " +
				"CREATE TABLE dp_child (id UInt64, parent_id UInt64, name String, INDEX dp_child_name name TYPE bloom_filter GRANULARITY 1) " +
				"ENGINE = MergeTree ORDER BY (id, parent_id)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if err := m.Test(ctx, tc.conn, tc.pw); err != nil {
				t.Skip("not reachable:", err)
			}

			run(t, m, tc.conn, tc.pw, tc.setup, false)
			t.Cleanup(func() { run(t, m, tc.conn, tc.pw, tc.cleanup, false) })

			st, err := m.Structure(ctx, tc.conn, tc.pw, tc.schema, "dp_child")
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("indexes %+v\nfks %+v\n%s", st.Indexes, st.ForeignKeys, st.DDL)

			if len(st.Columns) < 3 || len(st.Indexes) < 2 || !st.Indexes[0].Primary {
				t.Fatalf("columns or indexes: %+v %+v", st.Columns, st.Indexes)
			}

			named := false
			for _, ix := range st.Indexes {
				if ix.Name == "dp_child_name" && (tc.name == "clickhouse" || len(ix.Columns) == 2 && ix.Columns[0] == "name") {
					named = true
				}
			}

			if !named {
				t.Fatalf("dp_child_name index missing: %+v", st.Indexes)
			}

			if tc.fk {
				if len(st.ForeignKeys) != 1 || st.ForeignKeys[0].RefTable != "dp_parent" || st.ForeignKeys[0].Columns[0] != "parent_id" ||
					st.ForeignKeys[0].OnDelete != "CASCADE" {
					t.Fatalf("foreign keys: %+v", st.ForeignKeys)
				}
			}

			if !strings.Contains(strings.ToUpper(st.DDL), "CREATE TABLE") || !strings.Contains(st.DDL, "dp_child") {
				t.Fatalf("ddl: %s", st.DDL)
			}

			// The rebuilt DDL must run as is: drop and recreate the table from it.
			if tc.name == "postgres" || tc.name == "mssql" {
				drop := "DROP TABLE dp_child"
				if tc.name == "mssql" {
					drop = "DROP TABLE dbo.dp_child"
				}

				res := run(t, m, tc.conn, tc.pw, drop+";\n"+st.DDL, false)
				if e := res.FirstError(); e != "" {
					t.Fatalf("ddl does not run: %s", e)
				}
			}
		})
	}
}

func TestIntegrationPoolLimits(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 4}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "pg-limits", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1, Limits: store.Limits{MaxOpen: 2, MaxIdleTimeSec: 30}}
	pw := env("PG_PASSWORD", "pw")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	if got := m.Limits(c); got != (store.Limits{MaxOpen: 2, MaxIdle: 5, MaxLifetimeSec: 3600, MaxIdleTimeSec: 30}) {
		t.Fatalf("effective limits %+v", got)
	}

	run(t, m, c, pw, "SELECT 1", true)

	p, err := m.pool(c, pw)
	if err != nil {
		t.Fatal(err)
	}

	if st := p.db.Stats(); st.MaxOpenConnections != 2 {
		t.Fatalf("max open %d", st.MaxOpenConnections)
	}

	// A user's own limits give them a separate pool, reopened when they change.
	own := *c
	own.Owner = "u1"
	own.Limits = c.Limits.Merge(store.Limits{MaxOpen: 1})

	run(t, m, &own, pw, "SELECT 1", true)

	po, _ := m.pool(&own, pw)
	if po == p || po.db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("own pool %v max open %d", po == p, po.db.Stats().MaxOpenConnections)
	}

	own.Limits.MaxOpen = 3
	if again, _ := m.pool(&own, pw); again == po || again.db.Stats().MaxOpenConnections != 3 {
		t.Fatal("changed limits must reopen the pool")
	}
}

func TestIntegrationOracle(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "ora", DBType: "oracle", Host: "127.0.0.1", Port: 51521, Database: "FREEPDB1", Username: "app", UpdatedAt: 1}
	pw := env("ORACLE_PASSWORD", "pw")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("oracle is not reachable:", err)
	}

	run(t, m, c, pw, "BEGIN EXECUTE IMMEDIATE 'DROP TABLE dp_items'; EXCEPTION WHEN OTHERS THEN NULL; END;\n/\n"+
		"CREATE TABLE dp_items (id NUMBER PRIMARY KEY, name VARCHAR2(50) NOT NULL);\n"+
		"INSERT INTO dp_items VALUES (1, 'a');\nINSERT INTO dp_items VALUES (2, 'b')", false)

	res := run(t, m, c, pw, "SELECT id, name FROM dp_items ORDER BY id", true)
	if rs := res.Results[0]; rs.Error != "" || len(rs.Rows) != 2 {
		t.Fatalf("select: %+v", rs)
	}

	res = run(t, m, c, pw, "SELECT id FROM dp_items FOR UPDATE", true)
	if res.FirstError() == "" {
		t.Fatal("read-only transaction allowed FOR UPDATE")
	}

	st, err := m.Structure(context.Background(), c, pw, "APP", "DP_ITEMS")
	if err != nil || len(st.Columns) != 2 || !st.Columns[0].PrimaryKey || st.DDL == "" {
		t.Fatalf("structure: %v %+v", err, st)
	}
}

func TestIntegrationTrino(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "tr", DBType: "trino", Host: "127.0.0.1", Port: 58080, Database: "memory", Username: "dataport", UpdatedAt: 1}

	if err := m.Test(context.Background(), c, ""); err != nil {
		t.Skip("trino is not reachable:", err)
	}

	run(t, m, c, "", "CREATE SCHEMA IF NOT EXISTS memory.dp; DROP TABLE IF EXISTS memory.dp.items; "+
		"CREATE TABLE memory.dp.items (id integer, name varchar); INSERT INTO memory.dp.items VALUES (1, 'a'), (2, 'b')", false)

	res := run(t, m, c, "", "SELECT id, name FROM memory.dp.items ORDER BY id", true)
	if rs := res.Results[0]; rs.Error != "" || len(rs.Rows) != 2 {
		t.Fatalf("select: %+v", rs)
	}

	schemas, err := m.Schemas(context.Background(), c, "")
	if err != nil || !strings.Contains(strings.Join(schemas, ","), "dp") {
		t.Fatalf("schemas: %v %v", schemas, err)
	}
}

func TestIntegrationPostgresRoutines(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "pgr", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}

	if err := m.Test(context.Background(), c, "pw"); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	run(t, m, c, "pw", "DROP SCHEMA IF EXISTS dp_r CASCADE; CREATE SCHEMA dp_r; CREATE SEQUENCE dp_r.s1; "+
		"CREATE FUNCTION dp_r.add(a int, b int) RETURNS int LANGUAGE sql AS $$ SELECT a + b $$; "+
		"CREATE PROCEDURE dp_r.noop() LANGUAGE sql AS $$ SELECT 1 $$", false)

	list, err := m.Routines(context.Background(), c, "pw", "dp_r")
	if err != nil || len(list) != 3 {
		t.Fatalf("routines: %v %+v", err, list)
	}

	for _, r := range list {
		def, err := m.Definition(context.Background(), c, "pw", "dp_r", r)
		if err != nil || def == "" {
			t.Fatalf("definition of %+v: %v", r, err)
		}
	}

	// A serial's sequence shows its owner and the column that owns it, and
	// the table DDL its owner, comments and triggers; all of it runs again.
	run(t, m, c, "pw", "CREATE TABLE dp_r.t (id serial PRIMARY KEY, v text); COMMENT ON TABLE dp_r.t IS 'it''s t'; "+
		"COMMENT ON COLUMN dp_r.t.v IS 'value'; CREATE FUNCTION dp_r.trg() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$; "+
		"CREATE TRIGGER t_ins BEFORE INSERT ON dp_r.t FOR EACH ROW EXECUTE FUNCTION dp_r.trg()", false)

	def, err := m.Definition(context.Background(), c, "pw", "dp_r", Routine{Name: "t_id_seq", Kind: "sequence"})
	if err != nil || !strings.HasPrefix(def, "CREATE SEQUENCE dp_r.t_id_seq") ||
		!strings.Contains(def, "ALTER SEQUENCE dp_r.t_id_seq OWNER TO postgres;") || !strings.Contains(def, "OWNED BY dp_r.t.id;") {
		t.Fatalf("sequence: %v\n%s", err, def)
	}

	st, err := m.Structure(context.Background(), c, "pw", "dp_r", "t")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`ALTER TABLE "dp_r"."t" OWNER TO postgres;`, `COMMENT ON TABLE "dp_r"."t" IS 'it''s t';`, `COMMENT ON COLUMN "dp_r"."t".v IS 'value';`, "CREATE TRIGGER t_ins"} {
		if !strings.Contains(st.DDL, want) {
			t.Fatalf("ddl misses %q:\n%s", want, st.DDL)
		}
	}

	if e := run(t, m, c, "pw", "DROP TABLE dp_r.t;\n"+st.DDL, false).FirstError(); e != "" {
		t.Fatalf("ddl does not run: %s\n%s", e, st.DDL)
	}

	if strings.Contains(withoutOwner(st.DDL), "OWNER TO") || !strings.Contains(withoutOwner(st.DDL), "COMMENT ON TABLE") {
		t.Fatalf("withoutOwner:\n%s", withoutOwner(st.DDL))
	}

	t.Logf("%s\n%s", def, st.DDL)
}

func TestIntegrationNoTimeoutCancel(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "pgt", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}

	if err := m.Test(context.Background(), c, "pw"); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		m.Cancel(context.Background(), "u", "q1")
	}()

	start := time.Now()

	res, err := m.Execute(context.Background(), c, "pw", ExecRequest{QueryID: "q1", UserID: "u", SQL: "SELECT pg_sleep(10)", ReadOnly: true, MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}

	if res.FirstError() != "query cancelled" || time.Since(start) > 5*time.Second {
		t.Fatalf("not cancelled: %q after %v", res.FirstError(), time.Since(start))
	}
}

// A read past the row limit must not fail the run: the rows not shown are
// never produced, so an error in one of them cannot turn the commit into a
// rollback, and a large table is not read whole.
func TestIntegrationPostgresRowLimit(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "pg", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}
	pw := env("PG_PASSWORD", "pw")

	if err := m.Test(context.Background(), c, pw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	run(t, m, c, pw, "DROP VIEW IF EXISTS dp_late_error; CREATE VIEW dp_late_error AS SELECT g, 1 / (g - 5000) AS x FROM generate_series(1, 10000) g", false)

	res := run(t, m, c, pw, "SELECT * FROM dp_late_error", false)
	if rs := res.Results; len(rs) != 1 || rs[0].Error != "" || !rs[0].Truncated || len(rs[0].Rows) != 100 || !res.Committed {
		t.Fatalf("read past the limit: %+v committed=%v", rs, res.Committed)
	}

	// A statement that cannot be a cursor still runs, and writes commit.
	res = run(t, m, c, pw, "DROP TABLE IF EXISTS dp_rows; SELECT 1 AS a INTO dp_rows; WITH x AS (INSERT INTO dp_rows VALUES (2) RETURNING a) SELECT * FROM x", false)
	if !res.Committed || res.Results[2].Error != "" || len(res.Results[2].Rows) != 1 {
		t.Fatalf("writes: %+v", res.Results)
	}

	if n := run(t, m, c, pw, "SELECT count(*) FROM dp_rows", true).Results[0].Rows[0][0]; n != int64(2) {
		t.Fatalf("count %v", n)
	}

	// An error in the rows shown still fails the statement.
	res = run(t, m, c, pw, "SELECT * FROM dp_late_error WHERE g >= 4990", false)
	if res.Results[0].Error == "" || res.Committed {
		t.Fatalf("error in shown rows: %+v", res.Results)
	}

	// A locking read runs whole, locking every row, so it reaches the error
	// past the limit and the run does not commit.
	run(t, m, c, pw, "DROP TABLE IF EXISTS dp_lock; CREATE TABLE dp_lock AS SELECT g FROM generate_series(1, 300) g", false)
	res = run(t, m, c, pw, "SELECT 1 / (g - 250) FROM dp_lock FOR UPDATE", false)
	if res.Committed && res.Results[0].Error == "" {
		t.Fatalf("locking read: %+v committed=%v", res.Results[0], res.Committed)
	}

	// A table without a schema is found where the search path reads it.
	run(t, m, c, pw, "CREATE SCHEMA IF NOT EXISTS dp_other; CREATE TABLE IF NOT EXISTS dp_other.dp_lock (g int)", false)
	if s, ok, err := m.ResolveTable(context.Background(), c, pw, "dp_lock"); err != nil || !ok || s != "public" {
		t.Fatalf("resolve: %q %v %v", s, ok, err)
	}
	if s, ok, err := m.ResolveTable(context.Background(), c, pw, "dp_missing"); err != nil || !ok || s != "" {
		t.Fatalf("resolve missing: %q %v %v", s, ok, err)
	}
	run(t, m, c, pw, "DROP SCHEMA dp_other CASCADE; DROP TABLE dp_lock", false)
}
