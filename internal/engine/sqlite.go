package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(sqliteDriver{})
}

// sqliteDriver opens a SQLite file on the dataport server, inside the
// directory the server allows for file databases (see Manager.SetFileDir).
//
// Read-only runs set PRAGMA query_only, which SQLite enforces itself, and
// are rolled back. ATTACH is disabled on every connection, so a statement
// cannot reach any other file.
type sqliteDriver struct{}

func (sqliteDriver) Type() string     { return "sqlite" }
func (sqliteDriver) Dialect() Dialect { return DialectSQLite }
func (sqliteDriver) DefaultPort() int { return 0 }
func (sqliteDriver) Target() string   { return TargetFile }

func (sqliteDriver) Open(c *store.Connection, _ string) (*sql.DB, error) {
	path, err := resolveFile(c.Database, false)
	if err != nil {
		return nil, err
	}

	// mode=rw never creates a file: a typo fails instead of making an empty
	// database.
	mode := "rw"
	if c.ReadOnly {
		mode = "ro"
	}

	dsn := "file:" + path + "?mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	connector, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}

func (sqliteDriver) BackendID(context.Context, *sql.Conn) (string, error) { return "", nil }

// Kill has nothing to do: the driver interrupts the statement when the
// context is cancelled.
func (sqliteDriver) Kill(context.Context, *sql.DB, string) error { return nil }

func (sqliteDriver) BeginTx(ctx context.Context, conn *sql.Conn, readOnly bool) (*sql.Tx, error) {
	if _, err := sqlite.Limit(conn, sqlitelib.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return nil, err
	}

	if readOnly {
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return nil, err
		}
	}

	return conn.BeginTx(ctx, nil)
}

func (sqliteDriver) Prepare(context.Context, Runner, time.Duration) error { return nil }

func (sqliteDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

func (sqliteDriver) CheckReadOnly(string) error { return nil }

// CheckStatement refuses statements that reach other files: ATTACH is also
// disabled by a limit, VACUUM INTO writes a copy anywhere.
func (sqliteDriver) CheckStatement(stmt string) error {
	kw := FirstKeyword(stmt, DialectSQLite)
	upper := strings.ToUpper(stmt)

	switch {
	case kw == "ATTACH" || kw == "DETACH":
		return fmt.Errorf("%s is not allowed", kw)
	case kw == "VACUUM" && containsWord(upper, "INTO"):
		return errors.New("VACUUM INTO is not allowed")
	case containsWord(upper, "LOAD_EXTENSION"):
		return errors.New("load_extension is not allowed")
	}

	return nil
}

var errSQLiteNoReset = errors.New("sqlite sessions are not reused")

// Reset drops the connection: opening a file is cheap, and temporary
// tables or pragmas can never reach the next request.
func (sqliteDriver) Reset(context.Context, *sql.Conn) error { return errSQLiteNoReset }

func (sqliteDriver) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (sqliteDriver) Schemas(context.Context, Querier, *store.Connection) ([]string, error) {
	return []string{"main"}, nil
}

func (sqliteDriver) Objects(ctx context.Context, db Querier, _ string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, CASE type WHEN 'view' THEN 'view' ELSE 'table' END
FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (sqliteDriver) Columns(ctx context.Context, db Querier, _, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type, CASE WHEN "notnull" = 0 AND pk = 0 THEN 1 ELSE 0 END, dflt_value, CASE WHEN pk > 0 THEN 1 ELSE 0 END
FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// ////////////////////////////////////////////
// structure

func (sqliteDriver) Indexes(ctx context.Context, db Querier, _, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT il.name, il."unique" = 1, il.origin = 'pk', il.origin,
COALESCE((SELECT group_concat(ii.name, char(31)) FROM (SELECT name FROM pragma_index_info(il.name) ORDER BY seqno) ii), '')
FROM pragma_index_list(?) il ORDER BY il.origin = 'pk' DESC, il.name`, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (sqliteDriver) ForeignKeys(ctx context.Context, db Querier, _, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT 'fk_' || id, '', "table",
group_concat("from", char(31)), group_concat(COALESCE("to", ''), char(31)), on_update, on_delete
FROM (SELECT * FROM pragma_foreign_key_list(?) ORDER BY id, seq) GROUP BY id ORDER BY id`, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

// DDL returns the statements SQLite stored for the table and its indexes.
func (sqliteDriver) DDL(ctx context.Context, db Querier, _, table string, _ *TableStructure) (string, error) {
	stmts, err := queryStrings(ctx, db, `SELECT sql FROM sqlite_schema WHERE tbl_name = ? AND sql IS NOT NULL
ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'view' THEN 0 ELSE 1 END, name`, table)
	if err != nil {
		return "", err
	}

	var b strings.Builder

	for i, s := range stmts {
		if i > 0 {
			b.WriteString("\n")
		}

		b.WriteString(strings.TrimSuffix(strings.TrimSpace(s), ";") + ";\n")
	}

	return b.String(), nil
}
