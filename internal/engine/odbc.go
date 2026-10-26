package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(odbcDriver{})
}

// odbcDriver connects through unixODBC with the connection string in the
// "dsn" option, for databases without a Go driver: Ingres, DB2, Informix,
// Teradata, SAP HANA and the like. It is only part of builds made with the
// odbc tag (see odbc_cgo.go), which need cgo and libodbc at run time.
//
// The username and password fields are added to the connection string as
// UID and PWD, so users may sign in with their own account. The "dialect"
// option picks the catalog queries and the identifier quoting; without it
// information_schema is used.
//
// ODBC has no portable read-only transaction. A read-only run allows only
// reading statements and is always rolled back, as for SQL Server; give the
// configured account only the privileges users should have.
type odbcDriver struct {
	dialect string
}

func (odbcDriver) Type() string       { return "odbc" }
func (odbcDriver) DefaultPort() int   { return 0 }
func (odbcDriver) Target() string     { return TargetDSN }
func (d odbcDriver) Dialect() Dialect { return odbcDialects[d.dialect].split }

func (odbcDriver) ForConnection(c *store.Connection) Driver {
	return odbcDriver{dialect: strings.ToLower(strings.TrimSpace(c.Options["dialect"]))}
}

type odbcDialect struct {
	split Dialect
	quote func(string) string
	// catalog reads schemas, objects and columns; nil uses
	// information_schema.
	catalog odbcCatalog
	// resetSQL clears the session before a connection is reused.
	resetSQL string
}

type odbcCatalog interface {
	Schemas(ctx context.Context, db Querier) ([]string, error)
	Objects(ctx context.Context, db Querier, schema string) ([]Object, error)
	Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error)
}

func doubleQuote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

var odbcDialects = map[string]odbcDialect{
	"":       {split: DialectStandard, quote: doubleQuote, catalog: infoSchema{ph: questionMark, exclude: []string{"information_schema", "pg_catalog", "sys", "sysibm"}}},
	"ingres": {split: DialectStandard, quote: doubleQuote, catalog: ingresCatalog{}},
	"db2":    {split: DialectStandard, quote: doubleQuote, catalog: db2Catalog{}},
	"mysql": {split: DialectMySQL, quote: func(n string) string { return "`" + strings.ReplaceAll(n, "`", "``") + "`" },
		catalog: infoSchema{ph: questionMark, exclude: []string{"mysql", "information_schema", "performance_schema", "sys"}}},
	"mssql": {split: DialectMSSQL, quote: func(n string) string { return "[" + strings.ReplaceAll(n, "]", "]]") + "]" },
		catalog: infoSchema{ph: questionMark, exclude: []string{"information_schema", "sys"}}},
	"postgres": {split: DialectPostgres, quote: doubleQuote,
		catalog: infoSchema{ph: questionMark, exclude: []string{"information_schema", "pg_catalog", "pg_toast"}}},
}

// OdbcDialects lists the dialect option values, for the connection form.
func OdbcDialects() []string {
	return []string{"", "ingres", "db2", "mysql", "mssql", "postgres"}
}

func (d odbcDriver) spec() odbcDialect {
	if s, ok := odbcDialects[d.dialect]; ok {
		return s
	}

	return odbcDialects[""]
}

func (odbcDriver) BackendID(context.Context, *sql.Conn) (string, error) { return "", nil }

// Kill has nothing to do: the driver calls SQLCancel when the context ends.
func (odbcDriver) Kill(context.Context, *sql.DB, string) error { return nil }

// BeginTx starts a plain transaction: ODBC has no read-only one. Read-only
// runs are always rolled back by the engine.
func (odbcDriver) BeginTx(ctx context.Context, conn *sql.Conn, _ bool) (*sql.Tx, error) {
	return conn.BeginTx(ctx, nil)
}

func (odbcDriver) Prepare(context.Context, Runner, time.Duration) error { return nil }

func (odbcDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRowsWith(rows, maxRows, trimChar)
}

// trimChar drops the padding of fixed-width CHAR columns, which ODBC
// drivers of Ingres and DB2 return as is.
func trimChar(dbType string, v any) (any, bool) {
	if !strings.Contains(dbType, "char") || strings.Contains(dbType, "var") {
		return nil, false
	}

	switch x := v.(type) {
	case string:
		return strings.TrimRight(x, " "), true
	case []byte:
		return strings.TrimRight(string(x), " "), true
	}

	return nil, false
}

// CheckReadOnly rejects procedure calls, which may change data out of reach
// of the keyword check.
func (odbcDriver) CheckReadOnly(stmt string) error {
	upper := strings.ToUpper(stmt)

	for _, w := range []string{"EXEC", "EXECUTE", "CALL"} {
		if containsWord(upper, w) {
			return errReadOnlyWord(w)
		}
	}

	return nil
}

func (d odbcDriver) Reset(ctx context.Context, conn *sql.Conn) error {
	if q := d.spec().resetSQL; q != "" {
		_, err := conn.ExecContext(ctx, q)

		return err
	}

	return nil
}

func (d odbcDriver) QuoteIdent(name string) string { return d.spec().quote(name) }

func (d odbcDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	return d.spec().catalog.Schemas(ctx, db)
}

func (d odbcDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	return d.spec().catalog.Objects(ctx, db, schema)
}

func (d odbcDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	return d.spec().catalog.Columns(ctx, db, schema, table)
}

// ////////////////////////////////////////////
// Ingres

// ingresCatalog reads the iitables and iicolumns catalogs, whose names are
// padded with spaces.
type ingresCatalog struct{}

func (ingresCatalog) Schemas(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT TRIM(table_owner) FROM iitables
WHERE system_use = 'U' AND table_type IN ('T', 'V') ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (ingresCatalog) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT TRIM(table_name), CASE table_type WHEN 'V' THEN 'view' ELSE 'table' END
FROM iitables WHERE system_use = 'U' AND table_type IN ('T', 'V') AND table_owner = ? ORDER BY 1`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (ingresCatalog) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT TRIM(column_name), TRIM(column_datatype) ||
CASE WHEN TRIM(column_datatype) IN ('CHAR', 'VARCHAR', 'NCHAR', 'NVARCHAR', 'BYTE', 'VARBYTE') THEN '(' || VARCHAR(column_length) || ')' ELSE '' END,
CASE column_nulls WHEN 'Y' THEN 1 ELSE 0 END, '', CASE WHEN key_sequence > 0 THEN 1 ELSE 0 END
FROM iicolumns WHERE table_owner = ? AND table_name = ? ORDER BY column_sequence`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// ////////////////////////////////////////////
// DB2

// db2Catalog reads SYSCAT, the catalog of DB2 for Linux, Unix and Windows.
type db2Catalog struct{}

func (db2Catalog) Schemas(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT TRIM(tabschema) FROM syscat.tables
WHERE tabschema NOT LIKE 'SYS%' AND type IN ('T', 'V') ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (db2Catalog) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT TRIM(tabname), CASE type WHEN 'V' THEN 'view' ELSE 'table' END
FROM syscat.tables WHERE tabschema = ? AND type IN ('T', 'V') ORDER BY 1`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (db2Catalog) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT TRIM(colname), TRIM(typename) ||
CASE WHEN typename IN ('VARCHAR', 'CHARACTER', 'VARGRAPHIC', 'GRAPHIC') THEN '(' || TRIM(CHAR(length)) || ')'
  WHEN typename = 'DECIMAL' THEN '(' || TRIM(CHAR(length)) || ',' || TRIM(CHAR(scale)) || ')' ELSE '' END,
CASE nulls WHEN 'Y' THEN 1 ELSE 0 END, COALESCE(default, ''), CASE WHEN keyseq IS NOT NULL THEN 1 ELSE 0 END
FROM syscat.columns WHERE tabschema = ? AND tabname = ? ORDER BY colno`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// openODBC is set by odbc_cgo.go in builds with the odbc tag.
var openODBC func(connString string) (*sql.DB, error)

func (odbcDriver) Available() bool { return openODBC != nil }

func (odbcDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	if openODBC == nil {
		return nil, errNotBuilt("odbc")
	}

	return openODBC(odbcConnString(c.Options["dsn"], c.Username, password))
}

// odbcConnString adds the account to the connection string, quoting values
// in braces so a semicolon in the password cannot add attributes.
func odbcConnString(dsn, user, password string) string {
	dsn = strings.TrimRight(strings.TrimSpace(dsn), ";")

	if user != "" {
		dsn += ";UID=" + odbcValue(user)
	}

	if password != "" {
		dsn += ";PWD=" + odbcValue(password)
	}

	return dsn
}

func odbcValue(v string) string {
	if !strings.ContainsAny(v, ";{}= ") {
		return v
	}

	return "{" + strings.ReplaceAll(v, "}", "}}") + "}"
}

func errReadOnlyWord(w string) error {
	return fmt.Errorf("%w: %s is not allowed", ErrReadOnly, w)
}
