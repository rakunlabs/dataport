package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(mssqlDriver{})
}

// mssqlDriver serves Microsoft SQL Server and Azure SQL.
//
// SQL Server has no read-only transaction. A read-only run allows only
// reading statements and is always rolled back, so anything a reading
// statement might still change is undone. The login's permissions remain
// the real limit.
type mssqlDriver struct{}

func (mssqlDriver) Type() string     { return "mssql" }
func (mssqlDriver) Dialect() Dialect { return DialectMSSQL }
func (mssqlDriver) DefaultPort() int { return 1433 }

func (mssqlDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 1433
	}

	q := url.Values{}
	q.Set("app name", "dataport")
	q.Set("dial timeout", "10")
	q.Set("encrypt", optionOr(c.Options, "encrypt", "true"))

	if v := c.Options["trust_server_certificate"]; v != "" {
		q.Set("TrustServerCertificate", v)
	}

	if c.Database != "" {
		q.Set("database", c.Database)
	}

	u := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(c.Username, password),
		Host:     c.Host + ":" + strconv.Itoa(port),
		RawQuery: q.Encode(),
	}

	if inst := strings.TrimSpace(c.Options["instance"]); inst != "" {
		u.Host = c.Host
		u.Path = "/" + inst
	}

	connector, err := mssql.NewConnector(u.String())
	if err != nil {
		return nil, fmt.Errorf("parse sqlserver config; %w", err)
	}

	return sql.OpenDB(connector), nil
}

func (mssqlDriver) BackendID(ctx context.Context, conn *sql.Conn) (string, error) {
	var id int64
	if err := conn.QueryRowContext(ctx, "SELECT @@SPID").Scan(&id); err != nil {
		return "", err
	}

	return strconv.FormatInt(id, 10), nil
}

func (mssqlDriver) Kill(ctx context.Context, db *sql.DB, backendID string) error {
	id, err := strconv.ParseInt(backendID, 10, 64)
	if err != nil {
		return err
	}

	// KILL ends the session; the pool then drops the connection.
	_, err = db.ExecContext(ctx, "KILL "+strconv.FormatInt(id, 10))

	return err
}

// BeginTx starts a regular transaction: the driver refuses the read-only
// option. Read-only runs are always rolled back by the engine.
func (mssqlDriver) BeginTx(ctx context.Context, conn *sql.Conn, _ bool) (*sql.Tx, error) {
	return conn.BeginTx(ctx, nil)
}

func (mssqlDriver) Prepare(ctx context.Context, tx Runner, timeout time.Duration) error {
	ms := timeout.Milliseconds()
	if ms <= 0 {
		return nil
	}

	_, err := tx.ExecContext(ctx, "SET LOCK_TIMEOUT "+strconv.FormatInt(ms, 10))

	return err
}

func (mssqlDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRowsWith(rows, maxRows, mssqlValue)
}

// mssqlValue renders uniqueidentifier as the usual UUID text. SQL Server
// sends the first three groups little-endian.
func mssqlValue(dbType string, v any) (any, bool) {
	b, ok := v.([]byte)
	if !ok || dbType != "uniqueidentifier" || len(b) != 16 {
		return nil, false
	}

	var u mssql.UniqueIdentifier
	if err := u.Scan(b); err != nil {
		return nil, false
	}

	return strings.ToLower(u.String()), true
}

// CheckReadOnly rejects SELECT ... INTO, which creates a table, and calls
// that can run arbitrary code or reach other servers.
func (mssqlDriver) CheckReadOnly(stmt string) error {
	upper := strings.ToUpper(stmt)

	if containsWord(upper, "INTO") {
		return fmt.Errorf("%w: INTO is not allowed", ErrReadOnly)
	}

	for _, w := range []string{"EXEC", "EXECUTE", "OPENROWSET", "OPENQUERY", "OPENDATASOURCE"} {
		if containsWord(upper, w) {
			return fmt.Errorf("%w: %s is not allowed", ErrReadOnly, w)
		}
	}

	return nil
}

// Reset relies on the driver, which resets the session with
// sp_reset_connection when a pooled connection is reused.
func (mssqlDriver) Reset(context.Context, *sql.Conn) error {
	return nil
}

func (mssqlDriver) Placeholder(n int) string {
	return "@p" + strconv.Itoa(n)
}

func (mssqlDriver) QuoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func (mssqlDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.name FROM sys.schemas s
WHERE s.name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest')
AND s.name NOT LIKE 'db[_]%'
AND EXISTS (SELECT 1 FROM sys.objects o WHERE o.schema_id = s.schema_id AND o.type IN ('U', 'V'))
ORDER BY CASE WHEN s.name = 'dbo' THEN 0 ELSE 1 END, s.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (mssqlDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT o.name, CASE o.type WHEN 'V' THEN 'view' ELSE 'table' END
FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE s.name = @p1 AND o.type IN ('U', 'V') AND o.is_ms_shipped = 0
ORDER BY o.name`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (mssqlDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.name,
TYPE_NAME(c.user_type_id) + CASE
  WHEN TYPE_NAME(c.user_type_id) IN ('varchar', 'char', 'varbinary', 'binary')
    THEN '(' + CASE WHEN c.max_length = -1 THEN 'max' ELSE CAST(c.max_length AS varchar) END + ')'
  WHEN TYPE_NAME(c.user_type_id) IN ('nvarchar', 'nchar')
    THEN '(' + CASE WHEN c.max_length = -1 THEN 'max' ELSE CAST(c.max_length / 2 AS varchar) END + ')'
  WHEN TYPE_NAME(c.user_type_id) IN ('decimal', 'numeric')
    THEN '(' + CAST(c.precision AS varchar) + ',' + CAST(c.scale AS varchar) + ')'
  ELSE '' END,
CAST(c.is_nullable AS bit),
COALESCE(OBJECT_DEFINITION(c.default_object_id), ''),
CAST(CASE WHEN EXISTS (
  SELECT 1 FROM sys.index_columns ic JOIN sys.indexes i ON i.object_id = ic.object_id AND i.index_id = ic.index_id
  WHERE i.is_primary_key = 1 AND ic.object_id = c.object_id AND ic.column_id = c.column_id) THEN 1 ELSE 0 END AS bit)
FROM sys.columns c
JOIN sys.objects o ON o.object_id = c.object_id
JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE s.name = @p1 AND o.name = @p2
ORDER BY c.column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanColumns(rows)
}

// ////////////////////////////////////////////
// structure

const mssqlObject = `OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(@p2))`

func (mssqlDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.name, i.is_unique, i.is_primary_key, LOWER(i.type_desc),
COALESCE(STRING_AGG(c.name, CHAR(31)) WITHIN GROUP (ORDER BY ic.key_ordinal), '')
FROM sys.indexes i
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.object_id = `+mssqlObject+` AND i.type > 0
GROUP BY i.name, i.is_unique, i.is_primary_key, i.type_desc
ORDER BY i.is_primary_key DESC, i.name`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (mssqlDriver) ForeignKeys(ctx context.Context, db Querier, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT fk.name, SCHEMA_NAME(rt.schema_id), rt.name,
STRING_AGG(pc.name, CHAR(31)) WITHIN GROUP (ORDER BY fkc.constraint_column_id),
STRING_AGG(rc.name, CHAR(31)) WITHIN GROUP (ORDER BY fkc.constraint_column_id),
fk.update_referential_action_desc, fk.delete_referential_action_desc
FROM sys.foreign_keys fk
JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
JOIN sys.objects rt ON rt.object_id = fk.referenced_object_id
WHERE fk.parent_object_id = `+mssqlObject+`
GROUP BY fk.name, rt.schema_id, rt.name, fk.update_referential_action_desc, fk.delete_referential_action_desc
ORDER BY fk.name`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

// DDL returns the stored definition of views. SQL Server has no statement
// that scripts a table, so CREATE TABLE is rebuilt from the catalog.
func (d mssqlDriver) DDL(ctx context.Context, db Querier, schema, table string, t *TableStructure) (string, error) {
	def, err := queryString(ctx, db, `SELECT OBJECT_DEFINITION(`+mssqlObject+`)`, schema, table)
	if err != nil {
		return "", err
	}

	if def != "" {
		return strings.TrimSpace(def) + "\n", nil
	}

	qualified := d.QuoteIdent(schema) + "." + d.QuoteIdent(table)

	rows, err := db.QueryContext(ctx, `SELECT c.name, CASE
  WHEN cc.definition IS NOT NULL THEN 'AS ' + cc.definition + CASE WHEN cc.is_persisted = 1 THEN ' PERSISTED' ELSE '' END
  ELSE '' END + ''
FROM sys.columns c
LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
WHERE c.object_id = `+mssqlObject+` AND c.is_computed = 1`, schema, table)
	if err != nil {
		return "", err
	}

	special, err := scanPairs(rows)
	if err != nil {
		return "", err
	}

	identity, err := db.QueryContext(ctx, `SELECT name, CAST(seed_value AS varchar(40)) + ',' + CAST(increment_value AS varchar(40))
FROM sys.identity_columns WHERE object_id = `+mssqlObject, schema, table)
	if err != nil {
		return "", err
	}

	ids, err := scanPairs(identity)
	if err != nil {
		return "", err
	}

	for _, c := range t.Columns {
		if v, ok := ids[c.Name]; ok {
			special[c.Name] = c.Type + " IDENTITY(" + v + ")"
		}
	}

	var constraints, indexes []string

	for _, ix := range t.Indexes {
		kind := strings.ToUpper(ix.Method)

		switch {
		case ix.Primary:
			constraints = append(constraints, "CONSTRAINT "+d.QuoteIdent(ix.Name)+" PRIMARY KEY "+kind+" ("+quoteList(d, ix.Columns)+")")
		case ix.Unique:
			indexes = append(indexes, "CREATE UNIQUE "+kind+" INDEX "+d.QuoteIdent(ix.Name)+" ON "+qualified+" ("+quoteList(d, ix.Columns)+")")
		default:
			indexes = append(indexes, "CREATE "+kind+" INDEX "+d.QuoteIdent(ix.Name)+" ON "+qualified+" ("+quoteList(d, ix.Columns)+")")
		}
	}

	for _, fk := range t.ForeignKeys {
		constraints = append(constraints, foreignKeyClause(d, fk))
	}

	return buildDDL(d, qualified, t.Columns, special, constraints, indexes), nil
}
