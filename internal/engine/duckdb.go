package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(duckdbDriver{})
}

// duckdbDriver opens a DuckDB file on the dataport server, inside the
// directory allowed for file databases. DuckDB runs inside the dataport
// process, so it is only part of builds made with the duckdb tag (see
// duckdb_cgo.go).
//
// A read-only connection opens the file with access_mode=read_only, which
// DuckDB enforces. External access (files, URLs, extensions) is disabled on
// every connection, so a statement cannot read the server's files.
type duckdbDriver struct{}

func (duckdbDriver) Type() string     { return "duckdb" }
func (duckdbDriver) Dialect() Dialect { return DialectPostgres }
func (duckdbDriver) DefaultPort() int { return 0 }
func (duckdbDriver) Target() string   { return TargetFile }

// openDuckDB is set by duckdb_cgo.go in builds with the duckdb tag.
var openDuckDB func(path string, readOnly bool) (*sql.DB, error)

func (duckdbDriver) Available() bool { return openDuckDB != nil }

func (duckdbDriver) Open(c *store.Connection, _ string) (*sql.DB, error) {
	if openDuckDB == nil {
		return nil, errNotBuilt("duckdb")
	}

	path, err := resolveFile(c.Database, false)
	if err != nil {
		return nil, err
	}

	return openDuckDB(path, c.ReadOnly)
}

func (duckdbDriver) BackendID(context.Context, *sql.Conn) (string, error) { return "", nil }

// Kill has nothing to do: the driver interrupts the query when the context
// ends.
func (duckdbDriver) Kill(context.Context, *sql.DB, string) error { return nil }

// BeginTx starts a plain transaction: the driver refuses the read-only
// option. Read-only runs are rolled back, and a read-only connection opens
// the file read-only.
func (duckdbDriver) BeginTx(ctx context.Context, conn *sql.Conn, _ bool) (*sql.Tx, error) {
	return conn.BeginTx(ctx, nil)
}

func (duckdbDriver) Prepare(context.Context, Runner, time.Duration) error { return nil }

func (duckdbDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

// CheckReadOnly has nothing to add: writes on a grant that may not write
// are rejected by the keyword check, and external access is off.
func (duckdbDriver) CheckReadOnly(string) error { return nil }

// CheckStatement refuses statements that change what the session may
// reach. The settings are locked as well; this gives a clear message.
func (duckdbDriver) CheckStatement(stmt string) error {
	switch kw := FirstKeyword(stmt, DialectPostgres); kw {
	case "ATTACH", "DETACH", "INSTALL", "LOAD", "COPY", "EXPORT", "IMPORT", "SET", "RESET", "PRAGMA", "USE":
		return fmt.Errorf("%s is not allowed", kw)
	}

	return nil
}

var errDuckDBNoReset = errors.New("duckdb sessions are not reused")

func (duckdbDriver) Reset(context.Context, *sql.Conn) error { return errDuckDBNoReset }

func (duckdbDriver) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var duckdbCatalog = infoSchema{ph: questionMark, exclude: []string{"information_schema", "pg_catalog"}}

func (duckdbDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT schema_name FROM information_schema.schemata
WHERE catalog_name = current_database() AND schema_name NOT IN ('information_schema', 'pg_catalog')
ORDER BY schema_name = 'main' DESC, schema_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (duckdbDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	return duckdbCatalog.Objects(ctx, db, schema)
}

func (duckdbDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.column_name, c.data_type, CASE WHEN c.is_nullable THEN 1 ELSE 0 END, c.column_default,
CASE WHEN EXISTS (SELECT 1 FROM duckdb_constraints() k WHERE k.constraint_type = 'PRIMARY KEY'
  AND k.schema_name = c.schema_name AND k.table_name = c.table_name AND list_contains(k.constraint_column_names, c.column_name)) THEN 1 ELSE 0 END
FROM duckdb_columns() c WHERE c.database_name = current_database() AND c.schema_name = ? AND c.table_name = ?
ORDER BY c.column_index`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// ////////////////////////////////////////////
// structure

func (duckdbDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT index_name, is_unique, is_primary, 'art', '' FROM duckdb_indexes()
WHERE database_name = current_database() AND schema_name = ? AND table_name = ? ORDER BY index_name`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (duckdbDriver) ForeignKeys(ctx context.Context, db Querier, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT constraint_name, schema_name, referenced_table,
array_to_string(constraint_column_names, chr(31)), array_to_string(referenced_column_names, chr(31)), 'NO ACTION', 'NO ACTION'
FROM duckdb_constraints() WHERE constraint_type = 'FOREIGN KEY' AND database_name = current_database()
AND schema_name = ? AND table_name = ? ORDER BY constraint_name`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

func (duckdbDriver) DDL(ctx context.Context, db Querier, schema, table string, _ *TableStructure) (string, error) {
	ddl, err := queryString(ctx, db, `SELECT sql FROM duckdb_tables() WHERE database_name = current_database() AND schema_name = ? AND table_name = ?
UNION ALL SELECT sql FROM duckdb_views() WHERE database_name = current_database() AND schema_name = ? AND view_name = ?`,
		schema, table, schema, table)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(ddl) + "\n", nil
}
