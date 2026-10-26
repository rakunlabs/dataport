package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(postgresDriver{})
}

type postgresDriver struct{}

func (postgresDriver) Type() string     { return "postgres" }
func (postgresDriver) Dialect() Dialect { return DialectPostgres }
func (postgresDriver) DefaultPort() int { return 5432 }

func (postgresDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 5432
	}

	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(port)),
		Path:   "/" + c.Database,
		User:   url.UserPassword(c.Username, password),
	}

	q := url.Values{}
	q.Set("sslmode", optionOr(c.Options, "sslmode", "prefer"))
	q.Set("connect_timeout", optionOr(c.Options, "connect_timeout", "10"))
	q.Set("application_name", "dataport")
	u.RawQuery = q.Encode()

	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("parse postgres config; %w", err)
	}

	// The extended protocol refuses several statements in one string, so a
	// statement the splitter got wrong cannot smuggle a second one through.
	// Exec mode also skips the per-connection prepared statement cache, which
	// arbitrary user SQL would otherwise grow without bound.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec

	return stdlib.OpenDB(*cfg), nil
}

func (postgresDriver) BackendID(ctx context.Context, conn *sql.Conn) (string, error) {
	var pid int64
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		return "", err
	}

	return strconv.FormatInt(pid, 10), nil
}

func (postgresDriver) Kill(ctx context.Context, db *sql.DB, backendID string) error {
	pid, err := strconv.ParseInt(backendID, 10, 64)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "SELECT pg_cancel_backend($1)", pid)

	return err
}

func (postgresDriver) Prepare(ctx context.Context, tx Runner, timeout time.Duration) error {
	ms := timeout.Milliseconds()
	if ms <= 0 {
		return nil
	}

	// SET does not take parameters; ms is an integer we formatted.
	_, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = "+strconv.FormatInt(ms, 10))

	return err
}

func (postgresDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

// CheckReadOnly has nothing to add: a READ ONLY transaction in PostgreSQL
// already rejects writes, data-modifying CTEs, sequence changes and attempts
// to switch the transaction back to read-write.
func (postgresDriver) CheckReadOnly(string) error {
	return nil
}

func (postgresDriver) Reset(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "DISCARD ALL")

	return err
}

func (postgresDriver) Placeholder(n int) string {
	return "$" + strconv.Itoa(n)
}

func (postgresDriver) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (postgresDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT nspname FROM pg_catalog.pg_namespace
WHERE nspname NOT IN ('pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp\_%'
AND has_schema_privilege(oid, 'USAGE')
ORDER BY nspname = 'public' DESC, nspname`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (postgresDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.relname,
CASE c.relkind WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized_view' WHEN 'f' THEN 'foreign_table' ELSE 'table' END
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm', 'f') AND NOT c.relispartition
ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (postgresDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), ''),
EXISTS (SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey))
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanColumns(rows)
}

// ////////////////////////////////////////////

func optionOr(options map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(options[key]); v != "" {
		return v
	}

	return fallback
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	list := []string{}

	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		list = append(list, s)
	}

	return list, rows.Err()
}

func scanObjects(rows *sql.Rows) ([]Object, error) {
	list := []Object{}

	for rows.Next() {
		var o Object
		if err := rows.Scan(&o.Name, &o.Kind); err != nil {
			return nil, err
		}

		list = append(list, o)
	}

	return list, rows.Err()
}

func scanColumns(rows *sql.Rows) ([]ColumnInfo, error) {
	list := []ColumnInfo{}

	for rows.Next() {
		var c ColumnInfo
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.PrimaryKey); err != nil {
			return nil, err
		}

		list = append(list, c)
	}

	return list, rows.Err()
}

// ////////////////////////////////////////////
// structure

func (postgresDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.relname, ix.indisunique, ix.indisprimary, am.amname,
COALESCE((SELECT string_agg(COALESCE(a.attname, pg_catalog.pg_get_indexdef(ix.indexrelid, k.ord::int, true)), chr(31) ORDER BY k.ord)
  FROM unnest(ix.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
  LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum AND k.attnum > 0
  WHERE k.ord <= ix.indnkeyatts), '')
FROM pg_catalog.pg_index ix
JOIN pg_catalog.pg_class t ON t.oid = ix.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
JOIN pg_catalog.pg_class i ON i.oid = ix.indexrelid
JOIN pg_catalog.pg_am am ON am.oid = i.relam
WHERE n.nspname = $1 AND t.relname = $2
ORDER BY ix.indisprimary DESC, i.relname`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (postgresDriver) ForeignKeys(ctx context.Context, db Querier, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT con.conname, rn.nspname, rc.relname,
(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord) FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
  JOIN pg_catalog.pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
(SELECT string_agg(a.attname, chr(31) ORDER BY k.ord) FROM unnest(con.confkey) WITH ORDINALITY k(attnum, ord)
  JOIN pg_catalog.pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum),
CASE con.confupdtype WHEN 'r' THEN 'RESTRICT' WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' ELSE 'NO ACTION' END,
CASE con.confdeltype WHEN 'r' THEN 'RESTRICT' WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' ELSE 'NO ACTION' END
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class c ON c.oid = con.conrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_class rc ON rc.oid = con.confrelid
JOIN pg_catalog.pg_namespace rn ON rn.oid = rc.relnamespace
WHERE con.contype = 'f' AND n.nspname = $1 AND c.relname = $2
ORDER BY con.conname`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

// DDL rebuilds CREATE TABLE from the catalog, as PostgreSQL has no statement
// that returns it. Views use their stored definition.
func (d postgresDriver) DDL(ctx context.Context, db Querier, schema, table string, t *TableStructure) (string, error) {
	const rel = `(SELECT c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2)`

	kind, err := queryString(ctx, db, `SELECT relkind::text FROM pg_catalog.pg_class WHERE oid = `+rel, schema, table)
	if err != nil {
		return "", err
	}

	qualified := d.QuoteIdent(schema) + "." + d.QuoteIdent(table)

	if kind == "v" || kind == "m" {
		def, err := queryString(ctx, db, `SELECT pg_catalog.pg_get_viewdef(`+rel+`, true)`, schema, table)
		if err != nil {
			return "", err
		}

		create := "CREATE VIEW "
		if kind == "m" {
			create = "CREATE MATERIALIZED VIEW "
		}

		return create + qualified + " AS\n" + strings.TrimSpace(def) + "\n", nil
	}

	constraints, err := queryStrings(ctx, db, `SELECT 'CONSTRAINT ' || quote_ident(conname) || ' ' || pg_catalog.pg_get_constraintdef(oid, true)
FROM pg_catalog.pg_constraint WHERE conrelid = `+rel+` AND contype IN ('p', 'u', 'f', 'c', 'x')
ORDER BY CASE contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'f' THEN 2 ELSE 3 END, conname`, schema, table)
	if err != nil {
		return "", err
	}

	// Serial columns own their sequence, which is dropped with the table, so
	// they are written as serial instead of DEFAULT nextval(...).
	// Identity and generated columns have no default in the catalog.
	rows, err := db.QueryContext(ctx, `SELECT a.attname,
CASE
  WHEN a.attidentity IN ('a', 'd') THEN pg_catalog.format_type(a.atttypid, a.atttypmod)
    || ' GENERATED ' || CASE a.attidentity WHEN 'a' THEN 'ALWAYS' ELSE 'BY DEFAULT' END || ' AS IDENTITY'
  WHEN a.attgenerated = 's' THEN pg_catalog.format_type(a.atttypid, a.atttypmod)
    || ' GENERATED ALWAYS AS (' || pg_catalog.pg_get_expr(ad.adbin, ad.adrelid) || ') STORED'
  ELSE CASE a.atttypid WHEN 'int8'::regtype THEN 'bigserial' WHEN 'int2'::regtype THEN 'smallserial' ELSE 'serial' END
END
FROM pg_catalog.pg_attribute a
LEFT JOIN pg_catalog.pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attrelid = `+rel+` AND a.attnum > 0 AND NOT a.attisdropped AND (
  a.attidentity IN ('a', 'd') OR a.attgenerated = 's' OR (a.atttypid IN ('int2'::regtype, 'int4'::regtype, 'int8'::regtype) AND EXISTS (
    SELECT 1 FROM pg_catalog.pg_depend dep JOIN pg_catalog.pg_class seq ON seq.oid = dep.objid AND seq.relkind = 'S'
    WHERE dep.refobjid = a.attrelid AND dep.refobjsubid = a.attnum AND dep.deptype = 'a'
    AND pg_catalog.pg_get_expr(ad.adbin, ad.adrelid) LIKE 'nextval(%')))`, schema, table)
	if err != nil {
		return "", err
	}

	special, err := scanPairs(rows)
	if err != nil {
		return "", err
	}

	indexes, err := queryStrings(ctx, db, `SELECT pg_catalog.pg_get_indexdef(ix.indexrelid)
FROM pg_catalog.pg_index ix WHERE ix.indrelid = `+rel+`
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint c WHERE c.conindid = ix.indexrelid AND c.contype IN ('p', 'u', 'x'))
ORDER BY 1`, schema, table)
	if err != nil {
		return "", err
	}

	return buildDDL(d, qualified, t.Columns, special, constraints, indexes), nil
}

func queryStrings(ctx context.Context, db Querier, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}
